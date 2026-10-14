package service

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// APIMart stores the polling collection in api_path; submission uses its sibling videos collection.
type apimartVideoProviderAdapter struct{}

func (apimartVideoProviderAdapter) Provider() string       { return videoProviderApimart }
func (apimartVideoProviderAdapter) DefaultBaseURL() string { return "https://api.apimart.ai" }
func (apimartVideoProviderAdapter) DefaultAPIPath() string { return "/v1/tasks" }
func isMinimaxH3Model(model string) bool {
	return model == VideoModelMinimaxH3 || model == VideoModelMinimaxH3Max
}
func (apimartVideoProviderAdapter) Compatible(model, resolution string) bool {
	return isMinimaxH3Model(model) && IsSupportedVideoResolution(model, resolution)
}
func (a apimartVideoProviderAdapter) CompatibleRequest(n *normalizedVideoRequest) bool {
	if n == nil || !a.Compatible(n.Model, n.Resolution) {
		return false
	}
	spec, _ := videoSpecForModel(n.Model)
	return n.GeneratedSeconds >= spec.MinSeconds && n.GeneratedSeconds <= spec.MaxSeconds && validateApimartVideoInput(&VideoCreateRequest{Ratio: n.Ratio, GenerateAudio: n.GenerateAudio, Raw: n.Raw, Model: n.Model}, n.Prompt, n.Content, n.Resolution) == nil
}
func (apimartVideoProviderAdapter) UpstreamModel(account *Account, n *normalizedVideoRequest) string {
	if n == nil {
		return ""
	}
	if mapped := resolvedMappedVideoModel(account, n.Model); mapped != "" {
		return mapped
	}
	if n.Model == VideoModelMinimaxH3Max {
		return "MiniMax-H3-Max"
	}
	return "MiniMax-H3"
}
func (apimartVideoProviderAdapter) CreateEndpoint(endpoint, _ string) string {
	return strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/tasks") + "/videos/generations"
}
func (apimartVideoProviderAdapter) ResultAuthorization(*Account, string) string { return "" }
func (apimartVideoProviderAdapter) PollMaxConsecutiveFailures() int             { return 1 }
func (apimartVideoProviderAdapter) BuildCreateBody(n *normalizedVideoRequest, model string) map[string]any {
	body := map[string]any{"model": model, "prompt": n.Prompt, "duration": n.GeneratedSeconds, "resolution": strings.ToUpper(n.Resolution)}
	if n.RatioProvided {
		body["aspect_ratio"] = n.Ratio
	}
	for _, key := range []string{"watermark", "nsfw_check"} {
		if value, ok := n.Raw[key]; ok {
			body[key] = value
		}
	}
	images, videos, audios := []string{}, []string{}, []string{}
	for _, item := range n.Content {
		switch item.Type {
		case "image_url":
			if item.ImageURL == nil {
				continue
			}
			switch item.Role {
			case "first_frame":
				body["first_frame_image"] = item.ImageURL.URL
			case "last_frame":
				body["last_frame_image"] = item.ImageURL.URL
			default:
				images = append(images, item.ImageURL.URL)
			}
		case "video_url":
			if item.VideoURL != nil {
				videos = append(videos, item.VideoURL.URL)
			}
		case "audio_url":
			if item.AudioURL != nil {
				audios = append(audios, item.AudioURL.URL)
			}
		}
	}
	if len(images) > 0 {
		body["image_urls"] = images
	}
	if len(videos) > 0 {
		body["video_urls"] = videos
	}
	if len(audios) > 0 {
		body["audio_urls"] = audios
	}
	return body
}
func validateApimartVideoInput(req *VideoCreateRequest, prompt string, content []VideoContent, resolution string) error {
	invalid := func(message string) error { return videoBadRequest("invalid_video_content", message) }
	if prompt == "" || utf8.RuneCountInString(prompt) > 7000 {
		return videoBadRequest("invalid_video_prompt", "Prompt must contain 1 to 7000 characters")
	}
	ratio := firstNonEmptyVideoString(req.Ratio, req.AspectRatio, req.AspectRatioCamel)
	if ratio != "" && ratio != "adaptive" && !isSeedanceAspectRatio(ratio) {
		return videoBadRequest("invalid_video_ratio", "Invalid video ratio")
	}
	if req.GenerateAudio != nil && !*req.GenerateAudio {
		return invalid("MiniMax H3 always generates audio; omit generate_audio or set true")
	}
	for _, key := range []string{"watermark", "nsfw_check"} {
		if v, ok := req.Raw[key]; ok {
			if _, ok := v.(bool); !ok {
				return invalid(key + " must be boolean")
			}
		}
	}
	for _, key := range []string{"aigc_watermark", "image_urls", "video_urls", "audio_urls", "first_frame_image", "last_frame_image", "image_with_roles", "webhook", "callback_url"} {
		if _, ok := req.Raw[key]; ok {
			return invalid("Use the unified video content and polling interface; unsupported field: " + key)
		}
	}
	if req.Model == VideoModelMinimaxH3Max && resolution == VideoResolution1080P {
		if _, ok := req.Raw["watermark"]; ok {
			return invalid("1080p requires omitting watermark, including false")
		}
	}
	stats := inspectVideoContent(content)
	if stats.FirstFrameCount+stats.LastFrameCount > 0 && (stats.ReferenceImageCount+stats.VideoCount+stats.AudioCount > 0) {
		return invalid("Frame images cannot be mixed with reference media")
	}
	for _, item := range content {
		var media *VideoContentURL
		switch item.Type {
		case "text":
			continue
		case "image_url":
			media = item.ImageURL
			if item.Role != "first_frame" && item.Role != "last_frame" && item.Role != "reference_image" {
				return invalid("Image role must be first_frame, last_frame or reference_image")
			}
		case "video_url":
			media = item.VideoURL
			if item.Role != "" && item.Role != "reference_video" {
				return invalid("Invalid video role")
			}
		case "audio_url":
			media = item.AudioURL
			if item.Role != "" && item.Role != "reference_audio" {
				return invalid("Invalid audio role")
			}
		default:
			return invalid("Unsupported content type")
		}
		if media == nil || (!strings.HasPrefix(media.URL, "https://") && !strings.HasPrefix(media.URL, "http://")) {
			return invalid("Media requires an HTTP(S) URL")
		}
	}
	return nil
}
func apimartResponseData(payload map[string]any, create bool) (map[string]any, error) {
	if code := stringFromMap(payload, "code"); code != "" && code != "200" {
		raw, _ := json.Marshal(payload)
		return nil, &videoUpstreamError{StatusCode: 502, Body: raw, Err: errors.New("APIMart rejected request")}
	}
	if create {
		if data, ok := payload["data"].([]any); ok && len(data) == 1 {
			if entry, ok := mapFromAny(data[0]); ok {
				return entry, nil
			}
		}
	} else if data, ok := mapFromAny(payload["data"]); ok {
		if stringFromMap(data, "status") != "" {
			return data, nil
		}
	}
	return nil, &videoUpstreamError{StatusCode: 502, Err: errors.New("invalid APIMart response envelope")}
}
func (apimartVideoProviderAdapter) ResultURL(_, _ string, payload map[string]any) string {
	result, _ := mapFromAny(payload["result"])
	videos, _ := result["videos"].([]any)
	for _, v := range videos {
		entry, _ := mapFromAny(v)
		if url := stringFromMap(entry, "url"); url != "" {
			return url
		}
		if urls, ok := entry["url"].([]any); ok {
			for _, value := range urls {
				if url, ok := value.(string); ok && strings.TrimSpace(url) != "" {
					return url
				}
			}
		}
	}
	return ""
}

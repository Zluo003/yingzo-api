package service

import (
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const videoProviderVidu = "vidu"

// Vidu's native protocol stays behind the unified video task API.
type viduVideoProviderAdapter struct{}

func (v viduVideoProviderAdapter) Provider() string       { return videoProviderVidu }
func (v viduVideoProviderAdapter) DefaultBaseURL() string { return "https://api.vidu.cn" }
func (v viduVideoProviderAdapter) DefaultAPIPath() string { return "/ent/v2/tasks" }
func (v viduVideoProviderAdapter) Compatible(model, resolution string) bool {
	return model == VideoModelViduQ4Preview && IsSupportedVideoResolution(model, resolution)
}

func (v viduVideoProviderAdapter) CompatibleRequest(r *normalizedVideoRequest) bool {
	if r == nil || !v.Compatible(r.Model, r.Resolution) {
		return false
	}
	spec, _ := videoSpecForModel(r.Model)
	if r.GeneratedSeconds < spec.MinSeconds || r.GeneratedSeconds > spec.MaxSeconds {
		return false
	}
	if err := validateVideoAbilityInput(r.Model, r.AbilityCode, r.Prompt, inspectVideoContent(r.Content)); err != nil {
		return false
	}
	req := &VideoCreateRequest{Raw: r.Raw}
	if r.RatioProvided {
		req.Ratio = r.Ratio
	}
	return validateViduVideoInput(req, r.AbilityCode, r.Prompt, r.Content) == nil
}

func (v viduVideoProviderAdapter) UpstreamModel(account *Account, r *normalizedVideoRequest) string {
	if r == nil || !v.Compatible(r.Model, r.Resolution) {
		return ""
	}
	if mapped := resolvedMappedVideoModel(account, r.Model); mapped != "" {
		return mapped
	}
	return "viduq4-preview"
}

func (v viduVideoProviderAdapter) BuildCreateBody(r *normalizedVideoRequest, model string) map[string]any {
	if r == nil {
		return nil
	}
	images, sounds := []string{}, []string{}
	for _, item := range r.Content {
		if item.Type == "image_url" && item.ImageURL != nil {
			images = append(images, strings.TrimSpace(item.ImageURL.URL))
		}
		if item.Type == "audio_url" && item.AudioURL != nil {
			sounds = append(sounds, strings.TrimSpace(item.AudioURL.URL))
		}
	}
	body := map[string]any{
		"model": model, "images": images, "prompt": r.Prompt,
		"duration": r.GeneratedSeconds, "resolution": r.Resolution, "audio": true,
		// The upstream documentation contradicts itself about watermark defaults.
		"watermark": false,
	}
	if r.GenerateAudio != nil {
		body["audio"] = *r.GenerateAudio
	}
	if r.AbilityCode == videoAbilityReferenceToVideo {
		body["aspect_ratio"] = r.Ratio
		if len(sounds) > 0 {
			body["sounds"] = sounds
		}
	} else {
		body["is_rec"] = true
		if value, ok := r.Raw["is_rec"]; ok {
			body["is_rec"] = value
		}
	}
	for _, field := range []string{"seed", "watermark"} {
		if value, ok := r.Raw[field]; ok {
			body[field] = value
		}
	}
	return body
}

func (v viduVideoProviderAdapter) RequestAuthorization(account *Account) string {
	return "Token " + strings.TrimSpace(account.GetCredential("api_key"))
}

// Results are signed CDN URLs: never send the Vidu credential to that host.
func (v viduVideoProviderAdapter) ResultAuthorization(*Account, string) string { return "" }
func (v viduVideoProviderAdapter) CreateEndpoint(endpoint, _ string) string    { return endpoint }
func (v viduVideoProviderAdapter) PollMaxConsecutiveFailures() int             { return 1 }

func (v viduVideoProviderAdapter) CreateRequestEndpoint(endpoint string, body map[string]any) string {
	root := strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/tasks")
	if _, reference := body["aspect_ratio"]; reference {
		return root + "/reference2video"
	}
	return root + "/img2video"
}

func (v viduVideoProviderAdapter) PollEndpoint(endpoint, taskID string) string {
	return strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(taskID) + "/creations"
}

func (v viduVideoProviderAdapter) ResultURL(_ string, _ string, payload map[string]any) string {
	creations, _ := payload["creations"].([]any)
	for _, item := range creations {
		if creation, ok := item.(map[string]any); ok {
			if result := strings.TrimSpace(stringFromMap(creation, "url")); result != "" {
				return result
			}
		}
	}
	return ""
}

func viduVideoResponse(payload map[string]any) map[string]any {
	out := cloneMap(payload)
	state := stringFromMap(payload, "state")
	if state == "created" || state == "queueing" {
		state = VideoTaskStatusQueued
	}
	out["status"] = state
	out["error_code"] = payload["err_code"]
	// Vidu reports named task errors with HTTP 200. Classify confirmed input
	// refusals so failover does not resubmit them as generic server failures.
	switch strings.ToLower(stringFromMap(payload, "err_code")) {
	case "taskpromptpolicyviolation", "creationpolicyviolation", "auditsubmitillegal", "photoauditnotpass", "auditfailed":
		out["status_code"] = http.StatusUnavailableForLegalReasons
	case "badrequest", "fieldlacking", "fieldunwanted", "fielditemcountoutofrange", "pagesizeoutofrange", "fieldinvalid",
		"imagedownloadfailure", "imageformatinvalid", "imagesizeinvalid", "imagecheckbodyjointsfailed", "imagecheckfacefailed", "imageobjectsundetected",
		"facedetectfailure", "facedetectnotpass", "nofacedetected", "multifacedetected", "videodownloadfailure", "videoformatinvalid", "usercancelled", "canceled":
		out["status_code"] = http.StatusBadRequest
	}
	return out
}

func validateViduVideoInput(req *VideoCreateRequest, ability, prompt string, content []VideoContent) error {
	bad := func(message string) error { return videoBadRequest("invalid_video_content", message) }
	if utf8.RuneCountInString(prompt) > 20000 {
		return videoBadRequest("invalid_video_prompt", "Video prompt must not exceed 20000 characters")
	}
	switch ability {
	case videoAbilityImageToVideo:
		if firstNonEmptyVideoString(req.Ratio, req.AspectRatio, req.AspectRatioCamel) != "" {
			return videoBadRequest("invalid_video_ratio", "Vidu image-to-video uses the first image's aspect ratio; omit ratio")
		}
	case videoAbilityReferenceToVideo:
		if prompt == "" {
			return videoBadRequest("invalid_video_prompt", "Reference-to-video requires a prompt")
		}
		ratio := firstNonEmptyVideoString(req.Ratio, req.AspectRatio, req.AspectRatioCamel)
		switch ratio {
		case "", "1:1", "9:16", "16:9", "3:4", "4:3":
		default:
			return videoBadRequest("invalid_video_ratio", "Invalid Vidu reference video ratio")
		}
		if _, ok := req.Raw["is_rec"]; ok {
			return videoBadRequest("invalid_video_options", "is_rec is only supported for image-to-video")
		}
	default:
		return videoBadRequest("invalid_video_ability", "Vidu Q4 Preview supports image-to-video and reference-to-video")
	}
	for _, item := range content {
		var ref *VideoContentURL
		switch item.Type {
		case "text":
			continue
		case "image_url":
			ref = item.ImageURL
			role := "reference_image"
			if ability == videoAbilityImageToVideo {
				role = "first_frame"
			}
			if item.Role != role {
				return bad("Vidu images must use first_frame for image-to-video or reference_image for reference-to-video")
			}
		case "audio_url":
			ref = item.AudioURL
			if ability != videoAbilityReferenceToVideo || (item.Role != "" && item.Role != "reference_audio") {
				return bad("Vidu reference audio requires reference-to-video mode")
			}
		default:
			return bad("Unsupported Vidu content type")
		}
		if ref == nil || strings.TrimSpace(ref.URL) == "" {
			return bad("Reference URL is required")
		}
		u, err := url.Parse(strings.TrimSpace(ref.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			// The handler turns inline images/audio into platform URLs before normalization.
			return bad("Vidu references must use HTTP(S) URLs or uploaded platform materials")
		}
	}
	for _, field := range []string{"is_rec", "watermark"} {
		if value, ok := req.Raw[field]; ok {
			if _, valid := value.(bool); !valid {
				return videoBadRequest("invalid_video_options", field+" must be a boolean")
			}
		}
	}
	if value, ok := req.Raw["seed"]; ok {
		switch n := value.(type) {
		case float64:
			if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
				return videoBadRequest("invalid_video_options", "seed must be an integer")
			}
		case int, int64:
		default:
			return videoBadRequest("invalid_video_options", "seed must be an integer")
		}
	}
	return nil
}

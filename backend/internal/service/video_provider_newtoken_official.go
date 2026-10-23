package service

import (
	"math"
	"regexp"
	"strings"
)

// Kept separate from NewToken's /v1/videos protocol and resolution-specific IDs.
type newtokenOfficialVideoProviderAdapter struct{ newtokenVideoProviderAdapter }

var newtokenOfficialModels = map[string]string{
	VideoModelSeedance20:     "doubao-seedance-2-0-260128",
	VideoModelSeedance20Fast: "doubao-seedance-2-0-fast-260128",
	VideoModelSeedance20Mini: "doubao-seedance-2-0-mini-260615",
	VideoModelSeedance25:     "doubao-seedance-2-5-260628",
}

func (n newtokenOfficialVideoProviderAdapter) Provider() string { return videoProviderNewtokenOfficial }
func (n newtokenOfficialVideoProviderAdapter) DefaultAPIPath() string {
	return "/api/v3/contents/generations/tasks"
}

func (n newtokenOfficialVideoProviderAdapter) Compatible(model, resolution string) bool {
	return newtokenOfficialModels[model] != "" && IsSupportedVideoResolution(model, resolution)
}

func (n newtokenOfficialVideoProviderAdapter) CompatibleRequest(r *normalizedVideoRequest) bool {
	if r == nil || !n.Compatible(r.Model, r.Resolution) || strings.TrimSpace(r.Prompt) == "" {
		return false
	}
	spec, _ := videoSpecForModel(r.Model)
	if r.GeneratedSeconds < spec.MinSeconds || r.GeneratedSeconds > spec.MaxSeconds {
		return false
	}
	mode, _ := r.Raw["omni_reference_task_type"].(string)
	// The gateway's generic auto-duration estimate must never replace an
	// explicitly requested official-protocol sentinel on the wire.
	if r.RequestedDuration == -1 && mode != "edit" {
		return false
	}
	adaptiveRatio := r.Model == VideoModelSeedance25 && r.Ratio == "auto" && (mode == "edit" || mode == "extend")
	if r.RatioProvided && !isSeedanceAspectRatio(r.Ratio) && !adaptiveRatio {
		return false
	}
	stats := inspectVideoContent(r.Content)
	if stats.ImageCount > spec.MaxRefImages || stats.VideoCount > spec.MaxRefVideos || stats.AudioCount > spec.MaxRefAudios || (spec.MaxRefTotal > 0 && stats.ImageCount+stats.VideoCount+stats.AudioCount > spec.MaxRefTotal) {
		return false
	}
	if stats.FirstFrameCount > 1 || stats.LastFrameCount > 1 ||
		(stats.FirstFrameCount+stats.LastFrameCount > 0 && (stats.ImageCount > 2 || stats.VideoCount > 0)) {
		return false
	}
	for _, item := range r.Content {
		switch item.Type {
		case "text":
		case "draft_task":
			if r.Model != VideoModelSeedance25 || strings.TrimSpace(stringFromMap(item.DraftTask, "id")) == "" {
				return false
			}
		case "image_url":
			if item.ImageURL == nil || strings.TrimSpace(item.ImageURL.URL) == "" || (item.Role != "reference_image" && item.Role != "first_frame" && item.Role != "last_frame") {
				return false
			}
		case "video_url":
			if item.VideoURL == nil || strings.TrimSpace(item.VideoURL.URL) == "" || (item.Role != "" && item.Role != "reference_video") {
				return false
			}
		case "audio_url":
			if item.AudioURL == nil || strings.TrimSpace(item.AudioURL.URL) == "" || (item.Role != "" && item.Role != "reference_audio") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (n newtokenOfficialVideoProviderAdapter) UpstreamModel(a *Account, r *normalizedVideoRequest) string {
	if r == nil {
		return ""
	}
	if mapped := resolvedMappedVideoModel(a, r.Model); mapped != "" {
		return mapped
	}
	return newtokenOfficialModels[r.Model]
}

func (n newtokenOfficialVideoProviderAdapter) BuildCreateBody(r *normalizedVideoRequest, model string) map[string]any {
	if r == nil {
		return nil
	}
	content := make([]map[string]any, 0, len(r.Content)+1)
	hasText := false
	for _, item := range r.Content {
		entry := map[string]any{"type": item.Type}
		switch item.Type {
		case "text":
			if item.Text == "" {
				continue
			}
			entry["text"] = item.Text
			hasText = true
		case "image_url", "video_url", "audio_url":
			var ref *VideoContentURL
			role := item.Role
			switch item.Type {
			case "image_url":
				ref = item.ImageURL
			case "video_url":
				ref = item.VideoURL
				if role == "" {
					role = "reference_video"
				}
			case "audio_url":
				ref = item.AudioURL
				if role == "" {
					role = "reference_audio"
				}
			}
			if ref == nil {
				continue
			}
			entry[item.Type] = map[string]any{"url": strings.TrimSpace(ref.URL)}
			entry["role"] = role
		case "draft_task":
			entry["draft_task"] = cloneMap(item.DraftTask)
		default:
			continue
		}
		content = append(content, entry)
	}
	if !hasText {
		content = append([]map[string]any{{"type": "text", "text": r.Prompt}}, content...)
	}
	body := map[string]any{"model": model, "content": content, "resolution": strings.ToLower(r.Resolution), "duration": r.GeneratedSeconds}
	if r.RatioProvided {
		ratio := r.Ratio
		if ratio == "auto" {
			ratio = "adaptive"
		}
		body["ratio"] = ratio
	}
	if r.GenerateAudio != nil {
		body["generate_audio"] = *r.GenerateAudio
	}
	if r.SafetyIdentifier != "" {
		body["safety_identifier"] = r.SafetyIdentifier
	}
	for _, field := range []string{"seed", "watermark", "return_last_frame", "priority", "execution_expires_after", "tools", "draft", "omni_reference_task_type"} {
		if value, provided := r.Raw[field]; provided {
			body[field] = value
		}
	}
	if r.Raw["omni_reference_task_type"] == "edit" {
		body["duration"] = -1
	}
	return body
}

var seedanceAssetReferencePattern = regexp.MustCompile(`^asset://[A-Za-z0-9][A-Za-z0-9_-]{0,255}$`)

// IsSeedanceAssetReference accepts opaque upstream asset IDs, never network URLs.
func IsSeedanceAssetReference(raw string) bool { return seedanceAssetReferencePattern.MatchString(raw) }

func hasNewtokenOfficialReferences(content []VideoContent, raw map[string]any) bool {
	for _, key := range []string{"draft", "omni_reference_task_type"} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	for _, item := range content {
		if item.Type == "draft_task" {
			return true
		}
		for _, ref := range []*VideoContentURL{item.ImageURL, item.VideoURL, item.AudioURL} {
			if ref != nil && IsSeedanceAssetReference(strings.TrimSpace(ref.URL)) {
				return true
			}
		}
	}
	return false
}

func validateNewtokenOfficialOptions(req *VideoCreateRequest) error {
	model := strings.TrimSpace(req.Model)
	if !IsSeedanceModel(model) {
		return nil
	}
	bad := func() error {
		return videoBadRequest("invalid_video_options", "Invalid Seedance official protocol options")
	}
	for _, field := range []string{"draft", "watermark", "return_last_frame"} {
		if value, ok := req.Raw[field]; ok {
			if _, valid := value.(bool); !valid {
				return bad()
			}
		}
	}
	for _, field := range []string{"seed", "priority", "execution_expires_after"} {
		if value, ok := req.Raw[field]; ok {
			switch v := value.(type) {
			case float64:
				if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
					return bad()
				}
			case int, int64:
			default:
				return bad()
			}
		}
	}
	if value, ok := req.Raw["tools"]; ok {
		if _, valid := value.([]any); !valid {
			return bad()
		}
	}
	if _, ok := req.Raw["draft"]; ok && model != VideoModelSeedance25 {
		return bad()
	}
	for _, item := range req.Content {
		if strings.EqualFold(strings.TrimSpace(item.Type), "draft_task") && (model != VideoModelSeedance25 || strings.TrimSpace(stringFromMap(item.DraftTask, "id")) == "") {
			return bad()
		}
	}
	if value, ok := req.Raw["omni_reference_task_type"]; ok {
		mode, valid := value.(string)
		if !valid || model != VideoModelSeedance25 || inspectVideoContent(normalizeVideoContent(req.Content)).VideoCount == 0 {
			return bad()
		}
		switch mode {
		case "reference", "auto":
		case "edit", "extend":
			if firstNonEmptyVideoString(req.Ratio, req.AspectRatio, req.AspectRatioCamel) != "adaptive" || (mode == "edit" && req.Duration != -1) {
				return bad()
			}
			if mode == "extend" && req.Duration <= 0 {
				return bad()
			}
		default:
			return bad()
		}
	}
	return nil
}

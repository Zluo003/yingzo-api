package service

import "strings"

// jingyuVideoProviderAdapter implements Jingyu's Seedance-compatible
// /v1/video/generations API.  Jingyu keeps the resolution in the request body
// and uses a single upstream model per Seedance generation family.
type jingyuVideoProviderAdapter struct{}

func (j jingyuVideoProviderAdapter) Provider() string { return videoProviderJingyu }

func (j jingyuVideoProviderAdapter) DefaultBaseURL() string { return videoDefaultJingyuBaseURL }

func (j jingyuVideoProviderAdapter) DefaultAPIPath() string { return videoDefaultJingyuAPIPath }

func (j jingyuVideoProviderAdapter) Compatible(model, resolution string) bool {
	model = strings.TrimSpace(model)
	resolution = strings.TrimSpace(resolution)
	switch model {
	case VideoModelSeedance20:
		return strings.EqualFold(resolution, VideoResolution480P) || strings.EqualFold(resolution, VideoResolution720P) ||
			strings.EqualFold(resolution, VideoResolution1080P) || strings.EqualFold(resolution, VideoResolution4K)
	case VideoModelSeedance25:
		return strings.EqualFold(resolution, VideoResolution480P) || strings.EqualFold(resolution, VideoResolution720P)
	default:
		return false
	}
}

func (j jingyuVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !j.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	maxSeconds := videoMaxDurationSeconds
	if normalized.Model == VideoModelSeedance25 {
		maxSeconds = videoSeedance25MaxDuration
	}
	if normalized.GeneratedSeconds < videoMinDurationSeconds || normalized.GeneratedSeconds > maxSeconds {
		return false
	}
	if normalized.RatioProvided && !videoRequestRatioAllowed(normalized.Model, normalized.Ratio) {
		return false
	}
	stats := inspectVideoContent(normalized.Content)
	if stats.ImageCount > 0 || stats.VideoCount > 0 || stats.AudioCount > 0 {
		switch normalized.AbilityCode {
		case videoAbilityImageToVideo:
			if stats.ImageCount != 1 || stats.FirstFrameCount != 1 || stats.VideoCount > 0 || stats.AudioCount > 0 {
				return false
			}
		case videoAbilityStartEndToVideo:
			if stats.ImageCount != 2 || stats.FirstFrameCount != 1 || stats.LastFrameCount != 1 || stats.VideoCount > 0 || stats.AudioCount > 0 {
				return false
			}
		case videoAbilityReferenceToVideo:
			maxImages, maxVideos, maxAudios := 9, 3, 3
			if normalized.Model == VideoModelSeedance25 {
				maxImages, maxVideos, maxAudios = 30, 10, 10
			}
			if stats.ImageCount > maxImages || stats.VideoCount > maxVideos || stats.AudioCount > maxAudios {
				return false
			}
			if normalized.Model != VideoModelSeedance25 && stats.ImageCount+stats.VideoCount == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// defaultJingyuUpstreamModel is kept as a named helper for migrations and
// account tooling that need to display the canonical mapping without creating
// an adapter instance.
func defaultJingyuUpstreamModel(model string) string {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20:
		return videoJingyuSeedance20Model
	case VideoModelSeedance25:
		return videoJingyuSeedance25Model
	default:
		return ""
	}
}

func (j jingyuVideoProviderAdapter) UpstreamModel(account *Account, normalized *normalizedVideoRequest) string {
	if normalized == nil {
		return ""
	}
	if mapped := resolvedMappedVideoModel(account, normalized.Model); mapped != "" {
		return mapped
	}
	return defaultJingyuUpstreamModel(normalized.Model)
}

func (j jingyuVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	model := strings.TrimSpace(upstreamModel)
	if model == "" {
		model = j.UpstreamModel(nil, normalized)
	}
	duration := any(normalized.GeneratedSeconds)
	// Jingyu documents -1 as smart duration for Seedance 2.5.
	if normalized.Model == VideoModelSeedance25 && normalized.RequestedDuration == -1 {
		duration = normalized.RequestedDuration
	}
	body := map[string]any{
		"model":      model,
		"prompt":     normalized.Prompt,
		"duration":   duration,
		"resolution": strings.ToLower(normalized.Resolution),
	}
	if normalized.RatioProvided {
		body["aspect_ratio"] = normalized.Ratio
	}
	if normalized.GenerateAudio != nil {
		body["generate_audio"] = *normalized.GenerateAudio
	}
	if normalized.Raw != nil {
		if seed, ok := normalized.Raw["seed"]; ok && seed != nil {
			body["seed"] = seed
		}
	}
	if refs := jingyuReferencesFromContent(normalized.Content, normalized.AbilityCode); len(refs) > 0 {
		body["references"] = refs
	}
	return body
}

func (j jingyuVideoProviderAdapter) ResultURL(_ string, _ string, payload map[string]any) string {
	return videoResultURLFromPayload(payload)
}

func (j jingyuVideoProviderAdapter) ResultAuthorization(*Account) string { return "" }

func (j jingyuVideoProviderAdapter) PollMaxConsecutiveFailures() int { return 1 }

func jingyuReferencesFromContent(content []VideoContent, ability string) []map[string]any {
	out := make([]map[string]any, 0, len(content))
	imageIndex := 0
	for _, item := range content {
		var typ, role, rawURL string
		switch item.Type {
		case "image_url":
			if item.ImageURL == nil {
				continue
			}
			typ, rawURL = "image", item.ImageURL.URL
			switch ability {
			case videoAbilityImageToVideo:
				role = "first_frame"
			case videoAbilityStartEndToVideo:
				if imageIndex == 0 {
					role = "first_frame"
				} else {
					role = "last_frame"
				}
			case videoAbilityReferenceToVideo:
				role = "reference_image"
			}
			imageIndex++
		case "video_url":
			if item.VideoURL == nil {
				continue
			}
			typ, rawURL, role = "video", item.VideoURL.URL, "reference_video"
		case "audio_url":
			if item.AudioURL == nil {
				continue
			}
			typ, rawURL, role = "audio", item.AudioURL.URL, "reference_audio"
		default:
			continue
		}
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			continue
		}
		if item.Role != "" && role == "" {
			role = item.Role
		}
		out = append(out, map[string]any{"type": typ, "role": role, "url": rawURL})
	}
	return out
}

package service

import "strings"

const (
	// Reference-video caps published by the newtoken Seedance API: the 2.0 family
	// accepts @Video1–@Video3 while 2.5 accepts @Video1–@Video10.
	newtokenMaxSeedance20ReferenceVideos = 3
	newtokenMaxSeedance25ReferenceVideos = 10
	// When a first/last frame is supplied, newtoken caps the combined image count
	// (frames plus ordinary references) at two.
	newtokenMaxFramedImages = 2
)

type newtokenVideoProviderAdapter struct{}

func (n newtokenVideoProviderAdapter) Provider() string {
	return videoProviderNewtoken
}

func (n newtokenVideoProviderAdapter) DefaultBaseURL() string {
	return videoDefaultNewtokenBaseURL
}

func (n newtokenVideoProviderAdapter) DefaultAPIPath() string {
	return videoDefaultAPIPath
}

func (n newtokenVideoProviderAdapter) Compatible(model, resolution string) bool {
	return videoNewtokenUpstreamModel(model, resolution) != ""
}

func (n newtokenVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !n.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	// 画幅是渠道能力，在这里判定（与 aigod 共用当前 Seedance 渠道的取值）。
	// 仅在显式提供时校验：未提供时上游按默认值处理。
	if normalized.RatioProvided && !videoRequestRatioAllowed(normalized.Model, normalized.Ratio) {
		return false
	}

	maxReferenceVideos := newtokenMaxSeedance20ReferenceVideos
	switch normalized.Model {
	case VideoModelSeedance20, VideoModelSeedance20Fast:
		if normalized.GeneratedSeconds < videoMinDurationSeconds || normalized.GeneratedSeconds > videoMaxDurationSeconds {
			return false
		}
	case VideoModelSeedance25:
		if normalized.GeneratedSeconds < videoMinDurationSeconds || normalized.GeneratedSeconds > videoSeedance25MaxDuration {
			return false
		}
		maxReferenceVideos = newtokenMaxSeedance25ReferenceVideos
	}

	stats := inspectVideoContent(normalized.Content)
	if stats.VideoCount > maxReferenceVideos {
		return false
	}
	// A trailing frame is only meaningful alongside a leading frame.
	if stats.LastFrameCount > 0 && stats.FirstFrameCount == 0 {
		return false
	}
	if stats.FirstFrameCount+stats.LastFrameCount > 0 {
		if stats.ImageCount > newtokenMaxFramedImages || stats.VideoCount > 0 {
			return false
		}
	}
	return true
}

func (n newtokenVideoProviderAdapter) UpstreamModel(account *Account, normalized *normalizedVideoRequest) string {
	if normalized == nil {
		return ""
	}
	if mappedModel := resolvedMappedVideoModel(account, normalized.Model); mappedModel != "" {
		return mappedModel
	}
	return videoNewtokenUpstreamModel(normalized.Model, normalized.Resolution)
}

func (n newtokenVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	body := map[string]any{
		"model":      upstreamModel,
		"prompt":     normalized.Prompt,
		"duration":   normalized.GeneratedSeconds,
		"resolution": strings.ToLower(normalized.Resolution),
	}
	if normalized.RatioProvided {
		body["aspect_ratio"] = normalized.Ratio
	}
	for field, urls := range newtokenMediaReferences(normalized.Content) {
		switch field {
		case "first_frame", "last_frame":
			body[field] = urls[0]
		default:
			body[field] = urls
		}
	}
	return body
}

// videoNewtokenUpstreamModel is the single source of truth for newtoken model
// routing. newtoken bakes the output resolution into the upstream model id, so
// the downstream model alone cannot select it. An unsupported pair returns "",
// which makes the account ineligible during scheduling.
func videoNewtokenUpstreamModel(model, resolution string) string {
	model = strings.TrimSpace(model)
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	switch model {
	case VideoModelSeedance20:
		switch resolution {
		case VideoResolution720P:
			return videoNewtokenSeedance20720PModel
		case VideoResolution1080P:
			return videoNewtokenSeedance201080PModel
		}
	case VideoModelSeedance20Fast:
		if resolution == VideoResolution720P {
			return videoNewtokenSeedance20Fast720PModel
		}
	case VideoModelSeedance25:
		switch resolution {
		case VideoResolution720P:
			return videoNewtokenSeedance25720PModel
		case VideoResolution1080P:
			return videoNewtokenSeedance251080PModel
		}
	}
	return ""
}

// newtokenMediaReferences groups canonical request content into the URL fields
// newtoken expects. Frame roles map to their own scalar fields; everything else
// is collected into the extra_* arrays.
func newtokenMediaReferences(content []VideoContent) map[string][]string {
	fields := make(map[string][]string, 5)
	appendURL := func(field, rawURL string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		// Only the first frame reference of each role is honoured upstream.
		if (field == "first_frame" || field == "last_frame") && len(fields[field]) > 0 {
			return
		}
		fields[field] = append(fields[field], rawURL)
	}
	for _, item := range content {
		switch item.Type {
		case "image_url":
			if item.ImageURL == nil {
				continue
			}
			field := "extra_images"
			switch item.Role {
			case "first_frame":
				field = "first_frame"
			case "last_frame":
				field = "last_frame"
			}
			appendURL(field, item.ImageURL.URL)
		case "video_url":
			if item.VideoURL == nil {
				continue
			}
			appendURL("extra_videos", item.VideoURL.URL)
		case "audio_url":
			if item.AudioURL == nil {
				continue
			}
			appendURL("extra_audios", item.AudioURL.URL)
		}
	}
	return fields
}

// isSeedanceAspectRatio 报告 ratio 是否在 Seedance 渠道支持的画幅白名单内。
// aigod 与 newtoken 的文档列出的是同一组取值（21:9 / 16:9 / 4:3 / 1:1 / 3:4 / 9:16），
// 因此共用一份，避免两边各写一套后漂移。
func isSeedanceAspectRatio(ratio string) bool {
	switch strings.ToLower(strings.TrimSpace(ratio)) {
	case "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
		return true
	default:
		return false
	}
}

// ResultURL：newtoken 的状态响应自带成片地址，交给通用解析。
func (n newtokenVideoProviderAdapter) ResultURL(string, string, map[string]any) string {
	return ""
}

// ResultAuthorization：newtoken 的成片地址自带授权，不需要额外请求头。
func (n newtokenVideoProviderAdapter) ResultAuthorization(*Account) string {
	return ""
}

// PollMaxConsecutiveFailures：保持既有轮询容错。
func (n newtokenVideoProviderAdapter) PollMaxConsecutiveFailures() int {
	return 1
}

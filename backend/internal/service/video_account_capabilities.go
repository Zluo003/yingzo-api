package service

import (
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// VideoModelCapabilitiesExtraKey narrows the media and generation modes an
// individual video account can serve. Missing models/fields keep the existing
// model-level limits and enabled modes for older accounts.
const VideoModelCapabilitiesExtraKey = "video_model_capabilities"

var videoAccountCountFields = map[string]func(videoModelSpec) int{
	"max_reference_images": func(spec videoModelSpec) int { return spec.MaxRefImages },
	"max_reference_videos": func(spec videoModelSpec) int { return spec.MaxRefVideos },
	"max_reference_audios": func(spec videoModelSpec) int { return spec.MaxRefAudios },
}

var videoAccountModeFields = map[string]struct{}{
	"text_to_video":      {},
	"image_to_video":     {},
	"start_end_to_video": {},
	"reference_to_video": {},
}

func normalizeVideoModelCapabilitiesExtra(extra map[string]any) error {
	raw, exists := extra[VideoModelCapabilitiesExtraKey]
	if !exists || raw == nil {
		delete(extra, VideoModelCapabilitiesExtraKey)
		return nil
	}
	byModel, ok := raw.(map[string]any)
	if !ok {
		return invalidVideoModelCapabilities("video_model_capabilities must be an object")
	}
	normalized := make(map[string]any, len(byModel))
	for rawModel, rawSettings := range byModel {
		model := strings.TrimSpace(rawModel)
		spec, known := videoSpecForModel(model)
		if !known || model == "" {
			return invalidVideoModelCapabilities("Unsupported video model: " + model)
		}
		settings, ok := rawSettings.(map[string]any)
		if !ok {
			return invalidVideoModelCapabilities(model + " capabilities must be an object")
		}
		clean := make(map[string]any, len(settings))
		for field, value := range settings {
			if maxForSpec, isCount := videoAccountCountFields[field]; isCount {
				count := numericVideoSeconds(value)
				if count < 0 || count > maxForSpec(spec) || float64(count) != toFloat(value) {
					return invalidVideoModelCapabilities(fmt.Sprintf("%s.%s must be an integer between 0 and %d", model, field, maxForSpec(spec)))
				}
				clean[field] = count
				continue
			}
			if _, isMode := videoAccountModeFields[field]; isMode {
				mode, ok := value.(bool)
				if !ok {
					return invalidVideoModelCapabilities(model + "." + field + " must be a boolean")
				}
				clean[field] = mode
				continue
			}
			return invalidVideoModelCapabilities("Unknown video account capability: " + field)
		}
		if len(clean) > 0 {
			normalized[model] = clean
		}
	}
	if len(normalized) == 0 {
		delete(extra, VideoModelCapabilitiesExtraKey)
	} else {
		extra[VideoModelCapabilitiesExtraKey] = normalized
	}
	return nil
}

func invalidVideoModelCapabilities(message string) error {
	return infraerrors.BadRequest("invalid_video_model_capabilities", message)
}

func videoAccountSupportsRequest(account *Account, request *normalizedVideoRequest) bool {
	if account == nil || request == nil {
		return false
	}
	if account.Extra == nil {
		return true
	}
	models, ok := account.Extra[VideoModelCapabilitiesExtraKey].(map[string]any)
	if !ok {
		return true
	}
	settings, ok := models[request.Model].(map[string]any)
	if !ok {
		return true
	}
	mode := ""
	switch request.AbilityCode {
	case videoAbilityTextToVideo:
		mode = "text_to_video"
	case videoAbilityImageToVideo:
		mode = "image_to_video"
	case videoAbilityStartEndToVideo:
		mode = "start_end_to_video"
	case videoAbilityReferenceToVideo:
		mode = "reference_to_video"
	}
	if enabled, exists := settings[mode].(bool); mode != "" && exists && !enabled {
		return false
	}
	stats := inspectVideoContent(request.Content)
	counts := map[string]int{
		"max_reference_images": stats.ImageCount,
		"max_reference_videos": stats.VideoCount,
		"max_reference_audios": stats.AudioCount,
	}
	for field, used := range counts {
		if limit, exists := settings[field]; exists && used > numericVideoSeconds(limit) {
			return false
		}
	}
	return true
}

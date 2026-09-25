package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeVideoModelCapabilitiesExtra(t *testing.T) {
	extra, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelCapabilitiesExtraKey: map[string]any{
			VideoModelSeedance20: map[string]any{
				"max_reference_images": 9.0,
				"max_reference_videos": 0.0,
				"max_reference_audios": 3.0,
				"text_to_video":        true,
				"image_to_video":       false,
				"start_end_to_video":   true,
				"reference_to_video":   true,
			},
		},
	})
	require.NoError(t, err)
	byModel := extra[VideoModelCapabilitiesExtraKey].(map[string]any)
	settings := byModel[VideoModelSeedance20].(map[string]any)
	require.Equal(t, 0, settings["max_reference_videos"])
	require.False(t, settings["image_to_video"].(bool))
}

func TestNormalizeVideoModelCapabilitiesExtraRejectsInvalidLimits(t *testing.T) {
	_, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelCapabilitiesExtraKey: map[string]any{
			VideoModelSeedance20: map[string]any{
				"max_reference_images": 10,
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid_video_model_capabilities")
}

func TestVideoAccountSupportsRequestHonoursZeroMediaAndModes(t *testing.T) {
	account := &Account{
		Platform: PlatformVideo,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			VideoModelCapabilitiesExtraKey: map[string]any{
				VideoModelSeedance20: map[string]any{
					"max_reference_images": 9,
					"max_reference_videos": 0,
					"max_reference_audios": 3,
					"text_to_video":        true,
					"image_to_video":       true,
					"start_end_to_video":   true,
					"reference_to_video":   true,
				},
			},
		},
	}
	video := &normalizedVideoRequest{
		Model:       VideoModelSeedance20,
		AbilityCode: videoAbilityReferenceToVideo,
		Content: []VideoContent{{
			Type:     "video_url",
			Role:     "reference_video",
			VideoURL: &VideoContentURL{URL: "https://cdn.example/video.mp4"},
		}},
	}
	require.False(t, videoAccountSupportsRequest(account, video))

	video.Content = []VideoContent{{
		Type:     "image_url",
		Role:     "reference_image",
		ImageURL: &VideoContentURL{URL: "https://cdn.example/image.png"},
	}}
	require.True(t, videoAccountSupportsRequest(account, video))

	account.Extra[VideoModelCapabilitiesExtraKey].(map[string]any)[VideoModelSeedance20].(map[string]any)["image_to_video"] = false
	video.AbilityCode = videoAbilityImageToVideo
	video.Content[0].Role = "first_frame"
	require.False(t, videoAccountSupportsRequest(account, video))
}

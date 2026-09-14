package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Jingyu is a separate upstream contract. Keep these checks independent from
// the callback lifecycle so a provider dispatch regression is caught before an
// end-to-end request is attempted against the live service.
func TestJingyuAdapterUsesJingyuEndpointAndModelCatalog(t *testing.T) {
	adapter := videoProviderAdapterByName("jingyu")

	require.Equal(t, "jingyu", adapter.Provider())
	require.Equal(t, "https://api.jingyuapi.art", adapter.DefaultBaseURL())
	require.Equal(t, "/v1/video/generations", adapter.DefaultAPIPath())

	// Jingyu's Seedance catalog is narrower than the shared model catalog:
	// 2.0 serves all four resolutions, while 2.5 currently serves 480p/720p.
	for _, resolution := range []string{VideoResolution480P, VideoResolution720P, VideoResolution1080P, VideoResolution4K} {
		require.True(t, adapter.Compatible(VideoModelSeedance20, resolution), "2.0 @ %s", resolution)
	}
	for _, resolution := range []string{VideoResolution480P, VideoResolution720P} {
		require.True(t, adapter.Compatible(VideoModelSeedance25, resolution), "2.5 @ %s", resolution)
	}
	require.False(t, adapter.Compatible(VideoModelSeedance25, VideoResolution1080P))
	require.False(t, adapter.Compatible(VideoModelSeedance25, VideoResolution4K))
	require.False(t, adapter.Compatible(VideoModelSeedance20Fast, VideoResolution720P))

	require.Equal(t, "yu-video-2-pro", adapter.UpstreamModel(nil, &normalizedVideoRequest{
		Model: VideoModelSeedance20, Resolution: VideoResolution4K,
	}))
	require.Equal(t, "yu-video-2.5-pro", adapter.UpstreamModel(nil, &normalizedVideoRequest{
		Model: VideoModelSeedance25, Resolution: VideoResolution720P,
	}))
}

func TestJingyuAdapterBuildsDocumentedRequestBody(t *testing.T) {
	adapter := videoProviderAdapterByName("jingyu")

	normalized, err := normalizeVideoCreateRequest(&VideoCreateRequest{
		Model:         VideoModelSeedance20,
		Prompt:        "a cinematic runway shot",
		Duration:      5,
		Resolution:    VideoResolution720P,
		AspectRatio:   "9:16",
		GenerateAudio: boolPtrForJingyuTest(true),
		Raw:           map[string]any{"seed": float64(12345), "extra": map[string]any{"drop": true}},
		Content: []VideoContent{
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn.example/ref.png"}},
			{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn.example/ref.mp3"}},
		},
	})
	require.NoError(t, err)

	body := adapter.BuildCreateBody(normalized, "yu-video-2-pro")
	require.Equal(t, "yu-video-2-pro", body["model"])
	require.Equal(t, "a cinematic runway shot", body["prompt"])
	require.Equal(t, 5, body["duration"])
	require.Equal(t, "720p", body["resolution"])
	require.Equal(t, "9:16", body["aspect_ratio"])
	require.Equal(t, true, body["generate_audio"])
	require.Equal(t, float64(12345), body["seed"])
	require.NotContains(t, body, "content")
	require.NotContains(t, body, "ratio")
	require.NotContains(t, body, "extra")

	references, ok := body["references"].([]map[string]any)
	require.True(t, ok)
	require.Equal(t, []map[string]any{
		{"type": "image", "role": "reference_image", "url": "https://cdn.example/ref.png"},
		{"type": "audio", "role": "reference_audio", "url": "https://cdn.example/ref.mp3"},
	}, references)
}

func TestJingyuAdapterPreservesSeedance25AutoDuration(t *testing.T) {
	adapter := videoProviderAdapterByName("jingyu")
	normalized, err := normalizeVideoCreateRequest(&VideoCreateRequest{
		Model:       VideoModelSeedance25,
		Prompt:      "follow the reference",
		Duration:    -1,
		Resolution:  VideoResolution720P,
		AbilityCode: videoAbilityReferenceToVideo,
		Content: []VideoContent{{
			Type:     "image_url",
			ImageURL: &VideoContentURL{URL: "https://cdn.example/ref.png"},
		}},
	})
	require.NoError(t, err)

	body := adapter.BuildCreateBody(normalized, "yu-video-2.5-pro")
	require.Equal(t, float64(-1), body["duration"], "Jingyu accepts -1 for automatic 2.5 duration")
	require.Equal(t, "720p", body["resolution"])
	require.NotContains(t, body, "aspect_ratio", "aspect ratio is optional for reference mode")
}

func boolPtrForJingyuTest(value bool) *bool { return &value }

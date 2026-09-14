package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJingyuProviderModelAndEndpoint(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderJingyu)
	require.Equal(t, videoProviderJingyu, a.Provider())
	require.Equal(t, "https://api.jingyuapi.art", a.DefaultBaseURL())
	require.Equal(t, "/v1/video/generations", a.DefaultAPIPath())
	require.Equal(t, videoJingyuSeedance20Model, a.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelSeedance20, Resolution: VideoResolution720P}))
	require.Equal(t, videoJingyuSeedance25Model, a.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelSeedance25, Resolution: VideoResolution720P}))
	require.True(t, a.Compatible(VideoModelSeedance20, VideoResolution4K))
	require.True(t, a.Compatible(VideoModelSeedance25, VideoResolution720P))
	require.False(t, a.Compatible(VideoModelSeedance25, VideoResolution1080P))
}

func TestJingyuProviderBuildsReferencesBody(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderJingyu)
	first := 1
	normalized := &normalizedVideoRequest{
		Model: VideoModelSeedance20, Prompt: "让人物走向镜头", Resolution: VideoResolution720P,
		GeneratedSeconds: 8, Ratio: "16:9", RatioProvided: true, AbilityCode: videoAbilityReferenceToVideo,
		Raw: map[string]any{"seed": first},
		Content: []VideoContent{
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/image.png"}},
			{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn/video.mp4"}},
			{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/audio.mp3"}},
		},
	}
	body := a.BuildCreateBody(normalized, "yu-video-2-pro")
	require.Equal(t, "yu-video-2-pro", body["model"])
	require.Equal(t, "720p", body["resolution"])
	require.Equal(t, "16:9", body["aspect_ratio"])
	require.Equal(t, first, body["seed"])
	require.Equal(t, []map[string]any{
		{"type": "image", "role": "reference_image", "url": "https://cdn/image.png"},
		{"type": "video", "role": "reference_video", "url": "https://cdn/video.mp4"},
		{"type": "audio", "role": "reference_audio", "url": "https://cdn/audio.mp3"},
	}, body["references"])
}

func TestNormalizeVideoProviderExtraAcceptsJingyu(t *testing.T) {
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: " Jingyu "})
	require.NoError(t, err)
	require.Equal(t, videoProviderJingyu, normalized[VideoProviderExtraKey])
}

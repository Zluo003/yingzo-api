package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func klingV4References(images, videos, audios int, seconds float64) []VideoContent {
	var content []VideoContent
	for i := 0; i < images; i++ {
		content = append(content, VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/ref.png"}})
	}
	for i := 0; i < videos; i++ {
		content = append(content, VideoContent{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn/ref.mp4"}, DurationSeconds: &seconds})
	}
	for i := 0; i < audios; i++ {
		content = append(content, VideoContent{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/ref.mp3"}})
	}
	return content
}

func TestMikuapiKlingV4CatalogAndRouting(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	require.Contains(t, SupportedVideoModels(), "kling-v4")
	require.False(t, IsSupportedVideoModel("kling-video-v4"))
	require.Equal(t, secondsRange(3, 30), SupportedVideoDurations(VideoModelKlingV4))
	require.Equal(t, []string{"720p", "1080p"}, SupportedVideoResolutions(VideoModelKlingV4))
	for _, resolution := range []string{"720p", "1080p"} {
		n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: VideoModelKlingV4, Prompt: "a boat drifting", Duration: 30, Resolution: resolution})
		require.NoError(t, err)
		require.True(t, adapter.CompatibleRequest(n))
		require.Equal(t, "kling-video-v4", adapter.UpstreamModel(nil, n))
		account := &Account{Credentials: map[string]any{"model_mapping": map[string]any{VideoModelKlingV4: VideoModelKlingV4}}}
		require.Equal(t, "kling-video-v4", adapter.UpstreamModel(account, n))
		account.Credentials["model_mapping"] = map[string]any{VideoModelKlingV4: "kling-video-v4-custom"}
		require.Equal(t, "kling-video-v4-custom", adapter.UpstreamModel(account, n))
	}
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken, videoProviderJingyu, videoProviderXingguang, videoProviderApimart} {
		require.False(t, videoProviderAdapterByName(provider).Compatible(VideoModelKlingV4, VideoResolution720P))
	}
	require.Equal(t, "https://mikuapi.org/v1/videos", adapter.CreateEndpoint("https://mikuapi.org/v1/videos", "kling-video-v4"))
	require.Empty(t, adapter.ResultAuthorization(&Account{Credentials: map[string]any{"api_key": "secret"}}, "kling-video-v4"))
	for _, seconds := range []float64{2, 31, 3.5, -1} {
		_, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: VideoModelKlingV4, Prompt: "boat", Duration: seconds})
		require.ErrorContains(t, err, "invalid_video_duration")
	}
	for _, resolution := range []string{"480p", "4K"} {
		_, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: VideoModelKlingV4, Prompt: "boat", Duration: 3, Resolution: resolution})
		require.ErrorContains(t, err, "invalid_video_resolution")
	}
}

func TestMikuapiKlingV4ModesAndBodies(t *testing.T) {
	first := VideoContent{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/start.png"}}
	last := VideoContent{Type: "image_url", Role: "last_frame", ImageURL: &VideoContentURL{URL: "https://cdn/end.png"}}
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	for _, tc := range []struct {
		name    string
		content []VideoContent
		ability string
		fields  map[string]any
	}{
		{"text", nil, videoAbilityTextToVideo, nil},
		{"first frame", []VideoContent{first}, videoAbilityImageToVideo, map[string]any{"input_reference": "https://cdn/start.png"}},
		{"first and last", []VideoContent{first, last}, videoAbilityStartEndToVideo, map[string]any{"input_reference": "https://cdn/start.png", "image_end": "https://cdn/end.png"}},
		{"references", klingV4References(1, 1, 1, 30), videoAbilityReferenceToVideo, map[string]any{
			"reference_images": []map[string]any{{"url": "https://cdn/ref.png"}},
			"reference_videos": []string{"https://cdn/ref.mp4"},
			"reference_audios": []string{"https://cdn/ref.mp3"},
		}},
	} {
		for _, ratio := range []string{"16:9", "9:16", "1:1", "21:9"} {
			t.Run(tc.name+"/"+ratio, func(t *testing.T) {
				n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: VideoModelKlingV4, Prompt: "boat", Duration: 3, Resolution: "1080p", Ratio: ratio, Content: tc.content})
				require.NoError(t, err)
				require.Equal(t, tc.ability, n.AbilityCode)
				require.True(t, adapter.CompatibleRequest(n))
				body := map[string]any{"model": "kling-video-v4", "prompt": "boat", "seconds": 3, "enable_audio": true, "resolution": "1080p", "aspect_ratio": ratio}
				for k, v := range tc.fields {
					body[k] = v
				}
				require.Equal(t, body, adapter.BuildCreateBody(n, adapter.UpstreamModel(nil, n)))
			})
		}
	}
	for _, content := range [][]VideoContent{{last}, {first, first}, {first, klingV4References(1, 0, 0, 0)[0]}} {
		n := &normalizedVideoRequest{Model: VideoModelKlingV4, Prompt: "boat", Resolution: "720p", GeneratedSeconds: 3, Content: content}
		require.False(t, adapter.CompatibleRequest(n), "invalid/mixed frame modes must not be silently rewritten")
	}
}

func TestMikuapiKlingV4ReferenceLimits(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		images, videos, audios int
		seconds                float64
		valid                  bool
	}{
		{"ten images", 10, 0, 0, 0, true},
		{"too many images", 11, 0, 0, 0, false},
		{"five videos", 0, 5, 0, 6, true},
		{"too many videos", 0, 6, 0, 3, false},
		{"three audios", 1, 0, 3, 0, true},
		{"too many audios", 1, 0, 4, 0, false},
		{"fifteen materials", 10, 2, 3, 15, true},
		{"too many materials", 10, 3, 3, 10, false},
		{"minimum clip", 0, 1, 0, 3, true},
		{"short clip", 0, 1, 0, 2.99, false},
		{"maximum clip", 0, 1, 0, 30, true},
		{"long clip", 0, 1, 0, 30.01, false},
		{"total above limit", 0, 2, 0, 15.01, false},
		{"fractional total below limit", 0, 5, 0, 5.9, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &VideoCreateRequest{Model: VideoModelKlingV4, Prompt: "boat", Duration: 3, Content: klingV4References(tc.images, tc.videos, tc.audios, tc.seconds)}
			n, err := normalizeVideoCreateRequest(req)
			if tc.valid {
				require.NoError(t, err)
				require.True(t, videoProviderAdapterByName(videoProviderMikuapi).CompatibleRequest(n))
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMikuapiKlingV4ConfirmedUpstreamProtocol(t *testing.T) {
	enableAudio := true
	n, err := normalizeVideoCreateRequest(&VideoCreateRequest{
		Model:         VideoModelKlingV4,
		Prompt:        "参考视频1的风格和镜头运动，生成新的3秒视频",
		Duration:      3,
		Resolution:    VideoResolution720P,
		GenerateAudio: &enableAudio,
		Content: []VideoContent{
			{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://example.com/reference.mp4"}, DurationSeconds: videoFloat64Ptr(3)},
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://example.com/reference.png"}},
		},
	})
	require.NoError(t, err)
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	require.True(t, adapter.CompatibleRequest(n))
	body := adapter.BuildCreateBody(n, adapter.UpstreamModel(nil, n))
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"kling-video-v4",
		"prompt":"参考视频1的风格和镜头运动，生成新的3秒视频",
		"seconds":3,
		"resolution":"720p",
		"reference_videos":["https://example.com/reference.mp4"],
		"reference_images":[{"url":"https://example.com/reference.png"}],
		"enable_audio":true
	}`, string(encoded))
	enableAudio = false
	require.Equal(t, false, adapter.BuildCreateBody(n, "kling-video-v4")["enable_audio"])
	n.GenerateAudio = nil
	require.Equal(t, true, adapter.BuildCreateBody(n, "kling-video-v4")["enable_audio"])
}

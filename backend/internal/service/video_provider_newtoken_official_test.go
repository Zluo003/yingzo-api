package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func officialRequest(t *testing.T, body string) *normalizedVideoRequest {
	t.Helper()
	var req VideoCreateRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	require.NoError(t, json.Unmarshal([]byte(body), &req.Raw))
	r, err := normalizeVideoCreateRequest(&req)
	require.NoError(t, err)
	return r
}

func TestNewtokenOfficialModelsAndAccountConfiguration(t *testing.T) {
	a := videoProviderAdapterByName("newtoken_official")
	require.Equal(t, "newtoken_official", a.Provider())
	require.Equal(t, "https://newtoken.club", a.DefaultBaseURL())
	require.Equal(t, "/api/v3/contents/generations/tasks", a.DefaultAPIPath())
	for _, tc := range []struct {
		model, id   string
		resolutions []string
	}{
		{VideoModelSeedance20, "doubao-seedance-2-0-260128", []string{"480p", "720p", "1080p", "4K"}},
		{VideoModelSeedance20Fast, "doubao-seedance-2-0-fast-260128", []string{"480p", "720p"}},
		{VideoModelSeedance20Mini, "doubao-seedance-2-0-mini-260615", []string{"480p", "720p"}},
		{VideoModelSeedance25, "doubao-seedance-2-5-260628", []string{"480p", "720p", "1080p"}},
	} {
		for _, resolution := range tc.resolutions {
			r := &normalizedVideoRequest{Model: tc.model, Resolution: resolution, Prompt: "move", GeneratedSeconds: 4}
			require.True(t, a.CompatibleRequest(r), "%s %s", tc.model, resolution)
			require.Equal(t, tc.id, a.UpstreamModel(nil, r))
		}
	}
	require.False(t, a.Compatible(VideoModelSeedance20Mini, "1080p"))
	require.False(t, a.Compatible(VideoModelGrokImagineVideo15, "720p"))
	require.False(t, newtokenVideoProviderAdapter{}.Compatible(VideoModelSeedance20Mini, "720p"))
	extra, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		"video_provider": "newtoken_official", "base_url": "https://custom.example", "api_path": "/custom/tasks", "poll_interval_ms": 7000,
		VideoModelResolutionsExtraKey:  map[string]any{VideoModelSeedance20Mini: []any{"720p"}},
		VideoModelDurationsExtraKey:    map[string]any{VideoModelSeedance20Mini: []any{8}},
		VideoModelCapabilitiesExtraKey: map[string]any{VideoModelSeedance20Mini: map[string]any{"max_reference_images": 1}},
	})
	require.NoError(t, err)
	account := &Account{Extra: extra, Credentials: map[string]any{"model_mapping": map[string]any{VideoModelSeedance20Mini: "custom-mini"}}}
	r := officialRequest(t, `{"model":"seedance-2.0-mini","prompt":"move","duration":8}`)
	require.Equal(t, "custom-mini", a.UpstreamModel(account, r))
	require.True(t, videoAccountSupportsResolution(account, r.Model, "720p"))
	require.False(t, videoAccountSupportsResolution(account, r.Model, "480p"))
	require.True(t, videoAccountSupportsDuration(account, r.Model, 8))
	require.False(t, videoAccountSupportsDuration(account, r.Model, 9))
	r.Content = []VideoContent{{Type: "image_url"}, {Type: "image_url"}}
	require.False(t, videoAccountSupportsRequest(account, r))
	endpoint, err := videoAccountEndpoint(account)
	require.NoError(t, err)
	require.Equal(t, "https://custom.example/custom/tasks", endpoint)
	require.Equal(t, 7*time.Second, videoAccountDuration(account, "poll_interval_ms", 0))
	require.Equal(t, 5*time.Minute, videoAccountDefaultDuration(account, "request_timeout_ms"))
}

func TestNewtokenOfficialContentAndOptions(t *testing.T) {
	r := officialRequest(t, `{"model":"seedance-2.5","content":[
        {"type":"text","text":"animate the references"},
        {"type":"image_url","role":"reference_image","image_url":{"url":"https://cdn.example/a%20b.jpg?signature=unchanged"}},
        {"type":"video_url","video_url":{"url":"https://cdn.example/a.mp4"}},
        {"type":"audio_url","audio_url":{"url":"asset://ast-audio"}},
        {"type":"draft_task","draft_task":{"id":"ultra720_draft","extra":"preserved"}}
    ],"draft":false,"seed":42,"generate_audio":false,"watermark":false,"return_last_frame":true,
    "priority":0,"execution_expires_after":3600,"tools":[],"safety_identifier":"caller","omni_reference_task_type":"reference"}`)
	a := newtokenOfficialVideoProviderAdapter{}
	require.True(t, a.CompatibleRequest(r))
	body := a.BuildCreateBody(r, a.UpstreamModel(nil, r))
	require.Equal(t, 4, body["duration"])
	require.Equal(t, "720p", body["resolution"])
	require.NotContains(t, body, "ratio")
	require.NotContains(t, body, "prompt")
	require.NotContains(t, body, "extra_images")
	for _, field := range []string{"draft", "generate_audio", "watermark"} {
		require.Equal(t, false, body[field])
	}
	require.Equal(t, float64(42), body["seed"])
	require.Equal(t, float64(0), body["priority"])
	require.Equal(t, float64(3600), body["execution_expires_after"])
	require.Equal(t, []any{}, body["tools"])
	require.Equal(t, true, body["return_last_frame"])
	require.Equal(t, "caller", body["safety_identifier"])
	content, ok := body["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, content, 5)
	require.Equal(t, map[string]any{"url": "https://cdn.example/a%20b.jpg?signature=unchanged"}, content[1]["image_url"])
	require.Equal(t, "reference_video", content[2]["role"])
	require.Equal(t, map[string]any{"url": "asset://ast-audio"}, content[3]["audio_url"])
	require.Equal(t, map[string]any{"id": "ultra720_draft", "extra": "preserved"}, content[4]["draft_task"])
	for _, item := range content {
		require.NotContains(t, item, "subject_type")
		require.NotContains(t, item, "duration_seconds")
	}
	plain := officialRequest(t, `{"model":"seedance-2.0-mini","prompt":"move","duration":4}`)
	require.NotContains(t, a.BuildCreateBody(plain, a.UpstreamModel(nil, plain)), "draft")
}

func TestNewtokenOfficialVideoModesAndRouting(t *testing.T) {
	a := newtokenOfficialVideoProviderAdapter{}
	for _, tc := range []struct {
		mode, ratio string
		duration    int
	}{{"edit", "adaptive", -1}, {"extend", "adaptive", 30}, {"reference", "16:9", 6}, {"auto", "9:16", 6}} {
		body, err := json.Marshal(map[string]any{"model": VideoModelSeedance25, "prompt": "move", "ratio": tc.ratio, "duration": tc.duration, "omni_reference_task_type": tc.mode, "content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/ref.mp4"}}}})
		require.NoError(t, err)
		r := officialRequest(t, string(body))
		require.True(t, a.CompatibleRequest(r))
		out := a.BuildCreateBody(r, a.UpstreamModel(nil, r))
		require.Equal(t, tc.duration, out["duration"])
		require.Equal(t, tc.ratio, out["ratio"])
		for _, provider := range supportedVideoProviders {
			account := &Account{Extra: map[string]any{"video_provider": provider}}
			require.Equal(t, provider == videoProviderNewtokenOfficial, isVideoAccountCompatibleForRequest(account, r), provider)
		}
	}
	for _, raw := range []string{
		`{"model":"seedance-2.0","prompt":"move","duration":4,"draft":true}`,
		`{"model":"seedance-2.5","prompt":"move","duration":4,"draft":"false"}`,
		`{"model":"seedance-2.5","prompt":"move","duration":4,"content":[{"type":"draft_task","draft_task":{}}]}`,
		`{"model":"seedance-2.5","prompt":"move","duration":4,"omni_reference_task_type":"edit"}`,
		`{"model":"seedance-2.5","prompt":"move","duration":-1,"ratio":"16:9","omni_reference_task_type":"edit","content":[{"type":"video_url","video_url":{"url":"https://cdn.example/a.mp4"}}]}`,
		`{"model":"seedance-2.5","prompt":"move","duration":4,"seed":1.5}`,
		`{"model":"seedance-2.5","prompt":"move","duration":-1,"ratio":"adaptive","omni_reference_task_type":"extend","content":[{"type":"video_url","video_url":{"url":"https://cdn.example/a.mp4"}}]}`,
	} {
		var req VideoCreateRequest
		require.NoError(t, json.Unmarshal([]byte(raw), &req))
		require.NoError(t, json.Unmarshal([]byte(raw), &req.Raw))
		_, err := normalizeVideoCreateRequest(&req)
		require.Error(t, err, raw)
	}
	r := officialRequest(t, `{"model":"seedance-2.5","prompt":"move","duration":8,"content":[{"type":"image_url","role":"first_frame","image_url":{"url":"https://cdn.example/first.jpg"}},{"type":"image_url","role":"last_frame","image_url":{"url":"https://cdn.example/last.jpg"}}]}`)
	require.True(t, a.CompatibleRequest(r))
	r.Content = append(r.Content, VideoContent{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn.example/ref.mp4"}})
	require.False(t, a.CompatibleRequest(r))
	autoDuration := officialRequest(t, `{"model":"seedance-2.5","prompt":"move","duration":-1}`)
	require.False(t, a.CompatibleRequest(autoDuration))
	asset := officialRequest(t, `{"model":"seedance-2.0","prompt":"move","duration":4,"content":[{"type":"image_url","role":"reference_image","image_url":{"url":"asset://ast-image"}}]}`)
	require.True(t, isVideoAccountCompatibleForRequest(&Account{Extra: map[string]any{"video_provider": videoProviderNewtokenOfficial}}, asset))
	require.False(t, isVideoAccountCompatibleForRequest(&Account{Extra: map[string]any{"video_provider": videoProviderNewtoken}}, asset))
}

func TestNewtokenOfficialHTTPLifecycle(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer sk-official-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "doubao-seedance-2-0-mini-260615", body["model"])
			require.NotContains(t, body, "prompt")
			_, _ = w.Write([]byte(`{"id":"ultra720_test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks/ultra720_test":
			polls++
			switch polls {
			case 1:
				_, _ = w.Write([]byte(`{"id":"ultra720_test","status":"processing","content":{}}`))
			case 2:
				_, _ = w.Write([]byte(`{"id":"ultra720_test","doubao-id":"cgt-never-poll-this","status":"succeeded","content":{"video_url":"https://cdn.example/result.mp4"},"usage":{"completion_tokens":93366,"total_tokens":93366}}`))
			default:
				_, _ = w.Write([]byte(`{"id":"ultra720_test","status":"failed","error":{"code":"ContentRisk","message":"blocked upstream"}}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	account := &Account{ID: 77, Extra: map[string]any{"video_provider": videoProviderNewtokenOfficial, "base_url": server.URL}, Credentials: map[string]any{"api_key": "sk-official-test"}}
	r := officialRequest(t, `{"model":"seedance-2.0-mini","prompt":"move","duration":6}`)
	a := videoProviderAdapterForAccount(account)
	svc := &VideoService{}
	created, err := svc.createUpstreamTask(context.Background(), account, a.BuildCreateBody(r, a.UpstreamModel(account, r)))
	require.NoError(t, err)
	require.Equal(t, "ultra720_test", created.ID)
	processing, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
	require.NoError(t, err)
	require.Equal(t, VideoTaskStatusProcessing, processing.Status)
	completed, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
	require.NoError(t, err)
	require.Equal(t, VideoTaskStatusCompleted, completed.Status)
	require.Equal(t, "https://cdn.example/result.mp4", completed.VideoURL)
	usage, ok := completed.Payload["usage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(93366), usage["total_tokens"])
	require.Empty(t, a.ResultAuthorization(account, a.UpstreamModel(account, r)))
	failed, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
	require.NoError(t, err)
	require.Equal(t, VideoTaskStatusFailed, failed.Status)
	require.NotNil(t, failed.Failure)
	require.Contains(t, string(failed.Failure.Body), "blocked upstream")
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func vippromptTestAccount(base string) *Account {
	return &Account{ID: 5, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "secret", "base_url": base},
		Extra:       map[string]any{VideoProviderExtraKey: videoProviderVipprompt, "poll_interval_ms": 1, "poll_timeout_ms": 500}}
}

func TestVippromptRequestMappingAndCompatibility(t *testing.T) {
	extra, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: " Vipprompt "})
	require.NoError(t, err)
	require.Equal(t, videoProviderVipprompt, extra[VideoProviderExtraKey])
	a := videoProviderAdapterByName("VIPPrompt")
	require.IsType(t, vippromptVideoProviderAdapter{}, a)
	account := vippromptTestAccount("")
	require.Equal(t, a.Provider(), videoAccountProvider(account))
	require.IsType(t, a, videoProviderAdapterForAccount(account))
	endpoint, err := videoAccountEndpoint(account)
	require.NoError(t, err)
	require.Equal(t, "https://www.vipprompt.cn/v1/videos", endpoint)
	require.Equal(t, 10*time.Second, videoAccountDefaultDuration(account, "poll_interval_ms"))
	require.True(t, videoProviderNeedsRequestCompatibility(a.Provider()))
	for _, model := range []string{VideoModelSeedance20, VideoModelSeedance25, VideoModelKlingV4} {
		t.Run(model, func(t *testing.T) {
			n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: model, Prompt: "镜头向前", Duration: 5, Resolution: "720p", Ratio: "16:9", Content: []VideoContent{
				{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn.example/a.png"}},
				{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn.example/b.mp4"}},
				{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn.example/c.wav"}},
			}})
			require.NoError(t, err)
			require.True(t, a.CompatibleRequest(n))
			if model == VideoModelKlingV4 {
				require.Equal(t, "kling-video-v4", a.UpstreamModel(nil, n))
				account.Credentials["model_mapping"] = map[string]any{model: model}
				require.Equal(t, "kling-video-v4", a.UpstreamModel(account, n))
				require.Equal(t, "kling-video-v4", a.BuildCreateBody(n, "")["model"])
			}
			upstream := " 自定义模型（官渠）/v4-1080P "
			account.Credentials["model_mapping"] = map[string]any{model: upstream}
			name := a.UpstreamModel(account, n)
			require.Equal(t, "自定义模型（官渠）/v4-1080P", name)
			body := a.BuildCreateBody(n, name)
			require.Equal(t, map[string]any{"model": name, "prompt": "镜头向前", "duration": 5, "resolution": "720P", "ratio": "16:9", "media": []map[string]any{
				{"type": "reference_image", "url": "https://cdn.example/a.png"},
				{"type": "reference_video", "url": "https://cdn.example/b.mp4"},
				{"type": "reference_audio", "url": "https://cdn.example/c.wav"},
			}}, body)
			require.Equal(t, model, n.Model, "downstream model is unchanged")
			frame := *n
			frame.AbilityCode = videoAbilityStartEndToVideo
			require.False(t, a.CompatibleRequest(&frame), "frame roles must not silently become references")
			tooLong := *n
			tooLong.GeneratedSeconds = 31
			require.False(t, a.CompatibleRequest(&tooLong))
			n.Content = nil
			n.AbilityCode = videoAbilityTextToVideo
			require.True(t, a.CompatibleRequest(n))
			require.NotContains(t, a.BuildCreateBody(n, name), "media")
		})
	}
	for _, model := range []string{VideoModelSeedance20Fast, VideoModelKlingV3Omni, VideoModelMinimaxH3, "unknown"} {
		require.False(t, a.Compatible(model, "720p"))
	}
	require.False(t, a.Compatible(VideoModelSeedance20, "4K"))
	require.False(t, a.Compatible(VideoModelKlingV4, "480p"))
	require.False(t, a.CompatibleRequest(nil))
	require.Empty(t, a.ResultAuthorization(account, "custom"))
}

func TestVippromptHTTPCreateAndPoll(t *testing.T) {
	for _, idKey := range []string{"task_id", "taskId"} {
		t.Run(idKey, func(t *testing.T) {
			response := map[string]any{"status": "accepted"}
			var traceID string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				switch r.Method {
				case http.MethodPost:
					require.Equal(t, "/v1/videos", r.URL.Path)
					traceID = r.Header.Get("X-LF-Task-ID")
					require.NotEmpty(t, traceID)
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, "SD-2.5-M支持真人满血稳定版", body["model"])
					w.WriteHeader(http.StatusAccepted)
					fmt.Fprintf(w, `{%q:"remote-id","status":"accepted"}`, idKey)
				case http.MethodGet:
					require.Equal(t, "/v1/videos/tasks/remote-id", r.URL.Path)
					_ = json.NewEncoder(w).Encode(response)
				}
			}))
			defer server.Close()
			account := vippromptTestAccount(server.URL)
			svc := newMikuapiPollTestService(nil, nil)
			created, err := svc.createUpstreamTask(context.Background(), account, map[string]any{"model": "SD-2.5-M支持真人满血稳定版"})
			require.NoError(t, err)
			require.Equal(t, "remote-id", created.ID)
			for _, status := range []string{"queued", "accepted", "running", "pending", "submitted", "created", "processing", "in_progress", "submission_unknown", "future_status", "failed", "cancelled"} {
				response = map[string]any{"task_status": status, "error": map[string]any{"message": "invalid parameter"}}
				polled, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
				require.NoError(t, err)
				require.Equal(t, normalizeVideoUpstreamStatus(status), polled.Status, status)
			}
			for _, field := range []string{"video_url", "download_url", "videoUrl", "downloadUrl"} {
				response = map[string]any{"status": "succeeded", field: "/v/result.mp4"}
				polled, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
				require.NoError(t, err)
				require.Equal(t, VideoTaskStatusCompleted, polled.Status)
				require.Equal(t, server.URL+"/v/result.mp4", polled.VideoURL)
			}
		})
	}
}

func TestVippromptAmbiguousCreateQueriesOriginalTrace(t *testing.T) {
	for _, code := range []int{200, 409, 429, 500, 502, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var trace string
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					trace = r.Header.Get("X-LF-Task-ID")
					w.WriteHeader(code)
					fmt.Fprint(w, `not json`)
					return
				}
				require.Equal(t, "/v1/videos/tasks/"+trace, r.URL.Path)
				fmt.Fprint(w, `{"status":"running"}`)
			}))
			defer server.Close()
			svc := newMikuapiPollTestService(nil, nil)
			account := vippromptTestAccount(server.URL)
			created, err := svc.createUpstreamTask(context.Background(), account, map[string]any{"model": "custom"})
			require.NoError(t, err)
			require.Equal(t, trace, created.ID)
			_, err = svc.pollUpstreamTask(context.Background(), account, created.ID)
			require.NoError(t, err)
			require.Equal(t, 1, posts)
		})
	}
	t.Run("network response lost", func(t *testing.T) {
		var trace string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace = r.Header.Get("X-LF-Task-ID")
			hijacker, ok := w.(http.Hijacker)
			require.True(t, ok)
			conn, _, err := hijacker.Hijack()
			require.NoError(t, err)
			_ = conn.Close()
		}))
		defer server.Close()
		svc := newMikuapiPollTestService(nil, nil)
		created, err := svc.createUpstreamTask(context.Background(), vippromptTestAccount(server.URL), map[string]any{"model": "custom"})
		require.NoError(t, err)
		require.NotEmpty(t, trace)
		require.Equal(t, trace, created.ID)
	})
}

func TestVippromptImmediateResultPublishesWithoutPolling(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, http.MethodPost, r.Method)
		fmt.Fprint(w, `{"ok":true,"videoUrl":"https://cdn.example/result.mp4"}`)
	}))
	defer server.Close()
	account := vippromptTestAccount(server.URL)
	repo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll", Status: VideoTaskStatusProcessing}}
	publisher := &mikuapiResultPublisherStub{}
	svc := newMikuapiPollTestService(repo, publisher)
	created, err := svc.createUpstreamTask(context.Background(), account, map[string]any{"model": "custom"})
	require.NoError(t, err)
	require.NoError(t, svc.pollLifecycle(mikuapiPollTestInput(account), created.ID, created.InitialResult))
	require.Equal(t, VideoTaskStatusCompleted, repo.task.Status)
	require.Equal(t, int64(1), requests.Load())
	require.Equal(t, []string{"https://cdn.example/result.mp4"}, publisher.urls)
	require.Empty(t, publisher.auths, "public results must not use an authenticated download")
}

func TestVippromptTerminalResponsesAndResultPrecedence(t *testing.T) {
	for _, p := range []map[string]any{
		{"ok": false, "error": map[string]any{"message": "invalid parameter"}},
		{"error": "invalid parameter"},
		{"status": "failed", "error": "content moderation"},
	} {
		result := vippromptPollResult("https://www.vipprompt.cn/v1/videos", "id", p, nil)
		require.Equal(t, VideoTaskStatusFailed, result.Status)
		require.NotNil(t, result.Failure)
		require.True(t, result.Failure.TaskFailure)
		require.False(t, isVideoCreateFailoverError(result.Failure))
	}
	result := vippromptPollResult("https://www.vipprompt.cn/v1/videos", "id", map[string]any{"status": "accepted", "video_url": "/v/primary.mp4", "download_url": "/v/secondary.mp4"}, nil)
	require.Equal(t, VideoTaskStatusCompleted, result.Status)
	require.Equal(t, "https://www.vipprompt.cn/v/primary.mp4", result.VideoURL)
	require.Equal(t, result.VideoURL, result.DirectURL)
	for _, p := range []map[string]any{{"status": "succeeded"}, {"status": "running", "error": nil}, {"status": "queued", "task_status": "failed"}} {
		result := vippromptPollResult("https://www.vipprompt.cn/v1/videos", "id", p, nil)
		require.Contains(t, []string{VideoTaskStatusProcessing, VideoTaskStatusQueued}, result.Status)
	}
}

func TestVippromptSchedulingHonorsAccountCapabilities(t *testing.T) {
	account := *vippromptTestAccount("")
	account.Status, account.Schedulable = StatusActive, true
	account.Credentials["model_mapping"] = map[string]any{VideoModelSeedance20: "sd-custom", VideoModelSeedance25: "sd25-custom", VideoModelKlingV4: "kling-custom"}
	account.Extra[VideoModelResolutionsExtraKey] = map[string]any{VideoModelSeedance20: []any{"720p"}}
	account.Extra[VideoModelDurationsExtraKey] = map[string]any{VideoModelSeedance20: []any{5}}
	svc := newMikuapiPollTestService(nil, nil)
	svc.accountRepo = &videoAccountRepoStub{accounts: []Account{account}}
	for _, agent := range []bool{false, true} {
		for _, model := range []string{VideoModelSeedance20, VideoModelSeedance25, VideoModelKlingV4} {
			n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: model, Prompt: "camera", Duration: 5, Resolution: "720p"})
			require.NoError(t, err)
			selected, err := svc.selectAccountForRequest(context.Background(), 1, n, agent)
			require.NoError(t, err)
			require.Equal(t, account.ID, selected.ID)
			if model == VideoModelSeedance20 {
				n.Resolution = "1080p"
				_, err = svc.selectAccountForRequest(context.Background(), 1, n, agent)
				require.ErrorIs(t, err, ErrVideoAccountNotFound)
				n.Resolution, n.GeneratedSeconds = "720p", 6
				_, err = svc.selectAccountForRequest(context.Background(), 1, n, agent)
				require.ErrorIs(t, err, ErrVideoAccountNotFound)
			}
		}
	}
}

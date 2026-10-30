package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func viduTestImage(role string) VideoContent {
	return VideoContent{Type: "image_url", Role: role, ImageURL: &VideoContentURL{URL: "https://cdn.example/image.png"}}
}

func viduTestAudio() VideoContent {
	return VideoContent{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn.example/voice.mp3"}}
}

func TestViduValidationAndMapping(t *testing.T) {
	adapter := viduVideoProviderAdapter{}
	for _, resolution := range SupportedVideoResolutions(VideoModelViduQ4Preview) {
		for _, duration := range []float64{3, 5, 16} {
			for _, role := range []string{"first_frame", "reference_image"} {
				req := &VideoCreateRequest{Model: VideoModelViduQ4Preview, Duration: duration, Resolution: resolution, Content: []VideoContent{viduTestImage(role)}}
				if role == "reference_image" {
					req.Prompt, req.Ratio = "参考图1的人物使用参考音频1说话", "3:4"
					req.Content = append(req.Content, viduTestAudio())
				}
				n, err := normalizeVideoCreateRequest(req)
				require.NoError(t, err)
				require.True(t, adapter.CompatibleRequest(n))
				body := adapter.BuildCreateBody(n, adapter.UpstreamModel(nil, n))
				require.Equal(t, "viduq4-preview", body["model"])
				require.Equal(t, resolution, body["resolution"])
				require.Equal(t, int(duration), body["duration"])
				require.Equal(t, true, body["audio"])
				require.Equal(t, false, body["watermark"])
				require.Equal(t, []string{"https://cdn.example/image.png"}, body["images"])
				require.NotContains(t, body, "content")
				if role == "first_frame" {
					require.Equal(t, true, body["is_rec"])
					require.NotContains(t, body, "aspect_ratio")
					require.NotContains(t, body, "sounds")
				} else {
					require.Equal(t, "3:4", body["aspect_ratio"])
					require.Equal(t, []string{"https://cdn.example/voice.mp3"}, body["sounds"])
					require.NotContains(t, body, "is_rec")
				}
			}
		}
	}
	refs := make([]VideoContent, 0, 18)
	for i := 0; i < 15; i++ {
		refs = append(refs, viduTestImage("reference_image"))
	}
	for i := 0; i < 3; i++ {
		refs = append(refs, viduTestAudio())
	}
	req := &VideoCreateRequest{Model: VideoModelViduQ4Preview, Duration: 5, Prompt: "test", Content: refs}
	_, err := normalizeVideoCreateRequest(req)
	require.NoError(t, err)
	for _, extra := range []VideoContent{viduTestImage("reference_image"), viduTestAudio()} {
		req.Content = append(append([]VideoContent{}, refs...), extra)
		_, err := normalizeVideoCreateRequest(req)
		require.Error(t, err)
	}
	muted := false
	req = &VideoCreateRequest{Model: VideoModelViduQ4Preview, Duration: 5, Content: []VideoContent{viduTestImage("first_frame")}, GenerateAudio: &muted, Raw: map[string]any{"seed": 42.0, "watermark": false, "is_rec": false, "callback_url": "https://example.com/callback"}}
	n, err := normalizeVideoCreateRequest(req)
	require.NoError(t, err)
	body := adapter.BuildCreateBody(n, adapter.UpstreamModel(nil, n))
	require.Equal(t, false, body["audio"])
	require.Equal(t, false, body["watermark"])
	require.Equal(t, false, body["is_rec"])
	require.Equal(t, 42.0, body["seed"])
	require.NotContains(t, body, "callback_url")
	req.Raw["watermark"] = true
	n, err = normalizeVideoCreateRequest(req)
	require.NoError(t, err)
	require.Equal(t, true, adapter.BuildCreateBody(n, adapter.UpstreamModel(nil, n))["watermark"])
	_, err = NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: "vidu", VideoModelDurationsExtraKey: map[string]any{VideoModelViduQ4Preview: []any{3, 16}}, VideoModelResolutionsExtraKey: map[string]any{VideoModelViduQ4Preview: []any{"540p", "2k", "4k"}}})
	require.NoError(t, err)
	require.Contains(t, SupportedVideoModels(), VideoModelViduQ4Preview)
	for _, provider := range supportedVideoProviders {
		if provider != videoProviderVidu {
			require.False(t, videoProviderAdapterByName(provider).Compatible(VideoModelViduQ4Preview, VideoResolution720P), provider)
		}
	}
}

func TestViduRejectsUnsupportedRequests(t *testing.T) {
	for name, change := range map[string]func(*VideoCreateRequest){
		"text only": func(r *VideoCreateRequest) { r.Content = nil },
		"start end": func(r *VideoCreateRequest) {
			r.Content = []VideoContent{viduTestImage("first_frame"), viduTestImage("last_frame")}
		},
		"mixed frames": func(r *VideoCreateRequest) { r.Content = append(r.Content, viduTestImage("first_frame")) },
		"audio only":   func(r *VideoCreateRequest) { r.Content = []VideoContent{viduTestAudio()} },
		"video reference": func(r *VideoCreateRequest) {
			r.Content = append(r.Content, VideoContent{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn.example/clip.mp4"}})
		},
		"missing prompt":    func(r *VideoCreateRequest) { r.Prompt = "" },
		"long prompt":       func(r *VideoCreateRequest) { r.Prompt = strings.Repeat("字", 20001) },
		"low duration":      func(r *VideoCreateRequest) { r.Duration = 2 },
		"high duration":     func(r *VideoCreateRequest) { r.Duration = 17 },
		"fraction duration": func(r *VideoCreateRequest) { r.Duration = 5.5 },
		"auto duration":     func(r *VideoCreateRequest) { r.Duration = -1 },
		"resolution":        func(r *VideoCreateRequest) { r.Resolution = "480p" },
		"ratio":             func(r *VideoCreateRequest) { r.Ratio = "21:9" },
		"image ratio": func(r *VideoCreateRequest) {
			r.Content = []VideoContent{viduTestImage("first_frame")}
			r.Ratio = "16:9"
		},
		"missing url":         func(r *VideoCreateRequest) { r.Content[0].ImageURL = nil },
		"asset url":           func(r *VideoCreateRequest) { r.Content[0].ImageURL = &VideoContentURL{URL: "asset://other-provider"} },
		"unknown content":     func(r *VideoCreateRequest) { r.Content = append(r.Content, VideoContent{Type: "draft_task"}) },
		"unknown role":        func(r *VideoCreateRequest) { r.Content[0].Role = "middle_frame" },
		"bad seed":            func(r *VideoCreateRequest) { r.Raw = map[string]any{"seed": 1.5} },
		"bad watermark":       func(r *VideoCreateRequest) { r.Raw = map[string]any{"watermark": "false"} },
		"reference expansion": func(r *VideoCreateRequest) { r.Raw = map[string]any{"is_rec": true} },
	} {
		t.Run(name, func(t *testing.T) {
			r := &VideoCreateRequest{Model: VideoModelViduQ4Preview, Duration: 5, Prompt: "test", Content: []VideoContent{viduTestImage("reference_image")}}
			change(r)
			_, err := normalizeVideoCreateRequest(r)
			require.Error(t, err)
			_, err = normalizeAgentVideoCreateRequest(r)
			require.Error(t, err)
		})
	}
}

func TestViduHTTPCreateAndPoll(t *testing.T) {
	state := "success"
	errCode := "TaskPromptPolicyViolation"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-key" {
			t.Errorf("unexpected authorization %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/ent/v2/img2video", "/proxy/ent/v2/reference2video":
			if r.Method != http.MethodPost {
				t.Errorf("unexpected method %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "custom-vidu-model" {
				t.Errorf("unexpected model %v", body["model"])
			}
			_, reference := body["aspect_ratio"]
			if reference != strings.HasSuffix(r.URL.Path, "/reference2video") {
				t.Errorf("wrong create path %s", r.URL.Path)
			}
			fmt.Fprint(w, `{"task_id":"task_123","state":"created"}`)
		case "/proxy/ent/v2/tasks/task_123/creations":
			if r.Method != http.MethodGet {
				t.Errorf("unexpected method %s", r.Method)
			}
			fmt.Fprintf(w, `{"id":"task_123","state":%q,"err_code":%q,"creations":[{"url":"https://cdn.example/result.mp4","cover_url":"https://cdn.example/cover.jpg"}]}`, state, errCode)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	account := &Account{Credentials: map[string]any{"api_key": "test-key", "base_url": server.URL + "/proxy", "model_mapping": map[string]any{VideoModelViduQ4Preview: "custom-vidu-model"}}, Extra: map[string]any{VideoProviderExtraKey: videoProviderVidu}}
	svc := newMikuapiPollTestService(nil, nil)
	for _, role := range []string{"first_frame", "reference_image"} {
		n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: VideoModelViduQ4Preview, Prompt: "test", Duration: 5, Content: []VideoContent{viduTestImage(role)}})
		require.NoError(t, err)
		created, err := svc.createUpstreamTask(context.Background(), account, videoUpstreamBodyForAccount(account, n, videoUpstreamModelForAccount(account, n)))
		require.NoError(t, err)
		require.Equal(t, "task_123", created.ID)
	}
	for native, unified := range map[string]string{"created": VideoTaskStatusQueued, "queueing": VideoTaskStatusQueued, "processing": VideoTaskStatusProcessing, "success": VideoTaskStatusCompleted, "failed": VideoTaskStatusFailed} {
		state = native
		result, err := svc.pollUpstreamTask(context.Background(), account, "task_123")
		require.NoError(t, err)
		require.Equal(t, unified, result.Status)
		if native == "success" {
			require.Equal(t, "https://cdn.example/result.mp4", result.VideoURL)
		}
		if native == "failed" {
			require.NotNil(t, result.Failure)
			require.Equal(t, http.StatusUnavailableForLegalReasons, result.Failure.StatusCode)
		}
	}
	state = "failed"
	for code, httpStatus := range map[string]int{"CreationPolicyViolation": 451, "AuditSubmitIllegal": 451, "FieldInvalid": 400, "ImageFormatInvalid": 400, "InternalServiceFailure": 503} {
		errCode = code
		result, err := svc.pollUpstreamTask(context.Background(), account, "task_123")
		require.NoError(t, err)
		require.Equal(t, VideoTaskStatusFailed, result.Status)
		require.NotNil(t, result.Failure)
		require.Equal(t, httpStatus, result.Failure.StatusCode, code)
	}
	require.Empty(t, videoProviderAdapterForAccount(account).ResultAuthorization(account, "custom-vidu-model"))
}

func TestViduAccountSelectionAndPricing(t *testing.T) {
	const groupID int64 = 7
	wrong := *videoAccountWithResolutions(t, videoProviderMikuapi, nil)
	wrong.ID, wrong.Schedulable = 1, true
	wrong.Credentials["model_mapping"] = map[string]any{VideoModelViduQ4Preview: VideoModelViduQ4Preview}
	right := *videoAccountWithResolutions(t, videoProviderVidu, nil)
	right.ID, right.Schedulable = 2, true
	right.Credentials["model_mapping"] = map[string]any{VideoModelViduQ4Preview: VideoModelViduQ4Preview}
	svc := &VideoService{accountRepo: &videoAccountRepoStub{accounts: []Account{wrong, right}}, pricingRepo: &videoPricingMemoryRepo{rule: &VideoGroupPricingRule{GroupID: groupID, ModelCode: VideoModelViduQ4Preview, Resolution: VideoResolution720P, CreditsPerSecond: 0.2, Enabled: true}}}
	req := &VideoCreateRequest{Model: VideoModelViduQ4Preview, Duration: 5, Prompt: "test", Content: []VideoContent{viduTestImage("reference_image"), viduTestAudio()}}
	n, err := normalizeVideoCreateRequest(req)
	require.NoError(t, err)
	for _, agent := range []bool{false, true} {
		account, err := svc.selectAccountForRequest(context.Background(), groupID, n, agent)
		require.NoError(t, err)
		require.Equal(t, right.ID, account.ID)
	}
	estimate, err := svc.EstimateGenerationCost(context.Background(), &APIKey{Group: &Group{ID: groupID, RateMultiplier: 2}}, req, 1)
	require.NoError(t, err)
	require.Equal(t, 5, estimate.BillableSeconds)
	require.InDelta(t, 1, estimate.TotalCost, 1e-9)
	require.InDelta(t, 2, estimate.ActualCost, 1e-9)
}

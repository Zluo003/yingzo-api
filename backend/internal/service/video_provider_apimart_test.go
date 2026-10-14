package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApimartValidationAndMapping(t *testing.T) {
	image := func(role string) VideoContent {
		return VideoContent{Type: "image_url", Role: role, ImageURL: &VideoContentURL{URL: "https://cdn.example/image.png"}}
	}
	for _, model := range []string{VideoModelMinimaxH3, VideoModelMinimaxH3Max} {
		for _, content := range [][]VideoContent{nil, {image("first_frame")}, {image("last_frame")}, {image("last_frame"), image("first_frame")}, {image("reference_image"), {Type: "audio_url", AudioURL: &VideoContentURL{URL: "https://cdn.example/a.mp3"}}}} {
			n, err := normalizeVideoCreateRequest(&VideoCreateRequest{Model: model, Prompt: "测试", Duration: 5, Content: content})
			require.NoError(t, err)
			a := apimartVideoProviderAdapter{}
			require.True(t, a.CompatibleRequest(n))
			body := a.BuildCreateBody(n, a.UpstreamModel(nil, n))
			require.Equal(t, strings.ToUpper(n.Resolution), body["resolution"])
			require.NotContains(t, body, "aspect_ratio")
			require.NotContains(t, body, "content")
			for _, item := range content {
				if item.Role == "first_frame" {
					require.Equal(t, item.ImageURL.URL, body["first_frame_image"])
				}
				if item.Role == "last_frame" {
					require.Equal(t, item.ImageURL.URL, body["last_frame_image"])
				}
			}
		}
	}
	cases := []VideoCreateRequest{
		{Prompt: "", Content: []VideoContent{image("first_frame")}},
		{Prompt: strings.Repeat("字", 7001)},
		{Resolution: "2K"}, {Duration: 4}, {Duration: 5.5},
		{Content: []VideoContent{image("first_frame"), image("reference_image")}},
		{Content: []VideoContent{image("first_frame"), image("first_frame")}},
		{Content: []VideoContent{{Type: "audio_url", AudioURL: &VideoContentURL{URL: "https://cdn.example/a.mp3"}}}},
		{Resolution: "1080p", Raw: map[string]any{"watermark": false}},
		{Raw: map[string]any{"watermark": "false"}},
		{Ratio: "2:1"},
	}
	for i, req := range cases {
		req.Model = VideoModelMinimaxH3Max
		if i != 0 && req.Prompt == "" {
			req.Prompt = "test"
		}
		if req.Duration == 0 {
			req.Duration = 5
		}
		_, err := normalizeVideoCreateRequest(&req)
		require.Error(t, err, "case %d", i)
	}
	_, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: "apimart"})
	require.NoError(t, err)
	for _, provider := range []string{videoProviderAigod, videoProviderMikuapi, videoProviderNewtoken, videoProviderJingyu, videoProviderXingguang} {
		require.False(t, videoProviderAdapterByName(provider).Compatible(VideoModelMinimaxH3Max, VideoResolution1080P))
	}
}

func TestApimartHTTPCreateAndPoll(t *testing.T) {
	status := "completed"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/videos/generations":
			require.Equal(t, http.MethodPost, r.Method)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "MiniMax-H3", body["model"])
			fmt.Fprint(w, `{"code":200,"data":[{"status":"submitted","task_id":"task_123"}]}`)
		case "/v1/tasks/task_123":
			require.Equal(t, http.MethodGet, r.Method)
			fmt.Fprintf(w, `{"code":200,"data":{"status":%q,"error":{"message":"generation failed"},"result":{"videos":[{"url":["https://cdn.example/result.mp4"]}]}}}`, status)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	account := &Account{Credentials: map[string]any{"api_key": "test-key", "base_url": server.URL}, Extra: map[string]any{VideoProviderExtraKey: videoProviderApimart}}
	svc := newMikuapiPollTestService(nil, nil)
	created, err := svc.createUpstreamTask(context.Background(), account, map[string]any{"model": "MiniMax-H3"})
	require.NoError(t, err)
	require.Equal(t, "task_123", created.ID)
	for _, s := range []string{"pending", "processing", "completed", "failed"} {
		status = s
		result, err := svc.pollUpstreamTask(context.Background(), account, created.ID)
		require.NoError(t, err)
		require.Equal(t, normalizeVideoUpstreamStatus(s), result.Status)
		if s == "completed" {
			require.Equal(t, "https://cdn.example/result.mp4", result.VideoURL)
		}
		if s == "failed" {
			require.NotNil(t, result.Failure)
		}
	}
}

func TestApimartInputImagePricing(t *testing.T) {
	for _, tc := range []struct {
		model        string
		images, free int
	}{{VideoModelMinimaxH3, 5, 5}, {VideoModelMinimaxH3, 7, 5}, {VideoModelMinimaxH3Max, 2, 2}, {VideoModelMinimaxH3Max, 6, 2}} {
		rule := &VideoGroupPricingRule{GroupID: 1, ModelCode: tc.model, Resolution: VideoResolution768P, Enabled: true, CreditsPerSecond: 0.2, CreditsPerInputImage: 0.1}
		svc := &VideoService{pricingRepo: &videoPricingMemoryRepo{rule: rule}}
		req := &VideoCreateRequest{Model: tc.model, Resolution: VideoResolution768P, Prompt: "test", Duration: 5}
		for i := 0; i < tc.images; i++ {
			req.Content = append(req.Content, VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn.example/ref.png"}})
		}
		seconds := 3.0
		req.Content = append(req.Content, VideoContent{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn.example/ref.mp4"}, DurationSeconds: &seconds})
		key := &APIKey{Group: &Group{ID: 1, RateMultiplier: 2}}
		estimate, err := svc.EstimateGenerationCost(context.Background(), key, req, 2)
		require.NoError(t, err)
		require.Equal(t, 16, estimate.BillableSeconds)
		require.Equal(t, (tc.images-tc.free)*2, estimate.BillableInputImages)
		expected := (8*0.2 + float64(tc.images-tc.free)*0.2) * 2
		require.InDelta(t, expected, estimate.TotalCost, 1e-9)
		require.InDelta(t, expected*2, estimate.ActualCost, 1e-9)
		zero := 0
		rule.FreeInputImages = &zero
		estimate, err = svc.EstimateGenerationCost(context.Background(), key, req, 1)
		require.NoError(t, err)
		require.Equal(t, tc.images, estimate.BillableInputImages)
	}
}

func TestApimartAgentPricingUsesModelAllowanceAndResolutionPrice(t *testing.T) {
	groupID := int64(9)
	catalog, _ := configuredAgentCatalogForEstimateTest(t, groupID, PlatformVideo, VideoModelMinimaxH3, AgentMediaTypeVideo, VideoResolution768P, 0.2)
	config, err := catalog.GetConfig(context.Background(), groupID)
	require.NoError(t, err)
	free := 0
	_, err = catalog.UpdateModel(context.Background(), groupID, config.Models[0].ID, AgentModelConfigInput{
		MediaType:       AgentMediaTypeVideo,
		Enabled:         true,
		FreeInputImages: &free,
		Prices:          []AgentModelPrice{{Resolution: VideoResolution768P, UnitPrice: 0.2}},
	})
	require.NoError(t, err)

	request := &VideoCreateRequest{Model: VideoModelMinimaxH3, Resolution: VideoResolution768P, Prompt: "test", Duration: 5}
	for i := 0; i < 2; i++ {
		request.Content = append(request.Content, VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn.example/ref.png"}})
	}
	seconds := 3.0
	request.Content = append(request.Content, VideoContent{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn.example/ref.mp4"}, DurationSeconds: &seconds})

	service := &VideoService{agentModels: catalog}
	estimate, err := service.EstimateGenerationCost(context.Background(), &APIKey{Group: &Group{ID: groupID, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 99}}, request, 1)
	require.NoError(t, err)
	require.Equal(t, 0, estimate.FreeInputImages)
	require.Equal(t, 2, estimate.BillableInputImages)
	// (5 generated + 3 reference) * 0.2 + 2 images * 0.2.
	require.InDelta(t, 2.0, estimate.ActualCost, 1e-9)
}

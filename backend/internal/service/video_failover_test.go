package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 媒体故障转移（视频）：上游创建失败时自动切换下一个满足能力要求的账号，
// 最多尝试 MediaFailoverMaxAccounts 个上游；全部失败按最后一个上游的错误判
// 失败并退费。能力过滤（账号级分辨率白名单）对重选同样生效。

type videoFailoverServer struct {
	createCalls atomic.Int64
	pollCalls   atomic.Int64
	server      *httptest.Server
}

func newVideoFailoverServer(t *testing.T, createStatus int, createBody string, pollBody string) *videoFailoverServer {
	t.Helper()
	fake := &videoFailoverServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/videos", func(w http.ResponseWriter, r *http.Request) {
		fake.createCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(createStatus)
		_, _ = w.Write([]byte(createBody))
	})
	mux.HandleFunc("GET /v1/videos/", func(w http.ResponseWriter, r *http.Request) {
		fake.pollCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pollBody))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func newVideoFailoverAccount(id int64, priority int, serverURL string, extra map[string]any) Account {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["base_url"] = serverURL
	return Account{
		ID:          id,
		Platform:    PlatformVideo,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Priority:    priority,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{VideoModelSeedance20: VideoModelSeedance20},
		},
		Extra: extra,
	}
}

func newVideoFailoverService(t *testing.T, accounts []Account, pricing *VideoGroupPricingRule) (*VideoService, *videoTaskMemoryRepo, *videoUsageLogRepoStub, *videoUsageBillingRepoStub) {
	t.Helper()
	taskRepo := newVideoTaskMemoryRepo()
	usageLogs := &videoUsageLogRepoStub{}
	billingRepo := &videoUsageBillingRepoStub{}
	service := NewVideoService(
		&videoAccountRepoStub{accounts: accounts},
		taskRepo,
		&videoPricingMemoryRepo{rule: pricing},
		usageLogs,
		billingRepo,
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	return service, taskRepo, usageLogs, billingRepo
}

func newVideoFailoverPricing(resolution string, creditsPerSecond float64) *VideoGroupPricingRule {
	return &VideoGroupPricingRule{
		GroupID:          20,
		ModelCode:        VideoModelSeedance20,
		Resolution:       resolution,
		CreditsPerSecond: creditsPerSecond,
		Enabled:          true,
	}
}

func newVideoFailoverCreateInput(resolution string) *VideoCreateInput {
	groupID := int64(20)
	return &VideoCreateInput{
		APIKey: &APIKey{
			ID:      10,
			UserID:  100,
			GroupID: &groupID,
			User:    &User{ID: 100, Balance: 100},
			Group:   &Group{ID: groupID, Platform: PlatformVideo, RateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard},
		},
		Request:            &VideoCreateRequest{Model: VideoModelSeedance20, Prompt: "move", Duration: 8, Resolution: resolution},
		RequestPayloadHash: "hash",
	}
}

func waitForVideoTaskStatus(t *testing.T, taskRepo *videoTaskMemoryRepo, publicID string, statuses ...string) *VideoTask {
	t.Helper()
	wanted := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	var task *VideoTask
	require.Eventually(t, func() bool {
		latest, err := taskRepo.GetByPublicID(context.Background(), publicID)
		if err != nil {
			return false
		}
		task = latest
		_, ok := wanted[latest.Status]
		return ok
	}, 5*time.Second, 20*time.Millisecond)
	return task
}

func videoTaskClientError(t *testing.T, task *VideoTask) VideoClientError {
	t.Helper()
	require.NotNil(t, task.ErrorJSON)
	raw, err := json.Marshal(task.ErrorJSON)
	require.NoError(t, err)
	var clientErr VideoClientError
	require.NoError(t, json.Unmarshal(raw, &clientErr))
	return clientErr
}

func TestVideoCreateFailoverSwitchesToNextCapableAccount(t *testing.T) {
	upstreamA := newVideoFailoverServer(t, http.StatusTooManyRequests, `{"error":{"message":"busy"}}`, "")
	upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b-1","status":"queued"}`, `{"id":"task-b-1","status":"processing"}`)

	service, taskRepo, usageLogs, _ := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, nil),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, nil),
	}, newVideoFailoverPricing(VideoResolution720P, 0.2))

	resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
	require.NoError(t, err)
	require.Equal(t, VideoTaskStatusQueued, resp.Status)

	// 账号 A（优先级更高）429 失败后应切到账号 B 并创建成功。
	task := waitForVideoTaskStatus(t, taskRepo, resp.ID, VideoTaskStatusProcessing)
	require.NotNil(t, task.UpstreamTaskID)
	require.Equal(t, "task-b-1", *task.UpstreamTaskID)
	require.Equal(t, int64(31), task.AccountID, "切换上游后任务归属应修正为实际服务的账号")
	require.Equal(t, "seedance-2.0-720p", task.UpstreamModel)
	require.Equal(t, int64(1), upstreamA.createCalls.Load())
	require.Equal(t, int64(1), upstreamB.createCalls.Load())

	// 计费流水的账号归属也应同步修正。
	require.NotEmpty(t, usageLogs.videoResultUpdates)
	last := usageLogs.videoResultUpdates[len(usageLogs.videoResultUpdates)-1]
	require.NotNil(t, last.update.AccountID)
	require.Equal(t, int64(31), *last.update.AccountID)
}

func TestVideoCreateFailoverRespectsResolutionCapability(t *testing.T) {
	// 与需求示例一致：A 支持 720p/1080p，B 只支持 720p。请求 1080p 时 A 失败后
	// 不会去 B 重试（不满足能力要求），直接按 A 的错误判失败并退费。
	upstreamA := newVideoFailoverServer(t, http.StatusInternalServerError, `{"error":{"message":"boom"}}`, "")
	upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b-1","status":"queued"}`, `{"id":"task-b-1","status":"processing"}`)
	t.Cleanup(upstreamB.server.Close)

	service, taskRepo, _, billingRepo := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, nil),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, map[string]any{
			"video_model_resolutions": map[string]any{VideoModelSeedance20: []any{VideoResolution720P}},
		}),
	}, newVideoFailoverPricing(VideoResolution1080P, 0.4))

	resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution1080P))
	require.NoError(t, err)

	task := waitForVideoTaskStatus(t, taskRepo, resp.ID, VideoTaskStatusFailed)
	require.Equal(t, VideoTaskStatusFailed, task.Status)
	require.Equal(t, int64(30), task.AccountID)
	require.Equal(t, int64(1), upstreamA.createCalls.Load(), "失败账号只应被尝试一次")
	require.Equal(t, int64(0), upstreamB.createCalls.Load(), "不满足 1080p 能力的上游不应被重试")

	require.NotNil(t, task.RefundedAt)
	require.NotEmpty(t, billingRepo.commands)
}

func TestVideoCreateFailoverExhaustsAllUpstreamsThenReportsLastError(t *testing.T) {
	upstreamA := newVideoFailoverServer(t, http.StatusTooManyRequests, `{"error":{"message":"busy a"}}`, "")
	upstreamB := newVideoFailoverServer(t, http.StatusTooManyRequests, `{"error":{"message":"busy b"}}`, "")
	upstreamC := newVideoFailoverServer(t, http.StatusServiceUnavailable, `{"error":{"message":"down c"}}`, "")

	service, taskRepo, _, _ := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, nil),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, nil),
		newVideoFailoverAccount(32, 20, upstreamC.server.URL, nil),
	}, newVideoFailoverPricing(VideoResolution720P, 0.2))

	resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
	require.NoError(t, err)

	task := waitForVideoTaskStatus(t, taskRepo, resp.ID, VideoTaskStatusFailed)
	require.Equal(t, VideoTaskStatusFailed, task.Status)
	require.Equal(t, int64(1), upstreamA.createCalls.Load())
	require.Equal(t, int64(1), upstreamB.createCalls.Load())
	require.Equal(t, int64(1), upstreamC.createCalls.Load(), "最多尝试 3 个上游")

	// 最后一个上游（503）的错误按中文报错信息库返回给下游。
	require.Equal(t, "上游模型服务暂不可用，稍等一会再试", videoTaskClientError(t, task).Message)
	// 归属修正为最后一个账号。
	require.Equal(t, int64(32), task.AccountID)
}

func TestVideoCreateFailoverSkipsContentErrors(t *testing.T) {
	// 400 属于请求内容类错误，换上游结果相同：不切换，直接判失败。
	upstreamA := newVideoFailoverServer(t, http.StatusBadRequest, `{"error":{"message":"invalid request"}}`, "")
	upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b-1","status":"queued"}`, `{"id":"task-b-1","status":"processing"}`)
	t.Cleanup(upstreamB.server.Close)

	service, taskRepo, _, _ := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, nil),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, nil),
	}, newVideoFailoverPricing(VideoResolution720P, 0.2))

	resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
	require.NoError(t, err)

	task := waitForVideoTaskStatus(t, taskRepo, resp.ID, VideoTaskStatusFailed)
	require.Equal(t, VideoTaskStatusFailed, task.Status)
	require.Equal(t, int64(1), upstreamA.createCalls.Load())
	require.Equal(t, int64(0), upstreamB.createCalls.Load(), "内容类错误不应切换上游")
}

func TestIsVideoCreateFailoverError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "network error", err: &videoUpstreamError{StatusCode: 0, Err: context.DeadlineExceeded}, want: true},
		{name: "rate limited", err: &videoUpstreamError{StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "server error", err: &videoUpstreamError{StatusCode: http.StatusBadGateway}, want: true},
		{name: "2xx with broken body", err: &videoUpstreamError{StatusCode: http.StatusOK, Err: errors.New("bad json")}, want: true},
		{name: "bad request", err: &videoUpstreamError{StatusCode: http.StatusBadRequest}, want: false},
		{name: "content moderated", err: &videoUpstreamError{StatusCode: http.StatusUnavailableForLegalReasons}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isVideoCreateFailoverError(tt.err))
		})
	}
	require.False(t, isVideoCreateFailoverError(context.DeadlineExceeded), "本地构造错误不应触发切换")
}

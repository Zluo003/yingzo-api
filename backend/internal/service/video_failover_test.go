package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 媒体故障转移（视频）：上游创建失败时自动切换下一个满足能力要求的账号，
// 最多尝试 MediaFailoverMaxAccounts 个上游；全部失败按最后一个上游的错误判
// 失败并退费。能力过滤（账号级分辨率白名单）对重选同样生效。

type videoFailoverServer struct {
	createCalls atomic.Int64
	pollCalls   atomic.Int64
	server      *httptest.Server
	onRequest   func(*http.Request)
	pollStatus  int
}

func newVideoFailoverServer(t *testing.T, createStatus int, createBody string, pollBody string) *videoFailoverServer {
	t.Helper()
	fake := &videoFailoverServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/videos", func(w http.ResponseWriter, r *http.Request) {
		if fake.onRequest != nil {
			fake.onRequest(r)
		}
		fake.createCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(createStatus)
		_, _ = w.Write([]byte(createBody))
	})
	mux.HandleFunc("GET /v1/videos/", func(w http.ResponseWriter, r *http.Request) {
		if fake.onRequest != nil {
			fake.onRequest(r)
		}
		fake.pollCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if fake.pollStatus != 0 {
			w.WriteHeader(fake.pollStatus)
		}
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

func TestVideoConfirmedFailureRetriesByPriorityWithoutRebilling(t *testing.T) {
	upstreamA := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-a","status":"queued"}`, `{"id":"task-a","status":"failed","error":{"code":"InternalError"}}`)
	upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b","status":"queued"}`, `{"id":"task-b","status":"failed"}`)
	upstreamC := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-c","status":"queued"}`, `{"id":"task-c","status":"completed","video_url":"https://media.example/result.mp4"}`)
	accounts := []Account{
		newVideoFailoverAccount(32, 20, upstreamC.server.URL, map[string]any{"poll_interval_ms": 5}),
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, map[string]any{"poll_interval_ms": 5}),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, map[string]any{"poll_interval_ms": 5}),
	}
	for i := range accounts {
		accounts[i].Credentials["api_key"] = fmt.Sprintf("key-%d", accounts[i].ID)
	}
	service, taskRepo, logs, billing := newVideoFailoverService(t, accounts, newVideoFailoverPricing(VideoResolution720P, 0.2))
	publisher := &videoFailoverPublisher{recordingVideoResultPublisher: recordingVideoResultPublisher{returnedURL: "https://gateway.example/result.mp4"}}
	service.SetVideoResultPublisher(publisher)
	events := make(chan string, 6)
	var publicID string
	for i, upstream := range []*videoFailoverServer{upstreamA, upstreamB, upstreamC} {
		accountID := int64(30 + i)
		upstream.onRequest = func(r *http.Request) {
			events <- fmt.Sprintf("%d %s %s %s", accountID, r.Method, r.URL.Path, r.Header.Get("Authorization"))
			// No intermediate refund, reference cleanup or downstream terminal state.
			task, err := taskRepo.GetByPublicID(r.Context(), publicID)
			assert.NoError(t, err)
			assert.Nil(t, task.RefundedAt)
			assert.NotEqual(t, VideoTaskStatusFailed, task.Status)
			assert.Zero(t, publisher.releases.Load())
			if r.Method == http.MethodGet {
				assert.Equal(t, accountID, task.AccountID)
				assert.Equal(t, "/v1/videos/"+*task.UpstreamTaskID, r.URL.Path)
			}
		}
	}
	// Run the lifecycle synchronously so billing assertions do not race its tail.
	service.startLifecycleFunc = func(input VideoTaskLifecycleInput) {
		publicID = input.PublicID
		service.runLifecycle(input)
	}
	input := newVideoFailoverCreateInput(VideoResolution720P)
	input.Request.AbilityCode = videoAbilityReferenceToVideo
	input.Request.Content = []VideoContent{{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://gateway.example/reference.png"}}}
	resp, err := service.CreateTask(context.Background(), input)
	require.NoError(t, err)
	task := waitForVideoTaskStatus(t, taskRepo, resp.ID, VideoTaskStatusCompleted)
	require.Equal(t, publicID, resp.ID)
	require.Equal(t, int64(32), task.AccountID)
	require.Equal(t, "task-c", *task.UpstreamTaskID)
	require.Nil(t, task.RefundedAt)
	require.Len(t, billing.commands, 1, "account failover must retain the original precharge")
	require.Len(t, logs.logs, 1)
	require.Equal(t, int64(1), publisher.releases.Load(), "release references only after final success")
	require.Equal(t, int64(1), upstreamA.createCalls.Load())
	require.Equal(t, int64(1), upstreamB.createCalls.Load())
	require.Equal(t, int64(1), upstreamC.createCalls.Load())
	for _, expected := range []string{
		"30 POST /v1/videos Bearer key-30", "30 GET /v1/videos/task-a Bearer key-30",
		"31 POST /v1/videos Bearer key-31", "31 GET /v1/videos/task-b Bearer key-31",
		"32 POST /v1/videos Bearer key-32", "32 GET /v1/videos/task-c Bearer key-32",
	} {
		select {
		case event := <-events:
			require.Equal(t, expected, event)
		default:
			t.Fatalf("missing upstream request %s", expected)
		}
	}
}

type videoFailoverPublisher struct {
	recordingVideoResultPublisher
	releases atomic.Int64
}

func (p *videoFailoverPublisher) ReleaseTaskReferenceAssets(context.Context, *APIKey, []VideoContent) {
	p.releases.Add(1)
}

func TestVideoConfirmedFailureExhaustionRefundsOnce(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed_create_and_generation_failures=%t", mixed), func(t *testing.T) {
			var upstreams []*videoFailoverServer
			var accounts []Account
			for i := 0; i < MediaFailoverMaxAccounts+1; i++ {
				status := http.StatusOK
				if mixed && i != 1 {
					status = http.StatusServiceUnavailable
				}
				upstream := newVideoFailoverServer(t, status, fmt.Sprintf(`{"id":"task-%d"}`, i), `{"status":"failed"}`)
				upstreams = append(upstreams, upstream)
				accounts = append(accounts, newVideoFailoverAccount(int64(30+i), i, upstream.server.URL, map[string]any{"poll_interval_ms": 5}))
			}
			service, repo, logs, billing := newVideoFailoverService(t, accounts, newVideoFailoverPricing(VideoResolution720P, 0.2))
			service.startLifecycleFunc = service.runLifecycle
			resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
			require.NoError(t, err)
			task := waitForVideoTaskStatus(t, repo, resp.ID, VideoTaskStatusFailed)
			require.NotNil(t, task.RefundedAt)
			require.Equal(t, int64(32), task.AccountID)
			if mixed {
				require.Empty(t, *task.UpstreamTaskID, "failed creation must not keep the previous account's ID")
			} else {
				require.Equal(t, "task-2", *task.UpstreamTaskID)
			}
			require.Len(t, billing.commands, 2, "one precharge and one refund, across all attempts")
			require.InDelta(t, 0, billing.commands[0].BalanceCost+billing.commands[1].BalanceCost, 1e-8)
			require.Equal(t, "video:"+resp.ID+":refund", billing.commands[1].RequestID)
			require.Len(t, logs.logs, 2)
			require.InDelta(t, 0, logs.logs[0].ActualCost+logs.logs[1].ActualCost, 1e-8)
			for i, upstream := range upstreams {
				want := int64(1)
				if i == MediaFailoverMaxAccounts {
					want = 0
				}
				require.Equal(t, want, upstream.createCalls.Load())
			}
		})
	}
}

func TestVideoConfirmedFailureDoesNotRetryTerminalInputErrors(t *testing.T) {
	for _, body := range []string{
		`{"status":"cancelled"}`,
		`{"status":"failed","error":{"code":"ContentPolicyViolation"}}`,
		`{"status":"failed","error":{"code":"InvalidParameter"}}`,
		`{"status":"failed","error":{"status_code":400}}`,
	} {
		t.Run(body, func(t *testing.T) {
			upstreamA := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-a"}`, body)
			upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b"}`, `{"status":"failed"}`)
			service, repo, _, billing := newVideoFailoverService(t, []Account{
				newVideoFailoverAccount(30, 0, upstreamA.server.URL, map[string]any{"poll_interval_ms": 5}),
				newVideoFailoverAccount(31, 10, upstreamB.server.URL, nil),
			}, newVideoFailoverPricing(VideoResolution720P, 0.2))
			service.startLifecycleFunc = service.runLifecycle
			resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
			require.NoError(t, err)
			task := waitForVideoTaskStatus(t, repo, resp.ID, VideoTaskStatusFailed, VideoTaskStatusCancelled)
			require.NotNil(t, task.RefundedAt)
			require.Zero(t, upstreamB.createCalls.Load())
			require.Len(t, billing.commands, 2)
		})
	}
}

func TestVideoConfirmedFailureRespectsNextAccountCapability(t *testing.T) {
	upstreamA := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-a"}`, `{"status":"failed"}`)
	upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b"}`, `{"status":"failed"}`)
	service, repo, _, billing := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstreamA.server.URL, map[string]any{"poll_interval_ms": 5}),
		newVideoFailoverAccount(31, 10, upstreamB.server.URL, map[string]any{
			"video_model_resolutions": map[string]any{VideoModelSeedance20: []any{VideoResolution720P}},
		}),
	}, newVideoFailoverPricing(VideoResolution1080P, 0.4))
	service.startLifecycleFunc = service.runLifecycle
	resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution1080P))
	require.NoError(t, err)
	task := waitForVideoTaskStatus(t, repo, resp.ID, VideoTaskStatusFailed)
	require.NotNil(t, task.RefundedAt)
	require.Zero(t, upstreamB.createCalls.Load())
	require.Len(t, billing.commands, 2)
}

func TestVideoUnknownPollOutcomeNeverResubmits(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstreamA := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-a"}`, `{"status":"processing"}`)
			upstreamA.pollStatus = status
			upstreamB := newVideoFailoverServer(t, http.StatusOK, `{"id":"task-b"}`, `{"status":"failed"}`)
			service, repo, _, billing := newVideoFailoverService(t, []Account{
				newVideoFailoverAccount(30, 0, upstreamA.server.URL, map[string]any{"poll_interval_ms": 5, "poll_timeout_ms": 50}),
				newVideoFailoverAccount(31, 10, upstreamB.server.URL, nil),
			}, newVideoFailoverPricing(VideoResolution720P, 0.2))
			service.startLifecycleFunc = service.runLifecycle
			resp, err := service.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
			require.NoError(t, err)
			task := waitForVideoTaskStatus(t, repo, resp.ID, VideoTaskStatusFailed)
			require.Equal(t, "task-a", *task.UpstreamTaskID)
			require.Zero(t, upstreamB.createCalls.Load(), "unknown upstream state must not trigger a duplicate generation")
			require.Len(t, billing.commands, 2)
		})
	}
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

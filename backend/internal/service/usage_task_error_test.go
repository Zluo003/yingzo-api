package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageTaskErrorPreservesUpstreamDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		code    int
		message string
	}{
		{"image moderation", 451, `{"error":{"message":"Image violates content policy"},"prompt":"private"}`, 451, "Image violates content policy"},
		{"rate limit", 429, `{"error":{"message":"Quota exceeded"}}`, 429, "Quota exceeded"},
		{"failed video poll", 200, `{"status":"failed","error":{"code":451,"message":"Video rejected"},"request":{"prompt":"private"}}`, 451, "Video rejected"},
		{"legacy", 0, `{"code":"video_generation_failed","message":"生成失败"}`, 0, "生成失败"},
		{"text response", 503, `upstream overloaded`, 503, "upstream overloaded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnostic := NewUsageTaskError(tc.status, []byte(tc.body), "fallback")
			require.Equal(t, tc.code, diagnostic.Code)
			require.Equal(t, tc.message, diagnostic.Message)
		})
	}

	diagnostic := NewUsageTaskError(429, nil, "see https://provider.test/?access_token=secret-value&key=private-key")
	require.NotContains(t, diagnostic.Message, "secret-value")
	require.NotContains(t, diagnostic.Message, "private-key")
	require.Nil(t, NewUsageTaskError(0, []byte(`{}`), ""))
}

func TestAsyncImageErrorSurvivesRefundRetry(t *testing.T) {
	execution := &AsyncImageExecution{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(WithAsyncImageExecution(context.Background(), execution))
	SetOpsUpstreamError(c, 451, "actual upstream moderation message", "")
	require.Equal(t, 451, execution.TaskError.Code)

	ledger := &imageWorkerTestLedger{}
	svc := &DurableImageService{ledger: ledger}
	task := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{TaskError: execution.TaskError}}
	svc.fail(task, "image generation failed")
	require.Equal(t, "actual upstream moderation message", ledger.final.TaskError.Message)
	// Simulate the persisted record being reloaded after a failed settlement.
	raw, err := json.Marshal(task.ImageTaskRecord)
	require.NoError(t, err)
	var recovered DurableImageTask
	require.NoError(t, json.Unmarshal(raw, &recovered.ImageTaskRecord))
	svc.fail(&recovered, "upstream execution could not be confirmed before its lease or deadline expired")
	require.Equal(t, 451, ledger.final.TaskError.Code)
	require.Equal(t, "actual upstream moderation message", ledger.final.TaskError.Message)
}

func TestVideoFailureDiagnosticDoesNotChangePublicError(t *testing.T) {
	client := videoClientError("video_generation_failed", "视频生成失败")
	payload := []byte(`{"status":"failed","error":{"code":451,"message":"Original policy rejection"}}`)
	result := videoFailureErrorJSON(client, &videoUpstreamError{StatusCode: 451, Body: payload, TaskFailure: true})
	require.Equal(t, 451, result["task_error"].(*UsageTaskError).Code)
	require.Equal(t, "Original policy rejection", result["task_error"].(*UsageTaskError).Message)
	public := videoErrorFromJSON(result)
	require.Equal(t, client, *public)
	// An inferred retry status must not be presented as a real upstream code.
	result = videoFailureErrorJSON(client, &videoUpstreamError{StatusCode: 503, Body: []byte(`{"error":"render failed"}`), TaskFailure: true})
	require.Zero(t, result["task_error"].(*UsageTaskError).Code)
}

func TestVideoRefundRetainsActualUpstreamError(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		create, poll string
		code         int
		message      string
	}{
		{"create rate limit", 429, `{"error":{"message":"provider quota exceeded"}}`, "", 429, "provider quota exceeded"},
		{"poll moderation", 200, `{"id":"upstream-task","status":"queued"}`, `{"status":"failed","error":{"code":451,"message":"provider content policy rejection"}}`, 451, "provider content policy rejection"},
		{"poll failure exhaustion", 200, `{"id":"upstream-task","status":"queued"}`, `{"status":"failed","error":{"code":503,"message":"renderer unavailable"}}`, 503, "renderer unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newVideoFailoverServer(t, tc.status, tc.create, tc.poll)
			svc, tasks, logs, billing := newVideoFailoverService(t, []Account{
				newVideoFailoverAccount(30, 0, upstream.server.URL, map[string]any{"poll_interval_ms": 5}),
			}, newVideoFailoverPricing(VideoResolution720P, 0.2))
			svc.startLifecycleFunc = svc.runLifecycle
			response, err := svc.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
			require.NoError(t, err)
			task, err := tasks.GetByPublicID(context.Background(), response.ID)
			require.NoError(t, err)
			require.NotNil(t, task.RefundedAt)
			require.Len(t, billing.commands, 2)
			require.Len(t, logs.logs, 2)
			raw, err := json.Marshal(task.ErrorJSON["task_error"])
			require.NoError(t, err)
			var diagnostic UsageTaskError
			require.NoError(t, json.Unmarshal(raw, &diagnostic))
			require.Equal(t, tc.code, diagnostic.Code)
			require.Equal(t, tc.message, diagnostic.Message)
		})
	}
}

func TestDurableImageWorkerRefundRetainsUpstreamError(t *testing.T) {
	ledger := &imageWorkerTestLedger{}
	key := &APIKey{ID: 2, UserID: 1, Key: "fixture"}
	svc := &DurableImageService{
		ledger: ledger, encryptor: fileStorageEncryptor{}, files: &FileStorageService{},
		keys: &APIKeyService{apiKeyRepo: imageWorkerKeyRepo{key: key}},
	}
	snapshot, err := json.Marshal(ImageRequestSnapshot{Method: "POST", Path: "/v1/images/generations"})
	require.NoError(t, err)
	protected, err := svc.encryptor.Encrypt(string(snapshot))
	require.NoError(t, err)
	task := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: "imgtask_failure", UserID: 1, APIKeyID: 2, Phase: "executing", DeadlineAt: time.Now().Add(time.Minute).Unix()}, EncryptedRequest: protected}
	svc.run(context.Background(), task, func(ctx context.Context, _ *ImageRequestSnapshot, _ *APIKey, _ *AsyncImageExecution) (int, json.RawMessage, error) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil).WithContext(ctx)
		SetOpsUpstreamError(c, 451, "Original upstream rejection", "")
		// Gateway maps the response to a generic 502; usage retains upstream 451.
		return http.StatusBadGateway, json.RawMessage(`{"error":{"message":"image generation failed"}}`), nil
	})
	require.NotNil(t, ledger.final)
	require.False(t, ledger.success)
	require.Equal(t, 451, ledger.final.TaskError.Code)
	require.Equal(t, "Original upstream rejection", ledger.final.TaskError.Message)
}

func TestVideoPollTimeoutRefundRetainsLastUpstreamError(t *testing.T) {
	upstream := newVideoFailoverServer(t, 200, `{"id":"upstream-task","status":"queued"}`, `{"error":{"message":"poll quota exceeded"}}`)
	upstream.pollStatus = 429
	svc, tasks, _, _ := newVideoFailoverService(t, []Account{
		newVideoFailoverAccount(30, 0, upstream.server.URL, map[string]any{"poll_interval_ms": 5, "poll_timeout_ms": 80}),
	}, newVideoFailoverPricing(VideoResolution720P, 0.2))
	svc.startLifecycleFunc = svc.runLifecycle
	response, err := svc.CreateTask(context.Background(), newVideoFailoverCreateInput(VideoResolution720P))
	require.NoError(t, err)
	task, err := tasks.GetByPublicID(context.Background(), response.ID)
	require.NoError(t, err)
	require.NotNil(t, task.RefundedAt)
	raw, err := json.Marshal(task.ErrorJSON["task_error"])
	require.NoError(t, err)
	var diagnostic UsageTaskError
	require.NoError(t, json.Unmarshal(raw, &diagnostic))
	require.Equal(t, 429, diagnostic.Code)
	require.Equal(t, "poll quota exceeded", diagnostic.Message)
}

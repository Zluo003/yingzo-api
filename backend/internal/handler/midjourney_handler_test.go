package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type midjourneyReceiptLedger struct {
	service.ImageTaskLedger
	task *service.DurableImageTask
}

type midjourneyTestEncryptor struct{}

func (midjourneyTestEncryptor) Encrypt(value string) (string, error) { return value, nil }
func (midjourneyTestEncryptor) Decrypt(value string) (string, error) { return value, nil }

func (l *midjourneyReceiptLedger) Find(_ context.Context, owner service.ImageTaskOwner, _ string, _ bool) (*service.DurableImageTask, error) {
	if owner.UserID != l.task.UserID || owner.APIKeyID != l.task.APIKeyID {
		return nil, service.ErrImageTaskNotFound
	}
	return l.task, nil
}

func TestMidjourneyAcceptedReplayWithoutStorageOrUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parsed, err := service.ParseMidjourneyRequest([]byte(`{"prompt":"cat"}`), "imagine")
	require.NoError(t, err)
	canonical, _ := json.Marshal(parsed)
	fingerprint, err := imageRequestFingerprint("/v1/midjourney/generations", "application/json", canonical)
	require.NoError(t, err)
	ledger := &midjourneyReceiptLedger{task: &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: "imgtask_original", UserID: 1, APIKeyID: 2, Status: service.ImageTaskStatusCompleted}, Fingerprint: fingerprint}}
	durable := service.NewDurableImageService(ledger, midjourneyTestEncryptor{}, &service.FileStorageService{}, nil, nil, nil, nil, nil, &service.APIKeyService{}, nil, nil, nil)
	h := &AsyncImageHandler{durable: durable}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"prompt":"cat"}`, 202},
		{`{"prompt":"cat","version":"8.2","model":"midjourney-v8.2","speed":"fast"}`, 202},
		{`{"prompt":"dog"}`, 409},
		{`{"prompt":"cat","speed":"turbo"}`, 400},
		{`{"prompt":"cat","version":"7"}`, 400},
	} {
		t.Run(tc.body, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/midjourney/generations/imagine", strings.NewReader(tc.body))
			c.Request.Header.Set("Idempotency-Key", "same-key")
			gid := int64(7)
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &service.Group{ID: gid, Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
			h.Midjourney(c)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusAccepted {
				require.Contains(t, w.Body.String(), "imgtask_original")
				require.Equal(t, "/v1/images/tasks/imgtask_original", w.Header().Get("Location"))
			}
		})
	}
}

func TestMidjourneyPublicModelAndOperationEstimates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gid := int64(9)
	prices := []service.AgentModelPrice{
		{Resolution: "generation", BillingUnit: "request", UnitPrice: 0.2},
		{Resolution: "upscale", BillingUnit: "request", UnitPrice: 0.05},
	}
	repo := &gatewayAgentModelRepoStub{models: []service.AgentGroupModel{{ID: 1, GroupID: gid, Platform: service.PlatformOpenAI, ModelCode: service.MidjourneyModel, MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true, Prices: prices}}}
	accounts := publicAgentImageAccountRepo{account: service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Extra: map[string]any{"image_provider": service.MidjourneyProvider}}}
	groups := &agentPricingGroupRepoStub{group: &service.Group{ID: gid, Kind: "agent", SystemCode: "yingzo"}}
	h := &AgentHandler{agentModels: service.NewAgentModelCatalogService(accounts, groups, repo), billingService: &service.BillingService{}, fileStorage: &service.FileStorageService{}}
	key := &service.APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &service.Group{ID: gid, Kind: "agent", SystemCode: "yingzo"}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/models", nil)
	c.Set(string(middleware.ContextKeyAPIKey), key)
	h.Models(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var manifest struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &manifest))
	require.Len(t, manifest.Data, 1)
	require.Equal(t, service.MidjourneyModel, manifest.Data[0]["id"])
	require.Equal(t, service.MidjourneyModel, manifest.Data[0]["display_name"])
	require.Equal(t, map[string]any{"supported_speeds": []any{"fast"}}, manifest.Data[0]["capabilities"])
	for _, tc := range []struct {
		body, tier string
		price      float64
	}{
		{`{"prompt":"cat"}`, "generation", 0.2},
		{`{"prompt":"cat","speed":"fast"}`, "generation", 0.2},
		{`{"prompt":"keep the cat, change background","image_urls":["https://image.example/cat.png"]}`, "generation", 0.2},
		{`{"prompt":"cat","sref":"https://image.example/style.png","sw":200}`, "generation", 0.2},
		{`{"task_id":"imgtask_parent","index":3}`, "upscale", 0.05},
	} {
		q, err := h.calculateGenerationQuote(context.Background(), key, agentGenerationEstimateRequest{Kind: "image", Platform: service.PlatformOpenAI, Model: service.MidjourneyModel, Count: 1, Request: json.RawMessage(tc.body)})
		require.NoError(t, err)
		require.Equal(t, "request", q.UnitKind)
		require.Equal(t, tc.tier, q.Details["operation"])
		require.Equal(t, tc.price, q.ActualPrice)
	}
	_, err := h.calculateGenerationQuote(context.Background(), key, agentGenerationEstimateRequest{Kind: "image", Platform: service.PlatformOpenAI, Model: service.MidjourneyModel, Count: 1, Request: json.RawMessage(`{"prompt":"cat","speed":"turbo"}`)})
	require.Error(t, err)
}

func TestMidjourneyEditsIdempotencyAndAudit(t *testing.T) {
	original := `{"prompt":"preserve the person","image_urls":["https://image.example/person.png"],"sref":"https://image.example/style.png","sw":200}`
	parsed, err := service.ParseMidjourneyRequest([]byte(original), "edits")
	require.NoError(t, err)
	audit := service.ExtractContentModerationInput(service.ContentModerationProtocolOpenAIImages, midjourneyAuditBody(parsed))
	require.Equal(t, "preserve the person", audit.Text)
	require.Equal(t, []string{"https://image.example/person.png", "https://image.example/style.png"}, audit.Images)
	canonical, _ := json.Marshal(parsed)
	fingerprint, err := imageRequestFingerprint("/v1/midjourney/generations/edits", "application/json", canonical)
	require.NoError(t, err)
	ledger := &midjourneyReceiptLedger{task: &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: "imgtask_edit", UserID: 1, APIKeyID: 2, Status: service.ImageTaskStatusCompleted}, Fingerprint: fingerprint}}
	durable := service.NewDurableImageService(ledger, midjourneyTestEncryptor{}, &service.FileStorageService{}, nil, nil, nil, nil, nil, &service.APIKeyService{}, nil, nil, nil)
	h := &AsyncImageHandler{durable: durable}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"edits", original, 202},
		{"edits", strings.Replace(original, `"sw":200`, `"sw":200,"speed":"fast"`, 1), 202},
		{"edits", strings.Replace(original, "person.png", "another.png", 1), 409},
		{"edits", strings.Replace(original, `"sw":200`, `"sw":100`, 1), 409},
		{"imagine", original, 409},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/midjourney/generations/"+tc.path, strings.NewReader(tc.body))
		c.Request.Header.Set("Idempotency-Key", "same-edit")
		gid := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &service.Group{ID: gid, Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
		h.Midjourney(c)
		require.Equal(t, tc.status, w.Code, w.Body.String())
	}
}

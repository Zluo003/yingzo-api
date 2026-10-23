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

func TestSeedreamAcceptedReplayCanonicalizesDefaultsAndPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req, err := service.ParseSeedreamRequest([]byte(`{"model":"seedream-5-0-pro","prompt":"cat","size":"1.5K"}`))
	require.NoError(t, err)
	canonical, _ := json.Marshal(req)
	fingerprint, err := imageRequestFingerprint("/v1/images/generations", "application/json", canonical)
	require.NoError(t, err)
	ledger := &midjourneyReceiptLedger{task: &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: "imgtask_original", UserID: 1, APIKeyID: 2, Status: service.ImageTaskStatusCompleted}, Fingerprint: fingerprint}}
	durable := service.NewDurableImageService(ledger, midjourneyTestEncryptor{}, &service.FileStorageService{}, nil, nil, nil, nil, nil, &service.APIKeyService{}, nil, nil, nil)
	h := &AsyncImageHandler{durable: durable}
	for _, tc := range []struct {
		body, path string
		code       int
	}{
		{`{"model":"seedream-5-0-pro","prompt":"cat","size":"1.5K"}`, "/v1/images/generations", 202},
		{`{"model":"seedream-5.0-pro","prompt":"cat","resolution":"1.5k","size":"auto","n":1,"output_format":"jpeg","background":"opaque"}`, "/v1/images/generations/async", 202},
		{`{"model":"seedream-5-0-pro","prompt":"dog","size":"1.5K"}`, "/v1/images/generations", 409},
		{`{"model":"seedream-5-0-flash","prompt":"cat","size":"1.5K"}`, "/v1/images/generations", 409},
		{`{"model":"seedream-5-0-pro","prompt":"cat","n":2}`, "/v1/images/generations", 400},
		{`{"model":"seedream-5-0-pro","prompt":"cat"}`, "/v1/images/edits", 400},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		c.Request.Header.Set("Idempotency-Key", "same-key")
		c.Request.Header.Set("Content-Type", "application/json")
		gid := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &service.Group{ID: gid, Platform: service.PlatformOpenAI, AllowImageGeneration: true}})
		require.True(t, h.TrySeedream(c))
		require.Equal(t, tc.code, w.Code, w.Body.String())
		if tc.code == 202 {
			require.Contains(t, w.Body.String(), "imgtask_original")
			require.Equal(t, "/v1/images/tasks/imgtask_original", w.Header().Get("Location"))
		}
	}
}
func TestSeedreamCatalogueEstimateAndReferencePricing(t *testing.T) {
	gid := int64(9)
	free, price := 1, 0.03
	repo := &gatewayAgentModelRepoStub{models: []service.AgentGroupModel{{ID: 1, GroupID: gid, Platform: service.PlatformOpenAI, ModelCode: service.SeedreamProModel, MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true, FreeInputImages: &free, InputImageUnitPrice: &price, Prices: []service.AgentModelPrice{{Resolution: "1K", BillingUnit: "image", UnitPrice: 0.1}, {Resolution: "2K", BillingUnit: "image", UnitPrice: 0.2}}}}}
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Extra: map[string]any{"image_account": true, "image_provider": service.SeedreamProvider}, Credentials: map[string]any{"model_mapping": map[string]any{service.SeedreamProModel: service.SeedreamProModel, service.SeedreamFlashModel: service.SeedreamFlashModel}}}
	groups := &agentPricingGroupRepoStub{group: &service.Group{ID: gid, Kind: "agent", SystemCode: "yingzo"}}
	h := &AgentHandler{agentModels: service.NewAgentModelCatalogService(publicAgentImageAccountRepo{account: account}, groups, repo), billingService: &service.BillingService{}}
	key := &service.APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: groups.group}
	for _, tc := range []struct {
		body    string
		charge  float64
		outputs int
	}{
		{`{"prompt":"cat","resolution":"1.5K","image_urls":["https://image.example/a.png","https://image.example/b.png"]}`, 0.23, 1},
		{`{"layer_decomposition":true,"image_urls":["https://image.example/a.png"]}`, 3.4, 17},
	} {
		q, err := h.calculateGenerationQuote(context.Background(), key, agentGenerationEstimateRequest{Kind: "image", Model: service.SeedreamProModel, Platform: service.PlatformOpenAI, Count: 1, Request: json.RawMessage(tc.body)})
		require.NoError(t, err)
		require.InDelta(t, tc.charge, q.ActualPrice, 1e-9)
		require.Equal(t, tc.outputs, q.Details["reserved_output_images"])
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware.ContextKeyAPIKey), key)
	h.Models(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var manifest struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &manifest))
	require.Len(t, manifest.Data, 1)
	capabilities, ok := manifest.Data[0]["capabilities"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(10), capabilities["max_input_images"])
	require.Equal(t, []any{"1K", "1.5K", "2K"}, capabilities["supported_image_sizes"])
	require.Equal(t, true, capabilities["asynchronous"])
	require.Equal(t, true, capabilities["supports_layer_decomposition"])
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/agent/pricing", nil)
	c.Set(string(middleware.ContextKeyAPIKey), key)
	h.GetAgentPricingSnapshot(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"input_image_unit_price":0.03`)
	require.Contains(t, w.Body.String(), `"free_input_images":1`)
}

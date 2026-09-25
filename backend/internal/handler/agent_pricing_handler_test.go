package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentPricingGroupRepoStub struct {
	service.GroupRepository
	group *service.Group
}

func (s *agentPricingGroupRepoStub) GetByIDLite(context.Context, int64) (*service.Group, error) {
	copy := *s.group
	return &copy, nil
}

func newAgentPricingHandlerForTest(groupID int64, models []service.AgentGroupModel) *AgentHandler {
	accounts := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{}}
	groups := &agentPricingGroupRepoStub{group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}}
	repo := &gatewayAgentModelRepoStub{models: models}
	return &AgentHandler{agentModels: service.NewAgentModelCatalogService(accounts, groups, repo)}
}

func agentTextModelRate(rate float64) *float64 { return &rate }

func agentPricingRequestContext(groupID int64) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/agent/pricing", nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 3, UserID: 7, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"},
	})
	return c, recorder
}

func TestGetAgentPricingSnapshotUsesPerModelTextRateAndMediaPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9)
	h := newAgentPricingHandlerForTest(groupID,
		[]service.AgentGroupModel{
			{
				ID: 3, GroupID: groupID, Platform: service.PlatformOpenAI, ModelCode: "gpt-text",
				MediaType: service.AgentMediaTypeText, Enabled: true, Available: true,
				RateMultiplier: agentTextModelRate(2), Prices: []service.AgentModelPrice{},
			},
			{
				ID: 1, GroupID: groupID, Platform: service.PlatformOpenAI, ModelCode: "image-custom",
				MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
				Prices: []service.AgentModelPrice{
					{Resolution: service.ImageBillingSize1K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.1},
					{Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.2},
					{Resolution: service.ImageBillingSize4K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.4, Enabled: boolPointer(false)},
				},
			},
			{
				ID: 2, GroupID: groupID, Platform: service.PlatformVideo, ModelCode: "video-custom",
				MediaType: service.AgentMediaTypeVideo, Enabled: true, Available: true,
				Prices: []service.AgentModelPrice{{Resolution: "1080p", BillingUnit: service.AgentBillingUnitSecond, UnitPrice: 0.3}},
			},
		},
	)
	c, recorder := agentPricingRequestContext(groupID)

	h.GetAgentPricingSnapshot(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var snapshot agentPricingSnapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &snapshot))
	require.Equal(t, "3.0.0", snapshot.SchemaVersion)
	require.Contains(t, snapshot.PricingVersion, "price_")
	require.Equal(t, "credit", snapshot.Currency)
	require.WithinDuration(t, time.Now().UTC(), snapshot.FetchedAt, time.Second)
	require.Equal(t, agentPricingSnapshotTTL, snapshot.ValidUntil.Sub(snapshot.FetchedAt))
	require.Len(t, snapshot.Rules, 4)

	language := snapshot.Rules[0]
	require.Equal(t, "gpt-text", language.Model)
	require.Equal(t, service.PlatformOpenAI, language.Platform)
	require.Equal(t, service.AgentMediaTypeText, language.MediaType)
	require.Equal(t, "channel_price_multiplier", language.UnitKind)
	require.InDelta(t, 2, language.BillingMultiplier, 1e-12)

	for i, expected := range []float64{0.1, 0.2} {
		image := snapshot.Rules[i+1]
		require.Equal(t, "image-custom", image.Model)
		require.Equal(t, service.AgentBillingUnitImage, image.UnitKind)
		require.InDelta(t, expected, image.UnitPrice, 1e-12)
	}
	video := snapshot.Rules[3]
	require.Equal(t, "video-custom", video.Model)
	require.Equal(t, service.PlatformVideo, video.Platform)
	require.Equal(t, service.AgentBillingUnitSecond, video.UnitKind)
	require.InDelta(t, 0.3, video.EffectiveUnitPrice, 1e-12)
}

func TestGetAgentPricingSnapshotReturnsOnlyConfiguredPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(19)
	h := newAgentPricingHandlerForTest(groupID, []service.AgentGroupModel{{
		ID: 1, GroupID: groupID, Platform: service.PlatformOpenAI, ModelCode: "image-custom",
		MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
		Prices: []service.AgentModelPrice{{Resolution: service.ImageBillingSize1K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.1}},
	}})
	c, recorder := agentPricingRequestContext(groupID)

	h.GetAgentPricingSnapshot(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var snapshot agentPricingSnapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &snapshot))
	require.Len(t, snapshot.Rules, 1)
	require.Equal(t, service.ImageBillingSize1K, snapshot.Rules[0].Resolution)
}

func TestGetAgentPricingCompatibilitySkipsDisabledVideoResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(21)
	h := newAgentPricingHandlerForTest(groupID, []service.AgentGroupModel{{
		ID: 1, GroupID: groupID, Platform: service.PlatformVideo, ModelCode: "video-custom",
		MediaType: service.AgentMediaTypeVideo, Enabled: true, Available: true,
		Prices: []service.AgentModelPrice{
			{Resolution: "720p", BillingUnit: service.AgentBillingUnitSecond, UnitPrice: 0.2},
			{Resolution: "4K", BillingUnit: service.AgentBillingUnitSecond, UnitPrice: 0.8, Enabled: boolPointer(false)},
		},
	}})
	c, recorder := agentPricingRequestContext(groupID)

	h.GetAgentPricingCompatibility(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Data []struct {
			Model  string `json:"model_name"`
			Schema struct {
				Resolution struct {
					Enum []string `json:"enum"`
				} `json:"resolution"`
			} `json:"billing_usage_schema"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.Data, 1)
	require.Equal(t, "video-custom", payload.Data[0].Model)
	require.Equal(t, []string{"720p"}, payload.Data[0].Schema.Resolution.Enum)
}

func TestGetAgentPricingSnapshotAcceptsExplicitZeroPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(20)
	h := newAgentPricingHandlerForTest(groupID,
		[]service.AgentGroupModel{
			{
				ID: 2, GroupID: groupID, Platform: service.PlatformGemini, ModelCode: "gemini-free-text",
				MediaType: service.AgentMediaTypeText, Enabled: true, Available: true,
				RateMultiplier: agentTextModelRate(0), Prices: []service.AgentModelPrice{},
			},
			{
				ID: 1, GroupID: groupID, Platform: service.PlatformGemini, ModelCode: "image-free",
				MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
				Prices: []service.AgentModelPrice{{Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0}},
			},
		},
	)
	c, recorder := agentPricingRequestContext(groupID)

	h.GetAgentPricingSnapshot(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var snapshot agentPricingSnapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &snapshot))
	require.Len(t, snapshot.Rules, 2)
	require.Zero(t, snapshot.Rules[0].BillingMultiplier)
	require.Zero(t, snapshot.Rules[1].UnitPrice)
}

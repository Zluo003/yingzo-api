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

func newAgentPricingHandlerForTest(groupID int64, rates []service.AgentPlatformRate, models []service.AgentGroupModel) *AgentHandler {
	accounts := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{}}
	groups := &agentPricingGroupRepoStub{group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}}
	repo := &gatewayAgentModelRepoStub{models: models, rates: rates}
	return &AgentHandler{agentModels: service.NewAgentModelCatalogService(accounts, groups, repo)}
}

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

func TestGetAgentPricingSnapshotUsesPlatformRatesAndPerModelMediaPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9)
	h := newAgentPricingHandlerForTest(groupID,
		[]service.AgentPlatformRate{{GroupID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 2}},
		[]service.AgentGroupModel{
			{
				ID: 1, GroupID: groupID, Platform: service.PlatformOpenAI, ModelCode: "image-custom",
				MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
				Prices: []service.AgentModelPrice{
					{Resolution: service.ImageBillingSize1K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.1},
					{Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.2},
					{Resolution: service.ImageBillingSize4K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.4},
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
	require.Len(t, snapshot.Rules, 5)

	language := snapshot.Rules[0]
	require.Equal(t, service.PlatformOpenAI, language.Platform)
	require.Equal(t, service.AgentMediaTypeText, language.MediaType)
	require.Equal(t, "channel_price_multiplier", language.UnitKind)
	require.InDelta(t, 2, language.BillingMultiplier, 1e-12)

	for i, expected := range []float64{0.1, 0.2, 0.4} {
		image := snapshot.Rules[i+1]
		require.Equal(t, "image-custom", image.Model)
		require.Equal(t, service.AgentBillingUnitImage, image.UnitKind)
		require.InDelta(t, expected, image.UnitPrice, 1e-12)
	}
	video := snapshot.Rules[4]
	require.Equal(t, "video-custom", video.Model)
	require.Equal(t, service.PlatformVideo, video.Platform)
	require.Equal(t, service.AgentBillingUnitSecond, video.UnitKind)
	require.InDelta(t, 0.3, video.EffectiveUnitPrice, 1e-12)
}

func TestGetAgentPricingSnapshotReturnsOnlyConfiguredPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(19)
	h := newAgentPricingHandlerForTest(groupID, nil, []service.AgentGroupModel{{
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

func TestGetAgentPricingSnapshotAcceptsExplicitZeroPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(20)
	h := newAgentPricingHandlerForTest(groupID,
		[]service.AgentPlatformRate{{GroupID: groupID, Platform: service.PlatformGemini, RateMultiplier: 0}},
		[]service.AgentGroupModel{{
			ID: 1, GroupID: groupID, Platform: service.PlatformGemini, ModelCode: "image-free",
			MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
			Prices: []service.AgentModelPrice{{Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0}},
		}},
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

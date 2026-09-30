package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func yingzoTestFloat(value float64) *float64 { return &value }

func yingzoTestPlaza(models []AgentGroupModel, accounts []Account, channels []Channel) (*ModelPlazaService, *AgentModelCatalogService) {
	repo := newAgentModelMemoryRepo()
	for _, model := range models {
		model.GroupID = 10
		repo.models[agentModelKey(model.Platform, model.ModelCode)] = &model
	}
	catalog := NewAgentModelCatalogService(&agentCatalogAccountRepoStub{accounts: accounts}, &agentCatalogGroupRepoStub{group: &Group{ID: 10, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 99}}, repo)
	channelService := agentPricingChannelServiceForTest(channels, map[int64]string{10: "agent", 20: PlatformOpenAI, 30: PlatformOpenAI})
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(channelService, billing)
	return NewModelPlazaService(nil, nil, nil, billing, resolver), catalog
}

func TestYingzoPlazaEnabledConfiguredCatalogue(t *testing.T) {
	disabled := false
	s, catalog := yingzoTestPlaza([]AgentGroupModel{
		{ModelCode: "text-enabled", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: true, Available: false, RateMultiplier: yingzoTestFloat(1.5)},
		{ModelCode: "text-disabled", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: false, Available: true},
		{ModelCode: "text-excluded", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: true, Available: true, Excluded: true},
		{ModelCode: "text-no-rate", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: true},
		{ModelCode: "text-no-channel", Platform: PlatformAnthropic, MediaType: AgentMediaTypeText, Enabled: true, RateMultiplier: yingzoTestFloat(1)},
		{ModelCode: "image", Platform: PlatformGemini, MediaType: AgentMediaTypeImage, Enabled: true, RateMultiplier: yingzoTestFloat(99), Prices: []AgentModelPrice{
			{Resolution: "1K", BillingUnit: AgentBillingUnitImage, UnitPrice: 0.2},
			{Resolution: "2K", BillingUnit: AgentBillingUnitImage, UnitPrice: 0.4, Enabled: &disabled},
			{Resolution: "4K", BillingUnit: AgentBillingUnitImage, UnitPrice: 0},
			{Resolution: "bad", BillingUnit: AgentBillingUnitSecond, UnitPrice: 10},
		}},
		{ModelCode: "video", Platform: PlatformVideo, MediaType: AgentMediaTypeVideo, Enabled: true, Prices: []AgentModelPrice{{Resolution: "1080p", BillingUnit: AgentBillingUnitSecond, UnitPrice: 0.8}}},
	}, nil, []Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{10}, ModelPricing: []ChannelModelPricing{
		{Platform: PlatformOpenAI, Models: []string{"text-enabled", "text-disabled", "text-no-rate", "channel-only"}, InputPrice: yingzoTestFloat(2e-6), OutputPrice: yingzoTestFloat(8e-6), CacheReadPrice: yingzoTestFloat(0.2e-6), CacheWritePrice: yingzoTestFloat(3e-6)},
	}}})
	result, err := s.ListYingzoModels(context.Background(), catalog, 10)
	require.NoError(t, err)
	require.Equal(t, "CNY", result.Currency)
	require.Len(t, result.Models, 5)
	byName := map[string]YingzoPlazaModel{}
	for _, model := range result.Models {
		byName[model.ModelCode] = model
	}
	require.NotContains(t, byName, "channel-only")
	require.NotContains(t, byName, "text-disabled")
	require.NotContains(t, byName, "text-excluded")
	textPrice := byName["text-enabled"].Prices[0]
	require.InDelta(t, 3, *textPrice.InputPrice, 1e-9)
	require.InDelta(t, 12, *textPrice.OutputPrice, 1e-9)
	require.InDelta(t, 0.3, *textPrice.CacheRead, 1e-9)
	require.InDelta(t, 4.5, *textPrice.CacheWrite, 1e-9)
	require.Equal(t, "unavailable", byName["text-no-rate"].PricingStatus)
	require.Empty(t, byName["text-no-rate"].Prices)
	require.Equal(t, "unavailable", byName["text-no-channel"].PricingStatus)
	require.Len(t, byName["image"].Prices, 2)
	require.Equal(t, 0.2, *byName["image"].Prices[0].UnitPrice)
	require.Zero(t, *byName["image"].Prices[1].UnitPrice)
	require.Equal(t, 0.8, *byName["video"].Prices[0].UnitPrice)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"channel_id", "account_id", "group_id", "rate_multiplier", "cost_price", "credentials"} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestYingzoPlazaTextSourceAccountVariantsAndFreeRates(t *testing.T) {
	for _, rate := range []float64{0, 2} {
		accounts := []Account{
			{ID: 1, Platform: PlatformOpenAI, GroupIDs: []int64{10, 20}, Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-model"}}},
			{ID: 2, Platform: PlatformOpenAI, GroupIDs: []int64{10, 30}, Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-model"}}},
			// Same source price deduplicates; an unrelated mapping cannot add a price.
			{ID: 3, Platform: PlatformOpenAI, GroupIDs: []int64{10, 20}, Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-model"}}},
		}
		s, catalog := yingzoTestPlaza([]AgentGroupModel{{ModelCode: "configured-model", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: true, RateMultiplier: yingzoTestFloat(rate)}}, accounts, []Channel{
			{ID: 1, Status: StatusActive, GroupIDs: []int64{20}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"configured-model"}, InputPrice: yingzoTestFloat(1e-6), OutputPrice: yingzoTestFloat(3e-6)}}},
			{ID: 2, Status: StatusActive, GroupIDs: []int64{30}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"configured-model"}, InputPrice: yingzoTestFloat(2e-6), OutputPrice: yingzoTestFloat(4e-6)}}},
		})
		result, err := s.ListYingzoModels(context.Background(), catalog, 10)
		require.NoError(t, err)
		prices := result.Models[0].Prices
		if rate == 0 {
			require.Len(t, prices, 1)
			require.Zero(t, *prices[0].InputPrice)
		} else {
			require.Len(t, prices, 2)
			require.InDelta(t, 2, *prices[0].InputPrice, 1e-9)
			require.InDelta(t, 4, *prices[1].InputPrice, 1e-9)
		}
	}
}

func TestYingzoPlazaPerRequestTextPrice(t *testing.T) {
	s, catalog := yingzoTestPlaza([]AgentGroupModel{{ModelCode: "request-model", Platform: PlatformOpenAI, MediaType: AgentMediaTypeText, Enabled: true, RateMultiplier: yingzoTestFloat(3)}}, nil, []Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{10}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"request-model"}, BillingMode: BillingModePerRequest, PerRequestPrice: yingzoTestFloat(0.2)}}}})
	result, err := s.ListYingzoModels(context.Background(), catalog, 10)
	require.NoError(t, err)
	require.Equal(t, "request", result.Models[0].Prices[0].Unit)
	require.InDelta(t, 0.6, *result.Models[0].Prices[0].UnitPrice, 1e-9)
}

func TestYingzoPlazaMidjourneyOperationPrices(t *testing.T) {
	for _, generationEnabled := range []bool{true, false} {
		s, catalog := yingzoTestPlaza([]AgentGroupModel{{ModelCode: MidjourneyModel, Platform: PlatformOpenAI, MediaType: AgentMediaTypeImage, Enabled: true, Prices: []AgentModelPrice{
			{Resolution: "generation", BillingUnit: "request", UnitPrice: 0.7, Enabled: &generationEnabled},
			{Resolution: "upscale", BillingUnit: "request", UnitPrice: 0},
			{Resolution: "imagine_turbo", BillingUnit: "request", UnitPrice: 1.4},
			{Resolution: "1K", BillingUnit: "image", UnitPrice: 0.3},
		}}}, nil, nil)
		result, err := s.ListYingzoModels(context.Background(), catalog, 10)
		require.NoError(t, err)
		require.Len(t, result.Models, 1)
		model := result.Models[0]
		require.Equal(t, "available", model.PricingStatus)
		expected := []YingzoModelPrice{{Unit: "request", Resolution: "upscale", UnitPrice: yingzoTestFloat(0)}}
		if generationEnabled {
			expected = append([]YingzoModelPrice{{Unit: "request", Resolution: "generation", UnitPrice: yingzoTestFloat(0.7)}}, expected...)
		}
		require.Equal(t, expected, model.Prices)
	}
}

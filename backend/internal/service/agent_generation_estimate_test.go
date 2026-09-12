package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCalculateConfiguredAgentImageCostUsesFinalModelPrice(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	cost, err := svc.CalculateConfiguredAgentImageCost(0.4, 2)
	require.NoError(t, err)
	require.InDelta(t, 0.8, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.8, cost.ActualCost, 1e-12)
	require.Equal(t, string(BillingModeImage), cost.BillingMode)

	cost, err = svc.CalculateConfiguredAgentImageCost(0, 2)
	require.NoError(t, err)
	require.Zero(t, cost.TotalCost)
	require.Zero(t, cost.ActualCost)
}

func TestLegacyGroupImageEstimateRejectsAgentPricing(t *testing.T) {
	groupID := int64(9)
	svc := NewBillingService(&config.Config{}, nil)
	cost, tier, err := svc.EstimateImageGenerationCost(
		context.Background(),
		&APIKey{UserID: 42, GroupID: &groupID, Group: &Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}},
		nil,
		"gpt-image-2",
		"2K",
		1,
	)
	require.Nil(t, cost)
	require.Equal(t, ImageBillingSize2K, tier)
	require.ErrorIs(t, err, ErrAgentImagePricingUnavailable)
}

func configuredAgentCatalogForEstimateTest(
	t *testing.T,
	groupID int64,
	platform string,
	modelCode string,
	mediaType string,
	resolution string,
	unitPrice float64,
) (*AgentModelCatalogService, *agentCatalogAccountRepoStub) {
	t.Helper()
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		ID: 1, Platform: platform,
		Credentials: map[string]any{"model_mapping": map[string]any{modelCode: modelCode}},
	}}}
	groups := &agentCatalogGroupRepoStub{group: &Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}}
	models := newAgentModelMemoryRepo()
	catalog := NewAgentModelCatalogService(accounts, groups, models)
	config, err := catalog.Sync(context.Background(), groupID)
	require.NoError(t, err)
	require.Len(t, config.Models, 1)
	_, err = catalog.UpdateModel(context.Background(), groupID, config.Models[0].ID, AgentModelConfigInput{
		MediaType: mediaType,
		Enabled:   true,
		Prices:    []AgentModelPrice{{Resolution: resolution, UnitPrice: unitPrice}},
	})
	require.NoError(t, err)
	return catalog, accounts
}

func TestEstimateAgentVideoUsesDynamicCatalogPriceAndGeneratedSecondsOnly(t *testing.T) {
	groupID := int64(9)
	catalog, _ := configuredAgentCatalogForEstimateTest(
		t, groupID, PlatformVideo, "video-custom", AgentMediaTypeVideo, VideoResolution720P, 0.2,
	)
	svc := NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.SetAgentModelCatalog(catalog)
	referenceDuration := 3.0
	request := &VideoCreateRequest{
		Model:       "video-custom",
		Prompt:      "continue the shot",
		Duration:    5,
		Resolution:  VideoResolution720P,
		AbilityCode: videoAbilityReferenceToVideo,
		Content: []VideoContent{{
			Type: "video_url", Role: "reference_video",
			VideoURL: &VideoContentURL{URL: "https://example.invalid/reference.mp4"}, DurationSeconds: &referenceDuration,
		}},
	}
	estimate, err := svc.EstimateGenerationCost(context.Background(), &APIKey{Group: &Group{
		ID: groupID, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 9,
	}}, request, 2)
	require.NoError(t, err)
	require.Equal(t, 10, estimate.BillableSeconds)
	require.Zero(t, estimate.ReferenceVideoMultiplier)
	require.InDelta(t, 1, estimate.RateMultiplier, 1e-12)
	require.InDelta(t, 2, estimate.TotalCost, 1e-12)
	require.InDelta(t, 2, estimate.ActualCost, 1e-12)
}

func TestAgentImageBillingUsesPerModelPriceForGatewayPaths(t *testing.T) {
	groupID := int64(91)
	catalog, _ := configuredAgentCatalogForEstimateTest(
		t, groupID, PlatformOpenAI, "shared-image", AgentMediaTypeImage, ImageBillingSize1K, 0.4,
	)
	billingService := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, billingService)
	resolver.SetAgentModelCatalog(catalog)
	group := &Group{ID: groupID, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 99}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Credentials: map[string]any{
		"model_mapping": map[string]any{"shared-image": "shared-image"},
	}}
	geminiGateway := &GatewayService{billingService: billingService, resolver: resolver}
	geminiCost, err := geminiGateway.calculateAgentRecordUsageCost(
		context.Background(),
		&ForwardResult{Model: "shared-image", ImageCount: 2, ImageSize: ImageBillingSize1K},
		group,
		account,
		[]string{"shared-image"},
		99,
	)
	require.NoError(t, err)
	require.InDelta(t, 0.8, geminiCost.TotalCost, 1e-12)
	require.InDelta(t, 0.8, geminiCost.ActualCost, 1e-12)

	openAIGateway := &OpenAIGatewayService{billingService: billingService, resolver: resolver}
	openAICost, err := openAIGateway.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{Model: "shared-image", ImageCount: 2, ImageSize: ImageBillingSize1K},
		&APIKey{GroupID: &groupID, Group: group},
		[]string{"shared-image"},
		99,
		99,
		99,
		99,
		UsageTokens{},
		"",
		nil,
		time.Now(),
	)
	require.NoError(t, err)
	require.InDelta(t, 0.8, openAICost.TotalCost, 1e-12)
	require.InDelta(t, 0.8, openAICost.ActualCost, 1e-12)
}

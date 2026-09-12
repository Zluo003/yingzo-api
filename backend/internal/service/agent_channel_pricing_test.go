package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveAgentAccountPricingUsesActualAccountPlatformChannel(t *testing.T) {
	tests := []struct {
		name     string
		platform string
	}{
		{name: "OpenAI", platform: PlatformOpenAI},
		{name: "Anthropic", platform: PlatformAnthropic},
		{name: "Gemini", platform: PlatformGemini},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agentGroupID := int64(100)
			sourceGroupID := int64(200 + i)
			channelID := int64(300 + i)
			inputPrice := 0.002 + float64(i)*0.001
			outputPrice := inputPrice * 2
			channelService := agentPricingChannelServiceForTest([]Channel{{
				ID:       channelID,
				Status:   StatusActive,
				GroupIDs: []int64{sourceGroupID},
				ModelPricing: []ChannelModelPricing{{
					ChannelID:   channelID,
					Platform:    test.platform,
					Models:      []string{"shared-language-model"},
					BillingMode: BillingModeToken,
					InputPrice:  &inputPrice,
					OutputPrice: &outputPrice,
				}},
			}}, map[int64]string{
				agentGroupID:  "agent",
				sourceGroupID: test.platform,
			})
			billingService := NewBillingService(&config.Config{}, nil)
			resolver := NewModelPricingResolver(channelService, billingService)
			account := &Account{
				ID:       int64(400 + i),
				Platform: test.platform,
				GroupIDs: []int64{agentGroupID, sourceGroupID},
			}

			resolved, err := resolver.ResolveAgentAccount(
				context.Background(), agentGroupID, account, "shared-language-model",
			)
			require.NoError(t, err)
			require.Equal(t, PricingSourceChannel, resolved.Source)
			require.Equal(t, sourceGroupID, resolved.PricingGroupID)
			require.Equal(t, channelID, resolved.ChannelID)

			cost, err := billingService.CalculateCostUnified(CostInput{
				Ctx:            context.Background(),
				Model:          "shared-language-model",
				Tokens:         UsageTokens{InputTokens: 10, OutputTokens: 5},
				RateMultiplier: 1.5,
				Resolver:       resolver,
				Resolved:       resolved,
			})
			require.NoError(t, err)
			require.InDelta(t, (inputPrice*10+outputPrice*5)*1.5, cost.ActualCost, 1e-12)
		})
	}
}

func TestResolveAgentAccountPricingExcludesAgentGroupAndOtherPlatforms(t *testing.T) {
	agentGroupID := int64(10)
	openAIGroupID := int64(11)
	geminiGroupID := int64(12)
	wrongAgentPrice := 9.0
	wrongPlatformPrice := 8.0
	openAIPrice := 0.01
	channelService := agentPricingChannelServiceForTest([]Channel{
		{ID: 20, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{{Platform: "agent", Models: []string{"model"}, InputPrice: &wrongAgentPrice}}},
		{ID: 21, Status: StatusActive, GroupIDs: []int64{geminiGroupID}, ModelPricing: []ChannelModelPricing{{Platform: PlatformGemini, Models: []string{"model"}, InputPrice: &wrongPlatformPrice}}},
		{ID: 22, Status: StatusActive, GroupIDs: []int64{openAIGroupID}, ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"model"}, InputPrice: &openAIPrice}}},
	}, map[int64]string{
		agentGroupID:  "agent",
		openAIGroupID: PlatformOpenAI,
		geminiGroupID: PlatformGemini,
	})

	match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), agentGroupID, &Account{
		ID: 1, Platform: PlatformOpenAI, GroupIDs: []int64{agentGroupID, openAIGroupID, geminiGroupID},
	}, "model")
	require.NoError(t, err)
	require.Equal(t, int64(22), match.ChannelID)
	require.NotNil(t, match.Pricing.InputPrice)
	require.InDelta(t, openAIPrice, *match.Pricing.InputPrice, 1e-12)
}

func TestResolveAgentAccountPricingDeduplicatesSameChannel(t *testing.T) {
	price := 0.01
	channelService := agentPricingChannelServiceForTest([]Channel{{
		ID:       30,
		Status:   StatusActive,
		GroupIDs: []int64{31, 32},
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformAnthropic, Models: []string{"claude-model"}, InputPrice: &price,
		}},
	}}, map[int64]string{31: PlatformAnthropic, 32: PlatformAnthropic})

	match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), 99, &Account{
		ID: 2, Platform: PlatformAnthropic, GroupIDs: []int64{99, 31, 32},
	}, "claude-model")
	require.NoError(t, err)
	require.Equal(t, int64(30), match.ChannelID)
}

func TestResolveAgentAccountPricingRejectsAmbiguousChannels(t *testing.T) {
	priceA, priceB := 0.01, 0.02
	channelService := agentPricingChannelServiceForTest([]Channel{
		{ID: 40, Status: StatusActive, GroupIDs: []int64{41}, ModelPricing: []ChannelModelPricing{{Platform: PlatformGemini, Models: []string{"gemini-model"}, InputPrice: &priceA}}},
		{ID: 50, Status: StatusActive, GroupIDs: []int64{51}, ModelPricing: []ChannelModelPricing{{Platform: PlatformGemini, Models: []string{"gemini-model"}, InputPrice: &priceB}}},
	}, map[int64]string{41: PlatformGemini, 51: PlatformGemini})

	_, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), 99, &Account{
		ID: 3, Platform: PlatformGemini, GroupIDs: []int64{99, 41, 51},
	}, "gemini-model")
	require.ErrorIs(t, err, ErrAgentChannelPricingAmbiguous)
}

func TestResolveAgentAccountPricingDoesNotFallback(t *testing.T) {
	channelService := agentPricingChannelServiceForTest(nil, map[int64]string{61: PlatformOpenAI})
	resolver := NewModelPricingResolver(channelService, NewBillingService(&config.Config{}, nil))

	_, err := resolver.ResolveAgentAccount(context.Background(), 99, &Account{
		ID: 4, Platform: PlatformOpenAI, GroupIDs: []int64{99, 61},
	}, "gpt-4o")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAgentChannelPricingUnavailable))
	require.True(t, errors.Is(err, ErrModelPricingUnavailable))
}

func agentPricingChannelServiceForTest(channels []Channel, groupPlatforms map[int64]string) *ChannelService {
	channelService := &ChannelService{}
	channelService.cache.Store(populateChannelCache(channels, groupPlatforms))
	return channelService
}

// 聚合分组允许多个渠道：按账号平台取价，同一平台多个渠道命中同一模型时取
// channel id 最小的一条（确定性），而不是判歧义让模型不可用。
func TestResolveAgentAccountPricingSupportsMultipleChannelsOnAgentGroup(t *testing.T) {
	agentGroupID := int64(700)
	openAIPrice, deepseekPrice := 0.01, 0.02

	newService := func(channels []Channel, platforms map[int64]string) *ChannelService {
		return agentPricingChannelServiceForTest(channels, platforms)
	}

	t.Run("按账号平台选渠道", func(t *testing.T) {
		channelService := newService([]Channel{
			{ID: 71, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{"gpt-x"}, InputPrice: &openAIPrice}}},
			{ID: 72, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformDeepseek, Models: []string{"deepseek-x"}, InputPrice: &deepseekPrice}}},
		}, map[int64]string{agentGroupID: PlatformOpenAI})

		match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), agentGroupID, &Account{
			ID: 1, Platform: PlatformDeepseek, GroupIDs: []int64{agentGroupID},
		}, "deepseek-x")
		require.NoError(t, err)
		require.Equal(t, int64(72), match.ChannelID)
		require.Equal(t, agentGroupID, match.GroupID)
		require.InDelta(t, deepseekPrice, *match.Pricing.InputPrice, 1e-12)
	})

	t.Run("同平台多渠道取最小 id", func(t *testing.T) {
		channelService := newService([]Channel{
			{ID: 82, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformGemini, Models: []string{"gemini-x"}, InputPrice: &openAIPrice}}},
			{ID: 81, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformGemini, Models: []string{"gemini-x"}, InputPrice: &deepseekPrice}}},
		}, map[int64]string{agentGroupID: PlatformOpenAI})

		match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), agentGroupID, &Account{
			ID: 2, Platform: PlatformGemini, GroupIDs: []int64{agentGroupID},
		}, "gemini-x")
		require.NoError(t, err)
		require.Equal(t, int64(81), match.ChannelID, "多渠道是显式配置，取价必须确定")
	})

	t.Run("聚合分组渠道优先于账号源分组渠道", func(t *testing.T) {
		sourceGroupID := int64(701)
		channelService := newService([]Channel{
			{ID: 91, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformKimi, Models: []string{"kimi-x"}, InputPrice: &deepseekPrice}}},
			{ID: 92, Status: StatusActive, GroupIDs: []int64{sourceGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformKimi, Models: []string{"kimi-x"}, InputPrice: &openAIPrice}}},
		}, map[int64]string{agentGroupID: PlatformOpenAI, sourceGroupID: PlatformKimi})

		match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), agentGroupID, &Account{
			ID: 3, Platform: PlatformKimi, GroupIDs: []int64{agentGroupID, sourceGroupID},
		}, "kimi-x")
		require.NoError(t, err)
		require.Equal(t, int64(91), match.ChannelID, "显式挂在聚合分组上的渠道就是该模型的基准价来源")
		require.Equal(t, agentGroupID, match.GroupID)
	})

	t.Run("聚合分组没有该模型时回落账号源分组", func(t *testing.T) {
		sourceGroupID := int64(702)
		channelService := newService([]Channel{
			{ID: 95, Status: StatusActive, GroupIDs: []int64{agentGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformOpenAI, Models: []string{"other-model"}, InputPrice: &openAIPrice}}},
			{ID: 96, Status: StatusActive, GroupIDs: []int64{sourceGroupID}, ModelPricing: []ChannelModelPricing{
				{Platform: PlatformZhipu, Models: []string{"glm-x"}, InputPrice: &deepseekPrice}}},
		}, map[int64]string{agentGroupID: PlatformOpenAI, sourceGroupID: PlatformZhipu})

		match, err := channelService.ResolveAgentAccountChannelPricing(context.Background(), agentGroupID, &Account{
			ID: 4, Platform: PlatformZhipu, GroupIDs: []int64{agentGroupID, sourceGroupID},
		}, "glm-x")
		require.NoError(t, err)
		require.Equal(t, int64(96), match.ChannelID)
		require.Equal(t, sourceGroupID, match.GroupID)
	})
}

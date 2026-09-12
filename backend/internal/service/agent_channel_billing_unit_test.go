//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGatewayServiceRecordUsage_AgentUsesSelectedAccountChannelAndModelMultiplier(t *testing.T) {
	agentGroupID := int64(7101)
	sourceGroupID := int64(7102)
	channelID := int64(7103)
	inputPrice := 0.012
	outputPrice := 0.024
	platformMultiplier := 1.7
	userMultiplier := 9.0
	channelService := agentPricingChannelServiceForTest([]Channel{{
		ID:       channelID,
		Status:   StatusActive,
		GroupIDs: []int64{sourceGroupID},
		ModelPricing: []ChannelModelPricing{{
			ChannelID: channelID, Platform: PlatformAnthropic, Models: []string{"claude-priced"},
			BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice,
		}},
	}}, map[int64]string{agentGroupID: "agent", sourceGroupID: PlatformAnthropic})
	resolver := agentBillingResolverWithModelRate(t, channelService, agentGroupID, PlatformAnthropic, "claude-priced", platformMultiplier)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	rateRepo := &openAIUserGroupRateRepoStub{rate: &userMultiplier}
	svc := newGatewayRecordUsageServiceForTest(
		usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{},
	)
	svc.channelService = channelService
	svc.resolver = resolver
	svc.userGroupRateRepo = rateRepo
	svc.userGroupRateResolver = newUserGroupRateResolver(
		rateRepo, nil, resolveUserGroupRateCacheTTL(svc.cfg), nil, "service.gateway.agent-pricing-test",
	)
	apiKey := &APIKey{
		ID: 8101, GroupID: &agentGroupID,
		Group: &Group{ID: agentGroupID, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 99},
	}
	account := &Account{
		ID: 9101, Platform: PlatformAnthropic, GroupIDs: []int64{agentGroupID, sourceGroupID},
	}

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "agent-anthropic-channel-price", Model: "claude-priced",
			Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 5}, Duration: time.Second,
		},
		APIKey: apiKey, User: &User{ID: 8201}, Account: account,
	})

	require.NoError(t, err)
	require.Zero(t, rateRepo.calls, "Agent billing must not read a per-user group multiplier")
	require.NotNil(t, usageRepo.lastLog)
	baseCost := inputPrice*10 + outputPrice*5
	require.InDelta(t, baseCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, baseCost*platformMultiplier, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, platformMultiplier, usageRepo.lastLog.RateMultiplier, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_AgentUsesSelectedAccountChannelAndModelMultiplier(t *testing.T) {
	agentGroupID := int64(7201)
	sourceGroupID := int64(7202)
	channelID := int64(7203)
	inputPrice := 0.015
	outputPrice := 0.03
	platformMultiplier := 1.8
	userMultiplier := 11.0
	channelService := agentPricingChannelServiceForTest([]Channel{{
		ID:       channelID,
		Status:   StatusActive,
		GroupIDs: []int64{sourceGroupID},
		ModelPricing: []ChannelModelPricing{{
			ChannelID: channelID, Platform: PlatformOpenAI, Models: []string{"gpt-priced"},
			BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice,
		}},
	}}, map[int64]string{agentGroupID: "agent", sourceGroupID: PlatformOpenAI})
	resolver := agentBillingResolverWithModelRate(t, channelService, agentGroupID, PlatformOpenAI, "gpt-priced", platformMultiplier)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	rateRepo := &openAIUserGroupRateRepoStub{rate: &userMultiplier}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, rateRepo,
	)
	svc.channelService = channelService
	svc.resolver = resolver
	apiKey := &APIKey{
		ID: 8301, GroupID: &agentGroupID,
		Group: &Group{ID: agentGroupID, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 99},
	}
	account := &Account{
		ID: 9301, Platform: PlatformOpenAI, GroupIDs: []int64{agentGroupID, sourceGroupID},
	}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "agent-openai-channel-price", Model: "gpt-priced",
			Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 5}, Duration: time.Second,
		},
		APIKey: apiKey, User: &User{ID: 8401}, Account: account,
	})

	require.NoError(t, err)
	require.Zero(t, rateRepo.calls, "Agent billing must not read a per-user group multiplier")
	require.NotNil(t, usageRepo.lastLog)
	baseCost := inputPrice*10 + outputPrice*5
	require.InDelta(t, baseCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, baseCost*platformMultiplier, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, platformMultiplier, usageRepo.lastLog.RateMultiplier, 1e-12)
}

// agentBillingResolverWithModelRate 装配一个 Agent 目录：目标文本模型以给定倍率启用。
func agentBillingResolverWithModelRate(
	t *testing.T,
	channelService *ChannelService,
	groupID int64,
	platform string,
	modelCode string,
	multiplier float64,
) *ModelPricingResolver {
	t.Helper()
	models := newAgentModelMemoryRepo()
	require.NoError(t, models.SyncDiscovered(context.Background(), groupID, []AgentModelDiscovery{
		{Platform: platform, ModelCode: modelCode, MediaType: AgentMediaTypeText},
	}, time.Now().UTC()))
	setAgentTextModelRate(t, models, groupID, platform, modelCode, multiplier)
	resolver := NewModelPricingResolver(channelService, NewBillingService(nil, nil))
	resolver.SetAgentModelCatalog(NewAgentModelCatalogService(nil, nil, models))
	return resolver
}

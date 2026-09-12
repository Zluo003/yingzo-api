package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrAgentChannelPricingUnavailable = fmt.Errorf("agent source channel pricing unavailable: %w", ErrModelPricingUnavailable)
	ErrAgentChannelPricingAmbiguous   = errors.New("agent source channel pricing is ambiguous")
)

// AgentAccountChannelPricing identifies the channel price selected from the
// actual upstream account's same-platform source groups.
type AgentAccountChannelPricing struct {
	GroupID   int64
	ChannelID int64
	Pricing   *ChannelModelPricing
}

// ResolveAgentAccountChannelPricing resolves a language-model base price for
// one concrete account. The Agent group itself is never a pricing source.
func (s *ChannelService) ResolveAgentAccountChannelPricing(
	ctx context.Context,
	agentGroupID int64,
	account *Account,
	model string,
) (*AgentAccountChannelPricing, error) {
	if s == nil || account == nil || agentGroupID <= 0 || strings.TrimSpace(model) == "" {
		return nil, ErrAgentChannelPricingUnavailable
	}
	if !isAgentLanguagePlatform(account.Platform) {
		return nil, fmt.Errorf("%w: platform %s", ErrAgentChannelPricingUnavailable, account.Platform)
	}

	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load channel cache: %v", ErrAgentChannelPricingUnavailable, err)
	}

	groupIDs := agentAccountGroupIDs(account)
	modelLower := strings.ToLower(strings.TrimSpace(model))
	matchesByChannel := make(map[int64]*AgentAccountChannelPricing)
	for _, groupID := range groupIDs {
		if groupID == agentGroupID || cache.groupPlatform[groupID] != account.Platform {
			continue
		}
		channel := cache.channelByGroupID[groupID]
		if channel == nil || !channel.IsActive() {
			continue
		}
		pricing := lookupPricingAcrossPlatforms(cache, groupID, account.Platform, modelLower)
		if pricing == nil || !channelModelPricingHasPrice(pricing) {
			continue
		}
		if _, exists := matchesByChannel[channel.ID]; exists {
			continue
		}
		pricingCopy := pricing.Clone()
		matchesByChannel[channel.ID] = &AgentAccountChannelPricing{
			GroupID:   groupID,
			ChannelID: channel.ID,
			Pricing:   &pricingCopy,
		}
	}

	if len(matchesByChannel) == 0 {
		return nil, fmt.Errorf(
			"%w: account %d platform %s model %s",
			ErrAgentChannelPricingUnavailable,
			account.ID,
			account.Platform,
			model,
		)
	}
	if len(matchesByChannel) > 1 {
		channelIDs := make([]int64, 0, len(matchesByChannel))
		for channelID := range matchesByChannel {
			channelIDs = append(channelIDs, channelID)
		}
		sort.Slice(channelIDs, func(i, j int) bool { return channelIDs[i] < channelIDs[j] })
		return nil, fmt.Errorf(
			"%w: account %d platform %s model %s matches channels %v",
			ErrAgentChannelPricingAmbiguous,
			account.ID,
			account.Platform,
			model,
			channelIDs,
		)
	}

	for _, match := range matchesByChannel {
		return match, nil
	}
	return nil, ErrAgentChannelPricingUnavailable
}

// ResolveAgentAccountCandidates tries billing-model candidates in order. Only
// a missing price advances to the next candidate; ambiguous channel ownership
// and other configuration errors fail immediately.
func (r *ModelPricingResolver) ResolveAgentAccountCandidates(
	ctx context.Context,
	agentGroupID int64,
	account *Account,
	models ...string,
) (*ResolvedPricing, string, error) {
	candidates := agentPricingModelCandidates(account, models...)
	var lastUnavailable error
	for _, candidate := range candidates {
		resolved, err := r.ResolveAgentAccount(ctx, agentGroupID, account, candidate)
		if err == nil {
			return resolved, candidate, nil
		}
		if !errors.Is(err, ErrAgentChannelPricingUnavailable) {
			return nil, "", err
		}
		lastUnavailable = err
	}
	if lastUnavailable != nil {
		return nil, "", lastUnavailable
	}
	return nil, "", ErrAgentChannelPricingUnavailable
}

func agentPricingModelCandidates(account *Account, models ...string) []string {
	seen := make(map[string]struct{}, len(models)*2)
	candidates := make([]string, 0, len(models)*2)
	for _, model := range models {
		candidates = appendUsageBillingModelCandidate(candidates, seen, model)
		if account != nil && strings.TrimSpace(model) != "" {
			candidates = appendUsageBillingModelCandidate(candidates, seen, account.GetMappedModel(model))
		}
	}
	return candidates
}

func validateAgentLanguagePricing(
	ctx context.Context,
	resolver *ModelPricingResolver,
	group *Group,
	account *Account,
	models ...string,
) error {
	if group == nil || !group.IsAgent() {
		return nil
	}
	if resolver == nil {
		return ErrAgentChannelPricingUnavailable
	}
	if resolver.agentModelCatalog == nil {
		return fmt.Errorf("%w: %v", ErrAgentChannelPricingUnavailable, ErrAgentModelCatalogUnavailable)
	}
	if _, err := resolver.agentModelCatalog.RequireAccountLanguageModel(ctx, group.ID, account, models...); err != nil {
		return fmt.Errorf("%w: %v", ErrAgentChannelPricingUnavailable, err)
	}
	if _, err := resolver.ResolveAgentPlatformRate(ctx, group.ID, account.Platform); err != nil {
		return fmt.Errorf("%w: %v", ErrAgentChannelPricingUnavailable, err)
	}
	_, _, err := resolver.ResolveAgentAccountCandidates(ctx, group.ID, account, models...)
	return err
}

// ValidateAgentLanguagePricing verifies that a selected Anthropic/Gemini
// account can be priced before the upstream request is sent.
func (s *GatewayService) ValidateAgentLanguagePricing(
	ctx context.Context,
	group *Group,
	account *Account,
	models ...string,
) error {
	if s == nil {
		return ErrAgentChannelPricingUnavailable
	}
	return validateAgentLanguagePricing(ctx, s.resolver, group, account, models...)
}

// ValidateAgentLanguagePricing verifies that a selected OpenAI account can be
// priced before the upstream request is sent.
func (s *OpenAIGatewayService) ValidateAgentLanguagePricing(
	ctx context.Context,
	group *Group,
	account *Account,
	models ...string,
) error {
	if s == nil {
		return ErrAgentChannelPricingUnavailable
	}
	return validateAgentLanguagePricing(ctx, s.resolver, group, account, models...)
}

func (s *GatewayService) ValidateAgentImagePricing(ctx context.Context, group *Group, platform, model, imageSize string, imageCount int) error {
	if group == nil || !group.IsAgent() {
		return nil
	}
	if s == nil || s.resolver == nil || imageCount <= 0 {
		return ErrAgentImagePricingUnavailable
	}
	_, _, err := s.resolver.ResolveAgentMediaUnitPrice(ctx, group.ID, platform, AgentMediaTypeImage, imageSize, model)
	return err
}

func (s *OpenAIGatewayService) ValidateAgentImagePricing(ctx context.Context, group *Group, platform, model, imageSize string, imageCount int) error {
	if group == nil || !group.IsAgent() {
		return nil
	}
	if s == nil || s.resolver == nil || imageCount <= 0 {
		return ErrAgentImagePricingUnavailable
	}
	_, _, err := s.resolver.ResolveAgentMediaUnitPrice(ctx, group.ID, platform, AgentMediaTypeImage, imageSize, model)
	return err
}

func isAgentLanguagePlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini:
		return true
	default:
		return false
	}
}

func agentAccountGroupIDs(account *Account) []int64 {
	if account == nil {
		return nil
	}
	capacity := len(account.GroupIDs) + len(account.AccountGroups) + len(account.Groups)
	seen := make(map[int64]struct{}, capacity)
	groupIDs := make([]int64, 0, capacity)
	add := func(groupID int64) {
		if groupID <= 0 {
			return
		}
		if _, exists := seen[groupID]; exists {
			return
		}
		seen[groupID] = struct{}{}
		groupIDs = append(groupIDs, groupID)
	}
	for _, groupID := range account.GroupIDs {
		add(groupID)
	}
	for _, binding := range account.AccountGroups {
		add(binding.GroupID)
	}
	for _, group := range account.Groups {
		if group != nil {
			add(group.ID)
		}
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	return groupIDs
}

func channelModelPricingHasPrice(pricing *ChannelModelPricing) bool {
	if pricing == nil {
		return false
	}
	if pricing.InputPrice != nil || pricing.OutputPrice != nil || pricing.CacheWritePrice != nil ||
		pricing.CacheReadPrice != nil || pricing.ImageOutputPrice != nil || pricing.PerRequestPrice != nil {
		return true
	}
	return len(filterValidIntervals(pricing.Intervals)) > 0
}

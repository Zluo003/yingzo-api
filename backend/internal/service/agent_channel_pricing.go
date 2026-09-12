package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// ResolveAgentAccountChannelPricing resolves a language-model base price for one
// concrete account.
//
// 取价顺序（两条路径都按账号平台过滤，模型名精确优先、其次通配符）：
//  1. 系统聚合分组自身关联的渠道。聚合分组把不同 provider 的账号聚在一起，管理员
//     显式挂上来的渠道就是这些模型的基准价来源；同一 (平台, 模型) 命中多个渠道时取
//     channel id 最小的一条并 warn —— 多渠道是显式配置出来的正常状态，取价必须确定，
//     不能让整个模型变成不可用。
//  2. 账号所属的其它源分组（既有语义）：一个分组只归一个渠道。跨分组命中多个渠道时
//     仍然判为歧义并失败——那是"这个账号到底按哪个分组计价"没配置清楚，必须显式修，
//     不能靠猜。
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

	modelLower := normalizeChannelPricingModelName(model)

	// 1) 聚合分组自己的渠道优先。
	if match := matchAgentGroupChannelPricing(cache, agentGroupID, account.Platform, modelLower); match != nil {
		return match, nil
	}

	// 2) 账号所属其它源分组的渠道（既有行为）。
	matchesByChannel := make(map[int64]*AgentAccountChannelPricing)
	for _, groupID := range agentAccountGroupIDs(account) {
		if groupID == agentGroupID || cache.groupPlatform[groupID] != account.Platform {
			continue
		}
		channel := cache.channelByGroupID[groupID]
		if channel == nil || !channel.IsActive() {
			continue
		}
		if _, exists := matchesByChannel[channel.ID]; exists {
			continue
		}
		pricing := lookupPricingAcrossPlatforms(cache, groupID, account.Platform, modelLower)
		if pricing == nil || !channelModelPricingHasPrice(pricing) {
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
		return nil, agentChannelPricingUnavailableError(account, model)
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
	return nil, agentChannelPricingUnavailableError(account, model)
}

// matchAgentGroupChannelPricing 在聚合分组关联的渠道里取价：命中多个渠道时取
// channel id 最小的一条（并 warn），保证同一请求多次解析结果一致。
func matchAgentGroupChannelPricing(cache *channelCache, agentGroupID int64, platform, modelLower string) *AgentAccountChannelPricing {
	channels := cache.channelsByGroupID[agentGroupID]
	if len(channels) == 0 {
		return nil
	}
	type candidate struct {
		channelID int64
		pricing   *ChannelModelPricing
	}
	matches := make([]candidate, 0, len(channels))
	for _, channel := range channels {
		if channel == nil || !channel.IsActive() {
			continue
		}
		pricing := channelPricingForAgentModel(channel, platform, modelLower)
		if pricing == nil || !channelModelPricingHasPrice(pricing) {
			continue
		}
		matches = append(matches, candidate{channelID: channel.ID, pricing: pricing})
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].channelID < matches[j].channelID })
	if len(matches) > 1 {
		channelIDs := make([]int64, 0, len(matches))
		for _, match := range matches {
			channelIDs = append(channelIDs, match.channelID)
		}
		slog.Warn("agent group has multiple channels pricing the same model; using the lowest channel id",
			"agent_group_id", agentGroupID,
			"platform", platform,
			"model", modelLower,
			"channel_ids", channelIDs,
			"selected_channel_id", matches[0].channelID)
	}
	pricingCopy := matches[0].pricing.Clone()
	return &AgentAccountChannelPricing{
		GroupID:   agentGroupID,
		ChannelID: matches[0].channelID,
		Pricing:   &pricingCopy,
	}
}

func agentChannelPricingUnavailableError(account *Account, model string) error {
	return fmt.Errorf(
		"%w: account %d platform %s model %s",
		ErrAgentChannelPricingUnavailable,
		account.ID,
		account.Platform,
		model,
	)
}

// channelPricingForAgentModel 在单个渠道自己的定价条目里找 (平台, 模型) 的价格。
//
// 不能复用缓存里的 pricingByGroupModel：那张表是按 groupID 单值索引的，聚合分组挂了
// 多个渠道时后写入的会覆盖前面的（结果取决于加载顺序）。这里直接读渠道自身的定价，
// 精确匹配优先，其次按配置顺序取第一个命中的通配符条目。
func channelPricingForAgentModel(channel *Channel, platform, modelLower string) *ChannelModelPricing {
	if channel == nil {
		return nil
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	var wildcard *ChannelModelPricing
	for i := range channel.ModelPricing {
		pricing := &channel.ModelPricing[i]
		if !strings.EqualFold(strings.TrimSpace(pricing.Platform), platform) {
			continue
		}
		for _, candidate := range pricing.Models {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if strings.HasSuffix(candidate, "*") {
				if wildcard != nil {
					continue
				}
				prefix := normalizeChannelPricingModelName(strings.TrimSuffix(candidate, "*"))
				if prefix != "" && strings.HasPrefix(modelLower, prefix) {
					wildcard = pricing
				}
				continue
			}
			if normalizeChannelPricingModelName(candidate) == modelLower {
				return pricing
			}
		}
	}
	return wildcard
}

// AgentTextPricing is the complete answer for one Agent text request: the source
// channel price of the concrete account that will serve it, plus the Agent
// group's own downstream multiplier for that model.
type AgentTextPricing struct {
	Resolved       *ResolvedPricing
	BillingModel   string
	RateMultiplier float64
}

// ResolveAgentTextPricing resolves the billing basis for an Agent text request.
// A candidate qualifies only when the Agent catalogue offers it as an enabled
// text model with a configured multiplier AND the concrete account's source
// channel prices it; anything half-configured simply advances to the next
// candidate. Ambiguous channel ownership fails immediately.
func (r *ModelPricingResolver) ResolveAgentTextPricing(
	ctx context.Context,
	agentGroupID int64,
	account *Account,
	models ...string,
) (*AgentTextPricing, error) {
	if r == nil || account == nil || agentGroupID <= 0 {
		return nil, ErrAgentChannelPricingUnavailable
	}
	if r.agentModelCatalog == nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentChannelPricingUnavailable, ErrAgentModelCatalogUnavailable)
	}
	var lastUnavailable error
	for _, candidate := range agentPricingModelCandidates(account, models...) {
		rate, modelCode, rateErr := r.agentModelCatalog.ResolveTextModelRate(ctx, agentGroupID, account.Platform, candidate)
		if rateErr != nil {
			lastUnavailable = rateErr
			continue
		}
		resolved, err := r.ResolveAgentAccount(ctx, agentGroupID, account, modelCode)
		if err != nil {
			if !errors.Is(err, ErrAgentChannelPricingUnavailable) {
				return nil, err
			}
			lastUnavailable = err
			continue
		}
		return &AgentTextPricing{Resolved: resolved, BillingModel: modelCode, RateMultiplier: rate}, nil
	}
	if lastUnavailable != nil {
		return nil, lastUnavailable
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
	_, err := resolver.ResolveAgentTextPricing(ctx, group.ID, account, models...)
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

// ValidateAgentRequestPricing 在转发前按目录登记的媒体类型选择校验口径。
//
// Gemini 标准图片接口（/v1beta ...:generateContent）与文本走同一个入口：图片模型必须
// 按"每张单价"口径校验，否则会被语言口径的"缺倍率/缺渠道价"误判为不可计费而在入口
// 4xx —— 图片模型本来就没有文本倍率。请求前拿不到分辨率时只要求存在任一档位价格，
// 实际计费仍按上游返回的 result.ImageSize 取档。
func (s *GatewayService) ValidateAgentRequestPricing(ctx context.Context, group *Group, account *Account, platform, model string) error {
	if group == nil || !group.IsAgent() {
		return nil
	}
	if s == nil || s.resolver == nil || s.resolver.agentModelCatalog == nil {
		return fmt.Errorf("%w: %v", ErrAgentChannelPricingUnavailable, ErrAgentModelCatalogUnavailable)
	}
	isImage, err := s.resolver.agentModelCatalog.EnsureAgentImageModelPriced(ctx, group.ID, platform, model)
	if err != nil {
		return err
	}
	if isImage {
		return nil
	}
	return validateAgentLanguagePricing(ctx, s.resolver, group, account, model)
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

// isAgentLanguagePlatform 报告该平台的账号能否承载聚合分组的文本模型计费
// （源渠道价 × 模型倍率）。覆盖全部文本平台：三大协议平台 + grok + 国产供应商。
func isAgentLanguagePlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini,
		PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
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

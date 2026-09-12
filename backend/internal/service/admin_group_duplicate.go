package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	maxGroupNameRunes            = 100
	duplicateGroupInactiveStatus = "inactive"
)

func duplicateGroupOperationID(sourceID int64, actorScope, operationKey string) string {
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return ""
	}
	actorScope = strings.TrimSpace(actorScope)
	if actorScope == "" {
		actorScope = "admin:0"
	}
	payload := "admin.groups.duplicate\x00" + actorScope + "\x00" + strconv.FormatInt(sourceID, 10) + "\x00" + operationKey
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", digest)
}

func duplicateGroupName(sourceName string, copyNumber int) string {
	if copyNumber < 1 {
		copyNumber = 1
	}
	suffix := " (Copy)"
	if copyNumber > 1 {
		suffix = fmt.Sprintf(" (Copy %d)", copyNumber)
	}
	baseRunes := []rune(strings.TrimSpace(sourceName))
	maxBaseRunes := maxGroupNameRunes - len([]rune(suffix))
	if maxBaseRunes < 0 {
		maxBaseRunes = 0
	}
	if len(baseRunes) > maxBaseRunes {
		baseRunes = baseRunes[:maxBaseRunes]
	}
	return string(baseRunes) + suffix
}

func cloneGroupValuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneGroupModelRouting(value map[string][]int64) map[string][]int64 {
	if value == nil {
		return nil
	}
	cloned := make(map[string][]int64, len(value))
	for model, accountIDs := range value {
		cloned[model] = append([]int64(nil), accountIDs...)
	}
	return cloned
}

func cloneGroupVideoModelPrices(value map[string]map[string]float64) map[string]map[string]float64 {
	if value == nil {
		return nil
	}
	cloned := make(map[string]map[string]float64, len(value))
	for model, prices := range value {
		clonedPrices := make(map[string]float64, len(prices))
		for resolution, price := range prices {
			clonedPrices[resolution] = price
		}
		cloned[model] = clonedPrices
	}
	return cloned
}

// cloneGroupVideoPricingRules deep-copies the per-model/resolution per-second
// rules so the duplicate never shares backing storage with its source.
func cloneGroupVideoPricingRules(rules []VideoGroupPricingRule) []VideoGroupPricingRule {
	if rules == nil {
		return nil
	}
	cloned := make([]VideoGroupPricingRule, len(rules))
	copy(cloned, rules)
	for i := range cloned {
		// The duplicate owns its own rows; ids and group ids are assigned on insert.
		cloned[i].ID = 0
		cloned[i].GroupID = 0
	}
	return cloned
}

// replaceDuplicateVideoPricingRules persists the cloned rules against the new
// group. A duplicate of a video group without its rules would fail every video
// request with video_pricing_rule_not_found.
func (s *adminServiceImpl) replaceDuplicateVideoPricingRules(ctx context.Context, duplicate *Group) error {
	if s.videoPricingRepo == nil || duplicate == nil || len(duplicate.VideoPricingRules) == 0 {
		return nil
	}
	if duplicate.Platform != PlatformVideo && !duplicate.IsAgent() {
		return nil
	}
	if err := s.videoPricingRepo.ReplaceForGroup(ctx, duplicate.ID, duplicate.VideoPricingRules); err != nil {
		return fmt.Errorf("copy video pricing rules: %w", err)
	}
	for i := range duplicate.VideoPricingRules {
		duplicate.VideoPricingRules[i].GroupID = duplicate.ID
	}
	return nil
}

func cloneGroupMessagesDispatchModelConfig(value OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	cloned := value
	if value.ExactModelMappings != nil {
		cloned.ExactModelMappings = make(map[string]string, len(value.ExactModelMappings))
		for requestedModel, mappedModel := range value.ExactModelMappings {
			cloned.ExactModelMappings[requestedModel] = mappedModel
		}
	}
	return cloned
}

// cloneGroupForDuplicate 只复制业务配置：kind/system_code 必须保持为空，
// 保证任何复制路径都产出普通分组（系统 Agent 分组由迁移独占种入）。
func cloneGroupForDuplicate(source *Group, operationID string) *Group {
	return &Group{
		Name:                            duplicateGroupName(source.Name, 1),
		Description:                     source.Description,
		Platform:                        source.Platform,
		RateMultiplier:                  source.RateMultiplier,
		PeakRateEnabled:                 source.PeakRateEnabled,
		PeakStart:                       source.PeakStart,
		PeakEnd:                         source.PeakEnd,
		PeakRateMultiplier:              source.PeakRateMultiplier,
		ProfitControlEnabled:            source.ProfitControlEnabled,
		ProfitMinMargin:                 source.ProfitMinMargin,
		ProfitSafetyBuffer:              source.ProfitSafetyBuffer,
		IsExclusive:                     source.IsExclusive,
		Status:                          duplicateGroupInactiveStatus,
		DuplicateOperationID:            operationID,
		SubscriptionType:                source.SubscriptionType,
		DailyLimitUSD:                   cloneGroupValuePointer(source.DailyLimitUSD),
		WeeklyLimitUSD:                  cloneGroupValuePointer(source.WeeklyLimitUSD),
		MonthlyLimitUSD:                 cloneGroupValuePointer(source.MonthlyLimitUSD),
		DefaultValidityDays:             source.DefaultValidityDays,
		AllowImageGeneration:            source.AllowImageGeneration,
		AllowBatchImageGeneration:       source.AllowBatchImageGeneration,
		ImageRateIndependent:            source.ImageRateIndependent,
		ImageRateMultiplier:             source.ImageRateMultiplier,
		ImagePrice1K:                    cloneGroupValuePointer(source.ImagePrice1K),
		ImagePrice2K:                    cloneGroupValuePointer(source.ImagePrice2K),
		ImagePrice4K:                    cloneGroupValuePointer(source.ImagePrice4K),
		BatchImageDiscountMultiplier:    source.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        source.BatchImageHoldMultiplier,
		VideoRateIndependent:            source.VideoRateIndependent,
		VideoRateMultiplier:             source.VideoRateMultiplier,
		VideoPrice480P:                  cloneGroupValuePointer(source.VideoPrice480P),
		VideoPrice720P:                  cloneGroupValuePointer(source.VideoPrice720P),
		VideoPrice1080P:                 cloneGroupValuePointer(source.VideoPrice1080P),
		VideoModelPrices:                cloneGroupVideoModelPrices(source.VideoModelPrices),
		VideoPricingRules:               cloneGroupVideoPricingRules(source.VideoPricingRules),
		WebSearchPricePerCall:           cloneGroupValuePointer(source.WebSearchPricePerCall),
		SearchPricePer1k:                cloneGroupValuePointer(source.SearchPricePer1k),
		AudioRealtimePricePerMin:        cloneGroupValuePointer(source.AudioRealtimePricePerMin),
		AudioTTSPricePerMillionChars:    cloneGroupValuePointer(source.AudioTTSPricePerMillionChars),
		AudioSTTPricePerHour:            cloneGroupValuePointer(source.AudioSTTPricePerHour),
		ClaudeCodeOnly:                  source.ClaudeCodeOnly,
		FallbackGroupID:                 cloneGroupValuePointer(source.FallbackGroupID),
		FallbackGroupIDOnInvalidRequest: cloneGroupValuePointer(source.FallbackGroupIDOnInvalidRequest),
		ModelRouting:                    cloneGroupModelRouting(source.ModelRouting),
		ModelRoutingEnabled:             source.ModelRoutingEnabled,
		MCPXMLInject:                    source.MCPXMLInject,
		SupportedModelScopes:            append([]string(nil), source.SupportedModelScopes...),
		SortOrder:                       source.SortOrder,
		AllowMessagesDispatch:           source.AllowMessagesDispatch,
		AllowLive:                       source.AllowLive,
		ForceOpenAIFast:                 source.ForceOpenAIFast,
		FreeOpenAIFast:                  source.FreeOpenAIFast,
		RequireOAuthOnly:                source.RequireOAuthOnly,
		RequirePrivacySet:               source.RequirePrivacySet,
		DefaultMappedModel:              source.DefaultMappedModel,
		MessagesDispatchModelConfig:     cloneGroupMessagesDispatchModelConfig(source.MessagesDispatchModelConfig),
		ModelAllowlist: GroupModelAllowlist{
			Enabled: source.ModelAllowlist.Enabled,
			Models:  append([]string(nil), source.ModelAllowlist.Models...),
		},
		// 固定账号 manifest 配置指向源分组的账号 ID，复制后成员关系可能变化，重置为关闭且列表为空。
		CodexModelsManifestConfig:   GroupCodexModelsManifestConfig{},
		RPMLimit:                    source.RPMLimit,
		MaxReasoningEffort:          source.MaxReasoningEffort,
		MaxReasoningEffortOverLimit: source.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:     append([]ReasoningEffortMapping(nil), source.ReasoningEffortMappings...),
	}
}

// RecoverDuplicateGroup performs a read-only lookup for a copy that was already
// committed for the same actor, source group, and idempotency key.
func (s *adminServiceImpl) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	if err := s.ValidateSimpleModeGroupOperation(AdminGroupOperationDuplicate); err != nil {
		return nil, err
	}
	operationID := duplicateGroupOperationID(id, actorScope, operationKey)
	if operationID == "" {
		return nil, nil
	}
	if s.groupDuplicateRepo == nil {
		return nil, errors.New("group duplicate repository is not configured")
	}
	group, err := s.groupDuplicateRepo.FindByDuplicateOperationID(ctx, operationID)
	if err != nil {
		return nil, fmt.Errorf("find duplicate group operation: %w", err)
	}
	if group == nil {
		return nil, nil
	}
	hydrated, err := s.groupRepo.GetByID(ctx, group.ID)
	if err != nil {
		return nil, fmt.Errorf("load recovered duplicate group: %w", err)
	}
	return hydrated, nil
}

// DuplicateGroup creates an inactive copy of a group's configuration and exact
// account priorities. The repository commits the group, bindings, and outbox
// event atomically so a failed binding never leaves an orphan group.
func (s *adminServiceImpl) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	if err := s.ValidateSimpleModeGroupOperation(AdminGroupOperationDuplicate); err != nil {
		return nil, err
	}
	source, err := s.groupRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 系统内置聚合分组是唯一的：复制它会连带复制账号绑定与视频计费规则，造出一个
	// "影子 Agent 分组"。这条判断放在幂等恢复之前——它不依赖 operation key，
	// 也必须在任何写库动作之前生效。
	if source.IsAgent() {
		return nil, errors.New("system Agent group cannot be duplicated")
	}

	existing, err := s.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	if s.groupDuplicateRepo == nil {
		return nil, errors.New("group duplicate repository is not configured")
	}

	duplicate := cloneGroupForDuplicate(source, duplicateGroupOperationID(id, actorScope, operationKey))
	sanitizeGroupReasoningEffortPolicy(duplicate)
	for copyNumber := 1; ; copyNumber++ {
		duplicate.Name = duplicateGroupName(source.Name, copyNumber)
		duplicate.ID = 0
		duplicate.CreatedAt = time.Time{}
		duplicate.UpdatedAt = time.Time{}
		if err := s.groupDuplicateRepo.CreateFromSource(ctx, duplicate, source.ID); err == nil {
			if err := s.replaceDuplicateVideoPricingRules(ctx, duplicate); err != nil {
				return nil, err
			}
			hydrated, loadErr := s.groupRepo.GetByID(ctx, duplicate.ID)
			if loadErr != nil {
				return nil, fmt.Errorf("load duplicate group: %w", loadErr)
			}
			return hydrated, nil
		} else if !errors.Is(err, ErrGroupExists) {
			return nil, fmt.Errorf("create duplicate group: %w", err)
		}

		// A unique conflict can be either the generated name or the operation ID.
		// Recover first; if no operation row exists, advance to the next name.
		recovered, recoverErr := s.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
		if recoverErr != nil {
			return nil, recoverErr
		}
		if recovered != nil {
			return recovered, nil
		}
	}
}

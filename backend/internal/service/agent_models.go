package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	AgentAPIContractVersion        = "2026-08-11.1"
	AgentModelCatalogSchemaVersion = 3
	AgentModelCatalogSource        = "configured_account_synced_catalog"
)

const (
	AgentMediaTypeText  = "text"
	AgentMediaTypeImage = "image"
	AgentMediaTypeVideo = "video"

	AgentBillingUnitImage  = "image"
	AgentBillingUnitSecond = "second"
)

const (
	AgentInterfaceOpenAIResponses       = "openai.responses"
	AgentInterfaceOpenAIChatCompletions = "openai.chat_completions"
	AgentInterfaceOpenAIEmbeddings      = "openai.embeddings"
	AgentInterfaceOpenAIImages          = "openai.images"
	AgentInterfaceAnthropicMessages     = "anthropic.messages"
	AgentInterfaceGeminiGenerateContent = "gemini.generate_content"
	AgentInterfaceSeedanceVideos        = "seedance.videos"
)

var (
	ErrAgentModelCatalogUnavailable = errors.New("agent model catalog unavailable")
	ErrAgentModelNotConfigured      = errors.New("agent model is not enabled or available")
	ErrAgentModelRateUnavailable    = errors.New("agent text model multiplier is not configured")
)

type AgentModelPrice struct {
	ID           int64   `json:"id"`
	AgentModelID int64   `json:"agent_model_id"`
	Resolution   string  `json:"resolution"`
	BillingUnit  string  `json:"billing_unit"`
	UnitPrice    float64 `json:"unit_price"`
	// Enabled is a per-resolution visibility switch. A nil value is treated as
	// enabled for backwards compatibility with callers that construct prices
	// before this field was introduced.
	Enabled   *bool     `json:"enabled,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentGroupModel struct {
	ID           int64             `json:"id"`
	GroupID      int64             `json:"group_id"`
	Platform     string            `json:"platform"`
	ModelCode    string            `json:"model_code"`
	MediaType    string            `json:"media_type"`
	Enabled      bool              `json:"enabled"`
	Available    bool              `json:"available"`
	Excluded     bool              `json:"excluded"`
	ExcludedAt   *time.Time        `json:"excluded_at,omitempty"`
	DiscoveredAt time.Time         `json:"discovered_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Prices       []AgentModelPrice `json:"prices"`
	// RateMultiplier 是文本模型在源渠道价之上的下游倍率。nil 表示尚未配置，
	// 此时该文本模型不可被调用（与"启用但没定价"等价）。0 是合法值。
	RateMultiplier *float64 `json:"rate_multiplier"`
}

type AgentModelDiscovery struct {
	Platform  string
	ModelCode string
	MediaType string
}

type AgentModelConfigInput struct {
	MediaType string            `json:"media_type"`
	Enabled   bool              `json:"enabled"`
	Prices    []AgentModelPrice `json:"prices"`
	// RateMultiplier 仅文本模型使用；媒体模型的价格在 Prices 里按分辨率给出。
	RateMultiplier *float64 `json:"rate_multiplier,omitempty"`
}

type AgentModelCatalogConfig struct {
	Models []AgentGroupModel `json:"models"`
}

type AgentModelRepository interface {
	SyncDiscovered(ctx context.Context, groupID int64, discovered []AgentModelDiscovery, seenAt time.Time) error
	ListModels(ctx context.Context, groupID int64, includeExcluded bool) ([]AgentGroupModel, error)
	GetModelByID(ctx context.Context, groupID, modelID int64) (*AgentGroupModel, error)
	GetEnabledModel(ctx context.Context, groupID int64, platform, modelCode string) (*AgentGroupModel, error)
	UpdateModelConfig(ctx context.Context, groupID, modelID int64, mediaType string, enabled bool, rateMultiplier *float64, prices []AgentModelPrice) error
	DeleteModel(ctx context.Context, groupID, modelID int64) error
}

// agentModelDriftHealer 是 AgentModelRepository 的可选能力：只增不减的目录
// 自愈写入。读路径用它把"账号已声明、模型表缺行"的漂移收敛掉，让图片账号
// 建号/改映射后无需依赖管理端手动同步；未实现该能力的仓库（测试替身）按
// 原行为工作。
type agentModelDriftHealer interface {
	EnsureDiscovered(ctx context.Context, groupID int64, discovered []AgentModelDiscovery, seenAt time.Time) error
}

// agentModelHealInterval 限制同一分组的读路径自愈频率：补写幂等，窗口内错过
// 的漂移由下一次读取继续收敛，避免并发读把自愈放大成写风暴。
const agentModelHealInterval = 5 * time.Second

// AgentModelCatalogEntry describes one client-visible model and the native
// provider interfaces through which Yingzo can invoke it.
type AgentModelCatalogEntry struct {
	ID         string   `json:"id"`
	MediaTypes []string `json:"media_types"`
	Platforms  []string `json:"platforms"`
	Interfaces []string `json:"interfaces"`
}

// AgentModelCatalogService owns the explicit model catalogue used by both the
// admin configuration surface and public Agent requests.
type AgentModelCatalogService struct {
	accountRepo AccountRepository
	groupRepo   GroupRepository
	modelRepo   AgentModelRepository

	// platformCache 缓存"模型 → 所属平台"，用于每个 Agent 请求的入口平台解析
	// （account selection 需要按模型归属平台选号）。目录写入路径会主动失效，
	// TTL 只用于兜底多实例部署下其它实例的写入。
	platformMu    sync.Mutex
	platformCache map[int64]agentModelPlatformSnapshot

	// healMu 守护各分组的读路径自愈节流。自愈只加行不删行，输掉竞争只是
	// 下次读取再补一次。
	healMu     sync.Mutex
	lastHealAt map[int64]time.Time
}

// agentModelPlatformSnapshotTTL 是模型→平台映射的最长陈旧时间。
const agentModelPlatformSnapshotTTL = 15 * time.Second

type agentModelPlatformSnapshot struct {
	byModel   map[string]string
	expiresAt time.Time
}

type agentModelCatalogAccumulator struct {
	mediaTypes map[string]struct{}
	platforms  map[string]struct{}
	interfaces map[string]struct{}
}

func NewAgentModelCatalogService(accountRepo AccountRepository, groupRepo GroupRepository, modelRepo AgentModelRepository) *AgentModelCatalogService {
	return &AgentModelCatalogService{accountRepo: accountRepo, groupRepo: groupRepo, modelRepo: modelRepo}
}

func (s *AgentModelCatalogService) Sync(ctx context.Context, groupID int64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list schedulable Agent accounts: %w", err)
	}
	discovered := discoverAgentModels(accounts)
	if err := s.modelRepo.SyncDiscovered(ctx, groupID, discovered, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("sync Agent models: %w", err)
	}
	s.invalidateModelPlatforms(groupID)
	return s.GetConfig(ctx, groupID)
}

// listCatalogAccounts returns accounts that are enabled for the Agent group by
// persistent configuration. Transient scheduler state (rate limits, overload,
// and cooldowns) must not erase a model from the catalog or turn its configured
// prices into a missing-model error; dispatch still applies those checks later.
func (s *AgentModelCatalogService) listCatalogAccounts(ctx context.Context, groupID int64) ([]Account, error) {
	return s.accountRepo.ListModelAvailabilityCandidates(ctx, &groupID, []string{
		PlatformOpenAI,
		PlatformAnthropic,
		PlatformGemini,
		PlatformGrok,
		PlatformKimi,
		PlatformZhipu,
		PlatformDeepseek,
		PlatformMiniMax,
		PlatformVideo,
		// Compatibility for databases before the video platform rename.
		"seedance",
	}, true)
}

func (s *AgentModelCatalogService) GetConfig(ctx context.Context, groupID int64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	models, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, fmt.Errorf("list Agent models: %w", err)
	}
	// 管理端配置页也走同一套自愈：账号刚加进分组时，新模型立刻出现在配置
	// 列表里（启用状态可直接配价），而不是等管理员先点一次同步。
	models = s.healAgentModelRows(ctx, groupID, models)
	if models == nil {
		models = []AgentGroupModel{}
	}
	return &AgentModelCatalogConfig{Models: models}, nil
}

// healAgentModelRows 对账分组模型表与目录账号池的当前发现结果：账号已声明、
// 但表里缺行（或被上次同步置为不可用）的模型补回可见行。只增不减——人工
// 排除（excluded）与管理员停用（enabled=false）的行永远不会被自愈翻转；
// 自愈失败或仓库不支持时按原样返回，绝不因自愈拒绝本次读取。
func (s *AgentModelCatalogService) healAgentModelRows(ctx context.Context, groupID int64, models []AgentGroupModel) []AgentGroupModel {
	if s == nil || s.accountRepo == nil || s.modelRepo == nil {
		return models
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return models
	}
	discovered := discoverAgentModels(accounts)
	if len(discovered) == 0 {
		// 没有目录账号池就没有"应该有"的基准；空发现不得清空现有行。
		return models
	}
	return s.healDiscoveredModelRows(ctx, groupID, discovered, models)
}

// healDiscoveredModelRows 在发现清单与现有行之间找漂移并补齐。补写受每分组
// 节流约束，成功后失效平台缓存并重读行。
// markHeal 记录某分组最近一次自愈/删除的时间戳，返回 false 表示距上次记录
// 不足一个节流窗口，调用方应跳过本次自愈（DeleteModel 借此抑制紧接着的
// GetConfig 把刚删的行立刻补回来）。
func (s *AgentModelCatalogService) markHeal(groupID int64, at time.Time) bool {
	s.healMu.Lock()
	defer s.healMu.Unlock()
	if s.lastHealAt == nil {
		s.lastHealAt = make(map[int64]time.Time)
	}
	if at.Sub(s.lastHealAt[groupID]) < agentModelHealInterval {
		return false
	}
	s.lastHealAt[groupID] = at
	return true
}

func (s *AgentModelCatalogService) healDiscoveredModelRows(ctx context.Context, groupID int64, discovered []AgentModelDiscovery, models []AgentGroupModel) []AgentGroupModel {
	healer, ok := s.modelRepo.(agentModelDriftHealer)
	if !ok {
		return models
	}
	rows := make(map[string]*AgentGroupModel, len(models))
	for i := range models {
		model := &models[i]
		rows[agentModelKey(model.Platform, model.ModelCode)] = model
	}
	drift := make([]AgentModelDiscovery, 0, len(discovered))
	for _, item := range discovered {
		row := rows[agentModelKey(item.Platform, item.ModelCode)]
		if row != nil && (row.Available || row.Excluded) {
			continue
		}
		drift = append(drift, item)
	}
	if len(drift) == 0 {
		return models
	}
	now := time.Now()
	if !s.markHeal(groupID, now) {
		return models
	}
	if err := healer.EnsureDiscovered(ctx, groupID, drift, now.UTC()); err != nil {
		slog.Error("agent_model_catalog_heal_failed", "group_id", groupID, "error", err)
		return models
	}
	s.invalidateModelPlatforms(groupID)
	fresh, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		slog.Error("agent_model_catalog_heal_reread_failed", "group_id", groupID, "error", err)
		return models
	}
	return fresh
}

func (s *AgentModelCatalogService) UpdateModel(ctx context.Context, groupID, modelID int64, input AgentModelConfigInput) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	model, err := s.modelRepo.GetModelByID(ctx, groupID, modelID)
	if err != nil {
		return nil, fmt.Errorf("get Agent model: %w", err)
	}
	mediaType := strings.ToLower(strings.TrimSpace(input.MediaType))
	if !isValidAgentMediaType(mediaType) {
		return nil, infraerrors.BadRequest("AGENT_MODEL_CONFIG_INVALID", fmt.Sprintf("invalid media_type %q", input.MediaType))
	}
	prices, err := normalizeAgentModelPrices(mediaType, input.Prices)
	if err != nil {
		return nil, infraerrors.BadRequest("AGENT_MODEL_CONFIG_INVALID", err.Error())
	}
	rate, err := normalizeAgentModelRate(mediaType, input.Enabled, input.RateMultiplier)
	if err != nil {
		return nil, infraerrors.BadRequest("AGENT_MODEL_CONFIG_INVALID", err.Error())
	}
	if input.Enabled && mediaType != AgentMediaTypeText && !hasEnabledAgentModelPrice(prices) {
		return nil, infraerrors.BadRequest("AGENT_MODEL_CONFIG_INVALID", "an enabled image or video model requires at least one resolution price, and at least one resolution must be enabled")
	}
	if err := s.modelRepo.UpdateModelConfig(ctx, groupID, model.ID, mediaType, input.Enabled, rate, prices); err != nil {
		return nil, fmt.Errorf("update Agent model: %w", err)
	}
	s.invalidateModelPlatforms(groupID)
	return s.GetConfig(ctx, groupID)
}

// DeleteModel 把模型从分组目录删除。删除不是永久排除：账号仍声明该模型时，
// 下一次同步或读路径自愈会以未启用状态把它带回来，由管理员重新启用后下游
// 才可见。需要"保留配置但下游不可见"应改用启用开关。
func (s *AgentModelCatalogService) DeleteModel(ctx context.Context, groupID, modelID int64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if _, err := s.modelRepo.GetModelByID(ctx, groupID, modelID); err != nil {
		return nil, fmt.Errorf("get Agent model: %w", err)
	}
	if err := s.modelRepo.DeleteModel(ctx, groupID, modelID); err != nil {
		return nil, fmt.Errorf("delete Agent model: %w", err)
	}
	// 删除后立即抑制本组的读路径自愈：否则本次返回的 GetConfig 会把刚删的行
	// 马上补回来，删除形同无效。节流窗口过后，账号仍声明该模型时自愈才会以
	// 未启用状态把它带回来。
	s.markHeal(groupID, time.Now())
	s.invalidateModelPlatforms(groupID)
	return s.GetConfig(ctx, groupID)
}

func (s *AgentModelCatalogService) ListAvailable(ctx context.Context, groupID int64) ([]AgentModelCatalogEntry, error) {
	if s == nil || s.accountRepo == nil || s.modelRepo == nil || groupID <= 0 {
		return nil, ErrAgentModelCatalogUnavailable
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	discovered := discoverAgentModels(accounts)
	models, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	current := agentDiscoverySet(discovered)
	models = s.healDiscoveredModelRows(ctx, groupID, discovered, models)
	entries := make(map[string]*agentModelCatalogAccumulator)
	for _, model := range models {
		if !model.Enabled || !model.Available || model.Excluded {
			continue
		}
		// The catalog account pool ignores transient scheduler state, but still
		// requires an active, schedulable, Agent-bound account with a mapping.
		// This keeps stale/unbound rows out while avoiding disappearance during a
		// temporary rate-limit or overload window.
		if _, ok := current[agentModelKey(model.Platform, model.ModelCode)]; !ok {
			continue
		}
		addConfiguredAgentCatalogEntry(entries, model)
	}
	return flattenAgentCatalog(entries), nil
}

// ResolveTextModelRate returns the downstream multiplier configured for the first
// candidate model that the Agent group currently offers as an enabled, priced
// text model. Candidates are matched in order, so the caller's billing-model
// preference is preserved.
func (s *AgentModelCatalogService) ResolveTextModelRate(ctx context.Context, groupID int64, platform string, models ...string) (float64, string, error) {
	if s == nil || s.modelRepo == nil || groupID <= 0 {
		return 0, "", ErrAgentModelRateUnavailable
	}
	platform = normalizeAgentPlatform(platform)
	if !isAgentLanguagePlatform(platform) {
		return 0, "", fmt.Errorf("%w: platform %s", ErrAgentModelRateUnavailable, platform)
	}
	for _, modelCode := range compactAgentModelCandidates(models) {
		model, err := s.modelRepo.GetEnabledModel(ctx, groupID, platform, modelCode)
		if err != nil || model == nil || model.MediaType != AgentMediaTypeText || model.RateMultiplier == nil {
			continue
		}
		if *model.RateMultiplier < 0 {
			continue
		}
		return *model.RateMultiplier, model.ModelCode, nil
	}
	return 0, "", fmt.Errorf("%w for platform %s model %s", ErrAgentModelRateUnavailable, platform, firstAgentModelCandidate(models))
}

func (s *AgentModelCatalogService) RequireAccountLanguageModel(ctx context.Context, groupID int64, account *Account, models ...string) (string, error) {
	if s == nil || s.modelRepo == nil || account == nil {
		return "", ErrAgentModelNotConfigured
	}
	current := agentDiscoverySet(discoverAgentModels([]Account{*account}))
	for _, modelCode := range compactAgentModelCandidates(models) {
		if _, ok := current[agentModelKey(account.Platform, modelCode)]; !ok {
			continue
		}
		model, err := s.modelRepo.GetEnabledModel(ctx, groupID, account.Platform, modelCode)
		if err == nil && model != nil && model.MediaType == AgentMediaTypeText {
			return model.ModelCode, nil
		}
	}
	return "", fmt.Errorf("%w for platform %s", ErrAgentModelNotConfigured, account.Platform)
}

// EnsureAgentImageModelPriced 判断该 (平台, 模型) 是否在目录里登记为图片模型，并校验
// 它至少配置了一个档位的每张单价（首个返回值表示"确实是图片模型"）。
//
// Gemini 标准图片接口只能在请求前确定"这是图片模型"，具体分辨率要等上游返回后才
// 知道（计费按 result.ImageSize 取档），因此这里只要求存在任一档位价格：不能因为
// 管理员只填了 2K 就把 1K/4K 的请求在入口拒掉（那是计费阶段按实际档位取值的事）。
// 返回 false 且 error 为 nil 表示目录里没有把它登记成图片模型，调用方应回退到语言
// 模型口径校验。
func (s *AgentModelCatalogService) EnsureAgentImageModelPriced(ctx context.Context, groupID int64, platform, model string) (bool, error) {
	if s == nil || s.modelRepo == nil || groupID <= 0 {
		return false, ErrAgentModelCatalogUnavailable
	}
	platform = normalizeAgentPlatform(platform)
	for _, modelCode := range compactAgentModelCandidates([]string{model}) {
		entry, err := s.modelRepo.GetEnabledModel(ctx, groupID, platform, modelCode)
		if err != nil || entry == nil || entry.MediaType != AgentMediaTypeImage {
			continue
		}
		if !hasEnabledAgentModelPrice(entry.Prices) {
			return true, fmt.Errorf("%w for model %s", ErrAgentImagePricingUnavailable, entry.ModelCode)
		}
		return true, nil
	}
	return false, nil
}

func (s *AgentModelCatalogService) ResolveMediaUnitPrice(
	ctx context.Context,
	groupID int64,
	platform string,
	mediaType string,
	resolution string,
	models ...string,
) (float64, string, error) {
	if s == nil || s.modelRepo == nil || s.accountRepo == nil {
		return 0, "", ErrAgentModelCatalogUnavailable
	}
	platform = normalizeAgentPlatform(platform)
	resolution, err := normalizeAgentPriceResolution(mediaType, resolution)
	if err != nil {
		return 0, "", err
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return 0, "", fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	current := agentDiscoverySet(discoverAgentModels(accounts))
	for _, modelCode := range compactAgentModelCandidates(models) {
		model, modelErr := s.modelRepo.GetEnabledModel(ctx, groupID, platform, modelCode)
		if modelErr != nil || model == nil || model.MediaType != mediaType {
			continue
		}
		if _, ok := current[agentModelKey(platform, modelCode)]; !ok {
			continue
		}
		if mediaType == AgentMediaTypeVideo && !isVideoAgentModelPlatform(model.Platform) {
			continue
		}
		for _, price := range model.Prices {
			if price.Resolution == resolution && price.BillingUnit == billingUnitForAgentMedia(mediaType) && price.UnitPrice >= 0 && agentModelPriceEnabled(price) {
				return price.UnitPrice, model.ModelCode, nil
			}
		}
	}
	if mediaType == AgentMediaTypeImage {
		return 0, "", fmt.Errorf("%w for model %s resolution %s", ErrAgentImagePricingUnavailable, firstAgentModelCandidate(models), resolution)
	}
	return 0, "", fmt.Errorf("%w for model %s resolution %s", ErrVideoPricingRuleNotFound, firstAgentModelCandidate(models), resolution)
}

// ResolveModelPlatform 返回分组内提供该模型的平台，供请求入口选择账号平台使用。
// 只要 enabled ∧ available ∧ 未排除 的目录行；同一个模型名出现在多个平台时取平台名
// 字典序最小者，保证同一请求在多次调用间稳定（避免选号与计费落在不同平台）。
func (s *AgentModelCatalogService) ResolveModelPlatform(ctx context.Context, groupID int64, model string) (string, bool, error) {
	model = strings.TrimSpace(model)
	if s == nil || s.modelRepo == nil || groupID <= 0 || model == "" {
		return "", false, nil
	}
	byModel, err := s.modelPlatformIndex(ctx, groupID)
	if err != nil {
		return "", false, err
	}
	platform, ok := byModel[model]
	return platform, ok, nil
}

func (s *AgentModelCatalogService) modelPlatformIndex(ctx context.Context, groupID int64) (map[string]string, error) {
	now := time.Now()
	s.platformMu.Lock()
	if snapshot, ok := s.platformCache[groupID]; ok && now.Before(snapshot.expiresAt) {
		byModel := snapshot.byModel
		s.platformMu.Unlock()
		return byModel, nil
	}
	s.platformMu.Unlock()

	models, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, fmt.Errorf("list Agent models: %w", err)
	}
	byModel := make(map[string]string, len(models))
	for _, model := range models {
		if !model.Enabled || !model.Available || model.Excluded {
			continue
		}
		code := strings.TrimSpace(model.ModelCode)
		if code == "" {
			continue
		}
		platform := normalizeAgentPlatform(model.Platform)
		if existing, ok := byModel[code]; ok && existing <= platform {
			continue
		}
		byModel[code] = platform
	}

	s.platformMu.Lock()
	if s.platformCache == nil {
		s.platformCache = make(map[int64]agentModelPlatformSnapshot)
	}
	s.platformCache[groupID] = agentModelPlatformSnapshot{byModel: byModel, expiresAt: now.Add(agentModelPlatformSnapshotTTL)}
	s.platformMu.Unlock()
	return byModel, nil
}

func (s *AgentModelCatalogService) invalidateModelPlatforms(groupID int64) {
	if s == nil {
		return
	}
	s.platformMu.Lock()
	delete(s.platformCache, groupID)
	s.platformMu.Unlock()
}

func (s *AgentModelCatalogService) requireAgentGroup(ctx context.Context, groupID int64) error {
	if s == nil || s.accountRepo == nil || s.groupRepo == nil || s.modelRepo == nil {
		return ErrAgentModelCatalogUnavailable
	}
	if groupID <= 0 {
		return errors.New("invalid Agent group id")
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return err
	}
	if group == nil || !group.IsAgent() {
		return errors.New("group is not an Agent group")
	}
	return nil
}

func normalizeAgentModelPrices(mediaType string, prices []AgentModelPrice) ([]AgentModelPrice, error) {
	if mediaType == AgentMediaTypeText {
		if len(prices) > 0 {
			return nil, errors.New("text models use a platform multiplier and cannot have model prices")
		}
		return []AgentModelPrice{}, nil
	}
	billingUnit := billingUnitForAgentMedia(mediaType)
	seen := make(map[string]struct{}, len(prices))
	out := make([]AgentModelPrice, 0, len(prices))
	for _, price := range prices {
		resolution, err := normalizeAgentPriceResolution(mediaType, price.Resolution)
		if err != nil {
			return nil, err
		}
		if price.UnitPrice < 0 {
			return nil, fmt.Errorf("unit price for %s must be non-negative", resolution)
		}
		if _, exists := seen[resolution]; exists {
			return nil, fmt.Errorf("duplicate price resolution %s", resolution)
		}
		seen[resolution] = struct{}{}
		enabled := true
		if price.Enabled != nil {
			enabled = *price.Enabled
		}
		out = append(out, AgentModelPrice{Resolution: resolution, BillingUnit: billingUnit, UnitPrice: price.UnitPrice, Enabled: &enabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resolution < out[j].Resolution })
	return out, nil
}

func agentModelPriceEnabled(price AgentModelPrice) bool {
	return price.Enabled == nil || *price.Enabled
}

func hasEnabledAgentModelPrice(prices []AgentModelPrice) bool {
	for _, price := range prices {
		if agentModelPriceEnabled(price) {
			return true
		}
	}
	return false
}

// normalizeAgentModelRate validates the per-model configuration shape: text
// models carry a multiplier, media models carry per-resolution unit prices.
// An enabled model must be fully priced, otherwise it would be selectable and
// then fail at request time.
func normalizeAgentModelRate(mediaType string, enabled bool, rate *float64) (*float64, error) {
	if mediaType == AgentMediaTypeText {
		if rate != nil && *rate < 0 {
			return nil, errors.New("text model rate_multiplier must be non-negative")
		}
		if enabled && rate == nil {
			return nil, errors.New("an enabled text model requires rate_multiplier")
		}
		return rate, nil
	}
	if rate != nil {
		return nil, errors.New("image and video models are priced per resolution and cannot set rate_multiplier")
	}
	return nil, nil
}

func normalizeAgentPriceResolution(mediaType, resolution string) (string, error) {
	resolution = strings.TrimSpace(resolution)
	if mediaType == AgentMediaTypeImage {
		if tier, ok := ClassifyImageBillingTier(resolution); ok && (tier == ImageBillingSize1K || tier == ImageBillingSize2K || tier == ImageBillingSize4K) {
			return tier, nil
		}
		return "", fmt.Errorf("invalid image resolution %q; expected 1K, 2K, or 4K", resolution)
	}
	if resolution == "" || len(resolution) > 32 {
		return "", errors.New("video resolution must be between 1 and 32 characters")
	}
	if strings.EqualFold(resolution, "2k") {
		return VideoResolution2K, nil
	}
	if strings.EqualFold(resolution, "4k") {
		return VideoResolution4K, nil
	}
	return strings.ToLower(resolution), nil
}

func billingUnitForAgentMedia(mediaType string) string {
	if mediaType == AgentMediaTypeImage {
		return AgentBillingUnitImage
	}
	if mediaType == AgentMediaTypeVideo {
		return AgentBillingUnitSecond
	}
	return ""
}

func isValidAgentMediaType(mediaType string) bool {
	return mediaType == AgentMediaTypeText || mediaType == AgentMediaTypeImage || mediaType == AgentMediaTypeVideo
}

func normalizeAgentPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	// 168 briefly renamed the persisted video platform to seedance. Keep
	// discovery/catalog reads compatible with databases that have not yet run
	// the corrective 239 migration; dispatch still uses the canonical video
	// platform and its existing provider adapters.
	if platform == "seedance" {
		return PlatformVideo
	}
	return platform
}

func isVideoAgentModelPlatform(platform string) bool {
	return normalizeAgentPlatform(platform) == PlatformVideo
}

func discoverAgentModels(accounts []Account) []AgentModelDiscovery {
	discovered := make(map[string]AgentModelDiscovery)
	for i := range accounts {
		account := &accounts[i]
		platform := normalizeAgentPlatform(account.Platform)
		defaults, supported := defaultAgentModels(platform)
		if !supported {
			continue
		}
		mapping := account.GetModelMapping()
		// 图片账号（账号管理里显式声明的）没有映射时不做内置清单兜底：它的模型清单
		// 就是管理员勾选的那些图片模型，灌入平台默认文本模型毫无意义。
		if len(mapping) == 0 {
			if account.IsImageAccount() {
				continue
			}
			for modelCode, descriptor := range defaults {
				addAgentDiscovery(discovered, platform, modelCode, descriptor.mediaType)
			}
			continue
		}
		for requestedModel, upstreamModel := range mapping {
			requestedModel = strings.TrimSpace(requestedModel)
			if requestedModel == "" {
				continue
			}
			// 图片账号声明的一切模型都是图片模型：不靠关键词猜类型。
			if account.IsImageAccount() {
				addAgentDiscovery(discovered, platform, requestedModel, AgentMediaTypeImage)
				continue
			}
			if strings.Contains(requestedModel, "*") {
				// 通配符只能在本平台已知的模型清单里展开（无法凭空知道上游新模型），
				// 展开时按族名重新判定类型，避免清单里的描述过时。
				for modelCode := range defaults {
					if matchWildcard(requestedModel, modelCode) {
						addAgentDiscovery(discovered, platform, modelCode, agentModelDescriptorForMapping(platform, modelCode, upstreamModel).mediaType)
					}
				}
				continue
			}
			descriptor, ok := defaults[requestedModel]
			if !ok {
				descriptor, ok = defaults[strings.TrimSpace(upstreamModel)]
			}
			if !ok {
				// 目录里没有的名字按族名猜类型：下游名与上游名都要看（上游常是别名）。
				descriptor = agentModelDescriptorForMapping(platform, requestedModel, upstreamModel)
			}
			addAgentDiscovery(discovered, platform, requestedModel, descriptor.mediaType)
		}
	}
	out := make([]AgentModelDiscovery, 0, len(discovered))
	for _, item := range discovered {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Platform != out[j].Platform {
			return out[i].Platform < out[j].Platform
		}
		return out[i].ModelCode < out[j].ModelCode
	})
	return out
}

// agentMediaTypeServable 报告该平台的账号能否在本网关真正服务这类模型。
// 目录里出现一个"能配价却永远调不通"的模型比不出现更糟：视频目前只有 video 平台
// （seedance）有计费链路，图片端点只对 openai / grok / gemini 开放。
func agentMediaTypeServable(platform, mediaType string) bool {
	switch mediaType {
	case AgentMediaTypeVideo:
		return platform == PlatformVideo
	case AgentMediaTypeImage:
		return isAgentImagePlatform(platform)
	default:
		return true
	}
}

func addAgentDiscovery(discovered map[string]AgentModelDiscovery, platform, modelCode, mediaType string) {
	modelCode = strings.TrimSpace(modelCode)
	if modelCode == "" || !isValidAgentMediaType(mediaType) || !agentMediaTypeServable(platform, mediaType) {
		return
	}
	key := agentModelKey(platform, modelCode)
	if _, exists := discovered[key]; exists {
		return
	}
	discovered[key] = AgentModelDiscovery{Platform: platform, ModelCode: modelCode, MediaType: mediaType}
}

func agentDiscoverySet(discovered []AgentModelDiscovery) map[string]struct{} {
	set := make(map[string]struct{}, len(discovered))
	for _, item := range discovered {
		set[agentModelKey(item.Platform, item.ModelCode)] = struct{}{}
	}
	return set
}

func agentModelKey(platform, modelCode string) string {
	return normalizeAgentPlatform(platform) + "\x00" + strings.TrimSpace(modelCode)
}

func compactAgentModelCandidates(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func firstAgentModelCandidate(models []string) string {
	candidates := compactAgentModelCandidates(models)
	if len(candidates) == 0 {
		return "unknown"
	}
	return candidates[0]
}

func firstNonEmptyAgentModel(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type agentModelDescriptor struct {
	mediaType string
}

// isAgentPlatformSupported 报告该平台的账号能否向聚合分组贡献模型。
// 覆盖应用支持的全部 provider：三大协议平台 + grok + 国产 OpenAI 兼容供应商 + 视频。
func isAgentPlatformSupported(platform string) bool {
	switch normalizeAgentPlatform(platform) {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformVideo,
		PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return true
	default:
		return false
	}
}

// isAgentImagePlatform 报告该平台是否有可用的图片接口（OpenAI 图片端点与 Gemini 图片输出）。
func isAgentImagePlatform(platform string) bool {
	switch normalizeAgentPlatform(platform) {
	case PlatformOpenAI, PlatformGrok, PlatformGemini:
		return true
	default:
		return false
	}
}

// IsOpenAICompatibleAgentPlatform 报告聚合分组里该平台是否由 OpenAI 兼容链路服务
// （决定 /v1/chat/completions、/v1/responses、/v1/messages 走哪个 handler）。
func IsOpenAICompatibleAgentPlatform(platform string) bool {
	switch normalizeAgentPlatform(platform) {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		return true
	default:
		return false
	}
}

func defaultAgentModels(platform string) (map[string]agentModelDescriptor, bool) {
	models := make(map[string]agentModelDescriptor)
	switch platform {
	case PlatformOpenAI:
		for _, model := range openai.DefaultModels {
			models[model.ID] = defaultAgentModelDescriptorForID(platform, model.ID)
		}
	case PlatformAnthropic:
		for _, model := range claude.DefaultModels {
			models[model.ID] = agentModelDescriptor{mediaType: AgentMediaTypeText}
		}
	case PlatformGemini:
		for _, model := range geminicli.DefaultModels {
			models[model.ID] = defaultAgentModelDescriptorForID(platform, model.ID)
		}
	case PlatformGrok:
		for _, model := range xai.DefaultModels() {
			models[model.ID] = defaultAgentModelDescriptorForID(platform, model.ID)
		}
	case PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		// 国产供应商的模型清单随上游版本频繁变化，不内置固定清单：与 video 平台同一
		// 策略，模型必须由账号的 model_mapping 声明（未声明则该账号不贡献模型）。
	case PlatformVideo:
		// Video models must be declared by each account's model_mapping. There is
		// intentionally no gateway-wide fixed video model list.
	default:
		return nil, false
	}
	return models, true
}

// agentImageModelFamilies / agentVideoModelFamilies 是"模型族"关键词表：只写族名，
// 不写版本号。上游出新版本（gemini-3.7-*-image、seedream-5、veo-4 …）时无需改代码，
// 名字里带族名就会被归类。
//
// 这张表只用于**猜**媒体类型，猜错不会造成不可恢复的后果：管理端「Yingzo Agent」
// 页可以逐模型改媒体类型（media_type 是目录里的字段），改完立即生效。
var (
	agentImageModelFamilies = []string{
		"image",                 // gpt-image-*, gemini-*-image*, qwen-image*
		"imagen",                // google imagen-*
		"nano-banana", "banana", // Gemini 图像模型的社区叫法
		"cogview", // 智谱
		"dall-e", "dalle",
		"flux",
		"seedream", // 字节图像
		"stable-diffusion", "sdxl",
		"kolors", // 快手图像
		"ideogram", "midjourney", "recraft",
	}
	agentVideoModelFamilies = []string{
		"video",
		"veo",                 // google
		"sora",                // openai
		"kling",               // 快手
		"seedance",            // 字节
		"hailuo", "minimax-h", // MiniMax
		"wan-",    // 阿里通义万相视频
		"imagine", // grok-imagine
		"pika", "runway", "luma", "vidu",
	}
)

// classifyAgentModelFamily 按族名关键词判断媒体类型，识别不出时返回空串。
// 先看视频关键词：视频族里存在同时含 image 的名字（grok-imagine-video 之类），
// 顺序反了会被误判成图片。
func classifyAgentModelFamily(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return ""
	}
	for _, keyword := range agentVideoModelFamilies {
		if strings.Contains(lower, keyword) {
			return AgentMediaTypeVideo
		}
	}
	for _, keyword := range agentImageModelFamilies {
		if strings.Contains(lower, keyword) {
			return AgentMediaTypeImage
		}
	}
	return ""
}

func defaultAgentModelDescriptorForID(platform, model string) agentModelDescriptor {
	// 视频平台上的模型一律是视频模型（该平台的账号只提供视频能力）。
	if normalizeAgentPlatform(platform) == PlatformVideo {
		return agentModelDescriptor{mediaType: AgentMediaTypeVideo}
	}
	if family := classifyAgentModelFamily(model); family != "" {
		return agentModelDescriptor{mediaType: family}
	}
	return agentModelDescriptor{mediaType: AgentMediaTypeText}
}

// agentModelDescriptorForMapping 判断一条账号映射该归到哪类模型。
//
// 两个名字都要看：下游名（requested，客户端看到的名字）与上游名（upstream，
// 实际转发名）经常不一致——运营会把 gemini-3.1-flash-image-preview 映射到上游别名
// （例如 nano-banana-pro），只看上游名就会把图片模型当成文本模型。任一侧命中族名
// 即采纳该类型，两侧都没命中才按文本处理。
func agentModelDescriptorForMapping(platform, requestedModel, upstreamModel string) agentModelDescriptor {
	for _, candidate := range []string{requestedModel, upstreamModel} {
		if family := classifyAgentModelFamily(candidate); family != "" {
			return agentModelDescriptor{mediaType: family}
		}
	}
	return defaultAgentModelDescriptorForID(platform, firstNonEmptyAgentModel(requestedModel, upstreamModel))
}

func addConfiguredAgentCatalogEntry(entries map[string]*agentModelCatalogAccumulator, model AgentGroupModel) {
	platform := normalizeAgentPlatform(model.Platform)
	entry := entries[model.ModelCode]
	if entry == nil {
		entry = &agentModelCatalogAccumulator{
			mediaTypes: make(map[string]struct{}),
			platforms:  make(map[string]struct{}),
			interfaces: make(map[string]struct{}),
		}
		entries[model.ModelCode] = entry
	}
	entry.mediaTypes[model.MediaType] = struct{}{}
	entry.platforms[platform] = struct{}{}
	for _, nativeInterface := range agentInterfacesForModel(platform, model.MediaType, model.ModelCode) {
		entry.interfaces[nativeInterface] = struct{}{}
	}
}

func agentInterfacesForModel(platform, mediaType, modelCode string) []string {
	switch platform {
	case PlatformOpenAI:
		if mediaType == AgentMediaTypeImage {
			return []string{AgentInterfaceOpenAIImages}
		}
		if strings.Contains(strings.ToLower(modelCode), "embedding") {
			return []string{AgentInterfaceOpenAIEmbeddings}
		}
		return []string{AgentInterfaceOpenAIResponses, AgentInterfaceOpenAIChatCompletions}
	case PlatformAnthropic:
		return []string{AgentInterfaceAnthropicMessages}
	case PlatformGemini:
		return []string{AgentInterfaceGeminiGenerateContent}
	case PlatformVideo:
		return []string{AgentInterfaceSeedanceVideos}
	case PlatformGrok:
		if mediaType == AgentMediaTypeImage {
			return []string{AgentInterfaceOpenAIImages}
		}
		return []string{AgentInterfaceOpenAIResponses, AgentInterfaceOpenAIChatCompletions}
	case PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax:
		if mediaType == AgentMediaTypeImage {
			return []string{AgentInterfaceOpenAIImages}
		}
		if platform == PlatformZhipu && isGLM53Model(modelCode) {
			// Z.AI's GLM-5.3 family is exposed through both its native
			// OpenAI Chat Completions endpoint and the Anthropic Messages
			// compatibility endpoint. Keep both in the manifest so clients
			// can select the protocol they natively speak; Yingzo Agent
			// prefers Chat Completions because it preserves the provider's
			// tool-call/reasoning shape without a protocol bridge.
			return []string{AgentInterfaceOpenAIChatCompletions, AgentInterfaceAnthropicMessages}
		}
		if platform == PlatformDeepseek {
			// DeepSeek V4 exposes a native Responses-compatible route in the
			// gateway. Advertise it first so clients that prefer Responses use
			// the state-safe adapter; keep Chat Completions for compatibility.
			return []string{AgentInterfaceOpenAIResponses, AgentInterfaceOpenAIChatCompletions}
		}
		// Other domestic providers remain OpenAI-compatible Chat Completions.
		return []string{AgentInterfaceOpenAIChatCompletions}
	default:
		return nil
	}
}

func flattenAgentCatalog(entries map[string]*agentModelCatalogAccumulator) []AgentModelCatalogEntry {
	modelIDs := make([]string, 0, len(entries))
	for modelID := range entries {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	result := make([]AgentModelCatalogEntry, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		entry := entries[modelID]
		result = append(result, AgentModelCatalogEntry{
			ID:         modelID,
			MediaTypes: orderedAgentMediaTypes(entry.mediaTypes),
			Platforms:  sortedAgentSet(entry.platforms),
			Interfaces: orderedAgentInterfaces(entry.interfaces),
		})
	}
	return result
}

func orderedAgentMediaTypes(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range []string{AgentMediaTypeText, AgentMediaTypeImage, AgentMediaTypeVideo} {
		if _, ok := values[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func orderedAgentInterfaces(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range []string{
		AgentInterfaceOpenAIResponses,
		AgentInterfaceOpenAIChatCompletions,
		AgentInterfaceOpenAIEmbeddings,
		AgentInterfaceOpenAIImages,
		AgentInterfaceAnthropicMessages,
		AgentInterfaceGeminiGenerateContent,
		AgentInterfaceSeedanceVideos,
	} {
		if _, ok := values[value]; ok {
			result = append(result, value)
		}
	}
	return result
}

func sortedAgentSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

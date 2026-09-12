package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
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
	ErrAgentPlatformRateUnavailable = errors.New("agent language platform multiplier is not configured")
)

type AgentPlatformRate struct {
	GroupID        int64     `json:"group_id"`
	Platform       string    `json:"platform"`
	RateMultiplier float64   `json:"rate_multiplier"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AgentModelPrice struct {
	ID           int64     `json:"id"`
	AgentModelID int64     `json:"agent_model_id"`
	Resolution   string    `json:"resolution"`
	BillingUnit  string    `json:"billing_unit"`
	UnitPrice    float64   `json:"unit_price"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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
}

type AgentModelCatalogConfig struct {
	PlatformRates []AgentPlatformRate `json:"platform_rates"`
	Models        []AgentGroupModel   `json:"models"`
}

type AgentModelRepository interface {
	SyncDiscovered(ctx context.Context, groupID int64, discovered []AgentModelDiscovery, seenAt time.Time) error
	ListModels(ctx context.Context, groupID int64, includeExcluded bool) ([]AgentGroupModel, error)
	GetModelByID(ctx context.Context, groupID, modelID int64) (*AgentGroupModel, error)
	GetEnabledModel(ctx context.Context, groupID int64, platform, modelCode string) (*AgentGroupModel, error)
	UpdateModelConfig(ctx context.Context, groupID, modelID int64, mediaType string, enabled bool, prices []AgentModelPrice) error
	ExcludeModel(ctx context.Context, groupID, modelID int64, excludedAt time.Time) error
	ListPlatformRates(ctx context.Context, groupID int64) ([]AgentPlatformRate, error)
	UpsertPlatformRate(ctx context.Context, groupID int64, platform string, multiplier float64) error
	GetPlatformRate(ctx context.Context, groupID int64, platform string) (*AgentPlatformRate, error)
}

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
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list schedulable Agent accounts: %w", err)
	}
	discovered := discoverAgentModels(accounts)
	if err := s.modelRepo.SyncDiscovered(ctx, groupID, discovered, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("sync Agent models: %w", err)
	}
	return s.GetConfig(ctx, groupID)
}

func (s *AgentModelCatalogService) GetConfig(ctx context.Context, groupID int64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	rates, err := s.modelRepo.ListPlatformRates(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("list Agent platform rates: %w", err)
	}
	models, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, fmt.Errorf("list Agent models: %w", err)
	}
	if rates == nil {
		rates = []AgentPlatformRate{}
	}
	if models == nil {
		models = []AgentGroupModel{}
	}
	return &AgentModelCatalogConfig{PlatformRates: rates, Models: models}, nil
}

func (s *AgentModelCatalogService) SetPlatformRate(ctx context.Context, groupID int64, platform string, multiplier float64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	platform = normalizeAgentPlatform(platform)
	if !isAgentLanguagePlatform(platform) {
		return nil, infraerrors.BadRequest("AGENT_PLATFORM_RATE_INVALID", fmt.Sprintf("unsupported Agent language platform %q", platform))
	}
	if multiplier < 0 {
		return nil, infraerrors.BadRequest("AGENT_PLATFORM_RATE_INVALID", "rate_multiplier must be non-negative")
	}
	if err := s.modelRepo.UpsertPlatformRate(ctx, groupID, platform, multiplier); err != nil {
		return nil, fmt.Errorf("save Agent platform rate: %w", err)
	}
	return s.GetConfig(ctx, groupID)
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
	if err := s.modelRepo.UpdateModelConfig(ctx, groupID, model.ID, mediaType, input.Enabled, prices); err != nil {
		return nil, fmt.Errorf("update Agent model: %w", err)
	}
	return s.GetConfig(ctx, groupID)
}

func (s *AgentModelCatalogService) ExcludeModel(ctx context.Context, groupID, modelID int64) (*AgentModelCatalogConfig, error) {
	if err := s.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	if _, err := s.modelRepo.GetModelByID(ctx, groupID, modelID); err != nil {
		return nil, fmt.Errorf("get Agent model: %w", err)
	}
	if err := s.modelRepo.ExcludeModel(ctx, groupID, modelID, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("exclude Agent model: %w", err)
	}
	return s.GetConfig(ctx, groupID)
}

func (s *AgentModelCatalogService) ListAvailable(ctx context.Context, groupID int64) ([]AgentModelCatalogEntry, error) {
	if s == nil || s.accountRepo == nil || s.modelRepo == nil || groupID <= 0 {
		return nil, ErrAgentModelCatalogUnavailable
	}
	models, err := s.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	current := agentDiscoverySet(discoverAgentModels(accounts))
	entries := make(map[string]*agentModelCatalogAccumulator)
	for _, model := range models {
		if !model.Enabled || !model.Available || model.Excluded {
			continue
		}
		if _, ok := current[agentModelKey(model.Platform, model.ModelCode)]; !ok {
			continue
		}
		addConfiguredAgentCatalogEntry(entries, model)
	}
	return flattenAgentCatalog(entries), nil
}

func (s *AgentModelCatalogService) ResolvePlatformRate(ctx context.Context, groupID int64, platform string) (float64, error) {
	if s == nil || s.modelRepo == nil || groupID <= 0 {
		return 0, ErrAgentPlatformRateUnavailable
	}
	platform = normalizeAgentPlatform(platform)
	if !isAgentLanguagePlatform(platform) {
		return 0, fmt.Errorf("%w: platform %s", ErrAgentPlatformRateUnavailable, platform)
	}
	rate, err := s.modelRepo.GetPlatformRate(ctx, groupID, platform)
	if err != nil || rate == nil || rate.RateMultiplier < 0 {
		return 0, fmt.Errorf("%w for platform %s", ErrAgentPlatformRateUnavailable, platform)
	}
	return rate.RateMultiplier, nil
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
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
	if err != nil {
		return 0, "", fmt.Errorf("%w: %v", ErrAgentModelCatalogUnavailable, err)
	}
	current := agentDiscoverySet(discoverAgentModels(accounts))
	for _, modelCode := range compactAgentModelCandidates(models) {
		if _, ok := current[agentModelKey(platform, modelCode)]; !ok {
			continue
		}
		model, modelErr := s.modelRepo.GetEnabledModel(ctx, groupID, platform, modelCode)
		if modelErr != nil || model == nil || model.MediaType != mediaType {
			continue
		}
		for _, price := range model.Prices {
			if price.Resolution == resolution && price.BillingUnit == billingUnitForAgentMedia(mediaType) && price.UnitPrice >= 0 {
				return price.UnitPrice, model.ModelCode, nil
			}
		}
	}
	if mediaType == AgentMediaTypeImage {
		return 0, "", fmt.Errorf("%w for model %s resolution %s", ErrAgentImagePricingUnavailable, firstAgentModelCandidate(models), resolution)
	}
	return 0, "", fmt.Errorf("%w for model %s resolution %s", ErrVideoPricingRuleNotFound, firstAgentModelCandidate(models), resolution)
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
		out = append(out, AgentModelPrice{Resolution: resolution, BillingUnit: billingUnit, UnitPrice: price.UnitPrice})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resolution < out[j].Resolution })
	return out, nil
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
	return strings.ToLower(strings.TrimSpace(platform))
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
		if len(mapping) == 0 {
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
			if strings.Contains(requestedModel, "*") {
				for modelCode, descriptor := range defaults {
					if matchWildcard(requestedModel, modelCode) {
						addAgentDiscovery(discovered, platform, modelCode, descriptor.mediaType)
					}
				}
				continue
			}
			descriptor, ok := defaults[requestedModel]
			if !ok {
				descriptor, ok = defaults[strings.TrimSpace(upstreamModel)]
			}
			if !ok {
				descriptor = defaultAgentModelDescriptorForID(platform, firstNonEmptyAgentModel(upstreamModel, requestedModel))
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

func addAgentDiscovery(discovered map[string]AgentModelDiscovery, platform, modelCode, mediaType string) {
	modelCode = strings.TrimSpace(modelCode)
	if modelCode == "" || !isValidAgentMediaType(mediaType) {
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

func isAgentPlatformSupported(platform string) bool {
	switch normalizeAgentPlatform(platform) {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformVideo:
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
	case PlatformVideo:
		// Video models must be declared by each account's model_mapping. There is
		// intentionally no gateway-wide fixed video model list.
	default:
		return nil, false
	}
	return models, true
}

func defaultAgentModelDescriptorForID(platform, model string) agentModelDescriptor {
	mediaType := AgentMediaTypeText
	lower := strings.ToLower(strings.TrimSpace(model))
	switch platform {
	case PlatformOpenAI:
		if strings.Contains(lower, "image") {
			mediaType = AgentMediaTypeImage
		}
	case PlatformGemini:
		if strings.Contains(lower, "image") || strings.Contains(lower, "imagen") {
			mediaType = AgentMediaTypeImage
		}
	case PlatformVideo:
		mediaType = AgentMediaTypeVideo
	}
	return agentModelDescriptor{mediaType: mediaType}
}

func addConfiguredAgentCatalogEntry(entries map[string]*agentModelCatalogAccumulator, model AgentGroupModel) {
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
	entry.platforms[model.Platform] = struct{}{}
	for _, nativeInterface := range agentInterfacesForModel(model.Platform, model.MediaType, model.ModelCode) {
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

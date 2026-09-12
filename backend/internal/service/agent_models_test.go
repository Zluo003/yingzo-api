package service

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type agentCatalogAccountRepoStub struct {
	AccountRepository
	accounts []Account
	err      error
}

func (s *agentCatalogAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), s.accounts...), s.err
}

func (s *agentCatalogAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]Account, error) {
	if s.err != nil {
		return nil, s.err
	}
	accounts := make([]Account, 0)
	for _, account := range s.accounts {
		if account.Platform == platform {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

type agentCatalogGroupRepoStub struct {
	GroupRepository
	group *Group
}

func (s *agentCatalogGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	if s.group == nil {
		return nil, ErrGroupNotFound
	}
	copy := *s.group
	return &copy, nil
}

type agentModelMemoryRepo struct {
	nextID int64
	models map[string]*AgentGroupModel
}

func newAgentModelMemoryRepo() *agentModelMemoryRepo {
	return &agentModelMemoryRepo{nextID: 1, models: map[string]*AgentGroupModel{}}
}

func (r *agentModelMemoryRepo) SyncDiscovered(_ context.Context, groupID int64, discovered []AgentModelDiscovery, seenAt time.Time) error {
	for _, model := range r.models {
		if model.GroupID == groupID && !model.Excluded {
			model.Available = false
		}
	}
	for _, item := range discovered {
		key := agentModelKey(item.Platform, item.ModelCode)
		model := r.models[key]
		if model == nil {
			model = &AgentGroupModel{
				ID: r.nextID, GroupID: groupID, Platform: item.Platform, ModelCode: item.ModelCode,
				MediaType: item.MediaType, Enabled: true, DiscoveredAt: seenAt, CreatedAt: seenAt,
				Prices: []AgentModelPrice{},
			}
			r.nextID++
			r.models[key] = model
		}
		model.LastSeenAt = seenAt
		model.UpdatedAt = seenAt
		model.Available = !model.Excluded
	}
	return nil
}

func (r *agentModelMemoryRepo) ListModels(_ context.Context, groupID int64, includeExcluded bool) ([]AgentGroupModel, error) {
	models := make([]AgentGroupModel, 0)
	for _, model := range r.models {
		if model.GroupID != groupID || (model.Excluded && !includeExcluded) {
			continue
		}
		models = append(models, cloneAgentModelForTest(model))
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Platform != models[j].Platform {
			return models[i].Platform < models[j].Platform
		}
		return models[i].ModelCode < models[j].ModelCode
	})
	return models, nil
}

func (r *agentModelMemoryRepo) GetModelByID(_ context.Context, groupID, modelID int64) (*AgentGroupModel, error) {
	for _, model := range r.models {
		if model.GroupID == groupID && model.ID == modelID {
			copy := cloneAgentModelForTest(model)
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}

func (r *agentModelMemoryRepo) GetEnabledModel(_ context.Context, groupID int64, platform, modelCode string) (*AgentGroupModel, error) {
	model := r.models[agentModelKey(platform, modelCode)]
	if model == nil || model.GroupID != groupID || !model.Enabled || !model.Available || model.Excluded {
		return nil, sql.ErrNoRows
	}
	copy := cloneAgentModelForTest(model)
	return &copy, nil
}

func (r *agentModelMemoryRepo) UpdateModelConfig(_ context.Context, groupID, modelID int64, mediaType string, enabled bool, rateMultiplier *float64, prices []AgentModelPrice) error {
	for _, model := range r.models {
		if model.GroupID == groupID && model.ID == modelID && !model.Excluded {
			model.MediaType = mediaType
			model.Enabled = enabled
			model.Prices = append([]AgentModelPrice(nil), prices...)
			if rateMultiplier == nil {
				model.RateMultiplier = nil
			} else {
				rate := *rateMultiplier
				model.RateMultiplier = &rate
			}
			return nil
		}
	}
	return sql.ErrNoRows
}

func (r *agentModelMemoryRepo) ExcludeModel(_ context.Context, groupID, modelID int64, excludedAt time.Time) error {
	for _, model := range r.models {
		if model.GroupID == groupID && model.ID == modelID && !model.Excluded {
			model.Enabled = false
			model.Available = false
			model.Excluded = true
			model.ExcludedAt = &excludedAt
			model.Prices = []AgentModelPrice{}
			return nil
		}
	}
	return sql.ErrNoRows
}

func cloneAgentModelForTest(model *AgentGroupModel) AgentGroupModel {
	copy := *model
	copy.Prices = append([]AgentModelPrice(nil), model.Prices...)
	if model.RateMultiplier != nil {
		rate := *model.RateMultiplier
		copy.RateMultiplier = &rate
	}
	return copy
}

// setAgentTextModelRate 是测试里配置文本模型倍率的简写：直接改内存仓库，
// 避免为了过一遍服务层校验再额外装配分组仓库。
func setAgentTextModelRate(t *testing.T, repo *agentModelMemoryRepo, groupID int64, platform, modelCode string, rate float64) {
	t.Helper()
	model := repo.models[agentModelKey(platform, modelCode)]
	require.NotNil(t, model, "model %s/%s must be discovered first", platform, modelCode)
	require.Equal(t, groupID, model.GroupID)
	value := rate
	model.Enabled = true
	model.Available = true
	model.RateMultiplier = &value
}

func newAgentCatalogForTest(accounts *agentCatalogAccountRepoStub) (*AgentModelCatalogService, *agentModelMemoryRepo) {
	models := newAgentModelMemoryRepo()
	group := &agentCatalogGroupRepoStub{group: &Group{ID: 9, Kind: "agent", SystemCode: "yingzo"}}
	return NewAgentModelCatalogService(accounts, group, models), models
}

func TestAgentModelCatalogSyncsAssignedAccountMappingsAcrossNativeProviders(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{
		{ID: 1, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{
			"gpt-5.4": "gpt-5.4", "embedding-alias": "text-embedding-3-small", "image-alias": "gpt-image-2",
		}}},
		{ID: 2, Platform: PlatformAnthropic, Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4-8"}}},
		{ID: 3, Platform: PlatformGemini, Credentials: map[string]any{"model_mapping": map[string]any{
			"gemini-2.5-flash": "gemini-2.5-flash", "gemini-image-alias": "gemini-3.1-flash-image",
		}}},
		{ID: 4, Platform: PlatformVideo, Credentials: map[string]any{"model_mapping": map[string]any{"video-custom": "upstream-video"}}},
		{ID: 5, Platform: PlatformGrok, Credentials: map[string]any{"model_mapping": map[string]any{"grok-4": "grok-4"}}},
		{ID: 6, Platform: PlatformDeepseek, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4-pro": "deepseek-v4-pro"}}},
		{ID: 7, Platform: PlatformKimi, Credentials: map[string]any{"model_mapping": map[string]any{"kimi-k3": "kimi-k3"}}},
		{ID: 8, Platform: PlatformZhipu, Credentials: map[string]any{"model_mapping": map[string]any{"glm-5": "glm-5"}}},
		{ID: 9, Platform: PlatformMiniMax, Credentials: map[string]any{"model_mapping": map[string]any{"MiniMax-M3": "MiniMax-M3"}}},
	}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	catalog, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	// 聚合分组覆盖应用支持的全部 provider：三方协议平台 + grok + 国产供应商 + 视频。
	require.Equal(t, []string{
		"MiniMax-M3", "claude-opus-4-8", "deepseek-v4-pro", "embedding-alias",
		"gemini-2.5-flash", "gemini-image-alias", "glm-5", "gpt-5.4", "grok-4",
		"image-alias", "kimi-k3", "video-custom",
	}, agentCatalogIDsForTest(catalog))
	byID := map[string]AgentModelCatalogEntry{}
	for _, entry := range catalog {
		byID[entry.ID] = entry
	}
	require.Equal(t, []string{AgentMediaTypeText}, byID["embedding-alias"].MediaTypes)
	require.Equal(t, []string{AgentInterfaceOpenAIEmbeddings}, byID["embedding-alias"].Interfaces)
	require.Equal(t, []string{AgentMediaTypeVideo}, byID["video-custom"].MediaTypes)
	// 国产供应商按 OpenAI 兼容 chat completions 调用。
	require.Equal(t, []string{AgentInterfaceOpenAIChatCompletions}, byID["deepseek-v4-pro"].Interfaces)
	require.Equal(t, []string{PlatformDeepseek}, byID["deepseek-v4-pro"].Platforms)
	require.Equal(t, []string{PlatformKimi}, byID["kimi-k3"].Platforms)
	require.Equal(t, []string{PlatformZhipu}, byID["glm-5"].Platforms)
	require.Equal(t, []string{PlatformMiniMax}, byID["MiniMax-M3"].Platforms)
	// grok 同时支持 responses 与 chat completions。
	require.ElementsMatch(t, []string{
		AgentInterfaceOpenAIResponses, AgentInterfaceOpenAIChatCompletions,
	}, byID["grok-4"].Interfaces)
}

// 目录里不能出现"能配价却永远调不通"的模型：视频只有 video 平台有计费链路，
// 图片端点只对 openai / grok / gemini 开放。
func TestAgentModelCatalogSkipsModelsThePlatformCannotServe(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{
		{ID: 1, Platform: PlatformGrok, Credentials: map[string]any{"model_mapping": map[string]any{
			"grok-4": "grok-4", "grok-imagine-video": "grok-imagine-video", "grok-2-image": "grok-2-image",
		}}},
		{ID: 2, Platform: PlatformZhipu, Credentials: map[string]any{"model_mapping": map[string]any{
			"glm-5": "glm-5", "cogview-4": "cogview-4",
		}}},
	}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	byCode := map[string]AgentGroupModel{}
	for _, model := range config.Models {
		byCode[model.ModelCode] = model
	}
	require.Contains(t, byCode, "grok-4")
	require.Equal(t, AgentMediaTypeImage, byCode["grok-2-image"].MediaType, "grok 有图片端点，可以直接服务")
	require.NotContains(t, byCode, "grok-imagine-video", "grok 视频模型在聚合分组里没有计费链路")
	require.NotContains(t, byCode, "cogview-4", "国产供应商没有图片端点")
}

func TestAgentModelCatalogResolvesModelPlatform(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{
		{ID: 1, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}}},
		{ID: 2, Platform: PlatformDeepseek, Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4-pro": "deepseek-v4-pro"}}},
	}}
	catalogService, models := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	platform, found, err := catalogService.ResolveModelPlatform(context.Background(), 9, "deepseek-v4-pro")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, PlatformDeepseek, platform)

	_, found, err = catalogService.ResolveModelPlatform(context.Background(), 9, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, found)

	_, found, err = catalogService.ResolveModelPlatform(context.Background(), 9, "not-configured")
	require.NoError(t, err)
	require.False(t, found)

	// 停用后不再参与分发（避免把请求派给一个已经关掉的模型）。这里走服务层写入，
	// 顺带验证写入会失效平台缓存，而不是让旧映射陈尸 15 秒。
	deepseek := models.models[agentModelKey(PlatformDeepseek, "deepseek-v4-pro")]
	require.NotNil(t, deepseek)
	_, err = catalogService.UpdateModel(context.Background(), 9, deepseek.ID, AgentModelConfigInput{
		MediaType: AgentMediaTypeText, Enabled: false,
	})
	require.NoError(t, err)
	_, found, err = catalogService.ResolveModelPlatform(context.Background(), 9, "deepseek-v4-pro")
	require.NoError(t, err)
	require.False(t, found)
}

func TestAgentModelCatalogDoesNotInventSeedanceModels(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{Platform: PlatformVideo}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	require.Empty(t, config.Models)
}

func TestAgentModelCatalogDiscoversSeedanceFastFromAccountMapping(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform: PlatformVideo,
		Credentials: map[string]any{"model_mapping": map[string]any{
			VideoModelSeedance20Fast: "seedance-upstream-fast",
		}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	catalog, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, []string{VideoModelSeedance20Fast}, agentCatalogIDsForTest(catalog))
	require.Equal(t, []string{AgentMediaTypeVideo}, catalog[0].MediaTypes)
	require.Equal(t, []string{PlatformVideo}, catalog[0].Platforms)
	require.Equal(t, []string{AgentInterfaceSeedanceVideos}, catalog[0].Interfaces)
}

func TestAgentModelCatalogExpandsLanguageAndImageWildcardsAgainstProviderDefaults(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-*": "upstream-image"}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	catalog, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-image-1", "gpt-image-1.5", "gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst"}, agentCatalogIDsForTest(catalog))
}

func TestAgentModelCatalogPreservesExclusionAcrossSync(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}}}
	catalogService, models := newAgentCatalogForTest(accounts)
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, config.Models, 1)
	_, err = catalogService.ExcludeModel(context.Background(), 9, config.Models[0].ID)
	require.NoError(t, err)
	_, err = catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	visible, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Empty(t, visible)
	all, err := models.ListModels(context.Background(), 9, true)
	require.NoError(t, err)
	require.True(t, all[0].Excluded)
	require.False(t, all[0].Enabled)
}

func TestAgentModelPricingSupportsExplicitZeroModelPrices(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"image-alias": "gpt-image-2"}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	config, err := catalogService.GetConfig(context.Background(), 9)
	require.NoError(t, err)

	zero := 0.0
	_, err = catalogService.UpdateModel(context.Background(), 9, config.Models[0].ID, AgentModelConfigInput{
		MediaType: AgentMediaTypeImage,
		Enabled:   true,
		Prices:    []AgentModelPrice{{Resolution: ImageBillingSize2K, UnitPrice: zero}},
	})
	require.NoError(t, err)
	price, model, err := catalogService.ResolveMediaUnitPrice(
		context.Background(), 9, PlatformOpenAI, AgentMediaTypeImage, ImageBillingSize2K, "image-alias",
	)
	require.NoError(t, err)
	require.Zero(t, price)
	require.Equal(t, "image-alias", model)

	_, _, err = catalogService.ResolveMediaUnitPrice(
		context.Background(), 9, PlatformOpenAI, AgentMediaTypeImage, ImageBillingSize4K, "image-alias",
	)
	require.ErrorIs(t, err, ErrAgentImagePricingUnavailable)
}

func TestAgentModelCatalogIntersectsPersistedModelsWithCurrentAccounts(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformAnthropic,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4-8"}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	accounts.accounts = nil
	catalog, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Empty(t, catalog)
}

func TestAgentModelCatalogPropagatesAccountLookupFailure(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{err: errors.New("scheduler unavailable")}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.ListAvailable(context.Background(), 9)
	require.ErrorContains(t, err, "scheduler unavailable")
}

func agentCatalogIDsForTest(catalog []AgentModelCatalogEntry) []string {
	ids := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestAgentTextModelRateMustBeConfiguredToEnableAModel(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4"}},
	}}}
	catalogService, models := newAgentCatalogForTest(accounts)
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, config.Models, 1)
	modelID := config.Models[0].ID

	// 启用但没给倍率：直接拒绝，避免模型可选却在请求时才失败。
	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeText, Enabled: true,
	})
	require.ErrorContains(t, err, "requires rate_multiplier")

	// 停用则不要求倍率。
	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeText, Enabled: false,
	})
	require.NoError(t, err)

	rate := 0.0
	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeText, Enabled: true, RateMultiplier: &rate,
	})
	require.NoError(t, err, "explicit zero is a valid free multiplier")

	resolved, modelCode, err := catalogService.ResolveTextModelRate(context.Background(), 9, PlatformOpenAI, "gpt-5.4")
	require.NoError(t, err)
	require.Zero(t, resolved)
	require.Equal(t, "gpt-5.4", modelCode)
	require.NotNil(t, models.models[agentModelKey(PlatformOpenAI, "gpt-5.4")].RateMultiplier)
}

func TestAgentMediaModelRejectsTextMultiplierAndRequiresResolutionPrices(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, config.Models, 1)
	modelID := config.Models[0].ID

	rate := 1.5
	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeImage, Enabled: true, RateMultiplier: &rate,
		Prices: []AgentModelPrice{{Resolution: ImageBillingSize1K, UnitPrice: 0.1}},
	})
	require.ErrorContains(t, err, "cannot set rate_multiplier")

	// 启用但一张价都没有：同样在校验期拒绝。
	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeImage, Enabled: true,
	})
	require.ErrorContains(t, err, "requires at least one resolution price")

	_, err = catalogService.UpdateModel(context.Background(), 9, modelID, AgentModelConfigInput{
		MediaType: AgentMediaTypeImage, Enabled: true,
		Prices: []AgentModelPrice{{Resolution: ImageBillingSize1K, UnitPrice: 0.1}},
	})
	require.NoError(t, err)
}

func TestResolveTextModelRateFollowsCandidateOrderAndSkipsUnpricedModels(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform: PlatformAnthropic,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"claude-sonnet-4-6": "claude-sonnet-4-6",
			"claude-opus-4-8":   "claude-opus-4-8",
		}},
	}}}
	catalogService, models := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	setAgentTextModelRate(t, models, 9, PlatformAnthropic, "claude-opus-4-8", 2.5)

	rate, modelCode, err := catalogService.ResolveTextModelRate(
		context.Background(), 9, PlatformAnthropic, "claude-sonnet-4-6", "claude-opus-4-8",
	)
	require.NoError(t, err, "an unpriced candidate must not block a priced one")
	require.InDelta(t, 2.5, rate, 1e-12)
	require.Equal(t, "claude-opus-4-8", modelCode)

	_, _, err = catalogService.ResolveTextModelRate(context.Background(), 9, PlatformAnthropic, "claude-sonnet-4-6")
	require.ErrorIs(t, err, ErrAgentModelRateUnavailable)
}

func TestAgentTextModelRateIsNotResolvedForVideoOrUnknownPlatforms(t *testing.T) {
	catalogService, models := newAgentCatalogForTest(&agentCatalogAccountRepoStub{})
	require.NoError(t, models.SyncDiscovered(context.Background(), 9, []AgentModelDiscovery{
		{Platform: PlatformVideo, ModelCode: "seedance-2.5", MediaType: AgentMediaTypeVideo},
	}, time.Now().UTC()))

	_, _, err := catalogService.ResolveTextModelRate(context.Background(), 9, PlatformVideo, "seedance-2.5")
	require.ErrorIs(t, err, ErrAgentModelRateUnavailable)
}

// 媒体类型识别按"模型族关键词 + 下游/上游两个名字"来判，既不写死版本号，也不被
// 上游别名骗过去：Gemini 图像模型常被映射到 nano-banana-* 这类别名。
func TestAgentModelMediaTypeClassificationUsesFamiliesAndBothNames(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		upstream  string
		want      string
	}{
		{name: "gemini 图片模型（显式名字）", requested: "gemini-3-pro-image", upstream: "gemini-3-pro-image", want: AgentMediaTypeImage},
		{name: "gemini 图片模型 preview", requested: "gemini-3-pro-image-preview", upstream: "gemini-3-pro-image-preview", want: AgentMediaTypeImage},
		{name: "gemini 图片模型 3.1", requested: "gemini-3.1-flash-image-preview", upstream: "gemini-3.1-flash-image-preview", want: AgentMediaTypeImage},
		{name: "上游别名 nano-banana", requested: "gemini-3.1-flash-image-preview", upstream: "nano-banana-pro", want: AgentMediaTypeImage},
		{name: "别名只看上游名也不误判", requested: "nano-banana-pro", upstream: "nano-banana-pro", want: AgentMediaTypeImage},
		{name: "未来版本无需改代码", requested: "gemini-4.2-flash-image-preview", upstream: "gemini-4.2-flash-image-preview", want: AgentMediaTypeImage},
		{name: "seedream 图像族", requested: "seedream-5", upstream: "seedream-5", want: AgentMediaTypeImage},
		{name: "imagen 族", requested: "imagen-5", upstream: "imagen-5", want: AgentMediaTypeImage},
		{name: "gpt-image 族仍是图片", requested: "gpt-image-3", upstream: "gpt-image-3", want: AgentMediaTypeImage},
		{name: "veo 视频族", requested: "veo-4", upstream: "veo-4", want: AgentMediaTypeVideo},
		{name: "grok imagine 视频族", requested: "grok-imagine-video-2", upstream: "grok-imagine-video-2", want: AgentMediaTypeVideo},
		{name: "纯文本模型不受影响", requested: "gpt-5.4", upstream: "gpt-5.4", want: AgentMediaTypeText},
		{name: "claude 文本模型不受影响", requested: "claude-sonnet-4-6", upstream: "claude-sonnet-4-6", want: AgentMediaTypeText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentModelDescriptorForMapping(PlatformGemini, tc.requested, tc.upstream)
			require.Equal(t, tc.want, got.mediaType)
		})
	}
}

// 视频平台的账号只提供视频能力，名字里没有 video 也一律按视频处理。
func TestAgentVideoPlatformAlwaysClassifiesVideo(t *testing.T) {
	require.Equal(t, AgentMediaTypeVideo,
		defaultAgentModelDescriptorForID(PlatformVideo, "some-new-model").mediaType)
}

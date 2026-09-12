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
	rates  map[string]AgentPlatformRate
}

func newAgentModelMemoryRepo() *agentModelMemoryRepo {
	return &agentModelMemoryRepo{nextID: 1, models: map[string]*AgentGroupModel{}, rates: map[string]AgentPlatformRate{}}
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

func (r *agentModelMemoryRepo) UpdateModelConfig(_ context.Context, groupID, modelID int64, mediaType string, enabled bool, prices []AgentModelPrice) error {
	for _, model := range r.models {
		if model.GroupID == groupID && model.ID == modelID && !model.Excluded {
			model.MediaType = mediaType
			model.Enabled = enabled
			model.Prices = append([]AgentModelPrice(nil), prices...)
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

func (r *agentModelMemoryRepo) ListPlatformRates(_ context.Context, groupID int64) ([]AgentPlatformRate, error) {
	rates := make([]AgentPlatformRate, 0)
	for _, rate := range r.rates {
		if rate.GroupID == groupID {
			rates = append(rates, rate)
		}
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].Platform < rates[j].Platform })
	return rates, nil
}

func (r *agentModelMemoryRepo) UpsertPlatformRate(_ context.Context, groupID int64, platform string, multiplier float64) error {
	r.rates[platform] = AgentPlatformRate{GroupID: groupID, Platform: platform, RateMultiplier: multiplier}
	return nil
}

func (r *agentModelMemoryRepo) GetPlatformRate(_ context.Context, groupID int64, platform string) (*AgentPlatformRate, error) {
	rate, ok := r.rates[platform]
	if !ok || rate.GroupID != groupID {
		return nil, sql.ErrNoRows
	}
	return &rate, nil
}

func cloneAgentModelForTest(model *AgentGroupModel) AgentGroupModel {
	copy := *model
	copy.Prices = append([]AgentModelPrice(nil), model.Prices...)
	return copy
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
	}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)

	catalog, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, []string{
		"claude-opus-4-8", "embedding-alias", "gemini-2.5-flash", "gemini-image-alias",
		"gpt-5.4", "image-alias", "video-custom",
	}, agentCatalogIDsForTest(catalog))
	require.Equal(t, []string{AgentMediaTypeText}, catalog[1].MediaTypes)
	require.Equal(t, []string{AgentInterfaceOpenAIEmbeddings}, catalog[1].Interfaces)
	require.Equal(t, []string{AgentMediaTypeVideo}, catalog[6].MediaTypes)
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

func TestAgentModelPricingSupportsPlatformAndExplicitZeroModelPrices(t *testing.T) {
	accounts := &agentCatalogAccountRepoStub{accounts: []Account{{
		Platform:    PlatformOpenAI,
		Credentials: map[string]any{"model_mapping": map[string]any{"image-alias": "gpt-image-2"}},
	}}}
	catalogService, _ := newAgentCatalogForTest(accounts)
	_, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	config, err := catalogService.SetPlatformRate(context.Background(), 9, PlatformOpenAI, 0)
	require.NoError(t, err)
	require.Zero(t, config.PlatformRates[0].RateMultiplier)

	_, err = catalogService.UpdateModel(context.Background(), 9, config.Models[0].ID, AgentModelConfigInput{
		MediaType: AgentMediaTypeImage,
		Enabled:   true,
		Prices:    []AgentModelPrice{{Resolution: ImageBillingSize2K, UnitPrice: 0}},
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

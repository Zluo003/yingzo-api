package handler

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// These small test doubles are shared by Agent handler tests. They mirror the
// repository contract used by the upstream-first catalog service without
// pulling the full database repository into unit tests.
type gatewayAgentModelRepoStub struct {
	models []service.AgentGroupModel
	rates  []service.AgentPlatformRate
}

func (r *gatewayAgentModelRepoStub) SyncDiscovered(context.Context, int64, []service.AgentModelDiscovery, time.Time) error {
	return nil
}
func (r *gatewayAgentModelRepoStub) ListModels(context.Context, int64, bool) ([]service.AgentGroupModel, error) {
	return append([]service.AgentGroupModel(nil), r.models...), nil
}
func (r *gatewayAgentModelRepoStub) GetModelByID(context.Context, int64, int64) (*service.AgentGroupModel, error) {
	return nil, sql.ErrNoRows
}
func (r *gatewayAgentModelRepoStub) GetEnabledModel(_ context.Context, groupID int64, platform, modelCode string) (*service.AgentGroupModel, error) {
	for _, model := range r.models {
		if model.GroupID == groupID && model.Platform == platform && model.ModelCode == modelCode && model.Enabled && model.Available && !model.Excluded {
			copy := model
			copy.Prices = append([]service.AgentModelPrice(nil), model.Prices...)
			return &copy, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (r *gatewayAgentModelRepoStub) UpdateModelConfig(context.Context, int64, int64, string, bool, []service.AgentModelPrice) error {
	return nil
}
func (r *gatewayAgentModelRepoStub) ExcludeModel(context.Context, int64, int64, time.Time) error {
	return nil
}
func (r *gatewayAgentModelRepoStub) ListPlatformRates(context.Context, int64) ([]service.AgentPlatformRate, error) {
	return append([]service.AgentPlatformRate(nil), r.rates...), nil
}
func (r *gatewayAgentModelRepoStub) UpsertPlatformRate(context.Context, int64, string, float64) error {
	return nil
}
func (r *gatewayAgentModelRepoStub) GetPlatformRate(context.Context, int64, string) (*service.AgentPlatformRate, error) {
	return nil, sql.ErrNoRows
}

func enabledGatewayAgentModel(platform, modelCode, mediaType string) service.AgentGroupModel {
	return service.AgentGroupModel{
		Platform: platform, ModelCode: modelCode, MediaType: mediaType,
		Enabled: true, Available: true, Prices: []service.AgentModelPrice{},
	}
}

func newGatewayAgentCatalogForTest(repo service.AccountRepository, models ...service.AgentGroupModel) *service.AgentModelCatalogService {
	return service.NewAgentModelCatalogService(repo, nil, &gatewayAgentModelRepoStub{models: models})
}

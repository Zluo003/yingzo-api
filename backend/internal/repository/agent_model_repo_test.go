package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newAgentModelRepositoryMock(t *testing.T) (*agentModelRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &agentModelRepository{db: db}, mock
}

func TestAgentModelRepositorySyncPreservesManualConfigurationAndExclusion(t *testing.T) {
	repo, mock := newAgentModelRepositoryMock(t)
	groupID := int64(5)
	seenAt := time.Date(2026, 7, 24, 1, 2, 3, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE agent_group_models\s+SET available = FALSE, updated_at = \$2\s+WHERE group_id = \$1 AND excluded = FALSE`).
		WithArgs(groupID, seenAt).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`(?s)INSERT INTO agent_group_models .*ON CONFLICT \(group_id, platform, model_code\) DO UPDATE\s+SET available = CASE WHEN agent_group_models.excluded THEN FALSE ELSE TRUE END,\s+last_seen_at = EXCLUDED.last_seen_at,\s+updated_at = EXCLUDED.updated_at`).
		WithArgs(groupID, service.PlatformOpenAI, "gpt-image-custom", service.AgentMediaTypeImage, seenAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.SyncDiscovered(context.Background(), groupID, []service.AgentModelDiscovery{{
		Platform: service.PlatformOpenAI, ModelCode: "gpt-image-custom", MediaType: service.AgentMediaTypeImage,
	}}, seenAt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentModelRepositoryUpdateReplacesPricesInOneTransaction(t *testing.T) {
	repo, mock := newAgentModelRepositoryMock(t)
	groupID := int64(5)
	modelID := int64(17)

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE agent_group_models\s+SET media_type = \$3, enabled = \$4, updated_at = NOW\(\)\s+WHERE group_id = \$1 AND id = \$2 AND excluded = FALSE`).
		WithArgs(groupID, modelID, service.AgentMediaTypeImage, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM agent_model_prices WHERE agent_model_id = \$1`).
		WithArgs(modelID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`(?s)INSERT INTO agent_model_prices .*VALUES \(\$1, \$2, \$3, \$4\)`).
		WithArgs(modelID, service.ImageBillingSize1K, service.AgentBillingUnitImage, 0.0).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)INSERT INTO agent_model_prices .*VALUES \(\$1, \$2, \$3, \$4\)`).
		WithArgs(modelID, service.ImageBillingSize2K, service.AgentBillingUnitImage, 0.25).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	err := repo.UpdateModelConfig(context.Background(), groupID, modelID, service.AgentMediaTypeImage, true, []service.AgentModelPrice{
		{Resolution: service.ImageBillingSize1K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0},
		{Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.25},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentModelRepositoryExcludeDisablesModelAndDeletesPricesAtomically(t *testing.T) {
	repo, mock := newAgentModelRepositoryMock(t)
	groupID := int64(5)
	modelID := int64(19)
	excludedAt := time.Date(2026, 7, 24, 4, 5, 6, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE agent_group_models\s+SET enabled = FALSE, available = FALSE, excluded = TRUE,\s+excluded_at = \$3, updated_at = \$3\s+WHERE group_id = \$1 AND id = \$2 AND excluded = FALSE`).
		WithArgs(groupID, modelID, excludedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM agent_model_prices WHERE agent_model_id = \$1`).
		WithArgs(modelID).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.ExcludeModel(context.Background(), groupID, modelID, excludedAt))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentModelRepositoryReadsExplicitZeroPrice(t *testing.T) {
	repo, mock := newAgentModelRepositoryMock(t)
	groupID := int64(5)
	modelID := int64(23)
	now := time.Date(2026, 7, 24, 7, 8, 9, 0, time.UTC)

	modelColumns := []string{
		"id", "group_id", "platform", "model_code", "media_type", "enabled", "available",
		"excluded", "excluded_at", "discovered_at", "last_seen_at", "created_at", "updated_at",
	}
	mock.ExpectQuery(`(?s)SELECT id, group_id, platform, model_code, media_type, enabled, available,.*FROM agent_group_models.*WHERE group_id = \$1 AND platform = \$2 AND model_code = \$3`).
		WithArgs(groupID, service.PlatformOpenAI, "gpt-image-custom").
		WillReturnRows(sqlmock.NewRows(modelColumns).AddRow(
			modelID, groupID, service.PlatformOpenAI, "gpt-image-custom", service.AgentMediaTypeImage,
			true, true, false, nil, now, now, now, now,
		))
	mock.ExpectQuery(`(?s)SELECT id, agent_model_id, resolution, billing_unit, unit_price, created_at, updated_at.*FROM agent_model_prices.*WHERE agent_model_id = \$1`).
		WithArgs(modelID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "agent_model_id", "resolution", "billing_unit", "unit_price", "created_at", "updated_at",
		}).AddRow(int64(31), modelID, service.ImageBillingSize2K, service.AgentBillingUnitImage, 0.0, now, now))

	model, err := repo.GetEnabledModel(context.Background(), groupID, service.PlatformOpenAI, "gpt-image-custom")
	require.NoError(t, err)
	require.Len(t, model.Prices, 1)
	require.Zero(t, model.Prices[0].UnitPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}

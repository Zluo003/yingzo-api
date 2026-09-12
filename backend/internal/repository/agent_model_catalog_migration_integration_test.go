//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAgentModelCatalogMigrationCreatesStrictPricingSchema(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "agent_catalog_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE groups (id BIGINT PRIMARY KEY)`)
	require.NoError(t, err)

	content, err := migrations.FS.ReadFile("177_agent_model_catalog_and_pricing.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err, "migration must be idempotent")

	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups(id) VALUES (1);
		INSERT INTO agent_platform_rates(group_id, platform, rate_multiplier)
		VALUES (1, 'openai', 1.25);
		INSERT INTO agent_group_models(group_id, platform, model_code, media_type)
		VALUES (1, 'openai', 'gpt-image-custom', 'image');
		INSERT INTO agent_model_prices(agent_model_id, resolution, billing_unit, unit_price)
		SELECT id, '2K', 'image', 0 FROM agent_group_models;
	`)
	require.NoError(t, err, "explicit zero is a valid configured price")

	_, err = tx.ExecContext(ctx, `INSERT INTO agent_platform_rates(group_id, platform, rate_multiplier) VALUES (1, 'seedance', 1)`)
	require.Error(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_model_prices(agent_model_id, resolution, billing_unit, unit_price) SELECT id, '4K', 'image', -1 FROM agent_group_models`)
	require.Error(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE agent_group_models SET excluded = TRUE, enabled = TRUE, excluded_at = NOW()`)
	require.Error(t, err)
}

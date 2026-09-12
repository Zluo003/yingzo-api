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

// 242 把文本倍率从平台级下沉到逐模型，并删除 agent_platform_rates。
// 关键不变量：回填必须保住已配置的平台倍率，媒体模型不能被写上文本倍率，
// 且迁移可重复执行。
func TestAgentTextModelMultiplierMigrationBackfillsAndDropsPlatformRates(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "agent_text_rate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE groups (id BIGINT PRIMARY KEY)`)
	require.NoError(t, err)

	catalog, err := migrations.FS.ReadFile("177_agent_model_catalog_and_pricing.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(catalog))
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups(id) VALUES (1);
		INSERT INTO agent_platform_rates(group_id, platform, rate_multiplier) VALUES (1, 'openai', 1.25);
		INSERT INTO agent_group_models(group_id, platform, model_code, media_type) VALUES
			(1, 'openai', 'gpt-text', 'text'),
			(1, 'openai', 'gpt-image-custom', 'image');
	`)
	require.NoError(t, err)

	multiplier, err := migrations.FS.ReadFile("242_agent_model_text_multiplier.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(multiplier))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(multiplier))
	require.NoError(t, err, "migration must be idempotent")

	var textRate, imageRate *float64
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT rate_multiplier FROM agent_group_models WHERE model_code = 'gpt-text'`,
	).Scan(&textRate))
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT rate_multiplier FROM agent_group_models WHERE model_code = 'gpt-image-custom'`,
	).Scan(&imageRate))
	require.NotNil(t, textRate, "existing platform rate must be carried over to every text model")
	require.InDelta(t, 1.25, *textRate, 1e-9)
	require.Nil(t, imageRate, "media models are priced per resolution, never by a text multiplier")

	var platformRatesTable *string
	require.NoError(t, tx.QueryRowContext(ctx,
		`SELECT to_regclass('agent_platform_rates')::text`,
	).Scan(&platformRatesTable))
	require.Nil(t, platformRatesTable, "the per-platform table is superseded by the per-model column")

	// NULL 表示未配置；0 是合法的"免费"倍率；负数必须被拒绝。
	_, err = tx.ExecContext(ctx, `UPDATE agent_group_models SET rate_multiplier = 0 WHERE model_code = 'gpt-text'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE agent_group_models SET rate_multiplier = -1 WHERE model_code = 'gpt-text'`)
	require.Error(t, err)
}

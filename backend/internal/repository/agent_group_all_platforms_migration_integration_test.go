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

// 243 把 agent_group_models.platform 放宽到应用支持的全部 provider。
// 关键不变量：grok / 国产供应商必须可写入（否则绑定这些账号后同步直接失败），
// 未知平台仍要拒绝。
func TestAgentGroupAllPlatformsMigrationAcceptsEverySupportedProvider(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "agent_platforms_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE groups (id BIGINT PRIMARY KEY)`)
	require.NoError(t, err)

	// 177 的建表语句里 platform 只允许 openai/anthropic/gemini/seedance，
	// 243 必须在它之后把约束替换成完整清单。
	catalog, err := migrations.FS.ReadFile("177_agent_model_catalog_and_pricing.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(catalog))
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `INSERT INTO groups(id) VALUES (1)`)
	require.NoError(t, err)

	// 预期失败的语句会中止事务，用 SAVEPOINT 隔开。
	expectInsertRejected := func(platform, code string, wantMsg string) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `SAVEPOINT probe`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_group_models(group_id, platform, model_code, media_type)
			VALUES (1, $1, $2, 'text')
		`, platform, code)
		require.Error(t, err, wantMsg)
		_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT probe`)
		require.NoError(t, err)
	}
	expectInsertRejected("deepseek", "deepseek-v4-pro", "177 的旧约束理应拒绝 deepseek")

	allPlatforms, err := migrations.FS.ReadFile("243_agent_group_all_platforms.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(allPlatforms))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(allPlatforms))
	require.NoError(t, err, "migration must be idempotent")

	for _, platform := range []string{"openai", "anthropic", "gemini", "grok", "kimi", "zhipu", "deepseek", "minimax", "video"} {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_group_models(group_id, platform, model_code, media_type)
			VALUES (1, $1, $2, 'text')
		`, platform, "model-"+platform)
		require.NoError(t, err, "platform %s must be accepted", platform)
	}

	expectInsertRejected("unknown-provider", "whatever", "unknown platforms must stay rejected")
}

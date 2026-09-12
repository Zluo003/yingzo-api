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

func TestYingzoDistributionRemovalMigrationDropsOnlyRetiredState(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "yingzo_remove_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE api_keys (id BIGINT PRIMARY KEY, status TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE agent_installations (api_key_id BIGINT, system_code TEXT NOT NULL);
		CREATE TABLE agent_device_authorizations (id UUID PRIMARY KEY);
		CREATE TABLE yingzo_releases (id UUID PRIMARY KEY);
		CREATE TABLE yingzo_release_artifacts (id UUID PRIMARY KEY);
		CREATE TABLE yingzo_release_upload_sessions (id UUID PRIMARY KEY);
		CREATE TABLE temporary_assets (id UUID PRIMARY KEY);
		CREATE TABLE agent_model_pricing (id BIGINT PRIMARY KEY);
		CREATE TABLE groups (
			id BIGINT PRIMARY KEY,
			kind TEXT NOT NULL,
			system_code TEXT,
			is_exclusive BOOLEAN NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		);
	`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_keys(id,status) VALUES (1,'active'),(2,'active');
		INSERT INTO agent_installations(api_key_id,system_code) VALUES (1,'yingzo');
		INSERT INTO settings(key,value) VALUES ('yingzo_public_origin','https://example.test'),('other','kept');
		INSERT INTO groups(id,kind,system_code,is_exclusive) VALUES
			(1,'agent','yingzo',true),
			(2,'standard',NULL,true);
	`)
	require.NoError(t, err)

	content, err := migrations.FS.ReadFile("175_remove_yingzo_distribution.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err, "the removal migration must remain idempotent")

	var status string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT status FROM api_keys WHERE id=1`).Scan(&status))
	require.Equal(t, "disabled", status)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT status FROM api_keys WHERE id=2`).Scan(&status))
	require.Equal(t, "active", status)

	for _, table := range []string{
		"yingzo_release_upload_sessions",
		"yingzo_release_artifacts",
		"yingzo_releases",
		"agent_device_authorizations",
		"agent_installations",
	} {
		var exists bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		require.False(t, exists, table)
	}
	for _, table := range []string{"temporary_assets", "agent_model_pricing"} {
		var exists bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		require.True(t, exists, table)
	}
	var settingCount int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key='yingzo_public_origin'`).Scan(&settingCount))
	require.Zero(t, settingCount)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key='other'`).Scan(&settingCount))
	require.Equal(t, 1, settingCount)
	var agentExclusive, standardExclusive bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT is_exclusive FROM groups WHERE id=1`).Scan(&agentExclusive))
	require.False(t, agentExclusive)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT is_exclusive FROM groups WHERE id=2`).Scan(&standardExclusive))
	require.True(t, standardExclusive)
}

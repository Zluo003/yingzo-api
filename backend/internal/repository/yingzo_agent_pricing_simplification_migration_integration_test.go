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

func TestYingzoAgentPricingSimplificationMigrationKeepsManualChannels(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "yingzo_pricing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE agent_model_pricing (id BIGINT PRIMARY KEY);
		CREATE TABLE channels (
			id BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL
		);
		CREATE TABLE channel_groups (channel_id BIGINT NOT NULL, group_id BIGINT NOT NULL);
		CREATE TABLE groups (
			id BIGINT PRIMARY KEY,
			kind TEXT NOT NULL,
			system_code TEXT,
			image_rate_independent BOOLEAN NOT NULL,
			image_rate_multiplier NUMERIC NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		);
	`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO channels(id,name,description) VALUES
			(1,'System Agent Pricing #42','System-managed Agent model pricing'),
			(2,'System Agent Pricing #42','manually managed'),
			(3,'Manual Agent Pricing','System-managed Agent model pricing'),
			(4,'Yingzo Agent Pricing #42','System-managed Yingzo Agent model pricing'),
			(5,'Yingzo Agent Pricing #42','manually managed'),
			(6,'Manual Yingzo Pricing','System-managed Yingzo Agent model pricing');
		INSERT INTO channel_groups(channel_id,group_id) VALUES
			(1,1),(2,1),(3,1),(4,1),(5,1),(6,1);
		INSERT INTO groups(id,kind,system_code,image_rate_independent,image_rate_multiplier) VALUES
			(1,'agent','yingzo',false,9),
			(2,'standard',NULL,false,7);
	`)
	require.NoError(t, err)

	content, err := migrations.FS.ReadFile("176_simplify_yingzo_agent_pricing.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(content))
	require.NoError(t, err, "the pricing simplification migration must remain idempotent")

	var exists bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT to_regclass('agent_model_pricing') IS NOT NULL`).Scan(&exists))
	require.False(t, exists)

	var channelCount, bindingCount int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE id IN (1,4)`).Scan(&channelCount))
	require.Zero(t, channelCount)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE channel_id IN (1,4)`).Scan(&bindingCount))
	require.Zero(t, bindingCount)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE id IN (2,3,5,6)`).Scan(&channelCount))
	require.Equal(t, 4, channelCount)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_groups WHERE channel_id IN (2,3,5,6)`).Scan(&bindingCount))
	require.Equal(t, 4, bindingCount)

	var independent bool
	var multiplier float64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT image_rate_independent,image_rate_multiplier FROM groups WHERE id=1`).Scan(&independent, &multiplier))
	require.True(t, independent)
	require.Equal(t, 1.0, multiplier)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT image_rate_independent,image_rate_multiplier FROM groups WHERE id=2`).Scan(&independent, &multiplier))
	require.False(t, independent)
	require.Equal(t, 7.0, multiplier)
}

//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestVideoChannelsMigrationPreservesLegacyPricesAndAllowsSeparateChannels(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	schema := "video_channels_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
        CREATE TABLE agent_model_prices (agent_model_id BIGINT, resolution TEXT, unit_price NUMERIC, UNIQUE(agent_model_id,resolution));
        CREATE TABLE video_group_pricing_rules (group_id BIGINT, model_code TEXT, resolution TEXT, credits_per_second NUMERIC,
            CONSTRAINT video_group_pricing_rules_model_resolution_unique UNIQUE(group_id,model_code,resolution));
        CREATE TABLE video_tasks (id BIGINT, cost_per_second NUMERIC, total_cost NUMERIC);
        INSERT INTO agent_model_prices VALUES (1,'720p',0),(2,'720p',0.8);
        INSERT INTO video_group_pricing_rules VALUES (1,'seedance-2.0','720p',0.8);
        INSERT INTO video_tasks VALUES (1,0.8,8);
    `)
	require.NoError(t, err)
	sql, err := migrations.FS.ReadFile("266_seedance_video_channels.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(sql))
		require.NoError(t, err)
	}
	var price, total float64
	var channel, unit string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT video_channel_id,unit_price FROM agent_model_prices WHERE agent_model_id=1`).Scan(&channel, &price))
	require.Empty(t, channel)
	require.Zero(t, price)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT video_channel_id,billing_unit,unit_price,total_cost FROM video_tasks WHERE id=1`).Scan(&channel, &unit, &price, &total))
	require.Empty(t, channel)
	require.Equal(t, "second", unit)
	require.Equal(t, 0.8, price)
	require.Equal(t, 8.0, total)
	_, err = tx.ExecContext(ctx, `
        INSERT INTO video_channels(id,name) VALUES ('other','Other');
        INSERT INTO agent_model_prices(agent_model_id,resolution,unit_price,video_channel_id) VALUES (1,'720p',4,'other');
        INSERT INTO video_group_pricing_rules(group_id,model_code,resolution,credits_per_second,video_channel_id,billing_unit,credits_per_request) VALUES (1,'seedance-2.0','720p',0,'other','request',4);
        UPDATE video_channels SET name='Standard' WHERE id='';
    `)
	require.NoError(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_model_prices WHERE agent_model_id=1`).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM video_group_pricing_rules WHERE group_id=1`).Scan(&count))
	require.Equal(t, 2, count)
}

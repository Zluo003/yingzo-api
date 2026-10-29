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

func TestMidjourneyDiscordMigration(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	schema := "mj_discord_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
 CREATE TABLE accounts(id BIGINT,extra JSONB,status TEXT,schedulable BOOLEAN,error_message TEXT,updated_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ);
 CREATE TABLE agent_group_models(id BIGINT,model_code TEXT);
 CREATE TABLE agent_model_prices(id BIGINT,agent_model_id BIGINT,resolution TEXT,unit_price NUMERIC,enabled BOOLEAN,updated_at TIMESTAMPTZ);
 CREATE TABLE channel_model_pricing(id BIGINT,models JSONB,updated_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT,model_pricing JSONB,updated_at TIMESTAMPTZ);
 CREATE TABLE usage_logs(image_count INT,billing_mode TEXT,video_count INT,model TEXT,image_size TEXT);
 INSERT INTO accounts VALUES(1,'{"image_provider":"apimart_midjourney"}','active',true,'',NOW(),NULL),(2,'{"image_provider":"other"}','active',true,'',NOW(),NULL);
 INSERT INTO agent_group_models VALUES(1,'midjourney-v8.2');
 INSERT INTO agent_model_prices VALUES(1,1,'generation',0.7,true,NOW()),(2,1,'upscale',0.1,true,NOW());
 INSERT INTO channel_model_pricing VALUES(1,'["midjourney-v8.2:upscale","other"]',NOW());
 INSERT INTO groups VALUES(1,'[{"models":["midjourney-v8.2:upscale"],"per_request_price":0.1}]',NOW());
 `)
	require.NoError(t, err)
	raw, err := migrations.FS.ReadFile("271_midjourney_discord.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(raw))
	require.NoError(t, err)
	var status, resolution, models string
	var schedulable bool
	var price float64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT status,schedulable FROM accounts WHERE id=1").Scan(&status, &schedulable))
	require.Equal(t, "disabled", status)
	require.False(t, schedulable)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT schedulable FROM accounts WHERE id=2").Scan(&schedulable))
	require.True(t, schedulable)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT resolution,unit_price FROM agent_model_prices WHERE id=2").Scan(&resolution, &price))
	require.Equal(t, "select", resolution)
	require.Equal(t, .1, price)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT models::text FROM channel_model_pricing WHERE id=1").Scan(&models))
	require.JSONEq(t, `["midjourney-v8.2:select","other"]`, models)
	_, err = tx.ExecContext(ctx, "INSERT INTO usage_logs VALUES(1,'per_request',0,'midjourney-v8.2:select',NULL)")
	require.NoError(t, err)
}

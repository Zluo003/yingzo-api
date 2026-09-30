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

func TestMidjourneyFastPricingMigration(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	schema := "mj_pricing_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema+`; SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
CREATE TABLE agent_group_models (id BIGINT PRIMARY KEY, model_code TEXT);
CREATE TABLE agent_model_prices (id BIGINT PRIMARY KEY, agent_model_id BIGINT, resolution TEXT, billing_unit TEXT, unit_price NUMERIC, enabled BOOLEAN, updated_at TIMESTAMPTZ, UNIQUE(agent_model_id,resolution));
CREATE TABLE groups (id BIGINT PRIMARY KEY, model_pricing JSONB, updated_at TIMESTAMPTZ);
CREATE TABLE channel_model_pricing (id BIGINT PRIMARY KEY, models JSONB, per_request_price NUMERIC, updated_at TIMESTAMPTZ);
CREATE TABLE usage_logs (image_count INT, billing_mode TEXT, video_count INT, model TEXT, image_size TEXT);
INSERT INTO agent_group_models VALUES (1,'midjourney-v8.2'),(2,'another-image'),(3,'midjourney-v8.2');
INSERT INTO agent_model_prices VALUES
 (1,1,'imagine_relax','request',0.1,true,NOW()), (2,1,'imagine_fast','request',0.7,false,NOW()),
 (3,1,'imagine_turbo','request',0.9,true,NOW()), (4,1,'upscale_fast','request',0,true,NOW()),
 (5,2,'imagine_fast','image',0.4,true,NOW()),
 (6,3,'generation','request',0.8,true,NOW()), (7,3,'imagine_fast','request',0.2,true,NOW());
INSERT INTO groups VALUES (1,'[
 {"models":["midjourney-v8.2:imagine_fast"],"billing_mode":"per_request","per_request_price":0.7},
 {"models":["midjourney-v8.2:imagine_turbo"]},
 {"models":["another-model","midjourney-v8.2:imagine_relax"],"per_request_price":0.3},
 {"models":["midjourney-v8.2:upscale_fast"],"per_request_price":0},
 {"custom":"preserve"}
]',NOW());
INSERT INTO channel_model_pricing VALUES
 (1,'["midjourney-v8.2:imagine_fast"]',0.7,NOW()),
 (2,'["midjourney-v8.2:imagine_turbo"]',0.9,NOW()),
 (3,'["another-model","midjourney-v8.2:imagine_relax"]',0.3,NOW()),
 (4,'["midjourney-v8.2:upscale_fast"]',0,NOW());
`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("261_midjourney_fast_generation_pricing.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	var prices, group, channel string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT jsonb_agg(jsonb_build_array(id,resolution,unit_price,enabled) ORDER BY id)::text FROM agent_model_prices`).Scan(&prices))
	require.JSONEq(t, `[[2,"generation",0.7,false],[4,"upscale",0,true],[5,"imagine_fast",0.4,true],[6,"generation",0.8,true]]`, prices)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT model_pricing::text FROM groups WHERE id=1`).Scan(&group))
	require.JSONEq(t, `[{"models":["midjourney-v8.2:generation"],"billing_mode":"per_request","per_request_price":0.7},{"models":["another-model"],"per_request_price":0.3},{"models":["midjourney-v8.2:upscale"],"per_request_price":0},{"custom":"preserve"}]`, group)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT jsonb_agg(jsonb_build_array(id,models,per_request_price) ORDER BY id)::text FROM channel_model_pricing`).Scan(&channel))
	require.JSONEq(t, `[[1,["midjourney-v8.2:generation"],0.7],[3,["another-model"],0.3],[4,["midjourney-v8.2:upscale"],0]]`, channel)
}

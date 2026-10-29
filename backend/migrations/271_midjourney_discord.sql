-- APIMart Midjourney is retired. Do not silently reinterpret an API key as a
-- Discord user token. Operators must explicitly configure each Discord account.
UPDATE accounts
SET schedulable = FALSE, status = 'disabled',
    error_message = 'APIMart Midjourney retired: configure Discord Token, Server ID and Channel ID in the MJ account panel',
    updated_at = NOW()
WHERE extra->>'image_provider' = 'apimart_midjourney' AND deleted_at IS NULL;

-- The old "upscale" meant U1-U4 (selection), not an HD upscale. Preserve that
-- price under select; true HD upscale must be explicitly priced and enabled.
UPDATE agent_model_prices p SET resolution = 'select', updated_at = NOW()
FROM agent_group_models m
WHERE m.id = p.agent_model_id AND m.model_code = 'midjourney-v8.2'
 AND p.resolution = 'upscale'
 AND NOT EXISTS (SELECT 1 FROM agent_model_prices x WHERE x.agent_model_id=p.agent_model_id AND x.resolution='select');
DELETE FROM agent_model_prices p USING agent_group_models m
WHERE m.id=p.agent_model_id AND m.model_code='midjourney-v8.2' AND p.resolution='upscale';

CREATE OR REPLACE FUNCTION pg_temp.mj_discord_keys(keys JSONB) RETURNS JSONB
LANGUAGE SQL IMMUTABLE AS $$
 SELECT COALESCE(jsonb_agg(CASE value WHEN '"midjourney-v8.2:upscale"'::jsonb
 THEN '"midjourney-v8.2:select"'::jsonb ELSE value END ORDER BY ord), '[]'::jsonb)
 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(keys)='array' THEN keys ELSE '[]'::jsonb END) WITH ORDINALITY AS items(value,ord)
$$;
UPDATE channel_model_pricing SET models=pg_temp.mj_discord_keys(models),updated_at=NOW()
WHERE models ? 'midjourney-v8.2:upscale';
UPDATE groups g SET model_pricing=(SELECT jsonb_agg(CASE WHEN entry->'models' ? 'midjourney-v8.2:upscale'
 THEN jsonb_set(entry,'{models}',pg_temp.mj_discord_keys(entry->'models')) ELSE entry END ORDER BY ord)
 FROM jsonb_array_elements(g.model_pricing) WITH ORDINALITY AS items(entry,ord)),updated_at=NOW()
WHERE jsonb_typeof(g.model_pricing)='array' AND g.model_pricing::text LIKE '%midjourney-v8.2:upscale%';
DROP FUNCTION pg_temp.mj_discord_keys(JSONB);

ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;
ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_image_billing_size_check CHECK (
 image_count <= 0 OR billing_mode='video' OR COALESCE(video_count,0)>0
 OR (billing_mode='per_request' AND model IN (
 'midjourney-v8.2:generation','midjourney-v8.2:select','midjourney-v8.2:upscale',
 'midjourney-v8.2:imagine_relax','midjourney-v8.2:imagine_fast','midjourney-v8.2:imagine_turbo','midjourney-v8.2:upscale_fast'))
 OR (image_size IS NOT NULL AND image_size IN ('1K','2K','4K','mixed'))
) NOT VALID;

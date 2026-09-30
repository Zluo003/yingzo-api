-- Keep the existing Fast prices and enabled flags; generation covers both
-- imagine and edits. Never rewrite historical task quotes or usage records.
UPDATE agent_model_prices p
SET resolution = CASE p.resolution WHEN 'imagine_fast' THEN 'generation' ELSE 'upscale' END,
    billing_unit = 'request', updated_at = NOW()
FROM agent_group_models m
WHERE m.id = p.agent_model_id AND m.model_code = 'midjourney-v8.2'
  AND p.resolution IN ('imagine_fast', 'upscale_fast')
  AND NOT EXISTS (
      SELECT 1 FROM agent_model_prices existing
      WHERE existing.agent_model_id = p.agent_model_id
        AND existing.resolution = CASE p.resolution WHEN 'imagine_fast' THEN 'generation' ELSE 'upscale' END
  );

DELETE FROM agent_model_prices p USING agent_group_models m
WHERE m.id = p.agent_model_id AND m.model_code = 'midjourney-v8.2'
  AND p.resolution IN ('imagine_relax', 'imagine_fast', 'imagine_turbo', 'upscale_fast');

-- Standard groups and channels store model-key lists. Preserve every unrelated
-- model and all price attributes, removing only the retired speed-specific keys.
CREATE OR REPLACE FUNCTION pg_temp.mj_fast_model_keys(keys JSONB) RETURNS JSONB
LANGUAGE SQL IMMUTABLE AS $$
    SELECT COALESCE(jsonb_agg(
        CASE value
            WHEN '"midjourney-v8.2:imagine_fast"'::jsonb THEN '"midjourney-v8.2:generation"'::jsonb
            WHEN '"midjourney-v8.2:upscale_fast"'::jsonb THEN '"midjourney-v8.2:upscale"'::jsonb
            ELSE value
        END ORDER BY ord
    ), '[]'::jsonb)
    FROM jsonb_array_elements(CASE WHEN jsonb_typeof(keys) = 'array' THEN keys ELSE '[]'::jsonb END)
         WITH ORDINALITY AS items(value, ord)
    WHERE value NOT IN ('"midjourney-v8.2:imagine_relax"'::jsonb, '"midjourney-v8.2:imagine_turbo"'::jsonb)
$$;

UPDATE channel_model_pricing
SET models = pg_temp.mj_fast_model_keys(models), updated_at = NOW()
WHERE models ?| ARRAY['midjourney-v8.2:imagine_relax', 'midjourney-v8.2:imagine_fast', 'midjourney-v8.2:imagine_turbo', 'midjourney-v8.2:upscale_fast']
  AND pg_temp.mj_fast_model_keys(models) <> '[]'::jsonb;

DELETE FROM channel_model_pricing
WHERE models ?| ARRAY['midjourney-v8.2:imagine_relax', 'midjourney-v8.2:imagine_turbo']
  AND pg_temp.mj_fast_model_keys(models) = '[]'::jsonb;

UPDATE groups g
SET model_pricing = (
    SELECT COALESCE(jsonb_agg(
        CASE WHEN entry->'models' ?| ARRAY['midjourney-v8.2:imagine_relax', 'midjourney-v8.2:imagine_fast', 'midjourney-v8.2:imagine_turbo', 'midjourney-v8.2:upscale_fast']
             THEN jsonb_set(entry, '{models}', pg_temp.mj_fast_model_keys(entry->'models'))
             ELSE entry END ORDER BY ord
    ), '[]'::jsonb)
    FROM jsonb_array_elements(g.model_pricing) WITH ORDINALITY AS items(entry, ord)
    WHERE NOT COALESCE(entry->'models' ?| ARRAY['midjourney-v8.2:imagine_relax', 'midjourney-v8.2:imagine_turbo'], FALSE)
       OR pg_temp.mj_fast_model_keys(entry->'models') <> '[]'::jsonb
), updated_at = NOW()
WHERE jsonb_typeof(g.model_pricing) = 'array'
  AND g.model_pricing::text LIKE '%midjourney-v8.2:%';

DROP FUNCTION pg_temp.mj_fast_model_keys(JSONB);

ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;
ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_image_billing_size_check CHECK (
    image_count <= 0 OR billing_mode = 'video' OR COALESCE(video_count, 0) > 0
    OR (billing_mode = 'per_request' AND model IN (
        'midjourney-v8.2:generation', 'midjourney-v8.2:upscale',
        -- Old admitted tasks retain their frozen operation prices.
        'midjourney-v8.2:imagine_relax', 'midjourney-v8.2:imagine_fast',
        'midjourney-v8.2:imagine_turbo', 'midjourney-v8.2:upscale_fast'
    ))
    OR (image_size IS NOT NULL AND image_size IN ('1K', '2K', '4K', 'mixed'))
) NOT VALID;

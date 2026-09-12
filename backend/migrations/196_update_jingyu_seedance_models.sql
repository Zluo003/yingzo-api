-- Jingyu renamed its Seedance 2.0 upstream model and added Seedance 2.5.
-- Replace only known historical defaults so operator-defined mappings remain intact.
UPDATE accounts
SET credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{model_mapping}',
        COALESCE(
            CASE
                WHEN jsonb_typeof(credentials->'model_mapping') = 'object'
                    THEN credentials->'model_mapping'
            END,
            '{}'::jsonb
        )
        || CASE
            WHEN credentials->'model_mapping'->>'seedance-2.0' IS NULL
              OR credentials->'model_mapping'->>'seedance-2.0' IN (
                  'seedance-api-2.0',
                  'jing-video-2-pro',
                  'yu-video-2-pro'
              )
                THEN '{"seedance-2.0":"yu-video-2-pro"}'::jsonb
            ELSE '{}'::jsonb
        END
        || CASE
            WHEN credentials->'model_mapping'->>'seedance-2.5' IS NULL
                THEN '{"seedance-2.5":"yu-video-2.5-pro"}'::jsonb
            ELSE '{}'::jsonb
        END,
        TRUE
    ),
    updated_at = NOW()
WHERE platform = 'seedance'
  AND extra->>'video_provider' = 'jingyu';

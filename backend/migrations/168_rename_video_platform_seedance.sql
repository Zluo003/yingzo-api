-- Rename the persisted video platform identifier to seedance while preserving
-- the public /v1/videos API and video request/billing semantics.

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

UPDATE groups
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

UPDATE accounts
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

UPDATE channel_model_pricing
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

UPDATE channel_account_stats_model_pricing
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

UPDATE agent_model_pricing
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

-- Prefer an already-configured seedance quota, but carry over any missing
-- limits and conservatively retain usage from the legacy row.
UPDATE user_platform_quotas AS target
SET daily_limit_usd = COALESCE(target.daily_limit_usd, legacy.daily_limit_usd),
    weekly_limit_usd = COALESCE(target.weekly_limit_usd, legacy.weekly_limit_usd),
    monthly_limit_usd = COALESCE(target.monthly_limit_usd, legacy.monthly_limit_usd),
    daily_usage_usd = target.daily_usage_usd + legacy.daily_usage_usd,
    weekly_usage_usd = target.weekly_usage_usd + legacy.weekly_usage_usd,
    monthly_usage_usd = target.monthly_usage_usd + legacy.monthly_usage_usd,
    daily_window_start = CASE
        WHEN target.daily_window_start IS NULL THEN legacy.daily_window_start
        WHEN legacy.daily_window_start IS NULL THEN target.daily_window_start
        ELSE LEAST(target.daily_window_start, legacy.daily_window_start)
    END,
    weekly_window_start = CASE
        WHEN target.weekly_window_start IS NULL THEN legacy.weekly_window_start
        WHEN legacy.weekly_window_start IS NULL THEN target.weekly_window_start
        ELSE LEAST(target.weekly_window_start, legacy.weekly_window_start)
    END,
    monthly_window_start = CASE
        WHEN target.monthly_window_start IS NULL THEN legacy.monthly_window_start
        WHEN legacy.monthly_window_start IS NULL THEN target.monthly_window_start
        ELSE LEAST(target.monthly_window_start, legacy.monthly_window_start)
    END,
    updated_at = NOW()
FROM user_platform_quotas AS legacy
WHERE target.user_id = legacy.user_id
  AND target.platform = 'seedance'
  AND target.deleted_at IS NULL
  AND legacy.platform = 'video'
  AND legacy.deleted_at IS NULL;

UPDATE user_platform_quotas AS legacy
SET deleted_at = NOW(), updated_at = NOW()
WHERE legacy.platform = 'video'
  AND legacy.deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM user_platform_quotas AS target
      WHERE target.user_id = legacy.user_id
        AND target.platform = 'seedance'
        AND target.deleted_at IS NULL
  );

UPDATE user_platform_quotas
SET platform = 'seedance', updated_at = NOW()
WHERE platform = 'video';

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'seedance')) NOT VALID;

-- Channel mappings are nested by platform. Existing canonical seedance values
-- win when both keys are present.
UPDATE channels
SET model_mapping = jsonb_set(
        model_mapping - 'video',
        '{seedance}',
        COALESCE(
            CASE WHEN jsonb_typeof(model_mapping->'video') = 'object' THEN model_mapping->'video' END,
            '{}'::jsonb
        ) || COALESCE(
            CASE WHEN jsonb_typeof(model_mapping->'seedance') = 'object' THEN model_mapping->'seedance' END,
            '{}'::jsonb
        ),
        TRUE
    ),
    updated_at = NOW()
WHERE model_mapping ? 'video';

-- Migrate the old Jingyu default mapping without overriding custom mappings.
UPDATE accounts
SET credentials = jsonb_set(
        credentials,
        '{model_mapping,seedance-2.0}',
        '"jing-video-2-pro"'::jsonb,
        TRUE
    ),
    updated_at = NOW()
WHERE platform = 'seedance'
  AND extra->>'video_provider' = 'jingyu'
  AND credentials->'model_mapping'->>'seedance-2.0' = 'seedance-api-2.0';

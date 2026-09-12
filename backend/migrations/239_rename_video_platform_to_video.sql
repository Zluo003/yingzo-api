-- 把视频平台标识从 seedance 改回 video（168 曾做过 video → seedance 的反向重命名）。
--
-- 背景：168_rename_video_platform_seedance.sql 把平台标识改成了 seedance，
-- 但账号与分组统一使用 video 命名更贴合“视频账号 / 视频分组”的语义。
-- 168 之后的 224/237 又重建了 user_platform_quotas 的 platform CHECK 且不含
-- 视频平台，因此这里必须一并修复约束，否则视频用量的配额行无法写入。
--
-- 168 已被校验和锁定，不能回改，所以用本迁移做增量修复。

-- ---------------------------------------------------------------------------
-- 1. groups / accounts：平台标识 seedance → video
-- ---------------------------------------------------------------------------
UPDATE groups
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

UPDATE accounts
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

-- 账号 extra 里的 video_provider 是上游标识，不随平台改名而变化。

-- ---------------------------------------------------------------------------
-- 2. 关联计价表：平台维度同步改名
-- ---------------------------------------------------------------------------
UPDATE channel_model_pricing
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

UPDATE channel_account_stats_model_pricing
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

-- 注意：不要再更新 agent_model_pricing。168 曾更新过它，但 176 已经
-- `DROP TABLE IF EXISTS agent_model_pricing`（改由 177 的 agent_model_prices
-- 与 groups 上的价格字段承载），该表在当前 schema 中已不存在。

-- ---------------------------------------------------------------------------
-- 3. channels.model_mapping：按平台分组的映射键 seedance → video
--    已有 canonical video 键时保留它，仅把 seedance 的条目合并过去。
-- ---------------------------------------------------------------------------
UPDATE channels
SET model_mapping = jsonb_set(
        model_mapping - 'seedance',
        '{video}',
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
WHERE model_mapping ? 'seedance';

-- ---------------------------------------------------------------------------
-- 4. user_platform_quotas：先摘掉 CHECK，再合并重复行并改名，最后重建为超集
--    顺序很关键：旧约束不允许 video，改名语句会撞上旧约束而失败。
--    同 (user, platform) 唯一，改名后可能与既有 video 行冲突，必须先合并。
-- ---------------------------------------------------------------------------
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

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
  AND target.platform = 'video'
  AND target.deleted_at IS NULL
  AND legacy.platform = 'seedance'
  AND legacy.deleted_at IS NULL;

UPDATE user_platform_quotas AS legacy
SET deleted_at = NOW(), updated_at = NOW()
WHERE legacy.platform = 'seedance'
  AND legacy.deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM user_platform_quotas AS target
      WHERE target.user_id = legacy.user_id
        AND target.platform = 'video'
        AND target.deleted_at IS NULL
  );

UPDATE user_platform_quotas
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

-- 224/237 重建后的约束不含视频平台，这里重建为包含 video 与国产供应商的超集。
ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok',
                        'kimi', 'zhipu', 'deepseek', 'minimax', 'video'));

-- ---------------------------------------------------------------------------
-- 5. agent 模型目录：同样必须先摘约束，再改名，最后重建。
--    177 建的约束是 ('openai','anthropic','gemini','seedance')，
--    seedance → video 的 UPDATE 会被它直接拒绝。
-- ---------------------------------------------------------------------------
ALTER TABLE agent_group_models
    DROP CONSTRAINT IF EXISTS agent_group_models_platform_check;

UPDATE agent_group_models
SET platform = 'video', updated_at = NOW()
WHERE platform = 'seedance';

ALTER TABLE agent_group_models
    ADD CONSTRAINT agent_group_models_platform_check
    CHECK (platform IN ('openai', 'anthropic', 'gemini', 'video'));


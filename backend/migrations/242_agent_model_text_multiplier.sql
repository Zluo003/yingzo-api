-- Yingzo Agent 文本模型倍率改为逐模型配置。
--
-- 背景：文本模型的实际计费基准来自"账号所属源分组的渠道价"，Yingzo Agent 只决定
-- 在渠道价之上乘多少。早期设计把这个倍率放在平台级（agent_platform_rates：
-- openai/anthropic/gemini 各一个），无法给同一平台下的不同模型定不同倍率。
-- 现在把倍率落到 agent_group_models 行上，与"启用哪些模型、图片/视频单价"同源，
-- 管理端一次提交即可完成"启用 + 定价"。
--
-- NULL 表示"尚未配置"：文本模型只有配了倍率才允许被调用（与旧行为一致——旧实现
-- 同样要求平台倍率存在），0 是合法值，表示渠道价原价免费加价倍率 0。

ALTER TABLE agent_group_models
    ADD COLUMN IF NOT EXISTS rate_multiplier NUMERIC(20,10);

ALTER TABLE agent_group_models
    DROP CONSTRAINT IF EXISTS agent_group_models_rate_multiplier_check;

ALTER TABLE agent_group_models
    ADD CONSTRAINT agent_group_models_rate_multiplier_check
    CHECK (rate_multiplier IS NULL OR rate_multiplier >= 0);

-- 文本模型按 token 计费，不允许挂按张/按秒的价格行；媒体模型的价格存在性由服务端
-- 在解析时判定，以免存量未定价的媒体模型被迁移直接判死。
COMMENT ON COLUMN agent_group_models.rate_multiplier IS
    '可选：文本模型在源渠道价之上的下游倍率；NULL=未配置（该模型不可被调用），0 为合法值';

-- 把已有的平台级倍率下沉到该平台当前发现的每一个文本模型上，避免存量配置丢失。
DO $$
BEGIN
    IF to_regclass('agent_platform_rates') IS NOT NULL THEN
        UPDATE agent_group_models AS m
        SET rate_multiplier = r.rate_multiplier,
            updated_at = NOW()
        FROM agent_platform_rates AS r
        WHERE m.group_id = r.group_id
          AND m.platform = r.platform
          AND m.media_type = 'text'
          AND m.rate_multiplier IS NULL;
    END IF;
END $$;

DROP TABLE IF EXISTS agent_platform_rates;

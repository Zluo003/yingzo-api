-- Yingzo Agent 聚合分组要覆盖应用支持的全部 provider 平台。
--
-- 177 建表时 platform 只允许 openai/anthropic/gemini/seedance，239 把 seedance 改名为
-- video 后仍是四个值。而聚合分组的账号本来就可以来自任意平台（账号-分组绑定没有平台
-- 限制），目录却写不进去：绑定 grok/国产账号后同步会因为 CHECK 约束直接失败。
--
-- 这里把约束放宽到 grok 与国产 OpenAI 兼容供应商（kimi/zhipu/deepseek/minimax）。
-- 说明：antigravity 账号提供的是 Claude/Gemini 模型，与 anthropic/gemini 平台重复，
-- 目录不单独为其建平台维度，故不在本约束内。

ALTER TABLE agent_group_models
    DROP CONSTRAINT IF EXISTS agent_group_models_platform_check;

ALTER TABLE agent_group_models
    ADD CONSTRAINT agent_group_models_platform_check
    CHECK (platform IN (
        'openai', 'anthropic', 'gemini',
        'grok', 'kimi', 'zhipu', 'deepseek', 'minimax',
        'video'
    ));

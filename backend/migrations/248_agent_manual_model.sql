-- 允许管理员在「Yingzo Agent」页手工声明模型（当前用于图片模型）。
--
-- 背景：目录里的模型来自账号的 credentials.model_mapping（没有映射时用内置清单），
-- 上游新出的图片模型如果既不在账号映射里、也不在内置清单里，目录根本不知道它存在。
-- 关键词识别只能修正"已经进目录的模型"的类型，解决不了"名字没人写过"这一类。
--
-- manual = TRUE 的行表示"管理员显式声明"，行为与账号同步发现的模型不同：
--   * 同步不会把它标记为 available = FALSE（它本来就不来自任何账号映射）；
--   * 公共目录与取价不再要求"能在某个可调度账号上发现"，管理员声明即生效。
-- 账号侧的模型白名单过滤对这类模型放行（见 isModelSupportedByAccountWithContext），
-- 否则请求会因为账号 mapping 里没有它而选不到账号。

ALTER TABLE agent_group_models
    ADD COLUMN IF NOT EXISTS manual BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN agent_group_models.manual IS
    '管理员手工声明的模型：同步不会将其置为不可用，且不要求账号映射里存在';

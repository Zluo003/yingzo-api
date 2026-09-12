-- 把系统内置聚合分组「Yingzo Agent」找回来。
--
-- 背景：这个分组是系统基础设施（模型目录、账号聚合、Agent 凭证都挂在它上面），
-- 但 161 的种子迁移是一次性的（校验和锁定），删掉之后没有任何自愈路径。
-- 2026-09-11 的库里它的 deleted_at 被置上了，原因正是当时的删除守卫失效：
-- Group.Kind/SystemCode 没有从数据库回填，IsAgent() 恒为 false，管理端删除
-- 一路放行。守卫已经修好（admin_group.go + 集成测试钉住），但已经删掉的行
-- 需要在本迁移里恢复，否则面板会一直显示"未找到系统内置的 Yingzo Agent 分组"。
--
-- 恢复只做数据行本身：级联删除时已经硬删掉 account_groups 绑定，
-- 需要管理员在「账号管理」里重新勾选归属（这是本来就该走的流程）。

-- 1) 有软删除行、且当前没有存活行：恢复最近删除的那一条，并复位系统分组的固定属性。
UPDATE groups
SET deleted_at = NULL,
    status = 'active',
    kind = 'agent',
    platform = 'openai',
    is_exclusive = FALSE,
    allow_image_generation = TRUE,
    image_rate_independent = TRUE,
    image_rate_multiplier = 1,
    updated_at = NOW()
WHERE id = (
    SELECT id
    FROM groups
    WHERE system_code = 'yingzo' AND deleted_at IS NOT NULL
    ORDER BY id DESC
    LIMIT 1
)
AND NOT EXISTS (
    SELECT 1 FROM groups live
    WHERE live.system_code = 'yingzo' AND live.deleted_at IS NULL
);

-- 2) 连软删除行都没有（例如从更旧的库恢复）：补一条种子行。
INSERT INTO groups (
    name, description, kind, system_code, platform, status,
    rate_multiplier, is_exclusive, allow_image_generation,
    image_rate_independent, image_rate_multiplier
)
SELECT
    'Yingzo Agent',
    'System-managed multi-model Agent group',
    'agent',
    'yingzo',
    'openai',
    'active',
    1.0,
    FALSE,
    TRUE,
    TRUE,
    1
WHERE NOT EXISTS (
    SELECT 1 FROM groups WHERE system_code = 'yingzo' AND deleted_at IS NULL
);

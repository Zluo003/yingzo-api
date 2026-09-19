-- Agent 目录删除语义从软排除改为硬删除之后，`excluded` 不再有任何写入方，
-- 管理端也没有恢复排除行的入口。存量排除行会永远卡在"目录里不可见、同步
-- 不复活、无法恢复"的状态（正是本次要修的缺陷）。一次性解除这些排除，让
-- 账号仍声明的模型在下次同步或读路径自愈时以未启用状态回来，由管理员重新
-- 启用；账号已不再声明的行保持不可用，不会出现在下游目录里。
UPDATE agent_group_models
SET excluded = FALSE, excluded_at = NULL
WHERE excluded = TRUE;

-- Yingzo Agent 是唯一分组：全局最多只能有一条存活的 agent 分组。
--
-- 161 只保证"每个 system_code 一条存活行"，理论上仍可能通过手工 SQL 造出
-- kind='agent' 但 system_code 不同的第二条。产品约定是"系统聚合分组有且仅有一个"
-- （管理员不能新建、不能复制、不能删除），这里把它落成数据库不变式。
--
-- 服务端已经堵住了所有造第二条的入口（Create/Update 输入不含 kind/system_code，
-- DuplicateGroup 拒绝以 agent 分组为源且克隆不写这两个字段）。本迁移只处理
-- 已经存在的不一致数据：保留 id 最小的一条，其余软删除——它们只可能来自手工 SQL。

UPDATE groups
SET deleted_at = NOW(), updated_at = NOW()
WHERE kind = 'agent'
  AND deleted_at IS NULL
  AND id > (
      SELECT MIN(id) FROM groups WHERE kind = 'agent' AND deleted_at IS NULL
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_single_live_agent
    ON groups (kind)
    WHERE kind = 'agent' AND deleted_at IS NULL;

-- 允许一个分组关联多个渠道（仅系统聚合分组需要）。
--
-- 背景：Yingzo Agent 把不同 provider 的账号聚合在同一个分组里，而文本模型的基准价是
-- "该账号所属渠道的价格 × 模型倍率"。要让这一个分组同时拿到 openai / deepseek / glm /
-- kimi / minimax 等各家的渠道价，就必须让这个分组能关联多个渠道；此前的唯一索引
-- idx_channel_groups_group_id 把 group_id 限死成一条，第二个渠道直接写不进去。
--
-- 只放宽数据库层：普通分组"一个分组只归一个渠道"的不变式改由服务层保证
-- （checkGroupConflicts 只对非聚合分组生效）。渠道缓存是按 groupID 单值索引的
-- （channelByGroupID / pricingByGroupModel），普通分组保持单一归属才能保证渠道价计费
-- 唯一确定；聚合分组的定价改走"按账号平台 + 模型在多个渠道里确定性取价"的专用路径。

DROP INDEX IF EXISTS idx_channel_groups_group_id;

CREATE INDEX IF NOT EXISTS idx_channel_groups_group_lookup
    ON channel_groups (group_id);

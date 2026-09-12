-- 修正 usage_logs.video_duration_seconds 的 NOT NULL 漂移。
--
-- 156_video_gateway.sql 建了这一列并写成 `INTEGER NOT NULL DEFAULT 0`；
-- 172_video_per_second_billing_metadata.sql 想把它改成可空（`ADD COLUMN IF NOT EXISTS
-- video_duration_seconds INTEGER`），但列已存在，IF NOT EXISTS 让这条语句变成了空操作，
-- 于是实际 schema 与代码/断言不一致：
--   * 写入侧 UsageLog.VideoDurationSeconds 是 *int，非视频请求为 nil，经 nullInt 绑定 NULL；
--   * 仓库自带的 schema 集成测试断言该列 nullable = true；
--   * 结果所有非视频用量写入都会撞 NOT NULL（集成测试里表现为 TestUsageLogRepoSuite 全线失败）。
--
-- 这里按代码与既有断言的本意把列改为可空，保留 DEFAULT 0 让"不写该列"的插入行为不变。

ALTER TABLE usage_logs
    ALTER COLUMN video_duration_seconds DROP NOT NULL;

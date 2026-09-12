-- 本仓库的 usage_logs.request_type 取值与上游有一处偏移。
--
-- 上游把 OpenAI Live 会话记为 request_type=5（迁移 188 把 CHECK 放宽到 0..5）。
-- 但本仓库在 156/158 引入 /v1/videos 视频网关时已经把 5 用于 video，
-- 并且线上 usage_logs 中存量就有 request_type=5 的视频行。
-- 同一个整数不能承载两种语义，因此 live 在本仓库顺延为 6
-- （见 internal/service/usage_log.go 的 RequestTypeVideo / RequestTypeLive）。
--
-- 这里把上界从 5 放宽到 6，让 live 用量可以写入。存量行取值均在 0..5，
-- 新约束是旧约束的超集，校验瞬时通过。
ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type >= 0 AND request_type <= 6);

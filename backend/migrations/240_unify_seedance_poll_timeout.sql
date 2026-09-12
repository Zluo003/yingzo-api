-- Seedance 视频任务的轮询超时统一为 15 分钟（900000 毫秒）。
--
-- 背景：轮询超时此前按上游各配一套（aigod 5 分钟、newtoken 60 分钟），
-- 而 aigod 的参考素材统一携带 subject_type=person，会走"真人过白"流程，
-- 上游文档标注该流程最长等待 10 分钟。5 分钟的超时会在上游仍可能成功时
-- 提前把任务判失败并退费，上游却已经产生费用。
--
-- 代码侧的默认值已统一为 15 分钟（videoDefaultPollTimeout），但账号 extra 里
-- 已显式写入的 poll_timeout_ms 优先级高于默认值，因此需要一次性对齐存量账号。
-- 只改与目标值不同的行：没有该键的账号自动落到新的默认值，无需写入。

UPDATE accounts
SET extra = jsonb_set(extra, '{poll_timeout_ms}', '900000'::jsonb, true),
    updated_at = NOW()
WHERE platform = 'video'
  AND extra IS NOT NULL
  AND extra ? 'poll_timeout_ms'
  AND extra->>'poll_timeout_ms' <> '900000';

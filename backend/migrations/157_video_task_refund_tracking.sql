-- Track idempotent refunds for async video tasks.

ALTER TABLE video_tasks
    ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMPTZ;


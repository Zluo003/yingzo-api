-- Durable asynchronous images and an append-only customer billing history.
ALTER TABLE usage_logs ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_task_id TEXT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS funds_event TEXT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_task_status TEXT;
CREATE INDEX IF NOT EXISTS idx_usage_logs_image_task ON usage_logs(image_task_id) WHERE image_task_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS image_tasks (
 id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 idempotency_key TEXT,
 fingerprint TEXT NOT NULL,
 record JSONB NOT NULL,
 quote JSONB NOT NULL,
 encrypted_request TEXT NOT NULL,
 encrypted_result TEXT,
 captured_usage JSONB,
 status TEXT NOT NULL DEFAULT 'processing' CHECK(status IN ('processing','completed','failed')),
 phase TEXT NOT NULL DEFAULT 'queued',
 lease_token TEXT,
 lease_until TIMESTAMPTZ,
 deadline TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(api_key_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_image_tasks_work ON image_tasks(lease_until,created_at) WHERE status='processing';

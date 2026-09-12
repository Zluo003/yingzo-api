-- Seedance 2.0 video gateway support.

CREATE TABLE IF NOT EXISTS video_group_pricing_rules (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_code VARCHAR(100) NOT NULL,
    resolution VARCHAR(16) NOT NULL,
    credits_per_second DECIMAL(20,10) NOT NULL,
    reference_video_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT video_group_pricing_rules_model_resolution_unique UNIQUE (group_id, model_code, resolution),
    CONSTRAINT video_group_pricing_rules_price_nonnegative CHECK (credits_per_second >= 0),
    CONSTRAINT video_group_pricing_rules_reference_multiplier_nonnegative CHECK (reference_video_multiplier >= 0)
);

CREATE INDEX IF NOT EXISTS idx_video_group_pricing_rules_group_id ON video_group_pricing_rules(group_id);
CREATE INDEX IF NOT EXISTS idx_video_group_pricing_rules_model_resolution ON video_group_pricing_rules(model_code, resolution);

CREATE TABLE IF NOT EXISTS video_tasks (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    request_id VARCHAR(128),
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model VARCHAR(100) NOT NULL,
    upstream_model VARCHAR(120) NOT NULL,
    resolution VARCHAR(16) NOT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    reference_duration_seconds INTEGER NOT NULL DEFAULT 0,
    billable_seconds INTEGER NOT NULL DEFAULT 0,
    cost_per_second DECIMAL(20,10) NOT NULL DEFAULT 0,
    total_cost DECIMAL(20,10) NOT NULL DEFAULT 0,
    actual_cost DECIMAL(20,10) NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    upstream_task_id VARCHAR(128),
    request_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    upstream_response_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_video_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    billed_at TIMESTAMPTZ,
    CONSTRAINT video_tasks_status_check CHECK (status IN ('queued','processing','completed','failed','cancelled')),
    CONSTRAINT video_tasks_duration_nonnegative CHECK (duration_seconds >= 0 AND reference_duration_seconds >= 0 AND billable_seconds >= 0),
    CONSTRAINT video_tasks_cost_nonnegative CHECK (cost_per_second >= 0 AND total_cost >= 0 AND actual_cost >= 0)
);

CREATE INDEX IF NOT EXISTS idx_video_tasks_user_id ON video_tasks(user_id);
CREATE INDEX IF NOT EXISTS idx_video_tasks_api_key_id ON video_tasks(api_key_id);
CREATE INDEX IF NOT EXISTS idx_video_tasks_group_id ON video_tasks(group_id);
CREATE INDEX IF NOT EXISTS idx_video_tasks_account_id ON video_tasks(account_id);
CREATE INDEX IF NOT EXISTS idx_video_tasks_status ON video_tasks(status);
CREATE INDEX IF NOT EXISTS idx_video_tasks_upstream_task_id ON video_tasks(upstream_task_id);
CREATE INDEX IF NOT EXISTS idx_video_tasks_created_at ON video_tasks(created_at);

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS video_task_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS video_resolution VARCHAR(16),
    ADD COLUMN IF NOT EXISTS video_duration_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS video_reference_duration_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS video_billable_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS video_result_url TEXT;

ALTER TABLE usage_logs
    ALTER COLUMN billing_mode TYPE VARCHAR(32);

CREATE INDEX IF NOT EXISTS idx_usage_logs_video_task_id ON usage_logs(video_task_id);

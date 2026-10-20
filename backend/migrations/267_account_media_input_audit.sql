-- Account-scoped media input audit; no foreign keys so evidence survives account deletion.
CREATE TABLE IF NOT EXISTS media_input_audit_events (
    id UUID PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    user_id BIGINT NOT NULL,
    user_email TEXT NOT NULL DEFAULT '',
    api_key_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL,
    media_type TEXT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('block', 'error')),
    category TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    reviewer TEXT NOT NULL DEFAULT '',
    encrypted_payload TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    notify_pending BOOLEAN NOT NULL DEFAULT FALSE,
    notify_attempts INT NOT NULL DEFAULT 0,
    notify_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notified_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS media_input_audit_events_created_idx ON media_input_audit_events (created_at DESC);
CREATE INDEX IF NOT EXISTS media_input_audit_events_expiry_idx ON media_input_audit_events (expires_at);
CREATE INDEX IF NOT EXISTS media_input_audit_events_notify_idx ON media_input_audit_events (notify_after) WHERE notify_pending;

CREATE TABLE IF NOT EXISTS agent_generation_quotes (
    id VARCHAR(80) PRIMARY KEY,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('image', 'video')),
    model VARCHAR(120) NOT NULL,
    request_hash CHAR(64) NOT NULL,
    pricing_version VARCHAR(80) NOT NULL,
    unit_kind VARCHAR(32) NOT NULL,
    units DOUBLE PRECISION NOT NULL CHECK (units > 0),
    count INTEGER NOT NULL CHECK (count > 0),
    unit_price DOUBLE PRECISION NOT NULL CHECK (unit_price >= 0),
    total_price DOUBLE PRECISION NOT NULL CHECK (total_price >= 0),
    actual_price DOUBLE PRECISION NOT NULL CHECK (actual_price >= 0),
    currency VARCHAR(24) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_generation_quotes_owner_expiry
    ON agent_generation_quotes(api_key_id, expires_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_generation_quotes_expiry
    ON agent_generation_quotes(expires_at);

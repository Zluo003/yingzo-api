ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kind VARCHAR(20) NOT NULL DEFAULT 'standard';
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS system_code VARCHAR(64);

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_kind_check;
ALTER TABLE groups
    ADD CONSTRAINT groups_kind_check CHECK (kind IN ('standard', 'agent'));

CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_system_code_active
    ON groups(system_code)
    WHERE system_code IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS agent_installations (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    system_code VARCHAR(64) NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_installations_user
    ON agent_installations(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_device_authorizations (
    id UUID PRIMARY KEY,
    device_code_hash CHAR(64) NOT NULL UNIQUE,
    user_code_hash CHAR(64) NOT NULL UNIQUE,
    installation_id UUID NOT NULL,
    installation_name VARCHAR(120) NOT NULL,
    system_code VARCHAR(64) NOT NULL,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'consumed')),
    expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_device_authorizations_expiry
    ON agent_device_authorizations(expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS temporary_assets (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    public_token_hash CHAR(64) NOT NULL UNIQUE,
    storage_backend VARCHAR(20) NOT NULL CHECK (storage_backend IN ('local', 's3')),
    storage_key TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    media_type VARCHAR(20) NOT NULL CHECK (media_type IN ('image', 'video', 'audio')),
    mime_type VARCHAR(120) NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    last_accessed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_temporary_assets_expiry
    ON temporary_assets(expires_at)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_temporary_assets_owner
    ON temporary_assets(user_id, created_at DESC);

INSERT INTO groups(
    name,
    description,
    kind,
    system_code,
    platform,
    status,
    rate_multiplier,
    is_exclusive
)
SELECT
    'Yingzo Agent',
    'System-managed multi-model Agent group',
    'agent',
    'yingzo',
    'openai',
    'active',
    1.0,
    true
WHERE NOT EXISTS (
    SELECT 1
    FROM groups
    WHERE system_code = 'yingzo' AND deleted_at IS NULL
);

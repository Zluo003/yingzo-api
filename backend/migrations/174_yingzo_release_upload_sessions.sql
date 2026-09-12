CREATE TABLE IF NOT EXISTS yingzo_release_upload_sessions (
    id UUID PRIMARY KEY,
    client_upload_id VARCHAR(128) NOT NULL,
    release_id UUID NOT NULL REFERENCES yingzo_releases(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artifact_kind VARCHAR(32) NOT NULL
        CHECK (artifact_kind IN ('host_package', 'runtime_installer')),
    target VARCHAR(32) NOT NULL
        CHECK (target IN ('openai', 'claude-code', 'claude-desktop', 'runtime')),
    os VARCHAR(16) NOT NULL
        CHECK (os IN ('macos', 'windows', 'any')),
    arch VARCHAR(16) NOT NULL
        CHECK (arch IN ('arm64', 'x64', 'any')),
    format VARCHAR(16) NOT NULL
        CHECK (format IN ('tar.gz', 'zip', 'mcpb', 'dmg', 'exe')),
    content_type VARCHAR(128) NOT NULL,
    runtime_protocol INTEGER NOT NULL CHECK (runtime_protocol > 0),
    package_filename TEXT NOT NULL,
    total_bytes BIGINT NOT NULL CHECK (total_bytes > 0),
    received_bytes BIGINT NOT NULL DEFAULT 0
        CHECK (received_bytes >= 0 AND received_bytes <= total_bytes),
    expected_sha256 CHAR(64),
    temp_storage_key TEXT NOT NULL,
    last_chunk_offset BIGINT,
    last_chunk_size INTEGER,
    last_chunk_sha256 CHAR(64),
    status VARCHAR(16) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'finalizing', 'completed', 'aborted', 'expired')),
    completed_artifact_id UUID REFERENCES yingzo_release_artifacts(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (release_id, target, os, arch)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_yingzo_release_upload_client
    ON yingzo_release_upload_sessions(created_by, client_upload_id);

CREATE INDEX IF NOT EXISTS idx_yingzo_release_upload_expiry
    ON yingzo_release_upload_sessions(status, expires_at);

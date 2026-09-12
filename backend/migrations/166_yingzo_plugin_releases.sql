CREATE TABLE IF NOT EXISTS yingzo_releases (
    id UUID PRIMARY KEY,
    version VARCHAR(64) NOT NULL UNIQUE,
    status VARCHAR(20) NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'published', 'superseded', 'disabled')),
    package_filename TEXT NOT NULL,
    storage_backend VARCHAR(20) NOT NULL DEFAULT 'local'
        CHECK (storage_backend IN ('local', 's3')),
    storage_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 CHAR(64) NOT NULL,
    signature TEXT,
    min_codex_version VARCHAR(32),
    min_claude_version VARCHAR(32),
    release_notes TEXT,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    published_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_yingzo_releases_status_published
    ON yingzo_releases(status, published_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_yingzo_releases_single_published
    ON yingzo_releases((status))
    WHERE status = 'published';

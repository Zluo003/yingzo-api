-- Online updates for the Yingzo desktop application. This is intentionally
-- independent from temporary asset storage and from the retired plugin release
-- tables removed by migration 175.
CREATE TABLE IF NOT EXISTS yingzo_desktop_releases (
    id UUID PRIMARY KEY,
    version VARCHAR(64) NOT NULL,
    platform VARCHAR(16) NOT NULL CHECK (platform IN ('win32', 'darwin')),
    arch VARCHAR(16) NOT NULL CHECK (arch IN ('x64', 'arm64', 'universal')),
    status VARCHAR(20) NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'published', 'superseded', 'disabled')),
    package_filename TEXT NOT NULL,
    storage_backend VARCHAR(10) NOT NULL DEFAULT 'local'
        CHECK (storage_backend IN ('local', 'r2')),
    storage_key TEXT NOT NULL,
    metadata_key TEXT NOT NULL,
    package_size_bytes BIGINT NOT NULL CHECK (package_size_bytes > 0),
    sha256 CHAR(64) NOT NULL,
    sha512 TEXT NOT NULL,
    release_notes TEXT NOT NULL DEFAULT '',
    download_url TEXT NOT NULL,
    metadata_url TEXT NOT NULL,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    published_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT yingzo_desktop_release_version_target_key UNIQUE (version, platform, arch)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_yingzo_desktop_releases_published_target
    ON yingzo_desktop_releases(platform, arch)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_yingzo_desktop_releases_listing
    ON yingzo_desktop_releases(created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_yingzo_desktop_releases_lookup
    ON yingzo_desktop_releases(platform, arch, status, version)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS yingzo_release_artifacts (
    id UUID PRIMARY KEY,
    release_id UUID NOT NULL REFERENCES yingzo_releases(id) ON DELETE CASCADE,
    host_family VARCHAR(20) NOT NULL
        CHECK (host_family IN ('openai', 'claude', 'combined')),
    package_filename TEXT NOT NULL,
    storage_backend VARCHAR(20) NOT NULL DEFAULT 'local'
        CHECK (storage_backend IN ('local', 's3')),
    storage_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    sha256 CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (release_id, host_family)
);

CREATE INDEX IF NOT EXISTS idx_yingzo_release_artifacts_release
    ON yingzo_release_artifacts(release_id, host_family);

INSERT INTO yingzo_release_artifacts (
    id, release_id, host_family, package_filename, storage_backend,
    storage_key, size_bytes, sha256, created_at, updated_at
)
SELECT
    md5(id::text || ':combined')::uuid,
    id,
    'combined',
    package_filename,
    storage_backend,
    storage_key,
    size_bytes,
    sha256,
    created_at,
    updated_at
FROM yingzo_releases
ON CONFLICT (release_id, host_family) DO NOTHING;

ALTER TABLE yingzo_releases ALTER COLUMN package_filename DROP NOT NULL;
ALTER TABLE yingzo_releases ALTER COLUMN storage_backend DROP NOT NULL;
ALTER TABLE yingzo_releases ALTER COLUMN storage_key DROP NOT NULL;
ALTER TABLE yingzo_releases ALTER COLUMN size_bytes DROP NOT NULL;
ALTER TABLE yingzo_releases ALTER COLUMN sha256 DROP NOT NULL;

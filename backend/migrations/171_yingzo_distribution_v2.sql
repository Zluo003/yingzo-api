ALTER TABLE yingzo_releases
    ADD COLUMN IF NOT EXISTS distribution_schema_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS channel VARCHAR(20) NOT NULL DEFAULT 'stable',
    ADD COLUMN IF NOT EXISTS runtime_protocol INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS compatibility JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE yingzo_releases
    ADD CONSTRAINT yingzo_releases_distribution_schema_version_check
        CHECK (distribution_schema_version IN (1, 2)),
    ADD CONSTRAINT yingzo_releases_channel_check
        CHECK (channel IN ('stable', 'prerelease')),
    ADD CONSTRAINT yingzo_releases_runtime_protocol_check
        CHECK (
            (distribution_schema_version = 1 AND runtime_protocol >= 0)
            OR (distribution_schema_version = 2 AND runtime_protocol > 0)
        ),
    ADD CONSTRAINT yingzo_releases_compatibility_object_check
        CHECK (jsonb_typeof(compatibility) = 'object');

DROP INDEX IF EXISTS idx_yingzo_releases_single_published;

CREATE UNIQUE INDEX IF NOT EXISTS idx_yingzo_releases_single_published_per_channel
    ON yingzo_releases(channel)
    WHERE status = 'published';

CREATE INDEX IF NOT EXISTS idx_yingzo_releases_channel_status_published
    ON yingzo_releases(channel, status, published_at DESC);

ALTER TABLE yingzo_release_artifacts
    DROP CONSTRAINT IF EXISTS yingzo_release_artifacts_release_id_host_family_key;

ALTER TABLE yingzo_release_artifacts
    ALTER COLUMN host_family DROP NOT NULL;

ALTER TABLE yingzo_release_artifacts
    ADD COLUMN IF NOT EXISTS artifact_kind VARCHAR(32),
    ADD COLUMN IF NOT EXISTS target VARCHAR(32),
    ADD COLUMN IF NOT EXISTS os VARCHAR(16),
    ADD COLUMN IF NOT EXISTS arch VARCHAR(16),
    ADD COLUMN IF NOT EXISTS format VARCHAR(16),
    ADD COLUMN IF NOT EXISTS content_type VARCHAR(128),
    ADD COLUMN IF NOT EXISTS runtime_protocol INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS validation_status VARCHAR(24) NOT NULL DEFAULT 'validated',
    ADD COLUMN IF NOT EXISTS signature_status VARCHAR(24) NOT NULL DEFAULT 'unverified',
    ADD COLUMN IF NOT EXISTS validated_at TIMESTAMPTZ;

UPDATE yingzo_release_artifacts
SET artifact_kind = COALESCE(artifact_kind, 'host_package'),
    target = COALESCE(target, host_family),
    os = COALESCE(os, 'any'),
    arch = COALESCE(arch, 'any'),
    format = COALESCE(format, 'tar.gz'),
    content_type = COALESCE(content_type, 'application/gzip'),
    validated_at = COALESCE(validated_at, updated_at);

ALTER TABLE yingzo_release_artifacts
    ALTER COLUMN artifact_kind SET NOT NULL,
    ALTER COLUMN target SET NOT NULL,
    ALTER COLUMN os SET NOT NULL,
    ALTER COLUMN arch SET NOT NULL,
    ALTER COLUMN format SET NOT NULL,
    ALTER COLUMN content_type SET NOT NULL;

ALTER TABLE yingzo_release_artifacts
    ADD CONSTRAINT yingzo_release_artifacts_kind_check
        CHECK (artifact_kind IN ('host_package', 'runtime_installer')),
    ADD CONSTRAINT yingzo_release_artifacts_target_check
        CHECK (target IN ('openai', 'claude-code', 'claude-desktop', 'runtime', 'claude', 'combined')),
    ADD CONSTRAINT yingzo_release_artifacts_os_check
        CHECK (os IN ('macos', 'windows', 'any')),
    ADD CONSTRAINT yingzo_release_artifacts_arch_check
        CHECK (arch IN ('arm64', 'x64', 'any')),
    ADD CONSTRAINT yingzo_release_artifacts_format_check
        CHECK (format IN ('tar.gz', 'zip', 'mcpb', 'dmg', 'exe')),
    ADD CONSTRAINT yingzo_release_artifacts_runtime_protocol_check
        CHECK (runtime_protocol >= 0),
    ADD CONSTRAINT yingzo_release_artifacts_validation_status_check
        CHECK (validation_status IN ('pending', 'validated', 'failed')),
    ADD CONSTRAINT yingzo_release_artifacts_signature_status_check
        CHECK (signature_status IN ('unverified', 'verified', 'failed'));

ALTER TABLE yingzo_release_artifacts
    ADD CONSTRAINT yingzo_release_artifacts_release_target_platform_key
        UNIQUE (release_id, target, os, arch);

DROP INDEX IF EXISTS idx_yingzo_release_artifacts_release;

CREATE INDEX IF NOT EXISTS idx_yingzo_release_artifacts_release_target_platform
    ON yingzo_release_artifacts(release_id, target, os, arch);

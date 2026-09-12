-- Yingzo distribution schema 3 removes the macOS Intel Runtime installer.
-- Schema 2 remains valid so already-published releases can still be served.
ALTER TABLE yingzo_releases
    DROP CONSTRAINT IF EXISTS yingzo_releases_distribution_schema_version_check;

ALTER TABLE yingzo_releases
    ADD CONSTRAINT yingzo_releases_distribution_schema_version_check
        CHECK (distribution_schema_version IN (1, 2, 3));

ALTER TABLE yingzo_releases
    DROP CONSTRAINT IF EXISTS yingzo_releases_runtime_protocol_check;

ALTER TABLE yingzo_releases
    ADD CONSTRAINT yingzo_releases_runtime_protocol_check
        CHECK (
            (distribution_schema_version = 1 AND runtime_protocol >= 0)
            OR (distribution_schema_version IN (2, 3) AND runtime_protocol > 0)
        );

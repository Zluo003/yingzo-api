ALTER TABLE yingzo_releases
    ADD COLUMN IF NOT EXISTS stable_eligible BOOLEAN NOT NULL DEFAULT TRUE;

UPDATE yingzo_releases
SET stable_eligible = FALSE
WHERE distribution_schema_version = 2
  AND status = 'draft';

ALTER TABLE yingzo_releases
    DROP CONSTRAINT IF EXISTS yingzo_releases_stable_publication_eligible_check;

ALTER TABLE yingzo_releases
    ADD CONSTRAINT yingzo_releases_stable_publication_eligible_check
    CHECK (status <> 'published' OR channel <> 'stable' OR stable_eligible);

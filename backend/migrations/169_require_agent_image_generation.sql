UPDATE groups
SET allow_image_generation = true,
    updated_at = NOW()
WHERE kind = 'agent'
  AND system_code IS NOT NULL
  AND deleted_at IS NULL
  AND allow_image_generation = false;

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_agent_image_generation_required;
ALTER TABLE groups
    ADD CONSTRAINT groups_agent_image_generation_required
    CHECK (kind <> 'agent' OR system_code IS NULL OR allow_image_generation = true);

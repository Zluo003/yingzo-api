-- Agent model availability comes from assigned accounts. Language billing uses
-- channel prices, while image and video prices live directly on the group.

DROP TABLE IF EXISTS agent_model_pricing;

WITH generated_channels AS (
    SELECT id
    FROM channels
    WHERE (name ~ '^Yingzo Agent Pricing #[0-9]+$'
           AND description = 'System-managed Yingzo Agent model pricing')
       OR (name ~ '^System Agent Pricing #[0-9]+$'
           AND description = 'System-managed Agent model pricing')
)
DELETE FROM channel_groups cg
USING generated_channels generated
WHERE cg.channel_id = generated.id;

DELETE FROM channels
WHERE (name ~ '^Yingzo Agent Pricing #[0-9]+$'
       AND description = 'System-managed Yingzo Agent model pricing')
   OR (name ~ '^System Agent Pricing #[0-9]+$'
       AND description = 'System-managed Agent model pricing');

UPDATE groups
SET image_rate_independent = TRUE,
    image_rate_multiplier = 1,
    updated_at = NOW()
WHERE kind = 'agent'
  AND system_code IS NOT NULL
  AND deleted_at IS NULL;

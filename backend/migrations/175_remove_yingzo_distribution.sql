-- Yingzo is now distributed independently. sub2api remains the model gateway,
-- so keep Agent groups, model pricing, generation quotes, and temporary assets.

-- Credentials provisioned by the retired device-authorization flow must stop
-- working before their installation records are removed.
DO $$
BEGIN
    IF to_regclass('agent_installations') IS NOT NULL THEN
        UPDATE api_keys
        SET status = 'disabled', updated_at = NOW()
        WHERE id IN (
            SELECT api_key_id
            FROM agent_installations
            WHERE system_code = 'yingzo' AND api_key_id IS NOT NULL
        )
          AND status = 'active';
    END IF;
END $$;

DROP TABLE IF EXISTS yingzo_release_upload_sessions;
DROP TABLE IF EXISTS yingzo_release_artifacts;
DROP TABLE IF EXISTS yingzo_releases;
DROP TABLE IF EXISTS agent_device_authorizations;
DROP TABLE IF EXISTS agent_installations;

DELETE FROM settings WHERE key = 'yingzo_public_origin';

-- The desktop application now accepts an ordinary user-created API Key. Keep
-- the multi-model Agent group, but make it publicly selectable instead of
-- relying on the removed device authorization flow to provision credentials.
UPDATE groups
SET is_exclusive = false,
    updated_at = NOW()
WHERE kind = 'agent'
  AND system_code = 'yingzo'
  AND deleted_at IS NULL
  AND is_exclusive = true;

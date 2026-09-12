-- Leases protect references without changing the configured retention deadline.
ALTER TABLE temporary_assets ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_temporary_assets_resolve
 ON temporary_assets(api_key_id,user_id,sha256,size_bytes,mime_type)
 WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_temporary_assets_effective_expiry
 ON temporary_assets(GREATEST(expires_at,lease_until)) WHERE deleted_at IS NULL;

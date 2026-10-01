-- Independent Suno tasks and per-mode audio pricing. Existing prices remain unchanged.
ALTER TABLE agent_group_models DROP CONSTRAINT IF EXISTS agent_group_models_media_type_check;
ALTER TABLE agent_group_models ADD CONSTRAINT agent_group_models_media_type_check CHECK(media_type IN ('text','image','video','audio'));
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS music_task_id TEXT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS music_task_status TEXT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS music_mode TEXT;
CREATE INDEX IF NOT EXISTS idx_usage_logs_music_task ON usage_logs(music_task_id) WHERE music_task_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS music_tasks (
 id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 idempotency_key TEXT,
 fingerprint TEXT NOT NULL,
 record JSONB NOT NULL,
 quote JSONB NOT NULL,
 encrypted_request TEXT NOT NULL,
 account_id BIGINT REFERENCES accounts(id),
 encrypted_result TEXT,
 captured_usage JSONB,
 status TEXT NOT NULL DEFAULT 'processing' CHECK(status IN ('processing','completed','failed')),
 phase TEXT NOT NULL DEFAULT 'queued',
 lease_token TEXT,
 lease_until TIMESTAMPTZ,
 deadline TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(api_key_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_music_tasks_work ON music_tasks(lease_until,created_at) WHERE status='processing';

CREATE INDEX IF NOT EXISTS idx_music_tasks_account_work ON music_tasks(account_id) WHERE status='processing';

-- Cover group-copy/import/bulk paths as well as the dedicated account form.
CREATE FUNCTION enforce_suno_group_binding() RETURNS trigger AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM accounts WHERE id=NEW.account_id AND extra->>'music_provider'='apimart_suno')
    AND NOT EXISTS (SELECT 1 FROM groups WHERE id=NEW.group_id AND kind='agent' AND system_code='yingzo' AND deleted_at IS NULL)
 THEN
  RAISE EXCEPTION 'Suno accounts can only bind to the Yingzo Agent group' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER suno_group_binding BEFORE INSERT OR UPDATE ON account_groups
 FOR EACH ROW EXECUTE FUNCTION enforce_suno_group_binding();

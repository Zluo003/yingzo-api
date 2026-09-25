-- A single row per user keeps multiple desktop instances from inflating the
-- online-user count. Old rows can remain; only recent timestamps count.
CREATE TABLE IF NOT EXISTS yingzo_agent_heartbeats (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    client_ip VARCHAR(45)
);

CREATE INDEX IF NOT EXISTS idx_yingzo_agent_heartbeats_last_seen_at
    ON yingzo_agent_heartbeats (last_seen_at);

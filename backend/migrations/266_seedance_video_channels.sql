-- Empty ID is the editable legacy/default channel. Existing accounts and prices
-- remain there; never copy a price into a newly created channel implicitly.
CREATE TABLE IF NOT EXISTS video_channels (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(80) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS video_channels_name_unique ON video_channels (lower(name));
INSERT INTO video_channels (id, name) VALUES ('', '默认通道') ON CONFLICT (id) DO NOTHING;

ALTER TABLE agent_model_prices ADD COLUMN IF NOT EXISTS video_channel_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE agent_model_prices DROP CONSTRAINT IF EXISTS agent_model_prices_agent_model_id_resolution_key;
CREATE UNIQUE INDEX IF NOT EXISTS agent_model_prices_channel_resolution_unique
    ON agent_model_prices (agent_model_id, video_channel_id, resolution);

ALTER TABLE video_group_pricing_rules ADD COLUMN IF NOT EXISTS video_channel_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE video_group_pricing_rules ADD COLUMN IF NOT EXISTS billing_unit VARCHAR(16) NOT NULL DEFAULT 'second';
ALTER TABLE video_group_pricing_rules ADD COLUMN IF NOT EXISTS credits_per_request DECIMAL(20,10) NOT NULL DEFAULT 0;
ALTER TABLE video_group_pricing_rules DROP CONSTRAINT IF EXISTS video_group_pricing_rules_model_resolution_unique;
DROP INDEX IF EXISTS videogrouppricingrule_group_id_model_code_resolution;
ALTER TABLE video_group_pricing_rules DROP CONSTRAINT IF EXISTS video_group_pricing_rules_group_id_model_code_resolution_key;
CREATE UNIQUE INDEX IF NOT EXISTS video_group_pricing_rules_channel_unique
    ON video_group_pricing_rules (group_id, model_code, video_channel_id, resolution);

ALTER TABLE video_tasks ADD COLUMN IF NOT EXISTS video_channel_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE video_tasks ADD COLUMN IF NOT EXISTS billing_unit VARCHAR(16) NOT NULL DEFAULT 'second';
ALTER TABLE video_tasks ADD COLUMN IF NOT EXISTS unit_price DECIMAL(20,10) NOT NULL DEFAULT 0;
UPDATE video_tasks SET unit_price = cost_per_second WHERE billing_unit = 'second' AND unit_price = 0;

ALTER TABLE agent_model_prices DROP CONSTRAINT IF EXISTS agent_model_prices_video_channel_fk;
ALTER TABLE agent_model_prices ADD CONSTRAINT agent_model_prices_video_channel_fk FOREIGN KEY (video_channel_id) REFERENCES video_channels(id);
ALTER TABLE video_group_pricing_rules DROP CONSTRAINT IF EXISTS video_group_pricing_rules_video_channel_fk;
ALTER TABLE video_group_pricing_rules ADD CONSTRAINT video_group_pricing_rules_video_channel_fk FOREIGN KEY (video_channel_id) REFERENCES video_channels(id);
ALTER TABLE video_group_pricing_rules DROP CONSTRAINT IF EXISTS video_group_pricing_rules_billing_check;
ALTER TABLE video_group_pricing_rules ADD CONSTRAINT video_group_pricing_rules_billing_check CHECK (billing_unit IN ('second','request') AND credits_per_request >= 0);
ALTER TABLE video_tasks DROP CONSTRAINT IF EXISTS video_tasks_video_channel_fk;
ALTER TABLE video_tasks ADD CONSTRAINT video_tasks_video_channel_fk FOREIGN KEY (video_channel_id) REFERENCES video_channels(id);
ALTER TABLE video_tasks DROP CONSTRAINT IF EXISTS video_tasks_billing_unit_check;
ALTER TABLE video_tasks ADD CONSTRAINT video_tasks_billing_unit_check CHECK (billing_unit IN ('second','request') AND unit_price >= 0);

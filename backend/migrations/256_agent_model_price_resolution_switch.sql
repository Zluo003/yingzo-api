-- Keep image/video resolution prices for auditing while allowing each
-- resolution to be exposed independently in the Agent model catalogue.
ALTER TABLE agent_model_prices
    ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN agent_model_prices.enabled IS
    'Whether this image/video resolution is exposed to Agent clients and used for billing';

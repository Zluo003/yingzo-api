-- Midjourney bills one imagine grid or one upscale selection per request.
ALTER TABLE agent_model_prices DROP CONSTRAINT IF EXISTS agent_model_prices_billing_unit_check;
ALTER TABLE agent_model_prices ADD CONSTRAINT agent_model_prices_billing_unit_check
    CHECK (billing_unit IN ('image', 'second', 'request'));

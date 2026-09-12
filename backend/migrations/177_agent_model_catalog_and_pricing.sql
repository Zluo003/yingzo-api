-- Persist the Yingzo Agent model catalogue discovered from assigned accounts.
-- Text pricing is configured once per provider platform. Image and video
-- pricing is configured per model and resolution.

CREATE TABLE IF NOT EXISTS agent_platform_rates (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    platform VARCHAR(32) NOT NULL,
    rate_multiplier NUMERIC(20,10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, platform),
    CONSTRAINT agent_platform_rates_platform_check
        CHECK (platform IN ('openai', 'anthropic', 'gemini')),
    CONSTRAINT agent_platform_rates_multiplier_check
        CHECK (rate_multiplier >= 0)
);

CREATE TABLE IF NOT EXISTS agent_group_models (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    platform VARCHAR(32) NOT NULL,
    model_code VARCHAR(255) NOT NULL,
    media_type VARCHAR(16) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    available BOOLEAN NOT NULL DEFAULT TRUE,
    excluded BOOLEAN NOT NULL DEFAULT FALSE,
    excluded_at TIMESTAMPTZ,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_group_models_platform_check
        CHECK (platform IN ('openai', 'anthropic', 'gemini', 'seedance')),
    CONSTRAINT agent_group_models_media_type_check
        CHECK (media_type IN ('text', 'image', 'video')),
    CONSTRAINT agent_group_models_code_not_blank_check
        CHECK (BTRIM(model_code) <> ''),
    CONSTRAINT agent_group_models_exclusion_check
        CHECK ((excluded AND excluded_at IS NOT NULL AND NOT enabled)
            OR (NOT excluded AND excluded_at IS NULL)),
    UNIQUE (group_id, platform, model_code)
);

CREATE INDEX IF NOT EXISTS idx_agent_group_models_public_catalog
    ON agent_group_models (group_id, enabled, available, media_type)
    WHERE excluded = FALSE;

CREATE INDEX IF NOT EXISTS idx_agent_group_models_last_seen
    ON agent_group_models (group_id, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS agent_model_prices (
    id BIGSERIAL PRIMARY KEY,
    agent_model_id BIGINT NOT NULL REFERENCES agent_group_models(id) ON DELETE CASCADE,
    resolution VARCHAR(32) NOT NULL,
    billing_unit VARCHAR(16) NOT NULL,
    unit_price NUMERIC(20,10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_model_prices_resolution_not_blank_check
        CHECK (BTRIM(resolution) <> ''),
    CONSTRAINT agent_model_prices_billing_unit_check
        CHECK (billing_unit IN ('image', 'second')),
    CONSTRAINT agent_model_prices_unit_price_check
        CHECK (unit_price >= 0),
    UNIQUE (agent_model_id, resolution)
);

CREATE INDEX IF NOT EXISTS idx_agent_model_prices_lookup
    ON agent_model_prices (agent_model_id, resolution);

CREATE TABLE IF NOT EXISTS agent_model_pricing (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model_code VARCHAR(120) NOT NULL,
    platform VARCHAR(50) NOT NULL,
    media_type VARCHAR(16) NOT NULL CHECK (media_type IN ('text', 'image', 'video')),
    resolution VARCHAR(32) NOT NULL DEFAULT '',
    unit_price DECIMAL(20,10) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    input_price_per_million DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (input_price_per_million >= 0),
    output_price_per_million DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (output_price_per_million >= 0),
    cache_write_price_per_million DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (cache_write_price_per_million >= 0),
    cache_read_price_per_million DECIMAL(20,8) NOT NULL DEFAULT 0 CHECK (cache_read_price_per_million >= 0),
    rate_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1 CHECK (rate_multiplier >= 0),
    reference_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1 CHECK (reference_multiplier >= 0),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_model_pricing_unique UNIQUE (group_id, model_code, media_type, resolution),
    CONSTRAINT agent_model_pricing_shape CHECK (
        (media_type = 'text' AND resolution = '') OR
        (media_type IN ('image', 'video') AND resolution <> '')
    )
);

CREATE INDEX IF NOT EXISTS idx_agent_model_pricing_group
    ON agent_model_pricing(group_id, media_type, model_code);

-- Preserve any Seedance prices configured before the unified Agent pricing UI existed.
INSERT INTO agent_model_pricing(
    group_id, model_code, platform, media_type, resolution, unit_price,
    rate_multiplier, reference_multiplier, enabled
)
SELECT
    vgpr.group_id,
    vgpr.model_code,
    'video',
    'video',
    vgpr.resolution,
    vgpr.credits_per_second,
    1,
    vgpr.reference_video_multiplier,
    vgpr.enabled
FROM video_group_pricing_rules vgpr
JOIN groups g ON g.id = vgpr.group_id
WHERE g.kind = 'agent'
  AND g.deleted_at IS NULL
ON CONFLICT (group_id, model_code, media_type, resolution) DO NOTHING;

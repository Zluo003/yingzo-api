-- MiniMax reference-image allowance is shared by all resolutions of one Agent model.
ALTER TABLE agent_group_models
    ADD COLUMN IF NOT EXISTS free_input_images integer CHECK (free_input_images >= 0);

-- Consolidate any prior per-resolution allowances to one model-wide value.
-- When older rows disagree, use the lowest allowance so migration cannot reduce
-- charges for requests that previously had fewer free images.
WITH configured AS (
    SELECT group_id, model_code, MIN(free_input_images) AS free_images
    FROM video_group_pricing_rules
    WHERE model_code IN ('minimax-h3', 'minimax-h3-max')
    GROUP BY group_id, model_code
)
UPDATE video_group_pricing_rules AS rule
SET free_input_images = configured.free_images,
    credits_per_input_image = 0
FROM configured
WHERE rule.group_id = configured.group_id
  AND rule.model_code = configured.model_code;

-- Carry the consolidated allowance into Agent model rows, where the setting
-- belongs to the model rather than to each resolution price.
UPDATE agent_group_models AS model
SET free_input_images = configured.free_images
FROM (
    SELECT group_id, model_code, MIN(free_input_images) AS free_images
    FROM video_group_pricing_rules
    WHERE model_code IN ('minimax-h3', 'minimax-h3-max')
    GROUP BY group_id, model_code
) AS configured
WHERE model.group_id = configured.group_id
  AND model.model_code = configured.model_code
  AND model.media_type = 'video';

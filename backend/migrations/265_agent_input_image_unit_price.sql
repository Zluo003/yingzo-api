-- MiniMax H3 / H3 Max overage input images can carry a model-wide per-image
-- unit price on Agent models. NULL keeps the legacy behaviour: overage images
-- are billed at the selected resolution's per-second unit price.
ALTER TABLE agent_group_models
    ADD COLUMN IF NOT EXISTS input_image_unit_price double precision
        CHECK (input_image_unit_price IS NULL OR input_image_unit_price >= 0);

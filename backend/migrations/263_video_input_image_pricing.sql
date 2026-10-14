-- NULL free allowance uses model defaults (H3=5, H3 Max=2).
ALTER TABLE video_group_pricing_rules
    ADD COLUMN IF NOT EXISTS free_input_images integer CHECK (free_input_images >= 0),
    ADD COLUMN IF NOT EXISTS credits_per_input_image decimal(20,10) NOT NULL DEFAULT 0 CHECK (credits_per_input_image >= 0);

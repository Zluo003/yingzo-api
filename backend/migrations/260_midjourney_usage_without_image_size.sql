-- Midjourney counts one imagine grid or one selection, billed per request.
-- Its operation is recorded in model, not as a pixel-resolution image_size.
-- Preserve the size requirement for all other image usage and existing video
-- exemptions. This is forward-only: migration 259 may already be deployed.
ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_image_billing_size_check
    CHECK (
        image_count <= 0
        OR billing_mode = 'video'
        OR COALESCE(video_count, 0) > 0
        OR (
            billing_mode = 'per_request'
            AND model IN (
                'midjourney-v8.2:imagine_relax',
                'midjourney-v8.2:imagine_fast',
                'midjourney-v8.2:imagine_turbo',
                'midjourney-v8.2:upscale_fast'
            )
        )
        OR (
            image_size IS NOT NULL
            AND image_size IN ('1K', '2K', '4K', 'mixed')
        )
    ) NOT VALID;

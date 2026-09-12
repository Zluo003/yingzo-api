-- Backfill endpoint and async duration metadata for video usage rows.

UPDATE usage_logs ul
SET inbound_endpoint = COALESCE(NULLIF(ul.inbound_endpoint, ''), '/v1/videos'),
    upstream_endpoint = COALESCE(NULLIF(ul.upstream_endpoint, ''), '/v1/videos'),
    duration_ms = COALESCE(
        ul.duration_ms,
        GREATEST(
            0,
            FLOOR(
                EXTRACT(EPOCH FROM (
                    COALESCE(v.completed_at, v.refunded_at, v.updated_at) - v.created_at
                )) * 1000
            )::INTEGER
        )
    )
FROM video_tasks v
WHERE ul.video_task_id = v.public_id
  AND ul.billing_mode = 'video_duration'
  AND v.status IN ('completed', 'failed', 'cancelled')
  AND v.created_at IS NOT NULL
  AND COALESCE(v.completed_at, v.refunded_at, v.updated_at) IS NOT NULL;

UPDATE usage_logs
SET inbound_endpoint = COALESCE(NULLIF(inbound_endpoint, ''), '/v1/videos'),
    upstream_endpoint = COALESCE(NULLIF(upstream_endpoint, ''), '/v1/videos')
WHERE billing_mode = 'video_duration'
  AND video_task_id IS NOT NULL;

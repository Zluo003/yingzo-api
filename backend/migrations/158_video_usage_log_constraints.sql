-- Allow the request/platform enum values already used by the application and
-- repair video usage rows that were billed while the old database checks were
-- still rejecting request_type=5 and platform='video'.

ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type IN (0, 1, 2, 3, 4, 5)) NOT VALID;

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'video')) NOT VALID;

WITH video_usage_source AS (
    SELECT
        v.public_id,
        v.user_id,
        v.api_key_id,
        v.account_id,
        v.group_id,
        v.model,
        v.upstream_model,
        v.resolution,
        v.duration_seconds,
        v.reference_duration_seconds,
        v.billable_seconds,
        v.total_cost,
        v.actual_cost,
        v.billed_at,
        v.created_at,
        COALESCE(g.rate_multiplier, 1) AS group_rate_multiplier,
        g.subscription_type,
        COALESCE(a.rate_multiplier, 1) AS account_rate_multiplier,
        us.id AS subscription_id
    FROM video_tasks v
    JOIN groups g ON g.id = v.group_id
    JOIN accounts a ON a.id = v.account_id
    LEFT JOIN LATERAL (
        SELECT id
        FROM user_subscriptions us
        WHERE us.user_id = v.user_id
          AND us.group_id = v.group_id
          AND us.deleted_at IS NULL
        ORDER BY us.id DESC
        LIMIT 1
    ) us ON TRUE
    WHERE v.billed_at IS NOT NULL
)
INSERT INTO usage_logs (
    user_id,
    api_key_id,
    account_id,
    request_id,
    model,
    requested_model,
    upstream_model,
    group_id,
    subscription_id,
    input_tokens,
    output_tokens,
    cache_creation_tokens,
    cache_read_tokens,
    cache_creation_5m_tokens,
    cache_creation_1h_tokens,
    image_output_tokens,
    image_output_cost,
    input_cost,
    output_cost,
    cache_creation_cost,
    cache_read_cost,
    total_cost,
    actual_cost,
    rate_multiplier,
    account_rate_multiplier,
    billing_type,
    request_type,
    stream,
    openai_ws_mode,
    image_count,
    cache_ttl_overridden,
    billing_mode,
    video_task_id,
    video_resolution,
    video_duration_seconds,
    video_reference_duration_seconds,
    video_billable_seconds,
    created_at
)
SELECT
    v.user_id,
    v.api_key_id,
    v.account_id,
    'video:' || v.public_id,
    v.model,
    v.model,
    NULLIF(v.upstream_model, v.model),
    v.group_id,
    CASE WHEN v.subscription_type = 'subscription' THEN v.subscription_id ELSE NULL END,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    v.total_cost,
    0,
    0,
    v.total_cost,
    v.actual_cost,
    v.group_rate_multiplier,
    v.account_rate_multiplier,
    CASE WHEN v.subscription_type = 'subscription' AND v.subscription_id IS NOT NULL THEN 1 ELSE 0 END,
    5,
    FALSE,
    FALSE,
    0,
    FALSE,
    'video_duration',
    v.public_id,
    v.resolution,
    v.duration_seconds,
    v.reference_duration_seconds,
    v.billable_seconds,
    COALESCE(v.billed_at, v.created_at)
FROM video_usage_source v
ON CONFLICT (request_id, api_key_id) DO NOTHING;

WITH video_completion_source AS (
    SELECT
        v.public_id,
        v.user_id,
        v.api_key_id,
        v.account_id,
        v.group_id,
        v.model,
        v.upstream_model,
        v.resolution,
        v.duration_seconds,
        v.reference_duration_seconds,
        v.billable_seconds,
        v.result_video_url,
        v.completed_at,
        v.updated_at,
        v.billed_at,
        v.created_at,
        COALESCE(g.rate_multiplier, 1) AS group_rate_multiplier,
        g.subscription_type,
        COALESCE(a.rate_multiplier, 1) AS account_rate_multiplier,
        us.id AS subscription_id
    FROM video_tasks v
    JOIN groups g ON g.id = v.group_id
    JOIN accounts a ON a.id = v.account_id
    LEFT JOIN LATERAL (
        SELECT id
        FROM user_subscriptions us
        WHERE us.user_id = v.user_id
          AND us.group_id = v.group_id
          AND us.deleted_at IS NULL
        ORDER BY us.id DESC
        LIMIT 1
    ) us ON TRUE
    WHERE v.billed_at IS NOT NULL
      AND v.status = 'completed'
      AND v.result_video_url IS NOT NULL
)
INSERT INTO usage_logs (
    user_id,
    api_key_id,
    account_id,
    request_id,
    model,
    requested_model,
    upstream_model,
    group_id,
    subscription_id,
    input_tokens,
    output_tokens,
    cache_creation_tokens,
    cache_read_tokens,
    cache_creation_5m_tokens,
    cache_creation_1h_tokens,
    image_output_tokens,
    image_output_cost,
    input_cost,
    output_cost,
    cache_creation_cost,
    cache_read_cost,
    total_cost,
    actual_cost,
    rate_multiplier,
    account_rate_multiplier,
    billing_type,
    request_type,
    stream,
    openai_ws_mode,
    image_count,
    cache_ttl_overridden,
    billing_mode,
    video_task_id,
    video_resolution,
    video_duration_seconds,
    video_reference_duration_seconds,
    video_billable_seconds,
    video_result_url,
    created_at
)
SELECT
    v.user_id,
    v.api_key_id,
    v.account_id,
    'video:' || v.public_id || ':completed',
    v.model,
    v.model,
    NULLIF(v.upstream_model, v.model),
    v.group_id,
    CASE WHEN v.subscription_type = 'subscription' THEN v.subscription_id ELSE NULL END,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    v.group_rate_multiplier,
    v.account_rate_multiplier,
    CASE WHEN v.subscription_type = 'subscription' AND v.subscription_id IS NOT NULL THEN 1 ELSE 0 END,
    5,
    FALSE,
    FALSE,
    0,
    FALSE,
    'video_duration',
    v.public_id,
    v.resolution,
    v.duration_seconds,
    v.reference_duration_seconds,
    v.billable_seconds,
    v.result_video_url,
    COALESCE(v.completed_at, v.updated_at, v.billed_at, v.created_at)
FROM video_completion_source v
ON CONFLICT (request_id, api_key_id) DO NOTHING;

WITH video_refund_source AS (
    SELECT
        v.public_id,
        v.user_id,
        v.api_key_id,
        v.account_id,
        v.group_id,
        v.model,
        v.upstream_model,
        v.resolution,
        v.duration_seconds,
        v.reference_duration_seconds,
        v.billable_seconds,
        v.total_cost,
        v.actual_cost,
        v.refunded_at,
        COALESCE(g.rate_multiplier, 1) AS group_rate_multiplier,
        g.subscription_type,
        COALESCE(a.rate_multiplier, 1) AS account_rate_multiplier,
        us.id AS subscription_id
    FROM video_tasks v
    JOIN groups g ON g.id = v.group_id
    JOIN accounts a ON a.id = v.account_id
    LEFT JOIN LATERAL (
        SELECT id
        FROM user_subscriptions us
        WHERE us.user_id = v.user_id
          AND us.group_id = v.group_id
          AND us.deleted_at IS NULL
        ORDER BY us.id DESC
        LIMIT 1
    ) us ON TRUE
    WHERE v.billed_at IS NOT NULL
      AND v.refunded_at IS NOT NULL
)
INSERT INTO usage_logs (
    user_id,
    api_key_id,
    account_id,
    request_id,
    model,
    requested_model,
    upstream_model,
    group_id,
    subscription_id,
    input_tokens,
    output_tokens,
    cache_creation_tokens,
    cache_read_tokens,
    cache_creation_5m_tokens,
    cache_creation_1h_tokens,
    image_output_tokens,
    image_output_cost,
    input_cost,
    output_cost,
    cache_creation_cost,
    cache_read_cost,
    total_cost,
    actual_cost,
    rate_multiplier,
    account_rate_multiplier,
    billing_type,
    request_type,
    stream,
    openai_ws_mode,
    image_count,
    cache_ttl_overridden,
    billing_mode,
    video_task_id,
    video_resolution,
    video_duration_seconds,
    video_reference_duration_seconds,
    video_billable_seconds,
    created_at
)
SELECT
    v.user_id,
    v.api_key_id,
    v.account_id,
    'video:' || v.public_id || ':refund',
    v.model,
    v.model,
    NULLIF(v.upstream_model, v.model),
    v.group_id,
    CASE WHEN v.subscription_type = 'subscription' THEN v.subscription_id ELSE NULL END,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    0,
    -v.total_cost,
    0,
    0,
    -v.total_cost,
    -v.actual_cost,
    v.group_rate_multiplier,
    v.account_rate_multiplier,
    CASE WHEN v.subscription_type = 'subscription' AND v.subscription_id IS NOT NULL THEN 1 ELSE 0 END,
    5,
    FALSE,
    FALSE,
    0,
    FALSE,
    'video_duration',
    v.public_id,
    v.resolution,
    v.duration_seconds,
    v.reference_duration_seconds,
    v.billable_seconds,
    v.refunded_at
FROM video_refund_source v
ON CONFLICT (request_id, api_key_id) DO NOTHING;

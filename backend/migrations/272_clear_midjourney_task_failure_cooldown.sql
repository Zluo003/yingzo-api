-- Task failures no longer quarantine the entire Discord MJ account. Remove only
-- the cooldown written by the previous MJ executor, preserving administrator
-- status/scheduling choices and unrelated overload, quota or rate-limit state.
WITH recovered AS (
    UPDATE accounts
    SET temp_unschedulable_until = NULL,
        temp_unschedulable_reason = NULL,
        updated_at = NOW()
    WHERE deleted_at IS NULL
      AND extra->>'image_provider' = 'discord_midjourney'
      AND temp_unschedulable_reason = 'Discord task failed; check MJ session, subscription and channel'
    RETURNING id
)
INSERT INTO scheduler_outbox (event_type, account_id)
SELECT 'account_changed', id FROM recovered;

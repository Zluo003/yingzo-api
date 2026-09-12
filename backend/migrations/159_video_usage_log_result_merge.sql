-- Merge completed video result URLs into the original billable usage rows.
-- Older builds wrote a separate zero-cost `video:<task_id>:completed` row that
-- made successful tasks look free in usage lists.

UPDATE usage_logs charge
SET video_result_url = v.result_video_url
FROM video_tasks v
WHERE charge.request_id = 'video:' || v.public_id
  AND charge.api_key_id = v.api_key_id
  AND charge.billing_mode = 'video_duration'
  AND charge.video_task_id = v.public_id
  AND v.result_video_url IS NOT NULL
  AND v.result_video_url <> ''
  AND (charge.video_result_url IS NULL OR charge.video_result_url = '');

DELETE FROM usage_logs ul
WHERE ul.billing_mode = 'video_duration'
  AND ul.request_type = 5
  AND ul.video_task_id IS NOT NULL
  AND ul.request_id LIKE 'video:%:completed'
  AND COALESCE(ul.actual_cost, 0) = 0
  AND COALESCE(ul.total_cost, 0) = 0;

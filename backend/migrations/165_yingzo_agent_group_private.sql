UPDATE groups
SET is_exclusive = true,
    updated_at = NOW()
WHERE kind = 'agent'
  AND system_code = 'yingzo'
  AND deleted_at IS NULL
  AND is_exclusive = false;

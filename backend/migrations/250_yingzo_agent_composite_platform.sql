-- Yingzo Agent is a multi-provider routing group.  Older foundation/restore
-- migrations seeded it as openai, which makes admin channel pricing associate
-- only with OpenAI rows and prevents pricing for the other provider channels
-- from being selected reliably.
--
-- Keep this repair scoped to rows carrying the stable system marker
-- (including soft-deleted rows so a later restore cannot resurrect the stale
-- platform value). The startup self-healing path handles rows whose marker
-- was manually removed.
UPDATE groups
SET platform = 'composite',
    updated_at = NOW()
WHERE system_code = 'yingzo'
  AND platform IS DISTINCT FROM 'composite';

-- 临时素材分两类：下游上传的参考素材（reference）与上游回捞的生成产物（generated）。
-- 两类各有独立的保存时长与容量预算，参考素材的容量压力不应挤掉已交付的产物。
ALTER TABLE temporary_assets ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'reference';

-- 存量行按 metadata.source 回填：产物的 metadata 由 TemporaryAssetPublisher 写入。
UPDATE temporary_assets
SET purpose = 'generated'
WHERE purpose = 'reference'
  AND metadata->>'source' IN ('generated', 'generated_video');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'temporary_assets_purpose_check'
    ) THEN
        ALTER TABLE temporary_assets
            ADD CONSTRAINT temporary_assets_purpose_check CHECK (purpose IN ('reference', 'generated'));
    END IF;
END $$;

-- 按类别的容量统计与"最早失效优先"驱逐都命中这个索引。
CREATE INDEX IF NOT EXISTS idx_temporary_assets_purpose_expiry
    ON temporary_assets(purpose, GREATEST(expires_at, lease_until))
    WHERE deleted_at IS NULL;

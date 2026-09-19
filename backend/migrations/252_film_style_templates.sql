-- Permanent film style catalogue. This is deliberately separate from
-- temporary_assets: style previews are product configuration and must not be
-- removed by the reference/result retention workers.
CREATE TABLE IF NOT EXISTS film_style_templates (
  id TEXT PRIMARY KEY,
    category TEXT NOT NULL CHECK (category IN ('realistic', '3d', '2d')),
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 160),
    prompt TEXT NOT NULL CHECK (length(trim(prompt)) BETWEEN 1 AND 50000),
    storage_key TEXT NOT NULL UNIQUE CHECK (length(trim(storage_key)) > 0),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg', 'image/png', 'image/webp', 'image/gif')),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    sort_order INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_film_style_templates_catalog
    ON film_style_templates(status, category, sort_order, updated_at, id);

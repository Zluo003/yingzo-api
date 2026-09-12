package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 241 迁移把素材分成参考素材与生成产物两类，并回填存量产物行。
func TestTemporaryAssetPurposeMigration(t *testing.T) {
	content, err := FS.ReadFile("241_temporary_asset_purpose.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'reference'")
	require.Contains(t, sql, "AND metadata->>'source' IN ('generated', 'generated_video')")
	require.Contains(t, sql, "CHECK (purpose IN ('reference', 'generated'))")
	require.Contains(t, sql, "idx_temporary_assets_purpose_expiry")
}

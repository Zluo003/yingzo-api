package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration196UpdatesJingyuSeedanceModelMappings(t *testing.T) {
	content, err := FS.ReadFile("196_update_jingyu_seedance_models.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "extra->>'video_provider' = 'jingyu'")
	require.Contains(t, sql, "'seedance-api-2.0'")
	require.Contains(t, sql, "'jing-video-2-pro'")
	require.Contains(t, sql, "{\"seedance-2.0\":\"yu-video-2-pro\"}")
	require.Contains(t, sql, "{\"seedance-2.5\":\"yu-video-2.5-pro\"}")
	require.Contains(t, sql, "WHEN credentials->'model_mapping'->>'seedance-2.5' IS NULL")
	require.Contains(t, sql, "ELSE '{}'::jsonb")
}

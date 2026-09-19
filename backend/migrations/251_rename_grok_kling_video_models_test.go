package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 251：下游视频模型改名（grok-imagine-video-1.5-preview -> grok-imagine-video-1.5，
// kling-video-v3-omni -> kling-v3-omni）。所有保存下游模型名的位置都必须同步：
// 账号 model_mapping（键 + 恒等值）、账号分辨率/时长白名单的模型键、两张定价表、
// 分组白名单数组。锁住新旧名字与覆盖面，防止迁移静默漏掉某一处。
func TestMigration251RenamesGrokKlingVideoModelNames(t *testing.T) {
	content, err := FS.ReadFile("251_rename_grok_kling_video_models.sql")
	require.NoError(t, err)

	sql := string(content)
	// 新旧名字成对出现。
	require.Contains(t, sql, "'grok-imagine-video-1.5-preview'")
	require.Contains(t, sql, "'grok-imagine-video-1.5'")
	require.Contains(t, sql, "'kling-video-v3-omni'")
	require.Contains(t, sql, "'kling-v3-omni'")

	// 覆盖面：账号映射、两个 extra 白名单、两张定价表、分组白名单。
	require.Contains(t, sql, "credentials->'model_mapping'")
	require.Contains(t, sql, "extra->'video_model_resolutions'")
	require.Contains(t, sql, "extra->'video_model_durations'")
	require.Contains(t, sql, "UPDATE video_group_pricing_rules")
	require.Contains(t, sql, "UPDATE agent_model_pricing")
	require.Contains(t, sql, "media_type = 'video'")
	require.Contains(t, sql, "UPDATE groups")
	require.Contains(t, sql, "model_allowlist->'models'")

	// 视频账号限定在 video 平台。
	require.Contains(t, sql, "platform = 'video'")
}

package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 253：Agent 目录删除语义改为硬删除后，一次性解除存量的软排除行。锁住迁移
// 的覆盖面：只清 excluded 标记，不动 enabled/available（回来的是"未启用"行，
// 由管理员重新启用，而不是静默重新上架）。
func TestMigration253ClearsAgentModelExclusions(t *testing.T) {
	content, err := FS.ReadFile("253_agent_group_models_clear_exclusions.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "UPDATE agent_group_models")
	require.Contains(t, sql, "SET excluded = FALSE, excluded_at = NULL")
	require.Contains(t, sql, "WHERE excluded = TRUE")
	require.NotContains(t, sql, "enabled")
	require.NotContains(t, sql, "available")
}

//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 线上故障回归：系统内置聚合分组只在迁移里种入一次，之后被删/停用就没有自愈路径，
// 表现成用户端"创建 API Key 时找不到 Yingzo Agent 分组"，而全新安装的库却正常。
// 启动自愈必须把三种坏状态都修回来。
func TestEnsureSystemAgentGroupHealsBrokenStates(t *testing.T) {
	ctx := context.Background()
	groupID := seededSystemAgentGroupID(t)

	liveAgentGroupCount := func() int {
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM groups
			 WHERE system_code = 'yingzo' AND kind = 'agent' AND deleted_at IS NULL AND status = 'active'
		`).Scan(&count))
		return count
	}
	require.Equal(t, 1, liveAgentGroupCount(), "迁移种入后应当只有一条存活 agent 分组")

	// 1) 被软删（历史故障形态）。
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at = NOW() WHERE id = $1`, groupID)
	require.NoError(t, err)
	require.Equal(t, 0, liveAgentGroupCount())

	healed, err := EnsureSystemAgentGroup(ctx, integrationDB)
	require.NoError(t, err)
	require.Equal(t, groupID, healed, "恢复的是原来那一行，不是新插一条")
	require.Equal(t, 1, liveAgentGroupCount())

	// 2) 存活但被停用 / 标记被清掉：必须在 /groups/available（ListActive）里重新出现。
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE groups SET status = 'inactive', kind = 'standard', system_code = '', is_exclusive = TRUE
		 WHERE id = $1
	`, groupID)
	require.NoError(t, err)
	require.Equal(t, 0, liveAgentGroupCount())

	healed, err = EnsureSystemAgentGroup(ctx, integrationDB)
	require.NoError(t, err)
	require.Equal(t, groupID, healed)
	require.Equal(t, 1, liveAgentGroupCount())

	var kind, systemCode, status string
	var exclusive bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT kind, system_code, status, is_exclusive FROM groups WHERE id = $1
	`, groupID).Scan(&kind, &systemCode, &status, &exclusive))
	require.Equal(t, "agent", kind)
	require.Equal(t, "yingzo", systemCode)
	require.Equal(t, "active", status)
	require.False(t, exclusive, "agent 分组不能是专属分组，否则普通用户绑定不到（/groups/available 会过滤掉）")

	// 3) 行被硬删（级联删除会连带 account_groups）：补种一行。
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	require.NoError(t, err)
	require.Equal(t, 0, liveAgentGroupCount())

	healed, err = EnsureSystemAgentGroup(ctx, integrationDB)
	require.NoError(t, err)
	require.NotZero(t, healed)
	require.NotEqual(t, groupID, healed, "硬删后只能补种新行")
	require.Equal(t, 1, liveAgentGroupCount())

	// 幂等：再跑一次不变。
	again, err := EnsureSystemAgentGroup(ctx, integrationDB)
	require.NoError(t, err)
	require.Equal(t, healed, again)
	require.Equal(t, 1, liveAgentGroupCount())
}

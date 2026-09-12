//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 系统内置分组（Yingzo Agent）靠 groups.kind + groups.system_code 识别。
// 这两个字段必须真的从数据库回填到 service.Group，否则 IsAgent() 恒为 false，
// Agent 的计价、模型目录与账号调度会整体失效——单元测试用的是内存构造的 Group，
// 覆盖不到这条链路，所以在这里用真实数据库盯住它。
func TestGroupRepositoryHydratesSystemAgentKindAndCode(t *testing.T) {
	ctx := context.Background()
	repo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)

	// system_code 上有"未删除行唯一"的部分索引，迁移已经种了一个 yingzo 分组，
	// 这里用带随机后缀的 code，避免与种子数据或并发用例撞车。
	systemCode := "agent-test-" + uuid.NewString()

	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (name, description, platform, status, rate_multiplier, is_exclusive, kind, system_code, allow_image_generation)
		VALUES ('Agent kind hydration', '', 'openai', 'active', 1, false, 'agent', $1, TRUE)
		RETURNING id
	`, systemCode).Scan(&groupID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, groupID)
	})

	group, err := repo.GetByID(ctx, groupID)
	require.NoError(t, err)
	require.Equal(t, "agent", group.Kind)
	require.Equal(t, systemCode, group.SystemCode)
	require.True(t, group.IsAgent(), "the whole Agent feature keys off IsAgent()")

	lite, err := repo.GetByIDLite(ctx, groupID)
	require.NoError(t, err)
	require.True(t, lite.IsAgent(), "the catalog service validates the group through GetByIDLite")

	// 普通分组不能被误判成系统分组。
	var plainID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO groups (name, description, platform, status, rate_multiplier, is_exclusive)
		VALUES ('Agent kind hydration plain', '', 'openai', 'active', 1, false)
		RETURNING id
	`).Scan(&plainID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, plainID)
	})
	plain, err := repo.GetByID(ctx, plainID)
	require.NoError(t, err)
	require.False(t, plain.IsAgent())
}

//go:build integration

package repository

// 投影漏列回归（repository 半程）：GetByKeyForAuth 是网关请求识别"系统内置聚合分组"
// 的唯一数据来源，它的分组显式投影必须带出 kind / system_code。漏选时 Group.IsAgent()
// 为 false，聚合分组的按模型分发、聚合计价与手工声明模型放行会整体静默失效——
// 而且只在认证快照未命中时复现（缓存里有这两个字段），属于最难查的一类问题。

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetByKeyForAuthCarriesAgentGroupKindProjection(t *testing.T) {
	ctx := context.Background()
	// 迁移 245 的唯一索引只允许一条存活 agent 分组，直接复用迁移种入的系统分组。
	groupID := seededSystemAgentGroupID(t)
	suffix := time.Now().UnixNano()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("agent-kind-proj-%d@example.com", suffix), Concurrency: 5,
	})
	keyValue := fmt.Sprintf("sk-agent-kind-proj-%d", suffix)
	apiKeyRepo := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: user.ID, GroupID: &groupID, Key: keyValue, Name: "agent-kind-proj", Status: service.StatusActive}
	require.NoError(t, apiKeyRepo.Create(ctx, key))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", keyValue)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
	})

	got, err := apiKeyRepo.GetByKeyForAuth(ctx, keyValue)
	require.NoError(t, err)
	require.NotNil(t, got.Group, "认证查询必须带出分组")
	require.Equal(t, "agent", got.Group.Kind, "kind 必须进入认证投影")
	require.Equal(t, "yingzo", got.Group.SystemCode, "system_code 必须进入认证投影")
	require.True(t, got.Group.IsAgent(), "认证拿到的分组必须自认是聚合分组")
}

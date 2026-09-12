package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 命中认证缓存的请求拿到的 Group 必须仍然是 Agent 分组：Agent 的计价、模型目录
// 与账号调度全部以 Group.IsAgent() 为开关，快照少带 kind/system_code 会让这些
// 请求静默退化成普通分组计费。
func TestAPIKeyAuthSnapshotGroupAgentIdentityRoundtrip(t *testing.T) {
	groupID := int64(51)
	apiKey := &APIKey{
		ID: 83, UserID: 41, GroupID: &groupID, Key: "sk-agent-roundtrip", Status: StatusActive,
		User: &User{ID: 41, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "Yingzo Agent", Platform: PlatformOpenAI, Status: StatusActive,
			Hydrated: true, Kind: "agent", SystemCode: "yingzo",
		},
	}
	svc := &APIKeyService{}

	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))

	materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.NotNil(t, materialized.Group)
	require.Equal(t, "agent", materialized.Group.Kind)
	require.Equal(t, "yingzo", materialized.Group.SystemCode)
	require.True(t, materialized.Group.IsAgent())
}

package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 系统内置聚合分组要在两端都能被识别：
//   - 管理端：前端据此把「Yingzo Agent」定位出来并隐藏删除入口；
//   - 用户端：Yingzo Web 新建 API Key 时要把 Key 默认绑定到该分组，而它拿到的是
//     /groups/available（用户 DTO）。因此 kind / system_code 必须一并下发，否则
//     前端只能靠分组名猜，改名就失效。
func TestGroupMappersExposeAgentIdentityOnBothSides(t *testing.T) {
	group := &service.Group{
		ID: 7, Name: "Yingzo Agent", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, RateMultiplier: 1,
		Kind: "agent", SystemCode: "yingzo",
	}

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"kind":"agent"`)
	require.Contains(t, string(adminJSON), `"system_code":"yingzo"`)

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)

	var user Group
	require.NoError(t, json.Unmarshal(userJSON, &user))
	require.Equal(t, "agent", user.Kind)
	require.Equal(t, "yingzo", user.SystemCode)
}

// 普通分组不下发这两个字段：它们只对系统内置分组有意义，避免污染普通分组响应。
func TestGroupFromServiceOmitsAgentIdentityForStandardGroups(t *testing.T) {
	group := &service.Group{
		ID: 8, Name: "standard", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, RateMultiplier: 1,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), `"kind"`)
	require.NotContains(t, string(userJSON), `"system_code"`)
}

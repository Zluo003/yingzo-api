package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 管理端要能识别系统内置聚合分组：前端据此把「Yingzo Agent」定位出来并隐藏删除入口。
// 用户侧 DTO 不带这两个字段（dto.Group），避免把内部标识下放。
func TestGroupFromServiceAdminExposesAgentIdentity(t *testing.T) {
	group := &service.Group{
		ID: 7, Name: "Yingzo Agent", Platform: service.PlatformOpenAI,
		Status: service.StatusActive, RateMultiplier: 1,
		Kind: "agent", SystemCode: "yingzo",
	}

	adminEnvelope := struct {
		Data *AdminGroup `json:"data"`
	}{}
	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(`{"data":`+string(adminJSON)+`}`), &adminEnvelope))
	require.Equal(t, "agent", adminEnvelope.Data.Kind)
	require.Equal(t, "yingzo", adminEnvelope.Data.SystemCode)

	userEnvelope := struct {
		Data *Group `json:"data"`
	}{}
	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(`{"data":`+string(userJSON)+`}`), &userEnvelope))
	require.NotContains(t, string(userJSON), "system_code")
	require.NotContains(t, string(userJSON), "kind")
}

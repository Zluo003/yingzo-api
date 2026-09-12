package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Agent 目录是管理端 /admin/groups/:id/agent-models* 的唯一依赖：装配时漏传
// （NewGroupHandlerWithConfig 的 agentModels 为 nil）会让这几个接口全部 400
// "Agent model catalog is not configured"，而真实请求直到管理员点开页面才暴露。
// 这里把"依赖必须非 nil"钉在构造器上。
func TestGroupHandlerAgentCatalogDependencyIsRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(2)

	newContext := func() (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/2/agent-models", nil)
		c.Params = gin.Params{{Key: "id", Value: "2"}}
		return c, recorder
	}

	// 未注入依赖：明确拒绝，而不是 panic。
	handlerWithoutCatalog := NewGroupHandler(nil, nil, nil)
	c, recorder := newContext()
	handlerWithoutCatalog.GetAgentModels(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Agent model catalog is not configured")

	// 注入依赖后不再以"未配置"拒绝（这里用一个可控的仓库桩走通到服务层）。
	catalog := service.NewAgentModelCatalogService(
		nil,
		&groupHandlerAgentGroupRepoStub{group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}},
		&groupHandlerAgentModelRepoStub{},
	)
	handler := NewGroupHandlerWithConfig(nil, nil, nil, nil, catalog)
	c, recorder = newContext()
	handler.GetAgentModels(c)
	require.NotContains(t, recorder.Body.String(), "Agent model catalog is not configured")
}

type groupHandlerAgentGroupRepoStub struct {
	service.GroupRepository
	group *service.Group
}

func (s *groupHandlerAgentGroupRepoStub) GetByIDLite(context.Context, int64) (*service.Group, error) {
	copy := *s.group
	return &copy, nil
}

type groupHandlerAgentModelRepoStub struct {
	service.AgentModelRepository
}

func (s *groupHandlerAgentModelRepoStub) ListModels(context.Context, int64, bool) ([]service.AgentGroupModel, error) {
	return []service.AgentGroupModel{}, nil
}

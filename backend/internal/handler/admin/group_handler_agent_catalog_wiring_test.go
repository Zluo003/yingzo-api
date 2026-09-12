package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

// 手工添加图片模型的路由必须真的接上：handler 返回的目录要包含新模型，
// 否则管理员点了按钮只看到"成功"却什么都没发生。
func TestGroupHandlerCreateAgentModelCreatesManualImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(2)
	catalog := service.NewAgentModelCatalogService(
		&groupHandlerAgentAccountRepoStub{},
		&groupHandlerAgentGroupRepoStub{group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}},
		&groupHandlerAgentCreateModelRepoStub{},
	)
	handler := NewGroupHandlerWithConfig(nil, nil, nil, nil, catalog)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"platform":"gemini","model_code":"gemini-3-pro-image","enabled":true,` +
		`"prices":[{"resolution":"1K","unit_price":0.3}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/2/agent-models", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "2"}}
	handler.CreateAgentModel(c)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "gemini-3-pro-image")
	require.Contains(t, recorder.Body.String(), `"manual":true`)
}

type groupHandlerAgentAccountRepoStub struct {
	service.AccountRepository
}

type groupHandlerAgentCreateModelRepoStub struct {
	service.AgentModelRepository
	models []service.AgentGroupModel
	nextID int64
}

func (s *groupHandlerAgentCreateModelRepoStub) ListModels(context.Context, int64, bool) ([]service.AgentGroupModel, error) {
	return append([]service.AgentGroupModel(nil), s.models...), nil
}

func (s *groupHandlerAgentCreateModelRepoStub) CreateManual(_ context.Context, model *service.AgentGroupModel, prices []service.AgentModelPrice) error {
	s.nextID++
	model.ID = s.nextID
	stored := *model
	stored.Prices = append([]service.AgentModelPrice(nil), prices...)
	s.models = append(s.models, stored)
	return nil
}

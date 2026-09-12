package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func agentAuthForTest(agent bool) func(*gin.Context) {
	return func(c *gin.Context) {
		kind, code := "standard", ""
		if agent {
			kind, code = "agent", "yingzo"
		}
		groupID := int64(9)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Kind: kind, SystemCode: code}})
		c.Next()
	}
}

func TestAgentRoutesRejectOrdinaryAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/api/v1")
	h := &handler.Handlers{Agent: &handler.AgentHandler{}}
	RegisterAgentRoutes(r, v, h, agentAuthForTest(false))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/agent/generation/estimates", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "agent_credential_required")
}

func TestAgentAssetUploadRouteRequiresAgentCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ordinary := gin.New()
	ordinaryV1 := ordinary.Group("/api/v1")
	RegisterAgentRoutes(ordinary, ordinaryV1, &handler.Handlers{Agent: &handler.AgentHandler{}}, agentAuthForTest(false))
	ordinaryResponse := httptest.NewRecorder()
	ordinary.ServeHTTP(ordinaryResponse, httptest.NewRequest(http.MethodPost, "/api/v1/agent/assets", nil))
	require.Equal(t, http.StatusForbidden, ordinaryResponse.Code)
	require.Contains(t, ordinaryResponse.Body.String(), "agent_credential_required")

	agent := gin.New()
	agentV1 := agent.Group("/api/v1")
	RegisterAgentRoutes(agent, agentV1, &handler.Handlers{Agent: &handler.AgentHandler{}}, agentAuthForTest(true))
	agentResponse := httptest.NewRecorder()
	agent.ServeHTTP(agentResponse, httptest.NewRequest(http.MethodPost, "/api/v1/agent/assets", nil))
	require.Equal(t, http.StatusBadRequest, agentResponse.Code)
	require.Contains(t, agentResponse.Body.String(), "file_required")

	missingReadRoute := httptest.NewRecorder()
	agent.ServeHTTP(missingReadRoute, httptest.NewRequest(http.MethodGet, "/api/v1/agent/assets/not-a-uuid", nil))
	require.Equal(t, http.StatusNotFound, missingReadRoute.Code)
}

func TestRetiredYingzoDistributionRoutesAreNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/api/v1")
	h := &handler.Handlers{Agent: &handler.AgentHandler{}}
	RegisterAgentRoutes(r, v, h, agentAuthForTest(true))

	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/.well-known/yingzo.json"},
		{method: http.MethodPost, path: "/api/v1/agent/device/token"},
		{method: http.MethodGet, path: "/api/v1/agent/plugin/releases/latest"},
		{method: http.MethodPost, path: "/api/v1/agent/plugin/install-instructions"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		require.Equal(t, http.StatusNotFound, w.Code, endpoint.path)
	}
}

package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetYingzo serves the signed-in Yingzo product catalogue. It intentionally
// does not use the optional-auth, general model-plaza visibility or price cards.
func (h *ModelPlazaHandler) GetYingzo(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return
	}
	groups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	groupID := yingzoPlazaGroupID(groups)
	if groupID == 0 {
		response.NotFound(c, "Yingzo Agent 分组暂不可用")
		return
	}
	data, err := h.plazaService.ListYingzoModels(c.Request.Context(), h.agentCatalog, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

func yingzoPlazaGroupID(groups []service.Group) int64 {
	for _, group := range groups {
		if group.IsAgent() && group.SystemCode == "yingzo" && group.Status == service.StatusActive {
			return group.ID
		}
	}
	return 0
}

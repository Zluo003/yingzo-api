package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetTutorials(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	cfg, err := h.settingService.GetTutorialConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.PublicTutorialConfig(cfg))
}

func (h *SettingHandler) GetTutorialAuth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	auth, err := h.settingService.GetTutorialAuth(c.Request.Context(), c.Query("article_id"), c.Query("url"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, auth)
}

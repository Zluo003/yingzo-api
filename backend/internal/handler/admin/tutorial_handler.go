package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetTutorialConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	cfg, err := h.settingService.GetTutorialConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	cfg.SecretConfigured = cfg.AppSecret != ""
	cfg.AppSecret = ""
	response.Success(c, cfg)
}

func (h *SettingHandler) SaveTutorialConfig(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<10)
	var cfg service.TutorialConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "教程配置格式无效或过大")
		return
	}
	if err := h.settingService.SaveTutorialConfig(c.Request.Context(), &cfg); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	cfg.SecretConfigured = cfg.AppSecret != ""
	cfg.AppSecret = ""
	response.Success(c, cfg)
}

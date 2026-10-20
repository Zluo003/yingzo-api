package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ContentModerationHandler) SetMediaAuditService(s *service.MediaAuditService) {
	h.mediaAudit = s
}
func (h *ContentModerationHandler) GetMediaAuditConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	cfg, err := h.mediaAudit.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}
func (h *ContentModerationHandler) SaveMediaAuditConfig(c *gin.Context) {
	var cfg service.MediaAuditConfig
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "审核配置格式无效")
		return
	}
	saved, err := h.mediaAudit.SaveConfig(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}
func (h *ContentModerationHandler) ProbeMediaAudit(c *gin.Context) {
	var in struct {
		Slot   string   `json:"slot"`
		Prompt string   `json:"prompt"`
		Images []string `json:"images"`
	}
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "测试参数无效")
		return
	}
	result, err := h.mediaAudit.Probe(c.Request.Context(), in.Slot, in.Prompt, in.Images)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *ContentModerationHandler) ListMediaAuditEvents(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, _ := strconv.Atoi(c.Query("page"))
	items, err := h.mediaAudit.ListEvents(c.Request.Context(), page)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}
func (h *ContentModerationHandler) GetMediaAuditEvent(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	e, err := h.mediaAudit.GetEvent(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, e)
}
func (h *ContentModerationHandler) GetMediaAuditAsset(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		response.BadRequest(c, "素材索引无效")
		return
	}
	data, mime, err := h.mediaAudit.EvidenceAsset(c.Request.Context(), c.Param("id"), index)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "attachment; filename=evidence-"+strconv.Itoa(index))
	c.Data(http.StatusOK, mime, data)
}

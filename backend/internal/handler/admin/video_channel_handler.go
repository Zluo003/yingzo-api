package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *GroupHandler) ListVideoChannels(c *gin.Context) {
	if !h.requireAgentModels(c) {
		return
	}
	channels, err := h.agentModels.ListVideoChannels(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, channels)
}
func (h *GroupHandler) CreateVideoChannel(c *gin.Context) { h.saveVideoChannel(c, true) }
func (h *GroupHandler) UpdateVideoChannel(c *gin.Context) { h.saveVideoChannel(c, false) }
func (h *GroupHandler) saveVideoChannel(c *gin.Context, create bool) {
	if !h.requireAgentModels(c) {
		return
	}
	var input service.VideoChannel
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	channel, err := h.agentModels.SaveVideoChannel(c.Request.Context(), input, create)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, channel)
}

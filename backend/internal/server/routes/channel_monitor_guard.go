package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// channelMonitorAdminFeatureGuard gates the channel monitor admin APIs on the
// runtime feature flag. Keep this separate from the v2 mode guard because the
// admin read APIs are available in either monitor mode.
func channelMonitorAdminFeatureGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService != nil && settingService.GetChannelMonitorRuntime(c.Request.Context()).Enabled {
			c.Next()
			return
		}
		response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
		c.Abort()
	}
}

func channelMonitorModeV2Guard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		rt := settingService.GetChannelMonitorRuntime(c.Request.Context())
		if !rt.Enabled {
			response.ErrorFrom(c, service.ErrChannelMonitorDisabled)
			c.Abort()
			return
		}
		if !rt.PassiveAggregationAllowed() {
			response.ErrorFrom(c, service.ErrChannelMonitorModeMismatch)
			c.Abort()
			return
		}
		c.Next()
	}
}

package routes

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterAgentRoutes(r *gin.Engine, v1 *gin.RouterGroup, h *handler.Handlers, apiKeyAuth middleware.APIKeyAuthMiddleware) {
	g := v1.Group("/agent")
	g.Use(gin.HandlerFunc(apiKeyAuth))
	g.Use(requireAgentGroup())
	g.GET("/pricing", h.Agent.GetAgentPricingSnapshot)
	g.POST("/generation/estimates", h.Agent.EstimateGeneration)
	g.GET("/generation/estimates/:id", h.Agent.GetGenerationEstimate)
	g.POST("/assets", h.Agent.UploadTemporaryAsset)
	g.POST("/assets/resolve", h.Agent.ResolveTemporaryAssets)

	r.GET("/temporary-assets/:token", h.Agent.ServeTemporaryAsset)
	r.HEAD("/temporary-assets/:token", h.Agent.ServeTemporaryAsset)
	r.GET("/media/:id/:filename", h.Agent.ServeCleanTemporaryAsset)
	r.HEAD("/media/:id/:filename", h.Agent.ServeCleanTemporaryAsset)
}

func requireAgentGroup() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || apiKey.Group == nil || !apiKey.Group.IsAgent() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code":    "agent_credential_required",
				"message": "This endpoint requires an Agent credential",
			}})
			return
		}
		c.Next()
	}
}

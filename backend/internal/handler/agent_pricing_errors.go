package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const agentPricingUnavailableCode = "agent_pricing_unavailable"

func agentPricingPublicMessage(err error) string {
	switch {
	case errors.Is(err, service.ErrAgentChannelPricingAmbiguous):
		return "The selected model has conflicting source channel pricing"
	case errors.Is(err, service.ErrAgentImagePricingUnavailable):
		return "Image pricing is not configured for the requested resolution"
	default:
		return "Channel pricing is not configured for the selected model"
	}
}

func writeOpenAIAgentPricingError(c *gin.Context, err error) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"type":    "api_error",
		"code":    agentPricingUnavailableCode,
		"message": agentPricingPublicMessage(err),
	}})
}

func writeAnthropicAgentPricingError(c *gin.Context, err error) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    agentPricingUnavailableCode,
			"message": agentPricingPublicMessage(err),
		},
	})
}

func writeGoogleAgentPricingError(c *gin.Context, err error) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"code":    http.StatusServiceUnavailable,
		"message": agentPricingPublicMessage(err),
		"status":  "UNAVAILABLE",
		"reason":  agentPricingUnavailableCode,
	}})
}

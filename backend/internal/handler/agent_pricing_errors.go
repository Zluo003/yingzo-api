package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/googleapi"
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

// writeAgentPricingError 用 Anthropic 错误信封返回 Agent 计价缺失。
// Agent 分组的价格是请求准入的一部分：源渠道价或模型倍率缺失必须在转发上游之前
// 就失败，否则这一单拿不到钱还要付上游成本。
func writeAgentPricingError(c *gin.Context, err error) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    agentPricingUnavailableCode,
			"message": agentPricingPublicMessage(err),
		},
	})
}

func writeGeminiAgentPricingError(c *gin.Context, err error) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"code":    http.StatusServiceUnavailable,
		"message": agentPricingPublicMessage(err),
		"status":  googleapi.HTTPStatusToGoogleStatus(http.StatusServiceUnavailable),
		"reason":  agentPricingUnavailableCode,
	}})
}

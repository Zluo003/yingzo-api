package handler

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetAgentPricingCompatibility exposes the small pricing shape consumed by
// older desktop clients. It is an adapter over the Agent catalogue; the
// canonical endpoint remains /api/v1/agent/pricing.
func (h *AgentHandler) GetAgentPricingCompatibility(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || !apiKey.Group.IsAgent() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "invalid_api_key", "message": "Invalid Agent API key"}})
		return
	}
	if h.agentModels == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "agent_pricing_unavailable", "message": "Unable to resolve current Agent pricing"}})
		return
	}
	config, err := h.agentModels.GetConfig(c.Request.Context(), apiKey.Group.ID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "agent_pricing_unavailable", "message": "Unable to resolve current Agent pricing"}})
		return
	}
	data := make([]gin.H, 0)
	for _, model := range config.Models {
		if model.MediaType != service.AgentMediaTypeVideo || !model.Enabled || !model.Available || model.Excluded {
			continue
		}
		resolutions := make([]string, 0, len(model.Prices))
		seen := map[string]struct{}{}
		for _, price := range model.Prices {
			resolution := strings.TrimSpace(price.Resolution)
			if resolution == "" {
				continue
			}
			if _, exists := seen[resolution]; exists {
				continue
			}
			seen[resolution] = struct{}{}
			resolutions = append(resolutions, resolution)
		}
		if len(resolutions) == 0 {
			continue
		}
		data = append(data, gin.H{
			"model_name": model.ModelCode,
			"billing_usage_schema": gin.H{
				"resolution": gin.H{"enum": resolutions},
			},
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

type agentPricingSnapshotRule struct {
	Model               string  `json:"model"`
	Platform            string  `json:"platform"`
	MediaType           string  `json:"media_type"`
	Resolution          string  `json:"resolution"`
	UnitKind            string  `json:"unit_kind"`
	UnitPrice           float64 `json:"unit_price"`
	EffectiveUnitPrice  float64 `json:"effective_unit_price"`
	BillingMultiplier   float64 `json:"billing_multiplier"`
	ReferenceMultiplier float64 `json:"reference_multiplier"`
}

type agentPricingSnapshot struct {
	SchemaVersion  string                     `json:"schema_version"`
	PricingVersion string                     `json:"pricing_version"`
	Currency       string                     `json:"currency"`
	Rules          []agentPricingSnapshotRule `json:"rules"`
	FetchedAt      time.Time                  `json:"fetched_at"`
	ValidUntil     time.Time                  `json:"valid_until"`
}

const agentPricingSnapshotTTL = 24 * time.Hour

// GetAgentPricingSnapshot exposes only explicitly configured Agent pricing.
func (h *AgentHandler) GetAgentPricingSnapshot(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil || apiKey.GroupID == nil || !apiKey.Group.IsAgent() {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "invalid_api_key", "message": "Invalid Agent API key"}})
		return
	}

	if h.agentModels == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "agent_pricing_unavailable", "message": "Unable to resolve current Agent pricing"}})
		return
	}
	config, err := h.agentModels.GetConfig(c.Request.Context(), apiKey.Group.ID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "agent_pricing_unavailable", "message": "Unable to resolve current Agent pricing"}})
		return
	}
	rules := make([]agentPricingSnapshotRule, 0)
	for _, model := range config.Models {
		if !model.Enabled || !model.Available || model.Excluded {
			continue
		}
		// 文本模型：每模型一个倍率，作用在该账号所属源分组的渠道价之上。
		if model.MediaType == service.AgentMediaTypeText {
			if model.RateMultiplier == nil {
				continue
			}
			rules = append(rules, agentPricingSnapshotRule{
				Model: model.ModelCode, Platform: model.Platform, MediaType: service.AgentMediaTypeText,
				UnitKind: "channel_price_multiplier", UnitPrice: 1,
				BillingMultiplier: *model.RateMultiplier, EffectiveUnitPrice: *model.RateMultiplier,
			})
			continue
		}
		for _, price := range model.Prices {
			rules = append(rules, agentPricingSnapshotRule{
				Model: model.ModelCode, Platform: model.Platform, MediaType: model.MediaType,
				Resolution: price.Resolution, UnitKind: price.BillingUnit,
				UnitPrice: price.UnitPrice, EffectiveUnitPrice: price.UnitPrice,
				BillingMultiplier: 1,
			})
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Platform != rules[j].Platform {
			return rules[i].Platform < rules[j].Platform
		}
		if rules[i].Model != rules[j].Model {
			return rules[i].Model < rules[j].Model
		}
		return rules[i].Resolution < rules[j].Resolution
	})

	fetchedAt := time.Now().UTC()
	pricingHash := hashJSON(rules)
	c.JSON(http.StatusOK, agentPricingSnapshot{
		SchemaVersion:  "3.0.0",
		PricingVersion: "price_" + pricingHash[:24],
		Currency:       "credit",
		Rules:          rules,
		FetchedAt:      fetchedAt,
		ValidUntil:     fetchedAt.Add(agentPricingSnapshotTTL),
	})
}

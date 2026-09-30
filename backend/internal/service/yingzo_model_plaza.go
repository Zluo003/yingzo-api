package service

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"
)

// YingzoModelPrice contains final customer prices only: no account, channel or
// supplier-cost metadata. Token prices are CNY per million tokens.
type YingzoModelPrice struct {
	Unit         string   `json:"unit"`
	Resolution   string   `json:"resolution,omitempty"`
	UnitPrice    *float64 `json:"unit_price,omitempty"`
	InputPrice   *float64 `json:"input_price,omitempty"`
	OutputPrice  *float64 `json:"output_price,omitempty"`
	CacheRead    *float64 `json:"cache_read_price,omitempty"`
	CacheWrite   *float64 `json:"cache_write_price,omitempty"`
	CacheWrite1h *float64 `json:"cache_write_1h_price,omitempty"`
}

type YingzoPlazaModel struct {
	ModelCode     string             `json:"model_code"`
	Platform      string             `json:"platform"`
	MediaType     string             `json:"media_type"`
	PricingStatus string             `json:"pricing_status"`
	Prices        []YingzoModelPrice `json:"prices"`
}

type YingzoModelPlaza struct {
	Currency string             `json:"currency"`
	AsOf     time.Time          `json:"as_of"`
	Models   []YingzoPlazaModel `json:"models"`
}

// ListYingzoModels enumerates the configured Agent catalogue, never the general
// channel catalogue. Enabled entries remain visible even if availability is
// temporarily false or pricing is incomplete. This read does not sync/mutate it.
func (s *ModelPlazaService) ListYingzoModels(ctx context.Context, catalog *AgentModelCatalogService, groupID int64) (*YingzoModelPlaza, error) {
	if catalog == nil || catalog.modelRepo == nil {
		return nil, ErrAgentModelCatalogUnavailable
	}
	if err := catalog.requireAgentGroup(ctx, groupID); err != nil {
		return nil, err
	}
	models, err := catalog.modelRepo.ListModels(ctx, groupID, false)
	if err != nil {
		return nil, err
	}
	accounts, err := catalog.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return nil, err
	}
	byModel := make(map[string][]*Account)
	for i := range accounts {
		for key := range agentDiscoverySet(discoverAgentModels([]Account{accounts[i]})) {
			byModel[key] = append(byModel[key], &accounts[i])
		}
	}
	result := &YingzoModelPlaza{Currency: "CNY", AsOf: time.Now(), Models: []YingzoPlazaModel{}}
	for _, model := range models {
		if !model.Enabled || model.Excluded || !isValidAgentMediaType(model.MediaType) {
			continue
		}
		entry := YingzoPlazaModel{ModelCode: model.ModelCode, Platform: model.Platform, MediaType: model.MediaType, PricingStatus: "unavailable", Prices: []YingzoModelPrice{}}
		if model.MediaType == AgentMediaTypeText {
			entry.Prices = s.yingzoTextPrices(ctx, groupID, model, byModel[agentModelKey(model.Platform, model.ModelCode)], result.AsOf)
		} else {
			unit := billingUnitForAgentModel(model.ModelCode, model.MediaType)
			for _, price := range model.Prices {
				if (price.Enabled != nil && !*price.Enabled) || price.BillingUnit != unit || !validYingzoPrice(price.UnitPrice) {
					continue
				}
				if model.ModelCode == MidjourneyModel && !IsMidjourneyPriceTier(price.Resolution) {
					continue
				}
				value := price.UnitPrice
				entry.Prices = append(entry.Prices, YingzoModelPrice{Unit: unit, Resolution: price.Resolution, UnitPrice: &value})
			}
		}
		if len(entry.Prices) > 0 {
			entry.PricingStatus = "available"
		}
		result.Models = append(result.Models, entry)
	}
	sort.Slice(result.Models, func(i, j int) bool {
		a, b := result.Models[i], result.Models[j]
		if a.ModelCode == b.ModelCode {
			return a.Platform < b.Platform
		}
		return a.ModelCode < b.ModelCode
	})
	return result, ctx.Err()
}

func validYingzoPrice(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (s *ModelPlazaService) yingzoTextPrices(ctx context.Context, groupID int64, model AgentGroupModel, accounts []*Account, at time.Time) []YingzoModelPrice {
	prices := []YingzoModelPrice{}
	if model.RateMultiplier == nil || !validYingzoPrice(*model.RateMultiplier) || s.resolver == nil || s.billingService == nil {
		return prices
	}
	// A direct Agent-group channel price is valid even when no account currently
	// advertises the model. Otherwise resolve each eligible account's source group
	// just as the gateway does; distinct final prices are returned as variants.
	candidates := append([]*Account{{Platform: model.Platform}}, accounts...)
	seen := make(map[string]bool)
	for _, account := range candidates {
		resolved, _, err := s.resolver.ResolveAgentAccountCandidates(ctx, groupID, account, model.ModelCode)
		if err != nil {
			continue
		}
		price, err := s.yingzoResolvedPrice(ctx, model, resolved, at)
		if err != nil {
			continue
		}
		key, _ := json.Marshal(price)
		if !seen[string(key)] {
			seen[string(key)] = true
			prices = append(prices, price)
		}
	}
	return prices
}

func (s *ModelPlazaService) yingzoResolvedPrice(ctx context.Context, model AgentGroupModel, resolved *ResolvedPricing, at time.Time) (YingzoModelPrice, error) {
	quote := func(tokens UsageTokens, scale float64) (*float64, error) {
		cost, err := s.billingService.CalculateCostUnified(CostInput{
			Ctx: ctx, Model: model.ModelCode, Tokens: tokens, RequestCount: 1,
			RateMultiplier: *model.RateMultiplier, Resolver: s.resolver, Resolved: resolved, PricingAt: at,
		})
		if err != nil {
			return nil, err
		}
		value := cost.ActualCost * scale
		if !validYingzoPrice(value) {
			return nil, ErrAgentChannelPricingUnavailable
		}
		return &value, nil
	}
	price := YingzoModelPrice{Unit: "million_tokens"}
	if resolved.Mode != BillingModeToken {
		price.Unit = "request"
		value, err := quote(UsageTokens{}, 1)
		price.UnitPrice = value
		return price, err
	}
	// Quote one token, then scale the display. A million-token sample would
	// accidentally select a higher context tier instead of a unit price.
	fields := []struct {
		tokens UsageTokens
		dest   **float64
	}{
		{UsageTokens{InputTokens: 1}, &price.InputPrice},
		{UsageTokens{OutputTokens: 1}, &price.OutputPrice},
		{UsageTokens{CacheReadTokens: 1}, &price.CacheRead},
		{UsageTokens{CacheCreationTokens: 1}, &price.CacheWrite},
	}
	if resolved.BasePricing != nil && resolved.BasePricing.SupportsCacheBreakdown {
		fields = append(fields, struct {
			tokens UsageTokens
			dest   **float64
		}{UsageTokens{CacheCreation1hTokens: 1, CacheCreationTokens: 1}, &price.CacheWrite1h})
	}
	for _, field := range fields {
		value, err := quote(field.tokens, 1e6)
		if err != nil {
			return price, err
		}
		*field.dest = value
	}
	return price, nil
}

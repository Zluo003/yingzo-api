package service

import (
	"context"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const VideoModelChannelsExtraKey = "video_model_channels"

func normalizeSeedanceChannelPrices(prices []AgentModelPrice) ([]AgentModelPrice, error) {
	out := make([]AgentModelPrice, 0, len(prices))
	seen := map[string]bool{}
	units := map[string]string{}
	for _, price := range prices {
		resolution, err := normalizeAgentPriceResolution(AgentMediaTypeVideo, price.Resolution)
		if err != nil {
			return nil, err
		}
		key := price.VideoChannelID + "\x00" + resolution
		if !validVideoChannelID(price.VideoChannelID) || seen[key] || price.UnitPrice < 0 || math.IsNaN(price.UnitPrice) || math.IsInf(price.UnitPrice, 0) {
			return nil, fmt.Errorf("invalid or duplicate video channel price")
		}
		if price.BillingUnit == "" {
			price.BillingUnit = "second"
		}
		if price.BillingUnit != "second" && price.BillingUnit != "request" {
			return nil, fmt.Errorf("video billing unit must be second or request")
		}
		if unit, exists := units[price.VideoChannelID]; exists && unit != price.BillingUnit {
			return nil, fmt.Errorf("a model channel must use one billing unit across resolutions")
		}
		units[price.VideoChannelID] = price.BillingUnit
		seen[key] = true
		price.Resolution = resolution
		out = append(out, price)
	}
	return out, nil
}

type VideoChannel struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	Enabled               bool              `json:"enabled"`
	Available             bool              `json:"available"`
	Prices                []AgentModelPrice `json:"prices,omitempty"`
	Resolutions           []string          `json:"supported_video_resolutions,omitempty"`
	Durations             []int             `json:"supported_video_durations_sec,omitempty"`
	DurationsByResolution map[string][]int  `json:"durations_by_resolution,omitempty"`
}

type videoChannelRepository interface {
	ListVideoChannels(context.Context) ([]VideoChannel, error)
	SaveVideoChannel(context.Context, VideoChannel) error
}

func IsSeedanceModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "seedance-")
}

func validVideoChannelID(id string) bool {
	if id == "" {
		return true
	}
	_, err := uuid.Parse(id)
	return err == nil && strings.ToLower(id) == id
}

func normalizeVideoModelChannels(extra map[string]any) error {
	raw, exists := extra[VideoModelChannelsExtraKey]
	if !exists || raw == nil {
		return nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return videoBadRequest("invalid_video_channel", "Video model channels must be an object")
	}
	clean := map[string]any{}
	for model, value := range values {
		id, ok := value.(string)
		if !ok || !IsSeedanceModel(model) || model != strings.TrimSpace(model) || !validVideoChannelID(id) {
			return videoBadRequest("invalid_video_channel", "Invalid Seedance model channel")
		}
		if id != "" {
			clean[model] = id
		}
	}
	extra[VideoModelChannelsExtraKey] = clean
	return nil
}

// A malformed explicit binding is never treated as the default pool.
func videoAccountChannel(account *Account, model string) string {
	if account == nil || !IsSeedanceModel(model) {
		return ""
	}
	raw, exists := account.Extra[VideoModelChannelsExtraKey]
	if !exists || raw == nil {
		return ""
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return "!invalid"
	}
	value, exists := values[model]
	if !exists {
		return ""
	}
	id, ok := value.(string)
	if !ok || !validVideoChannelID(id) {
		return "!invalid"
	}
	return id
}

func (s *AgentModelCatalogService) ListVideoChannels(ctx context.Context) ([]VideoChannel, error) {
	if s != nil {
		if repo, ok := s.modelRepo.(videoChannelRepository); ok {
			return repo.ListVideoChannels(ctx)
		}
	}
	return []VideoChannel{{ID: "", Name: "默认通道", Enabled: true}}, nil
}

func (s *AgentModelCatalogService) SaveVideoChannel(ctx context.Context, channel VideoChannel, create bool) (*VideoChannel, error) {
	channel.Name = strings.TrimSpace(channel.Name)
	if channel.Name == "" || utf8.RuneCountInString(channel.Name) > 80 || !validVideoChannelID(channel.ID) {
		return nil, infraerrors.BadRequest("invalid_video_channel", "Channel name and ID are invalid")
	}
	if create {
		channel.ID = uuid.NewString()
	}
	repo, ok := s.modelRepo.(videoChannelRepository)
	if !ok {
		return nil, ErrAgentModelCatalogUnavailable
	}
	channels, err := repo.ListVideoChannels(ctx)
	if err != nil {
		return nil, err
	}
	found := create
	for _, existing := range channels {
		if existing.ID == channel.ID {
			found = true
		}
		if existing.ID != channel.ID && strings.EqualFold(existing.Name, channel.Name) {
			return nil, infraerrors.BadRequest("duplicate_video_channel", "Channel name already exists")
		}
	}
	if !found {
		return nil, infraerrors.BadRequest("invalid_video_channel", "Channel does not exist")
	}
	if err := repo.SaveVideoChannel(ctx, channel); err != nil {
		return nil, err
	}
	return &channel, nil
}

func (s *AgentModelCatalogService) requireVideoChannel(ctx context.Context, id string) error {
	if s == nil && id != "" {
		return ErrAgentModelCatalogUnavailable
	}
	channels, err := s.ListVideoChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if ch.ID == id && ch.Enabled {
			return nil
		}
	}
	return videoBadRequest("video_channel_unavailable", "Selected video channel is unavailable")
}

func (s *adminServiceImpl) validateVideoChannelBindings(ctx context.Context, platform string, extra map[string]any) error {
	if platform != PlatformVideo {
		return nil
	}
	bindings, _ := extra[VideoModelChannelsExtraKey].(map[string]any)
	for _, value := range bindings {
		id, _ := value.(string)
		if id == "" {
			continue
		}
		if s.entClient == nil {
			return ErrAgentModelCatalogUnavailable
		}
		rows, err := s.entClient.QueryContext(ctx, "SELECT id FROM video_channels WHERE id = $1", id)
		if err != nil {
			return err
		}
		found := rows.Next()
		scanErr := rows.Err()
		rows.Close()
		if scanErr != nil {
			return scanErr
		}
		if !found {
			return videoBadRequest("invalid_video_channel", "Account refers to an unknown video channel")
		}
	}
	return nil
}

func (s *AgentModelCatalogService) ResolveVideoChannelPrice(ctx context.Context, groupID int64, model, resolution, channelID string) (*AgentModelPrice, *AgentGroupModel, error) {
	if !IsSeedanceModel(model) {
		return s.ResolveMediaPrice(ctx, groupID, PlatformVideo, AgentMediaTypeVideo, resolution, model)
	}
	if err := s.requireVideoChannel(ctx, channelID); err != nil {
		return nil, nil, err
	}
	entry, err := s.modelRepo.GetEnabledModel(ctx, groupID, PlatformVideo, model)
	if err != nil || entry == nil || entry.MediaType != AgentMediaTypeVideo {
		return nil, nil, ErrVideoPricingRuleNotFound
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	available := false
	for i := range accounts {
		if accounts[i].Platform == PlatformVideo && accounts[i].IsModelSupported(model) && videoAccountChannel(&accounts[i], model) == channelID {
			available = true
			break
		}
	}
	if !available {
		return nil, nil, videoBadRequest("video_channel_unavailable", "Selected video channel has no configured accounts")
	}
	for _, price := range entry.Prices {
		if price.VideoChannelID == channelID && price.Resolution == resolution && agentModelPriceEnabled(price) && price.UnitPrice >= 0 && (price.BillingUnit == "second" || price.BillingUnit == "request") {
			return &price, entry, nil
		}
	}
	return nil, nil, fmt.Errorf("%w: channel %s resolution %s", ErrVideoPricingRuleNotFound, channelID, resolution)
}

func (s *AgentModelCatalogService) attachVideoChannels(ctx context.Context, groupID int64, models []AgentGroupModel) error {
	hasSeedance := false
	for _, model := range models {
		if model.MediaType == AgentMediaTypeVideo && IsSeedanceModel(model.ModelCode) {
			hasSeedance = true
			break
		}
	}
	if !hasSeedance {
		return nil
	}
	channels, err := s.ListVideoChannels(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.listCatalogAccounts(ctx, groupID)
	if err != nil {
		return err
	}
	for i := range models {
		model := &models[i]
		model.VideoChannels = nil
		if model.MediaType != AgentMediaTypeVideo || !IsSeedanceModel(model.ModelCode) {
			continue
		}
		for _, channel := range channels {
			channel.Prices = nil
			for _, price := range model.Prices {
				if price.VideoChannelID == channel.ID {
					channel.Prices = append(channel.Prices, price)
				}
			}
			for j := range accounts {
				account := &accounts[j]
				if account.Platform != PlatformVideo || !account.IsModelSupported(model.ModelCode) || videoAccountChannel(account, model.ModelCode) != channel.ID {
					continue
				}
				channel.Available = true
			}
			channel.DurationsByResolution = map[string][]int{}
			allDurations := map[int]bool{}
			for _, price := range channel.Prices {
				if !agentModelPriceEnabled(price) {
					continue
				}
				for _, duration := range SupportedVideoDurations(model.ModelCode) {
					for j := range accounts {
						account := &accounts[j]
						if account.Platform != PlatformVideo || !account.IsModelSupported(model.ModelCode) || videoAccountChannel(account, model.ModelCode) != channel.ID {
							continue
						}
						if videoAccountSupportsResolution(account, model.ModelCode, price.Resolution) && videoAccountSupportsDuration(account, model.ModelCode, duration) && videoProviderAdapterForAccount(account).CompatibleRequest(&normalizedVideoRequest{Model: model.ModelCode, Resolution: price.Resolution, GeneratedSeconds: duration, Duration: float64(duration), Ratio: "16:9", AbilityCode: videoAbilityTextToVideo}) {
							channel.DurationsByResolution[price.Resolution] = append(channel.DurationsByResolution[price.Resolution], duration)
							allDurations[duration] = true
							break
						}
					}
				}
			}
			for resolution := range channel.DurationsByResolution {
				channel.Resolutions = append(channel.Resolutions, resolution)
			}
			for duration := range allDurations {
				channel.Durations = append(channel.Durations, duration)
			}
			sort.Strings(channel.Resolutions)
			sort.Ints(channel.Durations)
			// Keep empty/default and previously priced channels editable even when unbound.
			if channel.ID == "" || channel.Available || len(channel.Prices) > 0 {
				model.VideoChannels = append(model.VideoChannels, channel)
			}
		}
	}
	return nil
}

// PublicVideoChannels removes disabled prices and internal price-row metadata.
func PublicVideoChannels(channels []VideoChannel) []VideoChannel {
	out := []VideoChannel{}
	for _, ch := range channels {
		if !ch.Enabled || !ch.Available || len(ch.Resolutions) == 0 {
			continue
		}
		prices := []AgentModelPrice{}
		for _, p := range ch.Prices {
			if !agentModelPriceEnabled(p) || len(ch.DurationsByResolution[p.Resolution]) == 0 {
				continue
			}
			prices = append(prices, AgentModelPrice{Resolution: p.Resolution, BillingUnit: p.BillingUnit, UnitPrice: p.UnitPrice, VideoChannelID: ch.ID})
		}
		if len(prices) == 0 {
			continue
		}
		ch.Prices = prices
		out = append(out, ch)
	}
	return out
}

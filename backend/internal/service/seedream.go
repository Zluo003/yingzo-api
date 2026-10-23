package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SeedreamProModel = "seedream-5-0-pro"
const SeedreamFlashModel = "seedream-5-0-flash"
const SeedreamProvider = "apimart_seedream"
const SeedreamLowPixelLimit int64 = 2601124

var SeedreamAspectRatios = []string{"1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "2:1", "1:2", "21:9"}
var seedreamPixels = regexp.MustCompile(`^([0-9]+)[xX×]([0-9]+)$`)

func IsSeedreamModel(model string) bool {
	return model == SeedreamProModel || model == SeedreamFlashModel
}
func CanonicalSeedreamModel(model string) string { return strings.ReplaceAll(model, "5.0", "5-0") }
func normalizeSeedreamPriceTier(resolution string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(resolution)) {
	case "1K", "1.5K":
		return "1K", nil
	case "2K":
		return "2K", nil
	default:
		return "", errors.New("seedream prices use 1.5K (including 1K) and 2K only")
	}
}
func (a *Account) IsSeedream() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey && a.Extra["image_provider"] == SeedreamProvider
}

type SeedreamRequest struct {
	Model                 string   `json:"model"`
	Prompt                string   `json:"prompt,omitempty"`
	Size                  string   `json:"size"`
	Resolution            string   `json:"resolution,omitempty"`
	N                     *int     `json:"n,omitempty"`
	ImageURLs             []string `json:"image_urls,omitempty"`
	OutputFormat          string   `json:"output_format"`
	ResponseFormat        string   `json:"response_format,omitempty"`
	Background            string   `json:"background"`
	LayerDecomposition    bool     `json:"layer_decomposition,omitempty"`
	OptimizePromptOptions *struct {
		Mode string `json:"mode"`
	} `json:"optimize_prompt_options,omitempty"`
	OptimizePromptMode string `json:"optimize_prompt_options.mode,omitempty"`
	Watermark          bool   `json:"watermark"`
	NSFWCheck          bool   `json:"nsfw_check"`
}

func ParseSeedreamRequest(body []byte) (*SeedreamRequest, error) {
	var r SeedreamRequest
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid Seedream request: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("request must contain one JSON object")
	}
	r.Model = CanonicalSeedreamModel(r.Model)
	if !IsSeedreamModel(r.Model) {
		return nil, errors.New("model must be seedream-5-0-pro or seedream-5-0-flash")
	}
	r.Prompt = strings.TrimSpace(r.Prompt)
	if r.Prompt == "" && !r.LayerDecomposition {
		return nil, errors.New("prompt is required except for layer decomposition")
	}
	if r.N != nil && *r.N != 1 {
		return nil, errors.New("seedream only supports n=1")
	}
	r.N = nil
	if len(r.ImageURLs) > 10 {
		return nil, errors.New("image_urls accepts at most 10 images")
	}
	for _, value := range r.ImageURLs {
		if strings.HasPrefix(value, "data:") {
			mime, encoded, ok := strings.Cut(value, ";base64,")
			format := strings.TrimPrefix(mime, "data:image/")
			supported := false
			for _, f := range []string{"jpeg", "png", "webp", "bmp", "tiff", "gif", "heic", "heif"} {
				if format == f {
					supported = true
				}
			}
			if !ok || !strings.HasPrefix(mime, "data:image/") || !supported || len(encoded) > 41943040 {
				return nil, errors.New("invalid image Data URI or image exceeds 30 MB")
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(data) == 0 || len(data) > 30*1024*1024 {
				return nil, errors.New("invalid image Base64 or image exceeds 30 MB")
			}
		} else {
			u, err := url.ParseRequestURI(value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
				return nil, errors.New("image_urls must contain HTTP(S) URLs or image Data URIs")
			}
		}
	}
	if r.OutputFormat == "" {
		r.OutputFormat = r.ResponseFormat
	}
	r.ResponseFormat = ""
	if r.OutputFormat == "" {
		r.OutputFormat = "jpeg"
	}
	if r.OutputFormat != "jpeg" && r.OutputFormat != "png" {
		return nil, errors.New("output_format must be jpeg or png; results are always URLs")
	}
	if r.Background == "" {
		r.Background = "opaque"
	}
	if r.Background != "opaque" && r.Background != "transparent" {
		return nil, errors.New("background must be opaque or transparent")
	}
	if r.Background == "transparent" && (len(r.ImageURLs) != 1 || r.OutputFormat != "png") {
		return nil, errors.New("transparent background requires exactly one transparent input image and output_format=png")
	}
	if r.OptimizePromptMode != "" {
		if r.OptimizePromptMode != "standard" {
			return nil, errors.New("only standard prompt optimization is supported")
		}
		if r.OptimizePromptOptions == nil {
			r.OptimizePromptOptions = &struct {
				Mode string `json:"mode"`
			}{Mode: "standard"}
		}
	}
	r.OptimizePromptMode = ""
	if r.OptimizePromptOptions == nil {
		r.OptimizePromptOptions = &struct {
			Mode string `json:"mode"`
		}{Mode: "standard"}
	}
	if r.OptimizePromptOptions.Mode != "standard" {
		return nil, errors.New("only standard prompt optimization is supported")
	}
	if r.Size == "" {
		r.Size = "auto"
	}
	tier := strings.ToUpper(r.Size)
	if tier == "1K" || tier == "1.5K" || tier == "2K" {
		r.Size, r.Resolution = "auto", tier
	}
	if r.LayerDecomposition && (len(r.ImageURLs) != 1 || r.Size != "auto") {
		return nil, errors.New("layer decomposition requires exactly one image and size=auto, 1K, 1.5K or 2K")
	}
	for _, ratio := range SeedreamAspectRatios {
		if r.Size == strings.ReplaceAll(ratio, ":", "x") {
			r.Size = ratio
			break
		}
	}
	if m := seedreamPixels.FindStringSubmatch(r.Size); m != nil {
		w, e1 := strconv.ParseInt(m[1], 10, 64)
		h, e2 := strconv.ParseInt(m[2], 10, 64)
		if e1 != nil || e2 != nil || w <= 0 || h <= 0 || w > 4624220 || h > 4624220 || w*h < 921600 || w*h > 4624220 || w > 16*h || h > 16*w {
			return nil, errors.New("size pixels must total 921600–4624220 with aspect ratio between 1:16 and 16:1")
		}
		r.Size = fmt.Sprintf("%dx%d", w, h)
		r.Resolution = ""
	} else {
		if r.Size != "auto" {
			r.Size = strings.ReplaceAll(r.Size, "x", ":")
			valid := false
			for _, ratio := range SeedreamAspectRatios {
				if r.Size == ratio {
					valid = true
				}
			}
			if !valid {
				return nil, errors.New("unsupported Seedream size or aspect ratio")
			}
		}
		r.Resolution = strings.ToUpper(r.Resolution)
		if r.Resolution == "" {
			r.Resolution = "1K"
		}
		if r.Resolution != "1K" && r.Resolution != "1.5K" && r.Resolution != "2K" {
			return nil, errors.New("resolution must be 1K, 1.5K or 2K")
		}
	}
	return &r, nil
}
func (r *SeedreamRequest) BillingTier() string {
	if m := seedreamPixels.FindStringSubmatch(r.Size); m != nil {
		w, _ := strconv.ParseInt(m[1], 10, 64)
		h, _ := strconv.ParseInt(m[2], 10, 64)
		return seedreamAreaTier(w * h)
	}
	if r.Resolution == "2K" {
		return "2K"
	}
	return "1K"
}
func seedreamAreaTier(area int64) string {
	if area > SeedreamLowPixelLimit {
		return "2K"
	}
	return "1K"
}

func normalizeSeedreamAccount(a *Account) error {
	if a.Extra["image_provider"] != SeedreamProvider {
		return nil
	}
	if !a.IsSeedream() || strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return infraerrors.BadRequest("INVALID_SEEDREAM_ACCOUNT", "Seedream requires an OpenAI API-key image account and APIMart API key")
	}
	base := strings.TrimSpace(a.GetCredential("base_url"))
	if base == "" {
		base = "https://api.apimart.ai"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (strings.TrimRight(u.Path, "/") != "" && strings.TrimRight(u.Path, "/") != "/v1") {
		return infraerrors.BadRequest("INVALID_SEEDREAM_ACCOUNT", "Seedream base URL must be an HTTPS origin or /v1 URL")
	}
	a.Credentials["base_url"] = strings.TrimRight(base, "/")
	a.Credentials["model_mapping"] = map[string]any{SeedreamProModel: SeedreamProModel, SeedreamFlashModel: SeedreamFlashModel}
	a.Extra["image_account"] = true
	return nil
}

type SeedreamSnapshot struct {
	GroupID int64           `json:"group_id"`
	Request SeedreamRequest `json:"request"`
}
type SeedreamFrozenPrices struct {
	Low, High *CostBreakdown
	InputCost *CostBreakdown
}

// Freeze both output tiers: automatic ratios and individual decomposed layers
// can land in either tier. Reserve the larger configured price for each output.
func (s *DurableImageService) seedreamQuote(ctx context.Context, key *APIKey, sub *UserSubscription, r *SeedreamRequest) (ImageTaskQuote, error) {
	if !key.Group.IsAgent() {
		resolved := s.resolver.Resolve(ctx, PricingInput{Model: r.Model, GroupID: key.GroupID, Group: key.Group})
		hasResolvedPricing := resolved != nil &&
			(resolved.Source == PricingSourceGroup || resolved.Source == PricingSourceChannel) &&
			(resolved.Mode == BillingModeImage || resolved.Mode == BillingModePerRequest)
		hasConfiguredTiers := apiKeyHasConfiguredImagePrice(key, "1K") && apiKeyHasConfiguredImagePrice(key, "2K")
		if !hasResolvedPricing && !hasConfiguredTiers {
			return ImageTaskQuote{}, ErrModelPricingUnavailable
		}
	}
	low, err := s.quote(ctx, key, sub, PlatformOpenAI, r.Model, "1K", 1)
	if err != nil {
		return low, err
	}
	high, err := s.quote(ctx, key, sub, PlatformOpenAI, r.Model, "2K", 1)
	if err != nil {
		return high, err
	}
	if low.PerImage == nil || high.PerImage == nil {
		return low, ErrModelPricingUnavailable
	}
	q := low
	if high.PerImage.ActualCost > low.PerImage.ActualCost {
		q = high
	}
	q.Seedream = &SeedreamFrozenPrices{Low: low.PerImage, High: high.PerImage, InputCost: &CostBreakdown{}}
	q.Size = r.BillingTier()
	q.Usage.ImageSize = &q.Size
	free, price := 1, 0.0
	if key.Group.IsAgent() {
		_, model, e := s.resolver.agentModelCatalog.ResolveMediaPrice(ctx, *key.GroupID, PlatformOpenAI, AgentMediaTypeImage, "1K", r.Model)
		if e != nil {
			return q, e
		}
		if model.FreeInputImages != nil {
			free = *model.FreeInputImages
		}
		if model.InputImageUnitPrice != nil {
			price = *model.InputImageUnitPrice
		}
		q.Seedream.InputCost, _ = s.billing.CalculateConfiguredAgentImageCost(price*float64(max(0, len(r.ImageURLs)-free)), 1)
	} else if len(r.ImageURLs) > free {
		resolved := s.resolver.Resolve(ctx, PricingInput{Model: r.Model + ":input", GroupID: key.GroupID, Group: key.Group})
		if resolved == nil || (resolved.Source != PricingSourceChannel && resolved.Source != PricingSourceGroup) || resolved.Mode != BillingModePerRequest {
			return q, ErrModelPricingUnavailable
		}
		q.Seedream.InputCost, err = s.billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: r.Model + ":input", RequestCount: len(r.ImageURLs) - free, Resolved: resolved, Resolver: s.resolver, RateMultiplier: q.Multiplier, PricingAt: q.PricingAt})
		if err != nil {
			return q, err
		}
	}
	q.Seedream.InputCost.ImageInputCost = q.Seedream.InputCost.TotalCost
	q.Seedream.InputCost.ImageOutputCost = 0
	if r.LayerDecomposition {
		q.Count = 17
	}
	charge := QuantizeUsageBillingAmount(q.PerImage.ActualCost*float64(q.Count) + q.Seedream.InputCost.ActualCost)
	q.Charge.APIKeyQuotaCost = charge
	q.Charge.APIKeyRateLimitCost = charge
	if q.Charge.SubscriptionID != nil {
		q.Charge.SubscriptionCost = charge
	} else {
		q.Charge.BalanceCost = charge
	}
	q.Usage.ActualCost = charge
	return q, nil
}
func (p *SeedreamFrozenPrices) cost(areas []int64) (*CostBreakdown, error) {
	if p == nil || p.Low == nil || p.High == nil || p.InputCost == nil {
		return nil, errors.New("missing Seedream prices")
	}
	c := *p.InputCost
	for _, area := range areas {
		if area <= 0 {
			return nil, errors.New("invalid output image area")
		}
		price := p.Low
		if seedreamAreaTier(area) == "2K" {
			price = p.High
		}
		c.TotalCost += price.TotalCost
		c.ActualCost += price.ActualCost
		c.ImageOutputCost += price.ImageOutputCost
	}
	c.ActualCost = QuantizeUsageBillingAmount(c.ActualCost)
	return &c, nil
}
func (s *DurableImageService) seedreamAccounts(ctx context.Context, snapshot *SeedreamSnapshot) ([]Account, error) {
	accounts, err := s.openAI.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, snapshot.GroupID, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})
	available := []Account{}
	for _, a := range accounts {
		if a.IsSeedream() && a.IsSchedulable() {
			available = append(available, a)
		}
	}
	return available, nil
}
func (s *DurableImageService) auditSeedream(ctx context.Context, key *APIKey, a *Account, r *SeedreamRequest) error {
	if !AccountNeedsMediaAudit(a) {
		return nil
	}
	in := MediaAuditInput{UserID: key.UserID, APIKeyID: key.ID, Model: r.Model, MediaType: "image", Prompt: r.Prompt, Images: r.ImageURLs}
	if key.User != nil {
		in.UserEmail = key.User.Email
	}
	err := s.mediaAudit.Check(ctx, a, in)
	var failure *MediaAuditError
	if errors.As(err, &failure) {
		return infraerrors.New(failure.StatusCode(), failure.Code(), failure.Error()).WithCause(failure)
	}
	return err
}

func (s *DurableImageService) executeSeedream(ctx context.Context, snapshot *SeedreamSnapshot, key *APIKey, capture *AsyncImageExecution) (int, json.RawMessage, error) {
	started := time.Now()
	// Admission already reserved the charge. Recheck identity and permissions,
	// but do not reject an admitted task because that reservation used its quota.
	if key.GroupID == nil || *key.GroupID != snapshot.GroupID || key.Group == nil || !key.Group.IsActive() ||
		key.User == nil || !key.User.IsActive() || key.IsExpired() ||
		(!key.IsActive() && key.Status != StatusAPIKeyQuotaExhausted) || !GroupAllowsImageGeneration(key.Group) ||
		(key.Group.Platform != PlatformOpenAI && !key.Group.IsAgent()) ||
		(key.Group.ModelAllowlistEnabled() && !key.Group.ModelAllowlist.Allows(snapshot.Request.Model)) {
		return 403, nil, errors.New("seedream task owner is no longer authorized")
	}
	if s.openAI.concurrencyService != nil {
		if key.User != nil {
			for {
				slot, err := s.openAI.concurrencyService.AcquireUserSlot(ctx, key.UserID, key.User.Concurrency)
				if err != nil {
					return 503, nil, err
				}
				if slot.Acquired {
					defer slot.ReleaseFunc()
					break
				}
				if err := waitMidjourneyPoll(ctx, time.Second); err != nil {
					return 503, nil, err
				}
			}
		}
	}
	// Acquire account concurrency before the only paid POST.
	var a *Account
	for a == nil {
		accounts, err := s.seedreamAccounts(ctx, snapshot)
		if err != nil || len(accounts) == 0 {
			return 503, nil, errors.New("seedream account unavailable")
		}
		for i := range accounts {
			if s.openAI.concurrencyService == nil {
				a = &accounts[i]
				break
			}
			slot, err := s.openAI.concurrencyService.AcquireAccountSlot(ctx, accounts[i].ID, accounts[i].Concurrency)
			if err != nil {
				return 503, nil, err
			}
			if slot.Acquired {
				defer slot.ReleaseFunc()
				a = &accounts[i]
				break
			}
		}
		if a == nil {
			if err := waitMidjourneyPoll(ctx, time.Second); err != nil {
				return 503, nil, err
			}
		}
	}
	if err := s.auditSeedream(ctx, key, a, &snapshot.Request); err != nil {
		return 403, nil, err
	}
	raw, err := json.Marshal(snapshot.Request)
	if err != nil {
		return 400, nil, err
	}
	body, err := s.apimartImageHTTP(ctx, a, http.MethodPost, "/v1/images/generations", raw)
	if err != nil {
		return 502, nil, err
	}
	var receipt struct {
		Code int `json:"code"`
		Data []struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &receipt) != nil || (receipt.Code != 0 && receipt.Code != 200) || len(receipt.Data) != 1 || receipt.Data[0].TaskID == "" {
		return 502, nil, errors.New("invalid Seedream receipt")
	}
	for {
		body, err = s.apimartImageHTTP(ctx, a, http.MethodGet, "/v1/tasks/"+url.PathEscape(receipt.Data[0].TaskID), nil)
		if err != nil {
			var status *midjourneyHTTPError
			if errors.As(err, &status) && status.status < 500 && status.status != 429 {
				return 502, nil, err
			}
			if err := waitMidjourneyPoll(ctx, 5*time.Second); err != nil {
				return 504, nil, err
			}
			continue
		}
		output, status, err := normalizeSeedreamResult(body, &snapshot.Request)
		if err != nil {
			return 502, nil, err
		}
		switch status {
		case "success":
			usage := capture.Quote.Usage
			usage.AccountID = a.ID
			rate := a.BillingRateMultiplier()
			usage.AccountRateMultiplier = &rate
			elapsed := int(time.Since(started).Milliseconds())
			usage.DurationMs = &elapsed
			model := snapshot.Request.Model
			usage.UpstreamModel = &model
			capture.Usage = &usage
			_ = s.openAI.accountRepo.UpdateLastUsed(ctx, a.ID)
			return 200, output, nil
		case "failed", "failure", "cancelled", "canceled":
			return 502, nil, errors.New("seedream upstream task failed")
		case "submitted", "pending", "queued", "processing", "in_progress":
		default:
			return 502, nil, errors.New("unknown Seedream task status")
		}
		if err := waitMidjourneyPoll(ctx, 5*time.Second); err != nil {
			return 504, nil, err
		}
	}
}

// Tasks may be returned directly or inside the common APIMart data envelope.
func normalizeSeedreamResult(body []byte, r *SeedreamRequest) (json.RawMessage, string, error) {
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, "", err
	}
	if envelope.Code != 0 && envelope.Code != 200 {
		return nil, "", errors.New("seedream task query failed")
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		body = envelope.Data
	}
	var result struct {
		Status string `json:"status"`
		Result struct {
			Images []struct {
				URLs    []string          `json:"url"`
				Sizes   []string          `json:"sizes"`
				Formats []string          `json:"output_formats"`
				Layers  []json.RawMessage `json:"layers"`
			} `json:"images"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", err
	}
	status := strings.ToLower(result.Status)
	if status != "success" {
		return nil, status, nil
	}
	if len(result.Result.Images) != 1 {
		return nil, "", errors.New("unexpected Seedream result groups")
	}
	images := result.Result.Images[0]
	n := len(images.URLs)
	if n < 1 || n > 17 || (!r.LayerDecomposition && n != 1) {
		return nil, "", errors.New("unexpected Seedream image count")
	}
	if r.LayerDecomposition && len(images.Layers) != n {
		return nil, "", errors.New("missing Seedream layer metadata")
	}
	data := make([]map[string]any, 0, n)
	for i, u := range images.URLs {
		if u == "" {
			return nil, "", errors.New("empty Seedream result URL")
		}
		item := map[string]any{"url": u, "index": i}
		if i < len(images.Sizes) {
			item["size"] = images.Sizes[i]
		}
		if i < len(images.Formats) {
			item["output_format"] = images.Formats[i]
		}
		if r.LayerDecomposition {
			item["layer"] = images.Layers[i]
		}
		data = append(data, item)
	}
	output, err := json.Marshal(map[string]any{"model": r.Model, "layer_decomposition": r.LayerDecomposition, "data": data})
	return output, status, err
}

// EstimateSeedream uses the same frozen reservation as actual task admission.
func (s *AgentModelCatalogService) EstimateSeedream(ctx context.Context, key *APIKey, billing *BillingService, r *SeedreamRequest) (ImageTaskQuote, error) {
	if key == nil || key.GroupID == nil || key.Group == nil || !key.Group.IsAgent() {
		return ImageTaskQuote{}, ErrAgentModelNotConfigured
	}
	adapter := &DurableImageService{billing: billing, resolver: &ModelPricingResolver{agentModelCatalog: s}}
	return adapter.seedreamQuote(ctx, key, nil, r)
}

func (s *DurableImageService) prepareSeedream(ctx context.Context, key *APIKey, snapshot *SeedreamSnapshot) error {
	snapshot.GroupID = *key.GroupID
	accounts, err := s.seedreamAccounts(ctx, snapshot)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return infraerrors.New(http.StatusServiceUnavailable, "SEEDREAM_ACCOUNT_UNAVAILABLE", "No available Seedream account for this task")
	}
	return s.auditSeedream(ctx, key, &accounts[0], &snapshot.Request)
}

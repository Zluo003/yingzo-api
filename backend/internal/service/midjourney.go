package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MidjourneyModel = "midjourney-v8.2"

// The current upstream accepts fewer than 1024 Unicode characters.
const MidjourneyPromptMaxCharacters = 1023
const MidjourneyProvider = "apimart_midjourney"
const AgentInterfaceMidjourney = "midjourney.generations"

var midjourneyAspectRatio = regexp.MustCompile(`^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$`)

// One v8.2 Fast generation/edit or U1-U4 selection per task. Native flags and
// legacy character-reference parameters are deliberately not exposed.
type MidjourneyRequest struct {
	Model          string   `json:"model,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	Version        string   `json:"version,omitempty"`
	Speed          string   `json:"speed,omitempty"`
	Size           string   `json:"size,omitempty"`
	Quality        string   `json:"quality,omitempty"`
	Style          string   `json:"style,omitempty"`
	Seed           *uint32  `json:"seed,omitempty"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	Stylize        *int     `json:"stylize,omitempty"`
	Chaos          *int     `json:"chaos,omitempty"`
	Weird          *int     `json:"weird,omitempty"`
	Tile           bool     `json:"tile,omitempty"`
	Raw            bool     `json:"raw,omitempty"`
	ImageURLs      []string `json:"image_urls,omitempty"`
	ImageWeight    *float64 `json:"iw,omitempty"`
	StyleReference string   `json:"sref,omitempty"`
	StyleWeight    *int     `json:"sw,omitempty"`
	TaskID         string   `json:"task_id,omitempty"`
	Index          int      `json:"index,omitempty"`
}

func ParseMidjourneyRequest(body []byte, action string) (*MidjourneyRequest, error) {
	if action != "imagine" && action != "edits" && action != "upscale" {
		return nil, errors.New("unsupported Midjourney action")
	}
	var r MidjourneyRequest
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid Midjourney request: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("request must contain one JSON object")
	}
	if r.Model != "" && r.Model != MidjourneyModel {
		return nil, errors.New("only midjourney-v8.2 is supported")
	}
	if r.Version != "" && r.Version != "8.2" {
		return nil, errors.New("only version 8.2 is supported")
	}
	r.Model, r.Version = MidjourneyModel, "8.2"
	if r.Speed != "" && r.Speed != "fast" {
		return nil, errors.New("midjourney v8.2 only supports fast speed")
	}
	r.Speed = "fast"
	if action == "upscale" {
		if !strings.HasPrefix(r.TaskID, "imgtask_") || r.Index < 1 || r.Index > 4 {
			return nil, errors.New("upscale requires a Yingzo task_id and index between 1 and 4")
		}
		if r.Prompt != "" || r.Size != "" || r.Quality != "" || r.Style != "" || r.Seed != nil || r.NegativePrompt != "" || r.Stylize != nil || r.Chaos != nil || r.Weird != nil || r.Tile || r.Raw || len(r.ImageURLs) > 0 || r.ImageWeight != nil || r.StyleReference != "" || r.StyleWeight != nil {
			return nil, errors.New("upscale accepts only model, version, task_id, index and speed")
		}
		r.Speed = "fast"
		return &r, nil
	}
	r.Prompt = strings.TrimSpace(r.Prompt)
	if r.Prompt == "" {
		return nil, errors.New("prompt must not be empty")
	}
	if length := utf8.RuneCountInString(r.Prompt); length > MidjourneyPromptMaxCharacters {
		return nil, fmt.Errorf("midjourney 提示词最多 %d 个字符，当前 %d 个字符，请至少删减 %d 个字符后重试", MidjourneyPromptMaxCharacters, length, length-MidjourneyPromptMaxCharacters)
	}
	// Native prompt flags can change version, action, speed or number of jobs.
	// Keep all controllable parameters in validated structured fields.
	for _, value := range []string{r.Prompt, r.NegativePrompt} {
		lower := strings.ToLower(value)
		if strings.Contains(value, "--") || strings.Contains(lower, "https:") || strings.Contains(lower, "http:") || strings.Contains(lower, "data:") {
			return nil, errors.New("use plain text without image URLs or native --parameters; use the structured fields instead")
		}
	}
	if r.TaskID != "" || r.Index != 0 {
		return nil, errors.New("task_id and index are only valid for upscale")
	}
	if len(r.ImageURLs) > 4 || (action == "edits" && len(r.ImageURLs) == 0) {
		return nil, errors.New("image_urls accepts up to 4 images; edits requires at least one image")
	}
	for _, imageURL := range r.ImageURLs {
		if !validMidjourneyImageURL(imageURL) {
			return nil, errors.New("image_urls must contain public HTTPS image URLs without credentials or native parameters")
		}
	}
	if r.StyleReference != "" && !validMidjourneyImageURL(r.StyleReference) {
		return nil, errors.New("sref must be a public HTTPS image URL without credentials or native parameters")
	}
	if r.StyleWeight != nil && (r.StyleReference == "" || *r.StyleWeight < 0 || *r.StyleWeight > 1000) {
		return nil, errors.New("sw requires sref and must be between 0 and 1000")
	}
	if r.ImageWeight != nil && (action != "imagine" || len(r.ImageURLs) == 0 || *r.ImageWeight < 0 || *r.ImageWeight > 3 || math.IsNaN(*r.ImageWeight) || math.IsInf(*r.ImageWeight, 0)) {
		return nil, errors.New("iw requires imagine image_urls and must be between 0 and 3")
	}
	if action == "edits" && r.Tile {
		return nil, errors.New("edits does not support tile")
	}
	if r.Size != "" && !midjourneyAspectRatio.MatchString(r.Size) {
		return nil, errors.New("size must be an aspect ratio such as 16:9")
	}
	if r.Quality == "" {
		r.Quality = "2"
	}
	switch r.Quality {
	case "0.25", "0.5", "1", "2":
	default:
		return nil, errors.New("quality must be 0.25, 0.5, 1 or 2")
	}
	if r.Style != "" && r.Style != "raw" {
		return nil, errors.New("style must be raw")
	}
	for _, field := range []struct {
		name  string
		value *int
		max   int
	}{{"stylize", r.Stylize, 1000}, {"chaos", r.Chaos, 100}, {"weird", r.Weird, 3000}} {
		if field.value != nil && (*field.value < 0 || *field.value > field.max) {
			return nil, fmt.Errorf("%s must be between 0 and %d", field.name, field.max)
		}
	}
	return &r, nil
}

func validMidjourneyImageURL(value string) bool {
	if len(value) > 8192 || strings.ContainsAny(value, " \t\r\n") || strings.Contains(value, "--") {
		return false
	}
	u, err := url.ParseRequestURI(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func (r *MidjourneyRequest) PriceTier() string {
	if r.TaskID != "" {
		return "upscale"
	}
	return "generation"
}
func IsMidjourneyPriceTier(tier string) bool {
	switch tier {
	case "generation", "upscale":
		return true
	}
	return false
}
func MidjourneyBillingModel(tier string) string { return MidjourneyModel + ":" + tier }
func (a *Account) IsMidjourney() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey && a.Extra["image_provider"] == MidjourneyProvider
}

// Called for both account create and edit after credentials have been merged.
func normalizeMidjourneyAccount(a *Account) error {
	if a.Extra["image_provider"] != MidjourneyProvider {
		return nil
	}
	if !a.IsMidjourney() {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Midjourney requires an OpenAI API-key image account")
	}
	if strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "APIMart API key is required")
	}
	base := strings.TrimSpace(a.GetCredential("base_url"))
	if base == "" {
		base = "https://api.apimart.ai"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Midjourney base URL must be an HTTPS origin or /v1 URL")
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" && p != "/v1" {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Midjourney base URL must not include an endpoint path")
	}
	a.Credentials["base_url"] = strings.TrimRight(base, "/")
	a.Credentials["model_mapping"] = map[string]any{MidjourneyModel: MidjourneyModel}
	a.Extra["image_account"] = true
	return nil
}

type MidjourneySnapshot struct {
	Action           string            `json:"action,omitempty"`
	GroupID          int64             `json:"group_id"`
	AccountID        int64             `json:"account_id"`
	ParentUpstreamID string            `json:"parent_upstream_id,omitempty"`
	Request          MidjourneyRequest `json:"request"`
}
type midjourneyReference struct {
	AccountID  int64  `json:"account_id"`
	UpstreamID string `json:"upstream_id"`
	Action     string `json:"action"`
	ImageCount int    `json:"image_count,omitempty"`
}

func (s *DurableImageService) prepareMidjourney(ctx context.Context, key *APIKey, r *MidjourneyRequest, action string) (*MidjourneySnapshot, error) {
	snapshot := &MidjourneySnapshot{GroupID: *key.GroupID, Request: *r, Action: action}
	if r.TaskID != "" {
		parent, err := s.ledger.Find(ctx, ImageTaskOwner{key.UserID, key.ID}, r.TaskID, false)
		if err != nil {
			return nil, err
		}
		if parent.Quote.Model != MidjourneyModel || parent.Status != ImageTaskStatusCompleted || (parent.Quote.Size != "generation" && !strings.HasPrefix(parent.Quote.Size, "imagine_")) {
			return nil, infraerrors.BadRequest("INVALID_MIDJOURNEY_PARENT", "parent must be a completed Midjourney v8.2 generation task")
		}
		if time.Now().Unix() >= parent.ExpiresAt {
			return nil, ErrImageTaskExpired
		}
		raw, err := s.encryptor.Decrypt(parent.Quote.EncryptedProviderReference)
		if err != nil {
			return nil, ErrImageTaskUnavailable
		}
		var saved midjourneyReference
		if json.Unmarshal([]byte(raw), &saved) != nil || saved.AccountID <= 0 || saved.UpstreamID == "" || (saved.Action != "imagine" && saved.Action != "edits") {
			return nil, ErrImageTaskUnavailable
		}
		if saved.ImageCount > 0 && r.Index > saved.ImageCount {
			return nil, infraerrors.BadRequest("INVALID_MIDJOURNEY_INDEX", "index exceeds the number of images returned by the parent task")
		}
		snapshot.AccountID, snapshot.ParentUpstreamID = saved.AccountID, saved.UpstreamID
	}
	accounts, err := s.midjourneyAccounts(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if len(accounts) > 0 {
		return snapshot, nil
	}
	return nil, infraerrors.New(http.StatusServiceUnavailable, "MIDJOURNEY_ACCOUNT_UNAVAILABLE", "No available Midjourney account for this task")
}

func (s *DurableImageService) midjourneyAccounts(ctx context.Context, snapshot *MidjourneySnapshot) ([]Account, error) {
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
	available := make([]Account, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if a.IsMidjourney() && a.IsSchedulable() && (snapshot.AccountID == 0 || snapshot.AccountID == a.ID) {
			available = append(available, *a)
		}
	}
	return available, nil
}

func (s *DurableImageService) executeMidjourney(ctx context.Context, snapshot *MidjourneySnapshot, key *APIKey, capture *AsyncImageExecution) (int, json.RawMessage, error) {
	started := time.Now()
	// Admission already reserved the charge. Recheck identity and permissions,
	// but do not reject an admitted task because that reservation used its quota.
	if key.GroupID == nil || *key.GroupID != snapshot.GroupID || key.Group == nil || !key.Group.IsActive() ||
		key.User == nil || !key.User.IsActive() || key.IsExpired() ||
		(!key.IsActive() && key.Status != StatusAPIKeyQuotaExhausted) || !GroupAllowsImageGeneration(key.Group) ||
		(key.Group.Platform != PlatformOpenAI && !key.Group.IsAgent()) ||
		(key.Group.ModelAllowlistEnabled() && !key.Group.ModelAllowlist.Allows(MidjourneyModel)) {
		return 403, nil, errors.New("midjourney task owner is no longer authorized")
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
	// Choose a free account before the only POST. Upscale stays pinned to its
	// parent account; imagine can use another account when the first is busy.
	var a *Account
	for a == nil {
		accounts, err := s.midjourneyAccounts(ctx, snapshot)
		if err != nil || len(accounts) == 0 {
			return 503, nil, errors.New("midjourney account unavailable")
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
	r := snapshot.Request
	r.Speed = "fast"
	action := "imagine"
	var payload any
	path := "/v1/midjourney/generations"
	if r.TaskID != "" {
		action = "upscale"
		path += "/upscale"
		payload = map[string]any{"task_id": snapshot.ParentUpstreamID, "index": r.Index, "speed": "fast"}
	} else {
		if snapshot.Action == "edits" {
			action = "edits"
			path += "/edits"
		}
		r.Model = ""
		r.Version = "8.2"
		payload = r
	}
	raw, _ := json.Marshal(payload)
	body, err := s.midjourneyHTTP(ctx, a, http.MethodPost, path, raw)
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
		return 502, nil, errors.New("invalid Midjourney receipt")
	}
	upstreamID := receipt.Data[0].TaskID
	for {
		body, err = s.midjourneyHTTP(ctx, a, http.MethodGet, "/v1/midjourney/"+url.PathEscape(upstreamID), nil)
		if err != nil {
			// GET may be retried safely; never resubmit a POST with an uncertain outcome.
			var status *midjourneyHTTPError
			if errors.As(err, &status) && status.status < 500 && status.status != 429 {
				return 502, nil, err
			}
			if err := waitMidjourneyPoll(ctx, 5*time.Second); err != nil {
				return 504, nil, err
			}
			continue
		}
		var result struct {
			Status string   `json:"status"`
			Grid   string   `json:"grid_image_url"`
			Images []string `json:"image_urls"`
		}
		if json.Unmarshal(body, &result) != nil {
			return 502, nil, errors.New("invalid Midjourney task response")
		}
		switch strings.ToUpper(result.Status) {
		case "SUCCESS":
			items := []map[string]any{}
			imageCount := len(result.Images)
			if result.Grid != "" && (action == "imagine" || (action == "edits" && imageCount == 0)) {
				imageCount = 4
				items = append(items, map[string]any{"url": result.Grid, "kind": "grid"})
			} else {
				if imageCount < 1 || imageCount > 4 || (action == "imagine" && imageCount != 4) || (action == "upscale" && imageCount != 1) {
					return 502, nil, errors.New("unexpected Midjourney image count")
				}
				for i, u := range result.Images {
					items = append(items, map[string]any{"url": u, "index": i + 1, "kind": "image"})
				}
			}
			output, _ := json.Marshal(map[string]any{"model": MidjourneyModel, "version": "8.2", "action": action, "speed": r.Speed, "index": r.Index, "parent_task_id": r.TaskID, "data": items, "_midjourney": midjourneyReference{AccountID: a.ID, UpstreamID: upstreamID, Action: action, ImageCount: imageCount}})
			usage := capture.Quote.Usage
			usage.AccountID = a.ID
			usage.ImageCount = 1
			usage.TotalCost = capture.Quote.PerImage.TotalCost
			usage.ImageOutputCost = capture.Quote.PerImage.ImageOutputCost
			rate := a.BillingRateMultiplier()
			usage.AccountRateMultiplier = &rate
			elapsed := int(time.Since(started).Milliseconds())
			usage.DurationMs = &elapsed
			upstreamModel := "midjourney"
			usage.UpstreamModel = &upstreamModel
			capture.Usage = &usage
			_ = s.openAI.accountRepo.UpdateLastUsed(ctx, a.ID)
			return 200, output, nil
		case "FAILURE", "FAILED", "CANCELLED":
			return 502, nil, errors.New("midjourney upstream task failed")
		case "NOT_START", "SUBMITTED", "IN_PROGRESS", "PROCESSING", "PENDING", "QUEUED":
		default:
			return 502, nil, errors.New("unknown Midjourney task status")
		}
		if err := waitMidjourneyPoll(ctx, 5*time.Second); err != nil {
			return 504, nil, err
		}
	}
}
func waitMidjourneyPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type midjourneyHTTPError struct{ status int }

func (e *midjourneyHTTPError) Error() string {
	return fmt.Sprintf("Midjourney upstream HTTP %d", e.status)
}
func (s *DurableImageService) midjourneyHTTP(ctx context.Context, a *Account, method, path string, body []byte) ([]byte, error) {
	base := strings.TrimSuffix(strings.TrimRight(a.GetCredential("base_url"), "/"), "/v1")
	if base == "" {
		base = "https://api.apimart.ai"
	}
	base, err := s.openAI.validateUpstreamBaseURL(base)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.GetCredential("api_key"))
	req.Header.Set("Content-Type", "application/json")
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	resp, err := s.openAI.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &midjourneyHTTPError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2*1024*1024 {
		return nil, errors.New("midjourney response too large")
	}
	return data, nil
}

func billingUnitForAgentModel(model, mediaType string) string {
	if model == SunoModel {
		return "request"
	}
	if model == MidjourneyModel {
		return "request"
	}
	return billingUnitForAgentMedia(mediaType)
}
func normalizeAgentModelPricesForModel(model, mediaType string, prices []AgentModelPrice) ([]AgentModelPrice, error) {
	if model == SunoModel {
		return normalizeSunoPrices(mediaType, prices)
	}
	if model != MidjourneyModel {
		return normalizeAgentModelPrices(mediaType, prices)
	}
	if mediaType != AgentMediaTypeImage {
		return nil, errors.New("midjourney is an image model")
	}
	out := make([]AgentModelPrice, 0, len(prices))
	seen := map[string]bool{}
	for _, p := range prices {
		if !IsMidjourneyPriceTier(p.Resolution) || seen[p.Resolution] || p.UnitPrice < 0 || math.IsNaN(p.UnitPrice) || math.IsInf(p.UnitPrice, 0) {
			return nil, errors.New("midjourney prices must use unique generation or upscale operations with finite non-negative prices")
		}
		seen[p.Resolution] = true
		p.BillingUnit = "request"
		out = append(out, p)
	}
	return out, nil
}

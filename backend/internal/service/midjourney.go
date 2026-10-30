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
const MidjourneyProvider = "discord_midjourney"
const AgentInterfaceMidjourney = "midjourney.generations"

var midjourneySnowflake = regexp.MustCompile(`^[0-9]{17,20}$`)
var midjourneyAspectRatio = regexp.MustCompile(`^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$`)

// One v8.2 Fast generation/edit, selection or follow-up per task. Native flags and
// legacy character-reference parameters are deliberately not exposed.
type MidjourneyRequest struct {
	Operation      string   `json:"operation,omitempty"`
	Mode           string   `json:"mode,omitempty"`
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
	if action != "imagine" && action != "edits" && action != "select" && action != "upscale" && action != "variations" {
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
	if r.Operation != "" && r.Operation != action {
		return nil, errors.New("operation does not match request endpoint")
	}
	r.Operation = action
	if action == "select" || action == "upscale" || action == "variations" {
		if !strings.HasPrefix(r.TaskID, "imgtask_") {
			return nil, errors.New("a Yingzo parent task_id is required")
		}
		if action == "select" {
			if r.Index < 1 || r.Index > 4 || r.Mode != "" {
				return nil, errors.New("select requires index 1–4 and no mode")
			}
		} else {
			if r.Index != 0 {
				return nil, errors.New("select a single image first; upscale and variations do not accept index")
			}
			if r.Mode == "" {
				r.Mode = "subtle"
			}
			validMode := r.Mode == "subtle" || action == "upscale" && r.Mode == "creative" || action == "variations" && r.Mode == "strong"
			if !validMode {
				return nil, errors.New("unsupported action mode")
			}
		}
		if r.Prompt != "" || r.Size != "" || r.Quality != "" || r.Style != "" || r.Seed != nil || r.NegativePrompt != "" || r.Stylize != nil || r.Chaos != nil || r.Weird != nil || r.Tile || r.Raw || len(r.ImageURLs) > 0 || r.ImageWeight != nil || r.StyleReference != "" || r.StyleWeight != nil {
			return nil, errors.New("follow-up actions accept only operation, model, version, task_id, index or mode and speed")
		}
		return &r, nil
	}
	if r.Mode != "" {
		return nil, errors.New("mode is only valid for upscale and variations")
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
		if strings.ContainsAny(value, "{}") || strings.Contains(value, "--") || strings.Contains(lower, "https:") || strings.Contains(lower, "http:") || strings.Contains(lower, "data:") {
			return nil, errors.New("use plain text without image URLs or native --parameters; use the structured fields instead")
		}
	}
	if r.TaskID != "" || r.Index != 0 {
		return nil, errors.New("task_id and index are only valid for follow-up actions")
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
	switch r.Quality {
	case "", "1":
	default:
		return nil, errors.New("discord v8.2 supports quality 1 in this integration")
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
	if _, err := r.DiscordPrompt(action); err != nil {
		return nil, err
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
	switch r.Operation {
	case "select":
		return "select"
	case "upscale":
		return "upscale"
	}
	return "generation"
}
func IsMidjourneyPriceTier(tier string) bool {
	switch tier {
	case "generation", "select", "upscale":
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
	if a.Extra["image_provider"] == "apimart_midjourney" {
		return infraerrors.BadRequest("MIDJOURNEY_PROVIDER_RETIRED", "APIMart Midjourney was removed; configure a Discord MJ account")
	}
	if a.Extra["image_provider"] != MidjourneyProvider {
		return nil
	}
	if !a.IsMidjourney() {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Midjourney requires an OpenAI API-key image account")
	}
	if a.Concurrency < 1 || a.Concurrency > 100 {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "MJ concurrency must be between 1 and 100")
	}
	if strings.TrimSpace(a.GetCredential("discord_token")) == "" || strings.HasPrefix(a.GetCredential("discord_token"), "Bot ") {
		return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Discord user token is required")
	}
	for _, field := range []string{"discord_server_id", "discord_channel_id"} {
		if !midjourneySnowflake.MatchString(a.GetCredential(field)) {
			return infraerrors.BadRequest("INVALID_MIDJOURNEY_ACCOUNT", "Discord server/channel IDs must be 17–20 digits")
		}
	}
	delete(a.Credentials, "api_key")
	delete(a.Credentials, "base_url")
	a.Credentials["model_mapping"] = map[string]any{MidjourneyModel: MidjourneyModel}
	a.Extra["image_account"] = true
	return nil
}

type MidjourneySnapshot struct {
	Action           string               `json:"action,omitempty"`
	GroupID          int64                `json:"group_id"`
	AccountID        int64                `json:"account_id"`
	ParentUpstreamID string               `json:"parent_upstream_id,omitempty"`
	Parent           *midjourneyReference `json:"parent,omitempty"`
	Request          MidjourneyRequest    `json:"request"`
}
type midjourneyReference struct {
	Prompt     string             `json:"prompt,omitempty"`
	Provider   string             `json:"provider"`
	ChannelID  string             `json:"channel_id"`
	ServerID   string             `json:"server_id"`
	MessageID  string             `json:"message_id"`
	Hash       string             `json:"hash"`
	Flags      int                `json:"flags"`
	Options    []midjourneyOption `json:"options"`
	AccountID  int64              `json:"account_id"`
	UpstreamID string             `json:"upstream_id"`
	Action     string             `json:"action"`
	ImageCount int                `json:"image_count,omitempty"`
}

func (s *DurableImageService) prepareMidjourney(ctx context.Context, key *APIKey, r *MidjourneyRequest, action string) (*MidjourneySnapshot, error) {
	snapshot := &MidjourneySnapshot{GroupID: *key.GroupID, Request: *r, Action: action}
	if r.TaskID != "" {
		parent, err := s.ledger.Find(ctx, ImageTaskOwner{key.UserID, key.ID}, r.TaskID, false)
		if err != nil {
			return nil, err
		}
		if parent.Quote.Model != MidjourneyModel || parent.Status != ImageTaskStatusCompleted {
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
		if json.Unmarshal([]byte(raw), &saved) != nil || saved.AccountID <= 0 || saved.Provider != MidjourneyProvider || saved.MessageID == "" {
			return nil, ErrImageTaskUnavailable
		}
		if (action == "select" && saved.ImageCount != 4) || ((action == "upscale" || action == "variations") && saved.ImageCount != 1) {
			return nil, infraerrors.BadRequest("INVALID_MIDJOURNEY_INDEX", "index exceeds the number of images returned by the parent task")
		}
		snapshot.AccountID, snapshot.ParentUpstreamID = saved.AccountID, saved.UpstreamID
		snapshot.Parent = &saved
		if _, err := midjourneyActionID(saved.Options, action, r.Index, r.Mode); err != nil {
			return nil, infraerrors.BadRequest("MIDJOURNEY_ACTION_UNAVAILABLE", err.Error())
		}
	}
	accounts, err := s.midjourneyAccounts(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if len(accounts) > 0 {
		if auditErr := s.auditMidjourneyInput(ctx, key, &accounts[0], r); auditErr != nil {
			return nil, auditErr
		}
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
		if accounts[i].LastUsedAt == nil {
			return accounts[j].LastUsedAt != nil || accounts[i].ID < accounts[j].ID
		}
		if accounts[j].LastUsedAt == nil {
			return false
		}
		if !accounts[i].LastUsedAt.Equal(*accounts[j].LastUsedAt) {
			return accounts[i].LastUsedAt.Before(*accounts[j].LastUsedAt)
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
	if auditErr := s.auditMidjourneyInput(ctx, key, a, &snapshot.Request); auditErr != nil {
		var failure *MediaAuditError
		if errors.As(auditErr, &failure) {
			return failure.StatusCode(), nil, auditErr
		}
		return 503, nil, auditErr
	}
	r := snapshot.Request
	action := snapshot.Action
	if action == "" {
		action = "imagine"
	}
	if capture == nil || capture.Quote == nil || capture.Quote.PerImage == nil || capture.TaskID == "" {
		return 503, nil, ErrImageTaskUnavailable
	}
	if snapshot.Parent != nil && (snapshot.Parent.ChannelID != a.GetCredential("discord_channel_id") || snapshot.Parent.ServerID != a.GetCredential("discord_server_id")) {
		return 400, nil, errors.New("parent Discord channel changed")
	}
	payload := midjourneyBridgeSubmission{ID: capture.TaskID, Account: midjourneyBridgeAccountFor(a), Action: action, Parent: snapshot.Parent, Index: r.Index, Mode: r.Mode}
	if r.TaskID == "" {
		prompt, promptErr := r.DiscordPrompt(action)
		if promptErr != nil {
			return 400, nil, promptErr
		}
		payload.Prompt = prompt
	}
	raw, _ := json.Marshal(payload)
	body, err := midjourneyBridgeHTTP(ctx, http.MethodPost, "/tasks", raw)
	if err != nil {
		return 502, nil, err
	}
	var receipt struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &receipt) != nil || receipt.ID != capture.TaskID {
		return 502, nil, errors.New("invalid Discord task receipt")
	}
	for {
		body, err = midjourneyBridgeHTTP(ctx, http.MethodGet, "/tasks/"+url.PathEscape(receipt.ID), nil)
		if err != nil {
			var status *midjourneyHTTPError
			if errors.As(err, &status) && status.status < 500 && status.status != 429 {
				return 502, nil, err
			}
			if err := waitMidjourneyPoll(ctx, 3*time.Second); err != nil {
				return 504, nil, err
			}
			continue
		}
		var result midjourneyBridgeResult
		if json.Unmarshal(body, &result) != nil {
			return 502, nil, errors.New("invalid Discord task response")
		}
		switch result.Status {
		case "completed":
			if !validDiscordImageURL(result.URI) || result.MessageID == "" {
				return 502, nil, errors.New("invalid Discord result")
			}
			if snapshot.Parent != nil {
				payload.Prompt = snapshot.Parent.Prompt
			}
			imageCount := 1
			for _, o := range result.Options {
				if o.Label == "U1" {
					imageCount = 4
				}
			}
			kind := "image"
			if imageCount == 4 {
				kind = "grid"
			}
			ref := midjourneyReference{Provider: MidjourneyProvider, AccountID: a.ID, UpstreamID: receipt.ID, Action: action, ImageCount: imageCount, ChannelID: a.GetCredential("discord_channel_id"), ServerID: a.GetCredential("discord_server_id"), MessageID: result.MessageID, Hash: result.Hash, Flags: result.Flags, Options: result.Options, Prompt: payload.Prompt}
			output, _ := json.Marshal(map[string]any{"model": MidjourneyModel, "version": "8.2", "action": action, "speed": "fast", "index": r.Index, "mode": r.Mode, "parent_task_id": r.TaskID, "data": []map[string]any{{"url": result.URI, "kind": kind, "width": result.Width, "height": result.Height}}, "_midjourney": ref, "available_actions": midjourneyAvailableActions(result.Options)})
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
		case "failed", "interrupted":
			capture.TaskError = midjourneyTaskFailure(result)
			// A failed task is refunded independently. Keep the account eligible
			// for the next request rather than imposing an account-wide cooldown.
			return capture.TaskError.Code, nil, errors.New("discord task failed")
		case "queued", "processing":
		default:
			return 502, nil, errors.New("unknown Discord task status")
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
	return fmt.Sprintf("image upstream HTTP %d", e.status)
}

func (s *DurableImageService) apimartImageHTTP(ctx context.Context, a *Account, method, path string, body []byte) ([]byte, error) {
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
		return nil, errors.New("APIMart image response too large")
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
	if IsMiMoSpeechModel(model) {
		if mediaType != AgentMediaTypeAudio {
			return nil, errors.New("MiMo speech is an audio model")
		}
		return normalizeAgentModelPrices(AgentMediaTypeText, prices)
	}
	if IsSeedanceModel(model) && mediaType == AgentMediaTypeVideo {
		return normalizeSeedanceChannelPrices(prices)
	}
	for _, p := range prices {
		if p.VideoChannelID != "" {
			return nil, errors.New("video channels require a Seedance model")
		}
	}
	if model == SunoModel {
		return normalizeSunoPrices(mediaType, prices)
	}
	if IsSeedreamModel(model) {
		if mediaType != AgentMediaTypeImage {
			return nil, errors.New("seedream is an image model")
		}
		normalized := make([]AgentModelPrice, len(prices))
		for i, price := range prices {
			resolution, err := normalizeSeedreamPriceTier(price.Resolution)
			if err != nil {
				return nil, err
			}
			price.Resolution = resolution
			normalized[i] = price
		}
		return normalizeAgentModelPrices(mediaType, normalized)
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
			return nil, errors.New("midjourney prices must use unique generation, select or upscale operations with finite non-negative prices")
		}
		seen[p.Resolution] = true
		p.BillingUnit = "request"
		out = append(out, p)
	}
	return out, nil
}

func (s *DurableImageService) auditMidjourneyInput(ctx context.Context, key *APIKey, account *Account, r *MidjourneyRequest) error {
	if !AccountNeedsMediaAudit(account) {
		return nil
	}
	in := MediaAuditInput{UserID: key.UserID, APIKeyID: key.ID, Model: MidjourneyModel, MediaType: "image", Prompt: r.Prompt + "\n" + r.NegativePrompt, Images: append([]string(nil), r.ImageURLs...)}
	if key.User != nil {
		in.UserEmail = key.User.Email
	}
	if r.StyleReference != "" {
		in.Images = append(in.Images, r.StyleReference)
	}
	// Parent references are opaque upstream IDs; do not silently audit an empty request.
	in.UnsupportedMedia = r.TaskID != ""
	err := s.mediaAudit.Check(ctx, account, in)
	var failure *MediaAuditError
	if errors.As(err, &failure) {
		return infraerrors.New(failure.StatusCode(), failure.Code(), failure.Error()).WithCause(failure)
	}
	return err
}

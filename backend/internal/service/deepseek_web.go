package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// DeepSeekWebError reports sanitized service errors without upstream credentials or bodies.
type DeepSeekWebError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string
}

func (e *DeepSeekWebError) Error() string { return e.Message }
func webToolError(status int, code, message string) *DeepSeekWebError {
	return &DeepSeekWebError{Status: status, Code: code, Message: message}
}

// ValidateDeepSeekWebRequest rejects model fields and invalid Search / Fetch API inputs.
// The normalized JSON is the complete upstream payload; clients cannot select accounts or targets.
func ValidateDeepSeekWebRequest(operation string, body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload any
	switch operation {
	case "search":
		req := struct {
			Query            string  `json:"query"`
			Count            *int    `json:"count,omitempty"`
			OriginalQuestion *string `json:"ori_question,omitempty"`
			Localization     *string `json:"localization,omitempty"`
			NeedContent      bool    `json:"need_content"`
		}{}
		if err := decoder.Decode(&req); err != nil {
			return nil, errors.New("invalid search JSON or unsupported fields")
		}
		if n := utf8.RuneCountInString(req.Query); n < 1 || n > 128 || strings.TrimSpace(req.Query) == "" {
			return nil, errors.New("query must contain 1–128 characters")
		}
		if req.Count != nil && (*req.Count < 1 || *req.Count > 50) {
			return nil, errors.New("count must be between 1 and 50")
		}
		if q := req.OriginalQuestion; q != nil && (strings.TrimSpace(*q) == "" || utf8.RuneCountInString(*q) > 128) {
			return nil, errors.New("ori_question must contain 1–128 characters")
		}
		if req.Localization != nil && net.ParseIP(*req.Localization) == nil {
			return nil, errors.New("localization must be an IP address")
		}
		payload = req
	case "fetch":
		req := struct {
			URL string `json:"url"`
		}{}
		if err := decoder.Decode(&req); err != nil {
			return nil, errors.New("invalid fetch JSON or unsupported fields")
		}
		if !validWebToolURL(req.URL) || utf8.RuneCountInString(req.URL) > 8192 {
			return nil, errors.New("url must be an HTTP(S) URL without credentials, at most 8192 characters")
		}
		payload = req
	default:
		return nil, errors.New("unsupported web operation")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("request must contain one JSON object")
	}
	return json.Marshal(payload)
}

func validWebToolURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// deepSeekWebResponseBillable validates the response before any deduction.
// Empty results/content succeed without billing. Unknown result fields remain intact.
func deepSeekWebResponseBillable(operation string, body []byte) (bool, error) {
	if operation == "fetch" {
		var result struct {
			Title   *string `json:"title"`
			Content *string `json:"content"`
		}
		if err := json.Unmarshal(body, &result); err != nil || result.Title == nil || result.Content == nil {
			return false, errors.New("invalid fetch response")
		}
		return strings.TrimSpace(*result.Content) != "", nil
	}
	var result struct {
		Results *[]struct {
			Title   *string `json:"title"`
			URL     string  `json:"url"`
			Snippet *string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Results == nil {
		return false, errors.New("invalid search response")
	}
	for _, item := range *result.Results {
		if item.Title == nil || item.Snippet == nil || !validWebToolURL(item.URL) {
			return false, errors.New("invalid search result")
		}
	}
	return len(*result.Results) > 0, nil
}

// DeepSeekWebUsage supplies authenticated billing identities and request metadata.
// Prices are global tool prices, independent of model, channel, group or peak multipliers.
type DeepSeekWebUsage struct {
	APIKey        *APIKey
	Subscription  *UserSubscription
	APIKeyService APIKeyQuotaUpdater
	QuotaPlatform string
	UserAgent     string
	IPAddress     string
}

// RunDeepSeekWeb forwards one validated operation and bills only nonempty HTTP 200 responses.
// No model request, account fallback or implicit upstream retry is performed. Search / Fetch
// throttling is independent of chat cooldowns; upstream Retry-After is preserved for callers.
func (s *GatewayService) RunDeepSeekWeb(ctx context.Context, operation string, payload []byte, usage DeepSeekWebUsage) ([]byte, string, error) {
	cfg, err := s.settingService.GetWebSearchEmulationConfig(ctx)
	if err != nil {
		return nil, "", webToolError(503, "web_service_unavailable", "Unable to load web service settings")
	}
	d := cfg.DeepSeek
	if d == nil || !d.Enabled {
		return nil, "", webToolError(503, "web_service_disabled", "Online search is temporarily unavailable")
	}
	if err := validateWebSearchConfig(cfg); err != nil {
		return nil, "", webToolError(503, "web_service_misconfigured", "DeepSeek web service configuration is invalid")
	}
	account, err := s.accountRepo.GetByID(ctx, d.AccountID)
	if err != nil || account == nil {
		return nil, "", webToolError(503, "web_account_unavailable", "Configured DeepSeek account is unavailable")
	}
	if account.Platform != PlatformDeepseek || account.Type != AccountTypeAPIKey || !account.IsActive() || !account.Schedulable || account.IsQuotaExceeded() ||
		(account.AutoPauseOnExpired && account.ExpiresAt != nil && !time.Now().Before(*account.ExpiresAt)) || strings.TrimSpace(account.GetCNAPIKey()) == "" {
		return nil, "", webToolError(503, "web_account_unavailable", "Online search is temporarily unavailable")
	}
	base := strings.TrimRight(account.GetCNProtocolBaseURL(APIProtocolChatCompletions), "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	endpoint, err := cnValidateProbeURL(s.cfg, base+"/tools/"+operation)
	if err != nil {
		return nil, "", webToolError(503, "web_service_misconfigured", "DeepSeek endpoint is rejected by the upstream URL policy")
	}
	requestCtx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), time.Duration(d.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", webToolError(503, "web_service_misconfigured", "Invalid DeepSeek endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+account.GetCNAPIKey())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	started := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, "", webToolError(504, "web_upstream_timeout", "DeepSeek web request timed out")
		}
		return nil, "", webToolError(502, "web_upstream_error", "DeepSeek web request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		status := http.StatusBadGateway
		code := "web_upstream_error"
		message := fmt.Sprintf("DeepSeek web service returned HTTP %d", resp.StatusCode)
		if resp.StatusCode == 429 || resp.StatusCode == 408 {
			status = resp.StatusCode
		}
		if resp.StatusCode == 402 || resp.StatusCode == 401 || resp.StatusCode == 403 {
			status, code, message = 503, "web_account_unavailable", "Online search is temporarily unavailable"
		}
		if resp.StatusCode == 400 || resp.StatusCode == 422 {
			status = 400
		}
		return nil, "", &DeepSeekWebError{Status: status, Code: code, Message: message, RetryAfter: resp.Header.Get("Retry-After")}
	}
	// Bound retained search/page data even when the upstream does not send Content-Length.
	const maxWebResponseBytes = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebResponseBytes+1))
	if err != nil || len(body) > maxWebResponseBytes {
		return nil, "", webToolError(502, "web_upstream_error", "DeepSeek web response is unreadable or too large")
	}
	billable, err := deepSeekWebResponseBillable(operation, body)
	if err != nil {
		return nil, "", webToolError(502, "web_upstream_error", "DeepSeek returned an invalid web response")
	}
	requestID := "web_search:deepseek-" + operation + ":" + uuid.NewString()
	price := d.SearchPricePer1K
	if operation == "fetch" {
		price = d.FetchPricePer1K
	}
	calls := 0
	if billable {
		calls = 1
	}
	cost := s.billingService.CalculateSearchCost(calls, price, 1)
	cost.BillingMode = string(BillingModePerRequest)
	if err := s.recordDeepSeekWebUsage(ctx, operation, requestID, account, usage, cost, time.Since(started)); err != nil {
		return nil, requestID, webToolError(503, "web_billing_error", "Online search is temporarily unavailable")
	}
	return body, requestID, nil
}

func (s *GatewayService) recordDeepSeekWebUsage(ctx context.Context, operation, requestID string, account *Account, usage DeepSeekWebUsage, cost *CostBreakdown, duration time.Duration) error {
	// Settlement survives client disconnects after the paid upstream operation completed.
	billingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	subscriptionBill := usage.Subscription != nil && usage.APIKey.Group != nil && usage.APIKey.Group.IsSubscriptionType()
	billingType := BillingTypeBalance
	if subscriptionBill {
		billingType = BillingTypeSubscription
	}
	result := &ForwardResult{RequestID: requestID, Model: "deepseek-" + operation, Duration: duration}
	input := &recordUsageCoreInput{InboundEndpoint: "/v1/tools/" + operation, UpstreamEndpoint: "/v1/tools/" + operation, UserAgent: usage.UserAgent, IPAddress: usage.IPAddress}
	accountMultiplier := account.BillingRateMultiplier()
	log := s.buildRecordUsageLog(billingCtx, input, result, usage.APIKey, usage.APIKey.User, account, usage.Subscription, result.Model, 1, 1, accountMultiplier, billingType, false, cost)
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		log.ActualCost = 0
		writeUsageLogBestEffort(billingCtx, s.usageLogRepo, log, "service.web")
		return nil
	}
	_, err := applyUsageBilling(billingCtx, log.RequestID, log, &postUsageBillingParams{
		Cost: cost, User: usage.APIKey.User, APIKey: usage.APIKey, Account: account,
		Subscription: usage.Subscription, IsSubscriptionBill: subscriptionBill,
		AccountRateMultiplier: accountMultiplier, APIKeyService: usage.APIKeyService, Platform: usage.QuotaPlatform,
	}, s.billingDeps(), s.usageBillingRepo)
	if err != nil {
		log.ActualCost = 0
	}
	writeUsageLogBestEffort(billingCtx, s.usageLogRepo, log, "service.web")
	return err
}

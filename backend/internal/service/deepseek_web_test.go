//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekWebRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		op, body string
		valid    bool
	}{
		{"search", `{"query":"当地护肤品偏好","count":8,"localization":"8.8.8.8"}`, true},
		{"search", `{"query":"` + strings.Repeat("😀", 128) + `"}`, true},
		{"search", `{"query":"` + strings.Repeat("😀", 129) + `"}`, false},
		{"search", `{"query":" "}`, false}, {"search", `null`, false},
		{"search", `{"query":"q","model":"deepseek-v4"}`, false},
		{"search", `{"query":"q","count":0}`, false}, {"search", `{"query":"q","count":51}`, false},
		{"search", `{"query":"q","ori_question":""}`, false},
		{"search", `{"query":"q","localization":"US"}`, false},
		{"search", `{"query":"q"} {}`, false},
		{"fetch", `{"url":"https://example.com/page"}`, true},
		{"fetch", `{"url":"file:///etc/passwd"}`, false},
		{"fetch", `{"url":"https://user:pass@example.com"}`, false},
		{"fetch", `{"url":"https://"}`, false},
		{"fetch", `{"url":"https://example.com","account_id":2}`, false},
		{"unknown", `{}`, false},
	} {
		t.Run(tc.op+tc.body[:min(len(tc.body), 40)], func(t *testing.T) {
			_, err := ValidateDeepSeekWebRequest(tc.op, []byte(tc.body))
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDeepSeekWebConfigRequiresExplicitPrices(t *testing.T) {
	price := 0.0
	valid := DeepSeekWebConfig{Enabled: true, AccountID: 7, SearchPricePer1K: &price, FetchPricePer1K: &price, TimeoutSeconds: 60}
	require.NoError(t, validateWebSearchConfig(&WebSearchEmulationConfig{DeepSeek: &valid}))
	for _, change := range []func(*DeepSeekWebConfig){
		func(d *DeepSeekWebConfig) { d.AccountID = 0 }, func(d *DeepSeekWebConfig) { d.SearchPricePer1K = nil },
		func(d *DeepSeekWebConfig) { d.FetchPricePer1K = nil }, func(d *DeepSeekWebConfig) { d.TimeoutSeconds = 0 },
		func(d *DeepSeekWebConfig) { d.TimeoutSeconds = 301 },
		func(d *DeepSeekWebConfig) { p := -1.0; d.SearchPricePer1K = &p },
		func(d *DeepSeekWebConfig) { p := math.Inf(1); d.FetchPricePer1K = &p },
	} {
		d := valid
		change(&d)
		require.Error(t, validateWebSearchConfig(&WebSearchEmulationConfig{DeepSeek: &d}))
	}
}

func TestDeepSeekWebOlderAdminSavePreservesConfig(t *testing.T) {
	repo := &panelRateLimitSettingRepo{values: map[string]string{SettingKeyWebSearchEmulationConfig: `{"enabled":false,"deepseek":{"enabled":true,"account_id":7,"search_price_per_1k":1,"fetch_price_per_1k":2,"timeout_seconds":60}}`}}
	settings := &SettingService{settingRepo: repo}
	incoming := &WebSearchEmulationConfig{}
	require.NoError(t, settings.SaveWebSearchEmulationConfig(context.Background(), incoming))
	require.Equal(t, int64(7), incoming.DeepSeek.AccountID)
	require.True(t, incoming.DeepSeek.Enabled)
}

type deepSeekWebUpstreamStub struct {
	HTTPUpstream
	status  int
	body    string
	calls   int
	req     *http.Request
	payload []byte
	proxy   string
	cancel  context.CancelFunc
	err     error
}

func (u *deepSeekWebUpstreamStub) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.req = req
	u.proxy = proxy
	u.payload, _ = io.ReadAll(req.Body)
	if u.err != nil {
		return nil, u.err
	}
	if u.cancel != nil {
		u.cancel()
	}
	return &http.Response{StatusCode: u.status, Header: http.Header{"Retry-After": []string{"2"}}, Body: io.NopCloser(strings.NewReader(u.body))}, nil
}

func deepSeekWebService(t *testing.T, upstream *deepSeekWebUpstreamStub) (*GatewayService, *openAIRecordUsageBillingRepoStub, *openAIRecordUsageBestEffortLogRepoStub, DeepSeekWebUsage) {
	t.Helper()
	price, fetchPrice := 2.0, 3.0
	d := &DeepSeekWebConfig{Enabled: true, AccountID: 7, SearchPricePer1K: &price, FetchPricePer1K: &fetchPrice, TimeoutSeconds: 60}
	// Each test supplies its own settings; do not let the process-wide settings cache cross cases.
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{config: &WebSearchEmulationConfig{DeepSeek: d}, expiresAt: time.Now().Add(time.Minute).UnixNano()})
	t.Cleanup(func() { webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{}) })
	logs := &openAIRecordUsageBestEffortLogRepoStub{}
	billing := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.settingService = &SettingService{}
	svc.accountRepo = &fakeCNProbeAccountRepo{account: &Account{ID: 7, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "upstream-secret"}}}
	svc.httpUpstream = upstream
	usage := DeepSeekWebUsage{APIKey: &APIKey{ID: 2, User: &User{ID: 1}, Group: &Group{ID: 3, Platform: PlatformComposite, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 9}}, QuotaPlatform: PlatformComposite}
	return svc, billing, logs, usage
}

func TestDeepSeekWebForwardingAndBilling(t *testing.T) {
	for _, tc := range []struct {
		op, body string
		cost     float64
	}{
		{"search", `{"results":[{"title":"source","url":"https://example.com","snippet":"short","summary":"long","date":null}]}`, 0.002},
		{"search", `{"results":[]}`, 0},
		{"fetch", `{"title":"source","content":"page markdown"}`, 0.003},
		{"fetch", `{"title":"source","content":" "}`, 0},
	} {
		t.Run(tc.op+tc.body, func(t *testing.T) {
			up := &deepSeekWebUpstreamStub{status: 200, body: tc.body}
			svc, billing, logs, usage := deepSeekWebService(t, up)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			up.cancel = cancel
			payload := []byte(`{"query":"q"}`)
			body, id, err := svc.RunDeepSeekWeb(ctx, tc.op, payload, usage)
			require.NoError(t, err)
			require.JSONEq(t, tc.body, string(body))
			require.NotEmpty(t, id)
			require.Equal(t, "https://api.deepseek.com/v1/tools/"+tc.op, up.req.URL.String())
			require.Equal(t, "Bearer upstream-secret", up.req.Header.Get("Authorization"))
			require.True(t, HTTPUpstreamRedirectsDisabled(up.req.Context()))
			require.Equal(t, 1, up.calls)
			require.Equal(t, payload, up.payload)
			require.Equal(t, 1, billing.calls)
			require.InDelta(t, tc.cost, billing.lastCmd.BalanceCost, 1e-10)
			require.NoError(t, billing.lastCtxErr)
			require.NoError(t, logs.lastCtxErr)
			require.Equal(t, "deepseek-"+tc.op, logs.lastLog.Model)
			require.Equal(t, 1.0, logs.lastLog.RateMultiplier)
			require.Equal(t, 0, logs.lastLog.TotalTokens())
			require.InDelta(t, tc.cost, logs.lastLog.ActualCost, 1e-10)
		})
	}
}

func TestDeepSeekWebFailuresNeverBillOrLeakSecrets(t *testing.T) {
	for _, tc := range []struct {
		status   int
		body     string
		expected int
	}{
		{429, `upstream-secret`, 429}, {402, `upstream-secret`, 503},
		{401, `upstream-secret`, 503}, {500, `upstream-secret`, 502}, {302, `upstream-secret`, 502},
		{200, `{"results":null}`, 502}, {200, `{"results":[{"title":"x","url":"file:///secret","snippet":"x"}]}`, 502},
		{200, `garbage upstream-secret`, 502},
	} {
		t.Run(tc.body+string(rune(tc.status)), func(t *testing.T) {
			svc, billing, logs, usage := deepSeekWebService(t, &deepSeekWebUpstreamStub{status: tc.status, body: tc.body})
			_, _, err := svc.RunDeepSeekWeb(context.Background(), "search", []byte(`{"query":"q"}`), usage)
			var failure *DeepSeekWebError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.expected, failure.Status)
			require.NotContains(t, err.Error(), "upstream-secret")
			require.Zero(t, billing.calls)
			require.Zero(t, logs.bestEffortCalls)
			if tc.status == 429 {
				require.Equal(t, "2", failure.RetryAfter)
			}
		})
	}
}

func TestDeepSeekWebRejectsWrongAccountAndURLPolicy(t *testing.T) {
	for _, mode := range []string{"wrong-platform", "expired", "disabled", "blocked-target", "missing-key", "transport"} {
		t.Run(mode, func(t *testing.T) {
			up := &deepSeekWebUpstreamStub{status: 200, body: `{"results":[]}`}
			svc, billing, _, usage := deepSeekWebService(t, up)
			account := svc.accountRepo.(*fakeCNProbeAccountRepo).account
			switch mode {
			case "wrong-platform":
				account.Platform = PlatformOpenAI
			case "expired":
				expired := time.Now().Add(-time.Hour)
				account.ExpiresAt = &expired
				account.AutoPauseOnExpired = true
			case "disabled":
				account.Schedulable = false
			case "blocked-target":
				svc.cfg = cnProbeAllowlistConfig("other.example")
			case "missing-key":
				delete(account.Credentials, "api_key")
			case "transport":
				up.err = errors.New("upstream-secret")
			}
			_, _, err := svc.RunDeepSeekWeb(context.Background(), "search", []byte(`{"query":"q"}`), usage)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "upstream-secret")
			require.Zero(t, billing.calls)
			if mode != "transport" {
				require.Zero(t, up.calls)
			}
		})
	}
}

func TestDeepSeekWebSettlementFailureIsVisible(t *testing.T) {
	svc, billing, logs, usage := deepSeekWebService(t, &deepSeekWebUpstreamStub{status: 200, body: `{"results":[{"title":"a","url":"https://a.test","snippet":"a"}]}`})
	billing.err = errors.New("database unavailable")
	_, id, err := svc.RunDeepSeekWeb(context.Background(), "search", []byte(`{"query":"q"}`), usage)
	require.NotEmpty(t, id)
	require.Error(t, err)
	require.Zero(t, logs.lastLog.ActualCost)
}

func TestDeepSeekWebParsePersistedConfig(t *testing.T) {
	var d DeepSeekWebConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"account_id":7,"search_price_per_1k":0,"fetch_price_per_1k":0,"timeout_seconds":60}`), &d))
	require.NoError(t, validateWebSearchConfig(&WebSearchEmulationConfig{DeepSeek: &d}))
}

func TestDeepSeekWebBillingDoesNotCollapseRepeatedContextIDs(t *testing.T) {
	svc, billing, logs, usage := deepSeekWebService(t, &deepSeekWebUpstreamStub{status: 200, body: `{"results":[{"title":"a","url":"https://a.test","snippet":"a"}]}`})
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "reused-id")
	_, first, err := svc.RunDeepSeekWeb(ctx, "search", []byte(`{"query":"q"}`), usage)
	require.NoError(t, err)
	require.Equal(t, first, billing.lastCmd.RequestID)
	require.Equal(t, first, logs.lastLog.RequestID)
	_, second, err := svc.RunDeepSeekWeb(ctx, "search", []byte(`{"query":"q"}`), usage)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Equal(t, second, billing.lastCmd.RequestID)
	require.Equal(t, 2, billing.calls)
}

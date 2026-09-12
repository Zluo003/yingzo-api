//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type agentPricingHandlerAccountRepo struct {
	service.AccountRepository
	accounts []*service.Account
}

func (r *agentPricingHandlerAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for _, account := range r.accounts {
		if account != nil && account.ID == id {
			copy := *account
			return &copy, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r *agentPricingHandlerAccountRepo) ListSchedulableByGroupIDAndPlatform(
	_ context.Context,
	groupID int64,
	platform string,
) ([]service.Account, error) {
	accounts := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account == nil || account.Platform != platform || !account.IsSchedulable() || !accountBelongsToTestGroup(account, groupID) {
			continue
		}
		accounts = append(accounts, *account)
	}
	return accounts, nil
}

func (r *agentPricingHandlerAccountRepo) ListSchedulableByPlatform(
	_ context.Context,
	platform string,
) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(context.Background(), 0, platform)
}

func (r *agentPricingHandlerAccountRepo) ListSchedulableUngroupedByPlatform(
	_ context.Context,
	platform string,
) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(context.Background(), 0, platform)
}

func accountBelongsToTestGroup(account *service.Account, groupID int64) bool {
	if account == nil {
		return false
	}
	if groupID == 0 {
		return len(account.GroupIDs) == 0 && len(account.AccountGroups) == 0
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	return false
}

func newAgentPricingGatewayHandler(
	t *testing.T,
	group *service.Group,
	accounts []*service.Account,
) (*GatewayHandler, func()) {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	accountRepo := &agentPricingHandlerAccountRepo{accounts: accounts}
	gatewayService := service.NewGatewayService(
		accountRepo, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	concurrencyService := service.NewConcurrencyService(&fakeConcurrencyCache{})
	return &GatewayHandler{
		gatewayService:           gatewayService,
		billingCacheService:      billingCacheService,
		concurrencyHelper:        NewConcurrencyHelper(concurrencyService, SSEPingFormatNone, 0),
		maxAccountSwitches:       1,
		maxAccountSwitchesGemini: 1,
	}, billingCacheService.Stop
}

func TestGatewayHandlerMessages_AgentMissingAnthropicChannelPriceStopsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7401)
	accountID := int64(8401)
	group := &service.Group{
		ID: groupID, Hydrated: true, Status: service.StatusActive,
		Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 1.5,
	}
	account := &service.Account{
		ID: accountID, Name: "anthropic-agent-account", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "upstream-token"},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
		GroupIDs:      []int64{groupID},
	}
	h, cleanup := newAgentPricingGatewayHandler(t, group, []*service.Account{account})
	t.Cleanup(cleanup)
	body := []byte(`{"model":"claude-priced","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req
	apiKey := &service.APIKey{
		ID: 9401, UserID: 9501, GroupID: &groupID, Group: group,
		User: &service.User{ID: 9501, Status: service.StatusActive},
	}
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
	servermiddleware.SetForcePlatform(c, service.PlatformAnthropic)

	require.NotPanics(t, func() { h.Messages(c) })
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, agentPricingUnavailableCode, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

func TestGatewayHandlerGeminiNative_AgentMissingGeminiChannelPriceStopsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7402)
	accountID := int64(8402)
	group := &service.Group{
		ID: groupID, Hydrated: true, Status: service.StatusActive,
		Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 1.5,
	}
	account := &service.Account{
		ID: accountID, Name: "gemini-agent-account", Platform: service.PlatformGemini,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "upstream-token"},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
		GroupIDs:      []int64{groupID},
	}
	h, cleanup := newAgentPricingGatewayHandler(t, group, []*service.Account{account})
	t.Cleanup(cleanup)
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-priced:generateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req
	c.Params = gin.Params{{Key: "modelAction", Value: "/gemini-priced:generateContent"}}
	apiKey := &service.APIKey{
		ID: 9402, UserID: 9502, GroupID: &groupID, Group: group,
		User: &service.User{ID: 9502, Status: service.StatusActive},
	}
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
	servermiddleware.SetForcePlatform(c, service.PlatformGemini)

	require.NotPanics(t, func() { h.GeminiV1BetaModels(c) })
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, agentPricingUnavailableCode, gjson.GetBytes(rec.Body.Bytes(), "error.reason").String())
}

func TestOpenAIGatewayHandlerResponses_AgentMissingOpenAIChannelPriceReleasesSlotsAndStopsUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7403)
	account := service.Account{
		ID: 8403, Name: "openai-agent-account", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials:   map[string]any{"api_key": "upstream-token", "base_url": "https://upstream.example.test"},
		AccountGroups: []service.AccountGroup{{AccountID: 8403, GroupID: groupID}},
		GroupIDs:      []int64{groupID},
	}
	accountRepo := publicAgentImageAccountRepo{account: account}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstream := &publicAgentImageUpstream{}
	concurrencyCache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	concurrencyService := service.NewConcurrencyService(concurrencyCache)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, concurrencyService,
		service.NewBillingService(cfg, nil), nil, billingCacheService, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(
		gatewayService,
		concurrencyService,
		billingCacheService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, nil, cfg,
	)
	group := &service.Group{
		ID: groupID, Hydrated: true, Status: service.StatusActive,
		Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 1.5,
	}
	body := []byte(`{"model":"gpt-priced","input":"hello"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req
	apiKey := &service.APIKey{
		ID: 9403, UserID: 9503, GroupID: &groupID, Group: group,
		User: &service.User{ID: 9503, Status: service.StatusActive},
	}
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
	servermiddleware.SetForcePlatform(c, service.PlatformOpenAI)

	require.NotPanics(t, func() { h.Responses(c) })
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, agentPricingUnavailableCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Zero(t, upstream.calls)
	require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseAccountCalled))
	require.Equal(t, int32(1), atomic.LoadInt32(&concurrencyCache.releaseUserCalled))
}

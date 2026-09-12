package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type publicAgentImageAccountRepo struct {
	service.AccountRepository
	account service.Account
}

func (r publicAgentImageAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if id != r.account.ID {
		return nil, service.ErrNoAvailableAccounts
	}
	account := r.account
	return &account, nil
}

func (r publicAgentImageAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	if platform != r.account.Platform {
		return nil, nil
	}
	return []service.Account{r.account}, nil
}

func (r publicAgentImageAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(context.Background(), 0, platform)
}

func (r publicAgentImageAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(context.Background(), 0, platform)
}

type publicAgentImageUpstream struct {
	service.HTTPUpstream
	calls int
}

func (u *publicAgentImageUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(
			`{"created":1,"output_format":"png","data":[{"b64_json":"aW1hZ2U=","revised_prompt":"kept"}]}`,
		)),
	}, nil
}

type publicAgentImagePublisher struct {
	owners []service.TemporaryAssetOwner
	inputs []string
}

func (p *publicAgentImagePublisher) PublishGeneratedImage(
	_ context.Context,
	owner service.TemporaryAssetOwner,
	_ string,
	encodedImage string,
	_ string,
) (string, error) {
	p.owners = append(p.owners, owner)
	p.inputs = append(p.inputs, encodedImage)
	return "https://api.example.test/media/result/asset.png", nil
}

func newPublicAgentImageTestHandler(
	t *testing.T,
	groupID int64,
	unitPrice *float64,
) (*OpenAIGatewayHandler, *publicAgentImageUpstream, *publicAgentImagePublisher) {
	t.Helper()
	account := service.Account{
		ID:          1,
		Name:        "image-account",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key": "upstream-token", "base_url": "https://upstream.example.test",
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		},
	}
	repo := publicAgentImageAccountRepo{account: account}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	upstream := &publicAgentImageUpstream{}
	pricingService := service.NewBillingService(cfg, nil)
	model := enabledGatewayAgentModel(service.PlatformOpenAI, "gpt-image-2", service.AgentMediaTypeImage)
	model.ID = 1
	model.GroupID = groupID
	if unitPrice != nil {
		model.Prices = []service.AgentModelPrice{{
			Resolution:  service.ImageBillingSize2K,
			BillingUnit: service.AgentBillingUnitImage,
			UnitPrice:   *unitPrice,
		}}
	}
	resolver := service.NewModelPricingResolver(nil, pricingService)
	resolver.SetAgentModelCatalog(newGatewayAgentCatalogForTest(repo, model))
	gatewayService := service.NewOpenAIGatewayService(
		repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, pricingService, nil, nil,
		upstream, nil, nil, nil, resolver, nil, nil, nil, nil,
	)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	publisher := &publicAgentImagePublisher{}
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingCacheService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, publisher, cfg,
	)
	return handler, upstream, publisher
}

func publicAgentImageTestContext(
	groupID int64,
	group *service.Group,
) (*gin.Context, *httptest.ResponseRecorder) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw","response_format":"b64_json"}`)
	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 99, UserID: 100, GroupID: &groupID, Group: group, User: &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})
	return c, rec
}

func TestShouldPublishOpenAIImageURLsForEveryPublicAgentImageResponse(t *testing.T) {
	publicAgent := &service.APIKey{Group: &service.Group{Kind: "agent", SystemCode: "yingzo", IsExclusive: false}}
	privateAgent := &service.APIKey{Group: &service.Group{Kind: "agent", SystemCode: "private", IsExclusive: true}}
	standard := &service.APIKey{Group: &service.Group{Kind: "standard", IsExclusive: false}}

	require.True(t, shouldPublishOpenAIImageURLs(publicAgent))
	require.False(t, shouldPublishOpenAIImageURLs(privateAgent))
	require.False(t, shouldPublishOpenAIImageURLs(standard))
	require.False(t, shouldPublishOpenAIImageURLs(nil))
}

func TestOpenAIGatewayHandlerImages_PublicAgentNeverReturnsRequestedBase64(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3131)
	imagePrice2K := 0.12
	handler, upstream, publisher := newPublicAgentImageTestHandler(t, groupID, &imagePrice2K)
	c, rec := publicAgentImageTestContext(groupID, &service.Group{
		ID:                   groupID,
		Kind:                 "agent",
		SystemCode:           "yingzo",
		IsExclusive:          false,
		Platform:             service.PlatformOpenAI,
		AllowImageGeneration: true,
	})

	handler.Images(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "https://api.example.test/media/result/asset.png", gjson.GetBytes(rec.Body.Bytes(), "data.0.url").String())
	require.False(t, gjson.GetBytes(rec.Body.Bytes(), "data.0.b64_json").Exists())
	require.NotContains(t, rec.Body.String(), "data:image/")
	require.NotContains(t, rec.Body.String(), "aW1hZ2U=")
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, []string{"aW1hZ2U="}, publisher.inputs)
	require.Equal(t, []service.TemporaryAssetOwner{{UserID: 100, APIKeyID: 99, GroupID: groupID}}, publisher.owners)
}

func TestOpenAIGatewayHandlerImages_AgentMissingTierPriceNeverCallsUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(3132)
	handler, upstream, publisher := newPublicAgentImageTestHandler(t, groupID, nil)
	c, rec := publicAgentImageTestContext(groupID, &service.Group{
		ID:                   groupID,
		Kind:                 "agent",
		SystemCode:           "yingzo",
		IsExclusive:          false,
		Platform:             service.PlatformOpenAI,
		AllowImageGeneration: true,
	})

	handler.Images(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, agentPricingUnavailableCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Zero(t, upstream.calls)
	require.Empty(t, publisher.inputs)
	require.Empty(t, publisher.owners)
}

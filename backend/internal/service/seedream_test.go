package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSeedreamRequestContract(t *testing.T) {
	for _, model := range []string{SeedreamProModel, SeedreamFlashModel} {
		for _, tc := range []struct{ fields, tier, size, resolution string }{
			{``, "1K", "auto", "1K"},
			{`,"size":"1.5k"`, "1K", "auto", "1.5K"},
			{`,"size":"2K","resolution":"4K"`, "2K", "auto", "2K"},
			{`,"size":"16x9","resolution":"1.5k"`, "1K", "16:9", "1.5K"},
			{`,"size":"2048×1024","resolution":"4K"`, "1K", "2048x1024", ""},
			{`,"size":"2048X2048"`, "2K", "2048x2048", ""},
			{`,"size":"1600x1600"`, "1K", "1600x1600", ""},
		} {
			r, err := ParseSeedreamRequest([]byte(`{"model":"` + model + `","prompt":"cat"` + tc.fields + `}`))
			require.NoError(t, err)
			require.Equal(t, tc.tier, r.BillingTier())
			require.Equal(t, tc.size, r.Size)
			require.Equal(t, tc.resolution, r.Resolution)
		}
	}
	for _, fields := range []string{
		`"n":0`, `"n":2`, `"resolution":"4K"`, `"size":"9:21"`, `"size":"16X9"`, `"size":"512x512"`, `"size":"999999999999999999999999x10"`, `"size":"90000x50"`, `"size":"4096x4096"`,
		`"stream":false`, `"tools":[]`, `"layer_decomposition":true`, `"background":"transparent"`, `"output_format":"webp"`, `"response_format":"b64_json"`, `"optimize_prompt_options":{"mode":"fast"}`,
		`"image_urls":["file:///tmp/image.png"]`, `"image_urls":["data:image/PNG;base64,YQ=="]`, `"image_urls":["data:image/png;base64,invalid"]`,
	} {
		_, err := ParseSeedreamRequest([]byte(`{"model":"` + SeedreamProModel + `","prompt":"cat",` + fields + `}`))
		require.Error(t, err, fields)
	}
	_, err := ParseSeedreamRequest([]byte(`{"model":"` + SeedreamProModel + `","prompt":"cat"} {}`))
	require.Error(t, err)
	r, err := ParseSeedreamRequest([]byte(`{"model":"seedream-5.0-flash","layer_decomposition":true,"size":"2K","image_urls":["https://image.example/poster.png"]}`))
	require.NoError(t, err)
	require.Equal(t, SeedreamFlashModel, r.Model)
	for _, fields := range []string{`"size":"16:9"`, `"size":"2048x1024"`, `"image_urls":[]`} {
		var raw map[string]any
		require.NoError(t, json.Unmarshal([]byte(`{"model":"`+SeedreamProModel+`","layer_decomposition":true,"image_urls":["https://image.example/poster.png"]}`), &raw))
		var change map[string]any
		require.NoError(t, json.Unmarshal([]byte(`{`+fields+`}`), &change))
		for k, v := range change {
			raw[k] = v
		}
		b, _ := json.Marshal(raw)
		_, err := ParseSeedreamRequest(b)
		require.Error(t, err)
	}
}

func TestSeedreamFrozenAreaBillingAndReservation(t *testing.T) {
	ctx := context.Background()
	gid := int64(9)
	catalog, _ := configuredAgentCatalogForEstimateTest(t, gid, PlatformOpenAI, SeedreamProModel, AgentMediaTypeImage, "1K", 0.1)
	config, err := catalog.GetConfig(ctx, gid)
	require.NoError(t, err)
	free, price := 1, 0.03
	_, err = catalog.UpdateModel(ctx, gid, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeImage, Enabled: true, FreeInputImages: &free, InputImageUnitPrice: &price, Prices: []AgentModelPrice{{Resolution: "1.5K", UnitPrice: 0.1}, {Resolution: "2K", UnitPrice: 0.2}}})
	require.NoError(t, err)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &Group{ID: gid, Kind: "agent", SystemCode: "yingzo"}}
	r, err := ParseSeedreamRequest([]byte(`{"model":"seedream-5-0-pro","prompt":"combine","size":"1.5K","image_urls":["https://image.example/a.png","https://image.example/b.png","https://image.example/c.png"]}`))
	require.NoError(t, err)
	q, err := catalog.EstimateSeedream(ctx, key, &BillingService{}, r)
	require.NoError(t, err)
	require.Equal(t, 1, q.Count)
	require.InDelta(t, 0.26, q.Charge.BalanceCost, 1e-9)
	cost, err := q.Seedream.cost([]int64{1536 * 1536})
	require.NoError(t, err)
	require.InDelta(t, 0.16, cost.ActualCost, 1e-9)
	// A boundary uses total area, even when an edge is larger than 2048.
	cost, err = q.Seedream.cost([]int64{SeedreamLowPixelLimit, SeedreamLowPixelLimit + 1, 2880 * 1440})
	require.NoError(t, err)
	require.InDelta(t, 0.56, cost.ActualCost, 1e-9)
	// Mutating live prices cannot change the JSON-persisted quote.
	encoded, err := json.Marshal(q)
	require.NoError(t, err)
	var restored ImageTaskQuote
	require.NoError(t, json.Unmarshal(encoded, &restored))
	_, err = catalog.UpdateModel(ctx, gid, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeImage, Enabled: true, Prices: []AgentModelPrice{{Resolution: "1K", UnitPrice: 5}, {Resolution: "2K", UnitPrice: 10}}})
	require.NoError(t, err)
	cost, err = restored.Seedream.cost([]int64{2048 * 2048, 100 * 100, 100 * 100})
	require.NoError(t, err)
	require.InDelta(t, 0.46, cost.ActualCost, 1e-9)
	r.ImageURLs = r.ImageURLs[:1]
	r.LayerDecomposition = true
	q, err = catalog.EstimateSeedream(ctx, key, &BillingService{}, r)
	require.NoError(t, err)
	require.Equal(t, 17, q.Count)
	require.InDelta(t, 170, q.Charge.BalanceCost, 1e-9)
	// Layer billing sums the actual base image and layers, with reference cost once per task.
	cost, err = q.Seedream.cost([]int64{2048 * 2048, 492 * 98, 100 * 100})
	require.NoError(t, err)
	require.InDelta(t, 20, cost.ActualCost, 1e-9)
	_, err = normalizeAgentModelPricesForModel(SeedreamProModel, AgentMediaTypeImage, []AgentModelPrice{{Resolution: "4K", UnitPrice: 1}})
	require.Error(t, err)
}

func TestSeedreamPriceTierAliases(t *testing.T) {
	ctx := context.Background()
	for _, model := range []string{SeedreamProModel, SeedreamFlashModel} {
		t.Run(model, func(t *testing.T) {
			catalog, _ := configuredAgentCatalogForEstimateTest(t, 9, PlatformOpenAI, model, AgentMediaTypeImage, "1.5K", 0.15)
			for _, resolution := range []string{"1K", "1.5K", " 1.5k "} {
				price, _, err := catalog.ResolveMediaPrice(ctx, 9, PlatformOpenAI, AgentMediaTypeImage, resolution, model)
				require.NoError(t, err)
				require.Equal(t, "1K", price.Resolution)
				require.InDelta(t, 0.15, price.UnitPrice, 1e-9)
			}
			_, err := normalizeAgentModelPricesForModel(model, AgentMediaTypeImage, []AgentModelPrice{
				{Resolution: "1K", UnitPrice: 0.1}, {Resolution: "1.5K", UnitPrice: 0.15},
			})
			require.ErrorContains(t, err, "duplicate price resolution")
			_, _, err = catalog.ResolveMediaPrice(ctx, 9, PlatformOpenAI, AgentMediaTypeImage, "4K", model)
			require.Error(t, err)
		})
	}
}

func seedreamTestAccount() Account {
	a := midjourneyTestAccount(7)
	a.Credentials = map[string]any{"api_key": "secret", "base_url": "https://api.apimart.ai/v1/"}
	a.Extra["image_provider"] = SeedreamProvider
	_ = normalizeSeedreamAccount(&a)
	return a
}
func TestSeedreamAccountAndAsyncUpstream(t *testing.T) {
	a := seedreamTestAccount()
	require.True(t, a.IsImageAccount())
	require.Len(t, a.GetModelMapping(), 2)
	require.False(t, a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	for _, model := range []string{SeedreamProModel, SeedreamFlashModel} {
		calls := 0
		upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			calls++
			require.Equal(t, "Bearer secret", req.Header.Get("Authorization"))
			body := `{"code":200,"data":[{"task_id":"provider_task"}]}`
			if req.Method == http.MethodPost {
				require.Equal(t, "/v1/images/generations", req.URL.Path)
				var sent SeedreamRequest
				require.NoError(t, json.NewDecoder(req.Body).Decode(&sent))
				require.Equal(t, model, sent.Model)
				require.Equal(t, "1.5K", sent.Resolution)
			} else {
				require.Equal(t, "/v1/tasks/provider_task", req.URL.Path)
				body = `{"code":200,"data":{"status":"success","result":{"images":[{"url":["https://image.example/result.png"],"sizes":["1536x1536"],"output_formats":["png"]}]}}}`
				if model == SeedreamFlashModel {
					body = strings.Replace(body, `"status":"success"`, `"status":"completed"`, 1)
				}
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}}
		s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(1), a}}, httpUpstream: upstream}}
		capture := &AsyncImageExecution{Quote: &ImageTaskQuote{Usage: UsageLog{Model: model}}}
		status, result, err := s.executeSeedream(context.Background(), &SeedreamSnapshot{GroupID: 9, Request: SeedreamRequest{Model: model, Prompt: "cat", Resolution: "1.5K"}}, midjourneyTestKey(), capture)
		require.NoError(t, err)
		require.Equal(t, 200, status)
		require.Equal(t, 2, calls)
		require.Equal(t, int64(7), capture.Usage.AccountID)
		require.Contains(t, string(result), `"size":"1536x1536"`)
		require.NotContains(t, string(result), "provider_task")
	}
	a.Credentials["base_url"] = "https://api.apimart.ai/v1/images"
	require.Error(t, normalizeSeedreamAccount(&a))
}
func TestSeedreamLayerMetadataAndOutputCount(t *testing.T) {
	body := []byte(`{"status":"success","result":{"images":[{"url":["https://image.example/base.jpeg","https://image.example/layer.png"],"sizes":["2048x2048","492x98"],"output_formats":["jpeg","png"],"layers":[{"z_index":0},{"z_index":1,"name":"title","bounding_box":{"absolute":[140,451,631,548],"normalized":[68,220,308,268]}}]}]}}`)
	r := &SeedreamRequest{Model: SeedreamProModel, LayerDecomposition: true}
	output, status, err := normalizeSeedreamResult(body, r)
	require.NoError(t, err)
	require.Equal(t, "success", status)
	require.Contains(t, string(output), `"bounding_box"`)
	var result struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Len(t, result.Data, 2)
	r.LayerDecomposition = false
	_, _, err = normalizeSeedreamResult(body, r)
	require.Error(t, err)
	_, _, err = normalizeSeedreamResult([]byte(`{"status":"success","result":{"images":[{"url":["https://image.example/base.png"]}]}}`), &SeedreamRequest{LayerDecomposition: true})
	require.Error(t, err)
}

func TestSeedreamCompletedTaskResult(t *testing.T) {
	for _, envelope := range []bool{false, true} {
		body := `{"status":"completed","result":{"images":[{"url":["https://image.example/result.png"]}]}}`
		if envelope {
			body = `{"code":200,"data":` + body + `}`
		}
		output, status, err := normalizeSeedreamResult([]byte(body), &SeedreamRequest{Model: SeedreamFlashModel})
		require.NoError(t, err)
		require.Equal(t, "success", status)
		require.Contains(t, string(output), `"url":"https://image.example/result.png"`)
	}
	_, _, err := normalizeSeedreamResult([]byte(`{"status":"completed","result":{"images":[]}}`), &SeedreamRequest{Model: SeedreamFlashModel})
	require.ErrorContains(t, err, "unexpected Seedream result groups")
}
func TestSeedreamUnknownPostOutcomeAndRevokedOwner(t *testing.T) {
	calls := 0
	upstream := &codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
		calls++
		return nil, errors.New("reset after write")
	}}
	s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{seedreamTestAccount()}}, httpUpstream: upstream}}
	snapshot := &SeedreamSnapshot{GroupID: 9, Request: SeedreamRequest{Model: SeedreamProModel, Prompt: "cat"}}
	_, _, err := s.executeSeedream(context.Background(), snapshot, midjourneyTestKey(), &AsyncImageExecution{})
	require.Error(t, err)
	require.Equal(t, 1, calls)
	key := midjourneyTestKey()
	key.Group.AllowImageGeneration = false
	status, _, err := s.executeSeedream(context.Background(), snapshot, key, nil)
	require.Error(t, err)
	require.Equal(t, 403, status)
	require.Equal(t, 1, calls)
}

func TestSeedreamPublicationKeepsAllLayersAndBillsStoredDimensions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}()
	ids := []string{uuid.NewString(), uuid.NewString()}
	body, _ := json.Marshal(map[string]any{"model": SeedreamProModel, "layer_decomposition": true, "data": []map[string]any{
		{"url": "https://gateway.example/media/" + ids[0] + "/asset.jpg", "size": "1x1", "layer": map[string]any{"z_index": 0}},
		{"url": "https://gateway.example/media/" + ids[1] + "/asset.png", "size": "9999x9999", "layer": map[string]any{"z_index": 1, "name": "title", "bounding_box": map[string]any{"absolute": []int{10, 20, 110, 120}}}},
	}})
	key := midjourneyTestKey()
	for _, id := range ids {
		mock.ExpectQuery("SELECT storage_backend,storage_key,mime_type").WithArgs(id, key.UserID, key.ID).WillReturnRows(sqlmock.NewRows([]string{"storage_backend", "storage_key", "mime_type"}).AddRow("local", "unused", "image/png"))
	}
	for i, metadata := range []string{`{"width":2880,"height":1440}`, `{"width":100,"height":100}`} {
		mock.ExpectQuery("SELECT metadata FROM temporary_assets").WithArgs(ids[i], key.UserID, key.ID).WillReturnRows(sqlmock.NewRows([]string{"metadata"}).AddRow(metadata))
	}
	billing := &BillingService{}
	low, _ := billing.CalculateConfiguredAgentImageCost(0.1, 1)
	high, _ := billing.CalculateConfiguredAgentImageCost(0.2, 1)
	svc := &DurableImageService{files: &FileStorageService{db: db}}
	task := &DurableImageTask{Quote: ImageTaskQuote{Model: SeedreamProModel, Count: 17, Charge: UsageBillingCommand{APIKeyQuotaCost: 3.4}, Seedream: &SeedreamFrozenPrices{Low: low, High: high, InputCost: &CostBreakdown{}}}, CapturedUsage: &UsageLog{AccountID: 7}}
	published, count, err := svc.publishResult(context.Background(), task, key, "https://gateway.example", body)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Contains(t, string(published), `"bounding_box"`)
	require.Contains(t, string(published), ids[1])
	usage, err := svc.settlement(context.Background(), task, count)
	require.NoError(t, err)
	require.Equal(t, 2, usage.ImageCount)
	require.InDelta(t, 0.3, usage.ActualCost, 1e-9)
	for _, id := range ids {
		mock.ExpectQuery("SELECT storage_backend,storage_key,mime_type").WithArgs(id, key.UserID, key.ID).WillReturnRows(sqlmock.NewRows([]string{"storage_backend", "storage_key", "mime_type"}).AddRow("local", "unused", "image/png"))
	}
	refreshed, err := svc.refreshResult(context.Background(), published, ImageTaskOwner{key.UserID, key.ID}, task.Quote.Count)
	require.NoError(t, err)
	var result struct {
		Data []json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(refreshed, &result))
	require.Len(t, result.Data, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSeedreamStandardGroupRequiresReferencePricing(t *testing.T) {
	ctx := context.Background()
	billing := &BillingService{}
	resolver := &ModelPricingResolver{billingService: billing}
	svc := &DurableImageService{billing: billing, resolver: resolver, openAI: &OpenAIGatewayService{billingService: billing, resolver: resolver}}
	key := midjourneyTestKey()
	key.Group.RateMultiplier = 2
	outputPrice, inputPrice := 0.15, 0.02
	key.Group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{SeedreamFlashModel}, BillingMode: BillingModePerRequest, PerRequestPrice: &outputPrice}, {Platform: PlatformOpenAI, Models: []string{SeedreamFlashModel + ":input"}, BillingMode: BillingModePerRequest, PerRequestPrice: &inputPrice}}
	r := &SeedreamRequest{Model: SeedreamFlashModel, ImageURLs: []string{"https://image.example/a.png", "https://image.example/b.png"}}
	q, err := svc.seedreamQuote(ctx, key, nil, r)
	require.NoError(t, err)
	require.InDelta(t, 0.34, q.Charge.BalanceCost, 1e-9)
	key.Group.ModelPricing = key.Group.ModelPricing[:1]
	_, err = svc.seedreamQuote(ctx, key, nil, r)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

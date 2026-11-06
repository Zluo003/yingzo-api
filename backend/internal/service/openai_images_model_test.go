//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesResponsesDriverAndImageModels(t *testing.T) {
	for _, override := range []string{"", "  ", " gpt-5.6-sol "} {
		t.Run(fmt.Sprintf("override=%q", override), func(t *testing.T) {
			t.Setenv("SUB2API_IMAGES_MAIN_MODEL", override)
			driver := strings.TrimSpace(override)
			if driver == "" {
				driver = "gpt-5.6-luna"
			}
			for _, model := range []string{"gpt-image-2", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare-2026-09-08"} {
				for _, quality := range []string{"xhigh", "max", "auto"} {
					for _, endpoint := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
						parsed := &OpenAIImagesRequest{Endpoint: endpoint, Model: model, Prompt: "draw a red cup", Quality: quality, Size: "1536x864", Background: "transparent", OutputFormat: "png", N: 1}
						if parsed.IsEdits() {
							parsed.InputImageURLs = []string{"https://example.com/input.png"}
						}
						body, err := buildOpenAIImagesResponsesRequest(parsed, model)
						require.NoError(t, err)
						require.Equal(t, driver, gjson.GetBytes(body, "model").String())
						require.Equal(t, model, gjson.GetBytes(body, "tools.0.model").String())
						require.Equal(t, quality, gjson.GetBytes(body, "tools.0.quality").String())
						require.Equal(t, "1536x864", gjson.GetBytes(body, "tools.0.size").String())
						require.Equal(t, "transparent", gjson.GetBytes(body, "tools.0.background").String())
						if parsed.IsEdits() {
							require.Equal(t, "edit", gjson.GetBytes(body, "tools.0.action").String())
							require.Equal(t, parsed.InputImageURLs[0], gjson.GetBytes(body, "input.0.content.1.image_url").String())
						}
					}
				}
				req := map[string]any{"model": model, "input": "draw a red cup"}
				require.True(t, normalizeOpenAIResponsesImageOnlyModel(req))
				require.Equal(t, driver, req["model"])
				require.Equal(t, model, req["tools"].([]any)[0].(map[string]any)["model"])
				require.False(t, normalizeOpenAIResponsesImageOnlyModel(req), "a valid driver must not be overwritten")
			}
			req := map[string]any{"model": "gpt-6-astra", "tools": []any{map[string]any{"type": "image_generation", "model": "gpt-image-2.5-sunburst"}}}
			require.False(t, normalizeOpenAIResponsesImageOnlyModel(req))
			require.Equal(t, "gpt-6-astra", req["model"])
		})
	}
}

func TestOpenAIImagesRejectedDriverDoesNotCoolImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SUB2API_IMAGES_MAIN_MODEL", "gpt-5.4-mini")
	for _, rejected := range []string{"gpt-5.4-mini", "gpt-image-2.5-flare"} {
		t.Run(rejected, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
			body := fmt.Sprintf(`{"error":{"message":"The '%s' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`, rejected)
			resp := &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			_, err := svc.handleOpenAIImagesErrorResponse(WithOpenAIImagesEndpoint(context.Background()), resp, c, openAICodexPlanGatedOAuthAccount(), "gpt-image-2.5-flare")
			require.Error(t, err)
			if rejected == "gpt-5.4-mini" {
				var upstreamErr *OpenAIImagesUpstreamError
				require.ErrorAs(t, err, &upstreamErr)
				require.Equal(t, 400, upstreamErr.StatusCode)
				require.Contains(t, upstreamErr.Message, rejected)
				require.Empty(t, repo.modelRateLimitCalls)
				require.Zero(t, repo.tempCalls)
			} else {
				require.Len(t, repo.modelRateLimitCalls, 1, "actual image-model rejection still needs bounded failover")
			}
		})
	}
}

func TestGPTImage25PricingDoesNotUseLegacyImageRates(t *testing.T) {
	for _, model := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gpt-image-2": {InputCostPerToken: 2.5e-6, OutputCostPerImageToken: 15e-6}}}
		p := svc.GetModelPricing(model)
		require.NotNil(t, p)
		require.Equal(t, 5e-6, p.InputCostPerToken)
		require.Equal(t, 8e-6, p.InputCostPerImageToken)
		require.Equal(t, 30e-6, p.OutputCostPerImageToken)
		require.Equal(t, 1.25e-6, p.CacheReadInputTokenCost)
		require.Zero(t, p.OutputCostPerToken)
		custom := &LiteLLMModelPricing{InputCostPerToken: 7e-6}
		svc.pricingData[model] = custom
		require.Same(t, custom, svc.GetModelPricing(model), "explicit pricing must win")
	}
}

func TestGPTImage25AccountModelPermissions(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, model := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
			for _, mapping := range []map[string]any{nil, {model: model}} {
				account := &Account{Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{"model_mapping": mapping}}
				require.True(t, account.IsModelSupported(model))
				require.Equal(t, model, account.GetMappedModel(model))
			}
			restricted := &Account{Platform: PlatformOpenAI, Type: accountType, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}}}
			require.False(t, restricted.IsModelSupported(model), "an explicit administrator allowlist must remain restricted")
		}
	}
}

func TestGPTImage25UsagePreservesImageInputTokens(t *testing.T) {
	svc := &OpenAIGatewayService{}
	var usage OpenAIUsage
	svc.parseOpenAIImagesSSEUsageBytes([]byte(`{"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":1550,"input_tokens_details":{"image_tokens":1521,"text_tokens":29},"output_tokens":515,"output_tokens_details":{"image_tokens":515}}}}}`), &usage)
	require.Equal(t, 1550, usage.InputTokens)
	require.Equal(t, 1521, usage.ImageInputTokens)
	require.Equal(t, 515, usage.ImageOutputTokens)
}

func TestGPTImage26GIFAdmissionAndForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, endpoint := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, nil)
			c.Request.Header.Set("Content-Type", "application/json")
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"a looping cat","output_format":"gif","size":"auto","frame_count":9,"frame_duration_ms":200,"images":[{"image_url":"https://example.com/ref.png"}]}`, model))
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, "high", parsed.Complexity)
			forwarded, _, err := rewriteOpenAIImagesModel(body, "application/json", model, IsGPTImage26Model(parsed.Model))
			require.NoError(t, err)
			require.Equal(t, "high", gjson.GetBytes(forwarded, "complexity").String())
			mapped, _, err := rewriteOpenAIImagesModel(body, "application/json", "gpt-image-upstream-alias", IsGPTImage26Model(parsed.Model))
			require.NoError(t, err)
			require.Equal(t, "high", gjson.GetBytes(mapped, "complexity").String())
			require.Equal(t, int64(9), gjson.GetBytes(forwarded, "frame_count").Int())
			response, err := buildOpenAIImagesResponsesRequest(parsed, model)
			require.NoError(t, err)
			require.Equal(t, "high", gjson.GetBytes(response, "tools.0.complexity").String())
			require.Equal(t, int64(9), gjson.GetBytes(response, "tools.0.frame_count").Int())
			require.Equal(t, int64(200), gjson.GetBytes(response, "tools.0.frame_duration_ms").Int())
		}
	}
	require.Equal(t, "image/gif", openAIImageOutputMIMEType("gif"))
	for _, fields := range []string{
		`"output_format":"svg"`, `"output_format":"gif","frame_count":1`,
		`"output_format":"gif","frame_count":17`, `"output_format":"gif","frame_count":2.5`,
		`"output_format":"gif","frame_duration_ms":19`, `"output_format":"gif","frame_duration_ms":2001`,
		`"output_format":"gif","stream":true`, `"output_format":"gif","output_compression":50`,
		`"output_format":"gif","mask":{}`, `"output_format":"gif","size":"511x512"`,
		`"output_format":"png","frame_count":9`, `"complexity":"low"`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
		c.Request.Header.Set("Content-Type", "application/json")
		_, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, []byte(`{"model":"gpt-image-2.6-flare","prompt":"cat",`+fields+`}`))
		require.Error(t, err, fields)
	}
}

func TestGPTImage26MultipartAnimation(t *testing.T) {
	for _, complexity := range []string{"", "high", "low"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"model": "gpt-image-2.6-flare", "prompt": "a waving cat", "output_format": "gif", "size": "auto", "frame_count": "9", "frame_duration_ms": "200"} {
			require.NoError(t, writer.WriteField(key, value))
		}
		if complexity != "" {
			require.NoError(t, writer.WriteField("complexity", complexity))
		}
		require.NoError(t, writer.Close())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body.Bytes())
		if complexity == "low" {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, 9, *parsed.FrameCount)
		rewritten, contentType, err := rewriteOpenAIImagesModel(body.Bytes(), writer.FormDataContentType(), "gpt-image-upstream-alias", IsGPTImage26Model(parsed.Model))
		require.NoError(t, err)
		reparsed := &OpenAIImagesRequest{}
		require.NoError(t, parseOpenAIImagesMultipartRequest(rewritten, contentType, reparsed))
		require.Equal(t, "high", reparsed.Complexity)
		require.Equal(t, 9, *reparsed.FrameCount)
		require.Equal(t, 200, *reparsed.FrameDurationMS)
	}
}

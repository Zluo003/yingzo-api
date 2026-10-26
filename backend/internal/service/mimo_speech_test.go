//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func mimoTestAccount(t *testing.T) *Account {
	t.Helper()
	a := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "mimo-secret"}, Extra: map[string]any{"speech_provider": MiMoSpeechProvider}}
	require.NoError(t, normalizeMiMoSpeechAccount(a))
	return a
}

func TestMiMoSpeechAccountIsolation(t *testing.T) {
	a := mimoTestAccount(t)
	require.Equal(t, MiMoBaseURL, a.GetOpenAIBaseURL())
	require.Equal(t, map[string]string{MiMoTTSModel: MiMoTTSModel, MiMoVoiceDesignModel: MiMoVoiceDesignModel}, a.GetModelMapping())
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityLive} {
		require.False(t, a.SupportsOpenAIEndpointCapability(capability))
	}
	require.False(t, a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	for _, base := range []string{"http://example.com", "https://example.com/v1/chat/completions", "https://example.com?key=secret", "https://u:p@example.com"} {
		a.Credentials["base_url"] = base
		require.Error(t, normalizeMiMoSpeechAccount(a), base)
	}
	a.Credentials["base_url"] = MiMoBaseURL
	a.Extra["image_account"] = true
	require.Error(t, normalizeMiMoSpeechAccount(a))
}

func TestMiMoSpeechSchedulerCapabilities(t *testing.T) {
	a := mimoTestAccount(t)
	require.True(t, accountSupportsOpenAICapabilities(a, OpenAIEndpointCapabilityChatCompletions, ""), "chat scheduling must not require an image capability")
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityLive} {
		require.False(t, accountSupportsOpenAICapabilities(a, capability, ""))
	}
	for _, capability := range []OpenAIImagesCapability{OpenAIImagesCapabilityBasic, OpenAIImagesCapabilityNative} {
		require.False(t, accountSupportsOpenAICapabilities(a, "", capability))
	}
}

func TestMiMoSpeechValidation(t *testing.T) {
	for _, tc := range []struct {
		name, model, body string
		valid             bool
	}{
		{"preset defaults", MiMoTTSModel, `{"messages":[{"role":"assistant","content":"你好"}]}`, true},
		{"preset MP3", MiMoTTSModel, `{"messages":[{"role":"assistant","content":"(温柔)你好"}],"audio":{"format":"mp3","voice":"茉莉"}}`, true},
		{"stream defaults", MiMoTTSModel, `{"stream":true,"messages":[{"role":"assistant","content":"你好"}]}`, true},
		{"design", MiMoVoiceDesignModel, `{"messages":[{"role":"user","content":"温柔女声"},{"role":"assistant","content":"晚安"}]}`, true},
		{"generated preview", MiMoVoiceDesignModel, `{"messages":[{"role":"user","content":"温柔女声"}],"audio":{"optimize_text_preview":true}}`, true},
		{"wrong role", MiMoTTSModel, `{"messages":[{"role":"user","content":"朗读文本"}]}`, false},
		{"no design prompt", MiMoVoiceDesignModel, `{"messages":[{"role":"assistant","content":"你好"}]}`, false},
		{"design preset conflict", MiMoVoiceDesignModel, `{"messages":[{"role":"user","content":"女声"}],"audio":{"voice":"Mia","optimize_text_preview":true}}`, false},
		{"unknown preset", MiMoTTSModel, `{"messages":[{"role":"assistant","content":"hi"}],"audio":{"voice":"alloy"}}`, false},
		{"stream wav", MiMoTTSModel, `{"stream":true,"messages":[{"role":"assistant","content":"hi"}],"audio":{"format":"wav"}}`, false},
		{"invalid optimize", MiMoVoiceDesignModel, `{"messages":[{"role":"user","content":"男声"}],"audio":{"optimize_text_preview":"true"}}`, false},
		{"preset optimize", MiMoTTSModel, `{"messages":[{"role":"assistant","content":"hi"}],"audio":{"optimize_text_preview":true}}`, false},
		{"no auto text", MiMoVoiceDesignModel, `{"messages":[{"role":"user","content":"男声"}],"audio":{"optimize_text_preview":false}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMiMoSpeechRequest([]byte(tc.body), tc.model)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMiMoSpeechForwardPreservesAudioAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{MiMoTTSModel, MiMoVoiceDesignModel} {
		for _, stream := range []bool{false, true} {
			t.Run(model+"/"+map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
				body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"温柔女声"},{"role":"assistant","content":"你好"}],"audio":{"format":"wav"},"stream":false}`)
				if stream {
					body = bytes.ReplaceAll(bytes.ReplaceAll(body, []byte(`"wav"`), []byte(`"pcm16"`)), []byte(`"stream":false`), []byte(`"stream":true`))
				}
				response := `{"model":"` + model + `","choices":[{"message":{"role":"assistant","audio":{"data":"AQIDBA==","transcript":null},"final_text_preview":"你好呀"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":80,"total_tokens":90}}`
				ct := "application/json"
				if stream {
					response = "data: " + strings.ReplaceAll(response, `"message":`, `"delta":`) + "\n\ndata: [DONE]\n\n"
					ct = "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
				result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, mimoTestAccount(t), body, "", "")
				require.NoError(t, err)
				require.Equal(t, "https://api.xiaomimimo.com/v1/chat/completions", upstream.lastReq.URL.String())
				require.Equal(t, "Bearer mimo-secret", upstream.lastReq.Header.Get("Authorization"))
				require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
				require.Equal(t, gjson.GetBytes(body, "audio").Raw, gjson.GetBytes(upstream.lastBody, "audio").Raw)
				require.Equal(t, response, rec.Body.String())
				require.Equal(t, 10, result.Usage.InputTokens)
				require.Equal(t, 80, result.Usage.OutputTokens)
				require.Equal(t, stream, result.Stream)
			})
		}
	}
}

func TestMiMoSpeechAudioReleasesLargeRequestStream(t *testing.T) {
	d := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)
	d.ObservePayload([]byte(`{"choices":[{"delta":{"audio":{"data":"AQIDBA=="}}}]}`))
	require.True(t, d.ShouldReleaseClientOutput(), "audio must release buffered SSE before the final usage frame")
	d.ObservePayload([]byte(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	require.False(t, d.IsSilentRefusal())
}

func TestMiMoSpeechCatalogAndTokenPricing(t *testing.T) {
	ctx := context.Background()
	a := mimoTestAccount(t)
	a.GroupIDs = []int64{7}
	repo := newAgentModelMemoryRepo()
	catalog := NewAgentModelCatalogService(&agentCatalogAccountRepoStub{accounts: []Account{*a}}, &agentCatalogGroupRepoStub{group: &Group{ID: 7, Kind: "agent", SystemCode: "yingzo"}}, repo)
	require.NoError(t, repo.SyncDiscovered(ctx, 7, discoverAgentModels([]Account{*a}), time.Now()))
	for _, id := range []string{MiMoTTSModel, MiMoVoiceDesignModel} {
		m := repo.models[agentModelKey(PlatformOpenAI, id)]
		require.Equal(t, AgentMediaTypeAudio, m.MediaType)
		rate := 1.5
		_, err := catalog.UpdateModel(ctx, 7, m.ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true, RateMultiplier: &rate})
		require.NoError(t, err)
		got, _, err := catalog.ResolveTextModelRate(ctx, 7, PlatformOpenAI, id)
		require.NoError(t, err)
		require.Equal(t, rate, got)
		inputPrice, outputPrice := 0.000001, 0.000002
		channels := agentPricingChannelServiceForTest([]Channel{{ID: 3, Status: StatusActive, GroupIDs: []int64{7}, ModelPricing: []ChannelModelPricing{{ChannelID: 3, Platform: PlatformOpenAI, Models: []string{id}, BillingMode: BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}}}, map[int64]string{7: PlatformComposite})
		billing := NewBillingService(&config.Config{}, nil)
		resolver := NewModelPricingResolver(channels, billing)
		resolver.agentModelCatalog = catalog
		require.NoError(t, validateAgentLanguagePricing(ctx, resolver, &Group{ID: 7, Kind: "agent", SystemCode: "yingzo"}, a, id))
		pricing, err := resolver.ResolveAgentTextPricing(ctx, 7, a, id)
		require.NoError(t, err)
		cost, err := billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: id, Tokens: UsageTokens{InputTokens: 10, OutputTokens: 80}, RateMultiplier: pricing.RateMultiplier, Resolver: resolver, Resolved: pricing.Resolved})
		require.NoError(t, err)
		require.InDelta(t, (10*inputPrice+80*outputPrice)*rate, cost.ActualCost, 1e-12)
		_, err = catalog.RequireAccountLanguageModel(ctx, 7, a, id)
		require.NoError(t, err)
		require.Equal(t, []string{AgentInterfaceOpenAIChatCompletions}, agentInterfacesForModel(PlatformOpenAI, AgentMediaTypeAudio, id))
		_, err = catalog.UpdateModel(ctx, 7, m.ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true})
		require.Error(t, err, "missing token multiplier must fail before the model can be enabled")
		_, err = normalizeAgentModelPricesForModel(id, AgentMediaTypeAudio, []AgentModelPrice{{Resolution: "song", UnitPrice: 1}})
		require.Error(t, err, "speech cannot inherit Suno pricing")
	}
}

func TestMiMoSpeechConnectionCheckIsReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := mimoTestAccount(t)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"object":"list","data":[{"id":"mimo-v2.5-tts"}]}`))}}
	svc := &AccountTestService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	require.NoError(t, svc.testMiMoSpeechAccount(c, a))
	require.Equal(t, http.MethodGet, upstream.lastReq.Method)
	require.Equal(t, "https://api.xiaomimimo.com/v1/models", upstream.lastReq.URL.String())
}

func TestMiMoSpeechAuditIncludesSpokenText(t *testing.T) {
	body := []byte(`{"model":"mimo-v2.5-tts","messages":[{"role":"user","content":"温柔"},{"role":"assistant","content":"朗读内容"}]}`)
	audit := MiMoSpeechAuditBody(body, MiMoTTSModel)
	require.Equal(t, "温柔\n\n朗读内容", gjson.GetBytes(audit, "messages.0.content").String())
	require.Equal(t, "温柔 朗读内容", ExtractContentModerationText(ContentModerationProtocolOpenAIChat, audit))
	require.Equal(t, "assistant", gjson.GetBytes(body, "messages.1.role").String())
	require.Equal(t, body, MiMoSpeechAuditBody(body, "gpt-5.4"))
}

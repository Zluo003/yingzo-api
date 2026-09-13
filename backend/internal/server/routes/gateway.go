package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RegisterGatewayRoutes 注册 API 网关路由（Claude/OpenAI/Gemini 兼容）
func RegisterGatewayRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	compositeResolver *service.CompositeRouteResolver,
	cfg *config.Config,
) {
	bodyLimit := middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	textBodyLimit := middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize)
	clientRequestID := middleware.ClientRequestID()
	opsErrorLogger := handler.OpsErrorLoggerMiddleware(opsService)
	endpointNorm := handler.InboundEndpointMiddleware()
	compositeTarget := compositeTargetPlatformMiddleware(compositeResolver)
	compositeGeminiTarget := compositeGeminiTargetPlatformMiddleware(compositeResolver)
	agentModelPlatform := agentModelPlatformMiddleware(h.Agent.ModelCatalog())

	// 未分组 Key 拦截中间件（按协议格式区分错误响应）
	requireGroupAnthropic := middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter)
	requireGroupOpenAI := middleware.RequireGroupAssignment(settingService, middleware.OpenAIErrorWriter)
	requireGroupGoogle := middleware.RequireGroupAssignment(settingService, middleware.GoogleErrorWriter)
	groupModelAllowlist := middleware.GroupModelAllowlist()

	// 聚合分组按"请求模型所属平台"分发，行为与该平台的独立分组保持一致：
	// openai/grok 走 OpenAI 兼容链路，其余（含国产供应商）走 Gateway 链路。
	isOpenAIResponsesCompatibleGatewayPlatform := func(c *gin.Context) bool {
		if platform, ok := agentDispatchPlatform(c, service.PlatformOpenAI); ok {
			return platform == service.PlatformOpenAI || platform == service.PlatformGrok
		}
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok:
			return true
		default:
			return false
		}
	}
	isOpenAIGatewayPlatform := func(c *gin.Context) bool {
		if platform, ok := agentDispatchPlatform(c, service.PlatformOpenAI); ok {
			return platform == service.PlatformOpenAI
		}
		return getGroupPlatform(c) == service.PlatformOpenAI
	}
	countTokensHandler := func(c *gin.Context) {
		if platform, ok := agentDispatchPlatform(c, service.PlatformAnthropic); ok {
			switch platform {
			case service.PlatformOpenAI:
				h.OpenAIGateway.CountTokens(c)
			case service.PlatformGrok:
				h.OpenAIGateway.GrokCountTokens(c)
			default:
				h.Gateway.CountTokens(c)
			}
			return
		}
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.CountTokens(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokCountTokens(c)
		default:
			h.Gateway.CountTokens(c)
		}
	}
	modelsHandler := func(c *gin.Context) {
		if isAgentGroup(c) {
			h.Agent.Models(c)
			return
		}
		if isOpenAIGatewayPlatform(c) && c.Query("client_version") != "" {
			h.OpenAIGateway.CodexModels(c)
			return
		}
		h.Gateway.Models(c)
	}
	isOpenAIOnlyEndpointGatewayPlatform := func(c *gin.Context) bool {
		if platform, ok := agentDispatchPlatform(c, service.PlatformOpenAI); ok {
			return platform == service.PlatformOpenAI
		}
		return getGroupPlatform(c) == service.PlatformOpenAI
	}
	imagesHandler := func(c *gin.Context) {
		switch agentOrGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.Images(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokImages(c)
		default:
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Images API is not supported for this platform",
				},
			})
		}
	}
	videoGenerationHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoGeneration(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoHandler := func(c *gin.Context) {
		setAgentRequestPlatform(c, service.PlatformVideo)
		if getGroupPlatform(c) != service.PlatformVideo && !isAgentGroup(c) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"code":    "video_endpoint_not_available",
					"message": "Videos API is not supported for this platform",
				},
			})
			return
		}
		h.Video.Create(c)
	}
	videoGetHandler := func(c *gin.Context) {
		setAgentRequestPlatform(c, service.PlatformVideo)
		if getGroupPlatform(c) != service.PlatformVideo && !isAgentGroup(c) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"code":    "video_endpoint_not_available",
					"message": "Videos API is not supported for this platform",
				},
			})
			return
		}
		h.Video.Get(c)
	}

	videoStatusHandler := func(c *gin.Context) {
		// Video status requests do not carry a model, so composite groups cannot
		// be resolved by compositeTargetPlatformMiddleware. Route them through
		// the Grok handler and let scheduler/account selection enforce capacity.
		if getGroupPlatform(c) == service.PlatformGrok || getGroupPlatform(c) == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoStatus(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoContentHandler := func(c *gin.Context) {
		// Video content requests do not carry a model, so composite groups cannot
		// be resolved by compositeTargetPlatformMiddleware. Route them through
		// the Grok handler just like video status lookups.
		if getGroupPlatform(c) == service.PlatformGrok || getGroupPlatform(c) == service.PlatformComposite {
			h.OpenAIGateway.GrokVideoContent(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoEditHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoEdit(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	videoExtensionHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoExtension(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Videos API is not supported for this platform"}})
	}
	// /responses/*subpath 的子路径会被转发到上游同名端点之后，因此在入口就拒掉
	// 不可转发的子路径，不让它进入调度与转发流程。可转发的判定见
	// service.IsForwardableOpenAIResponsesRequestPath 及 upstream_path_guard.go。
	guardResponsesSubpath := func(next gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			if !service.IsForwardableOpenAIResponsesRequestPath(c) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Unsupported responses subpath",
					},
				})
				return
			}
			next(c)
		}
	}
	videoCreateDispatch := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok || getGroupPlatform(c) == service.PlatformComposite {
			videoGenerationHandler(c)
			return
		}
		videoHandler(c)
	}
	videoGetDispatch := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformGrok, service.PlatformComposite:
			videoStatusHandler(c)
		default:
			videoGetHandler(c)
		}
	}

	// API网关（Claude API兼容）
	gateway := r.Group("/v1")
	gateway.Use(bodyLimit)
	gateway.Use(clientRequestID)
	gateway.Use(opsErrorLogger)
	gateway.Use(endpointNorm)
	gateway.Use(gin.HandlerFunc(apiKeyAuth))
	gateway.GET("/account", h.Gateway.AccountProfile)
	gateway.GET("/sub2api/billing", h.Gateway.KeyBillingInfo)
	gateway.Use(groupModelAllowlist)
	gateway.Use(compositeTarget)
	gateway.Use(agentModelPlatform)
	gateway.Use(requireGroupAnthropic)
	{
		// /v1/messages: auto-route based on group platform
		gateway.POST("/messages", func(c *gin.Context) {
			if platform, ok := agentDispatchPlatform(c, service.PlatformAnthropic); ok {
				if service.IsOpenAICompatibleAgentPlatform(platform) {
					h.OpenAIGateway.Messages(c)
					return
				}
				h.Gateway.Messages(c)
				return
			}
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Messages(c)
				return
			}
			h.Gateway.Messages(c)
		})
		// /v1/messages/count_tokens: OpenAI bridges upstream, Grok estimates
		// locally, and Anthropic-compatible platforms retain their existing path.
		gateway.POST("/messages/count_tokens", countTokensHandler)
		// Codex CLI / Codex app refresh their model picker from the provider's
		// /models endpoint with a client_version query and expect the ChatGPT
		// Codex manifest format; other clients keep the OpenAI-style list.
		gateway.GET("/models", modelsHandler)
		gateway.GET("/usage", h.Gateway.Usage)
		gateway.POST("/live", h.OpenAIGateway.Live)
		gateway.GET("/live/:call_id", h.OpenAIGateway.LiveSideband)
		// OpenAI Responses API: auto-route based on group platform
		gateway.POST("/responses", func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		})
		gateway.POST("/responses/*subpath", guardResponsesSubpath(func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		}))
		gateway.POST("/alpha/search", textBodyLimit, h.OpenAIGateway.AlphaSearch)
		gateway.GET("/responses", func(c *gin.Context) {
			setAgentRequestPlatform(c, service.PlatformOpenAI)
			h.OpenAIGateway.ResponsesWebSocket(c)
		})
		// OpenAI Chat Completions API: auto-route based on group platform
		gateway.POST("/chat/completions", func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.ChatCompletions(c)
				return
			}
			h.Gateway.ChatCompletions(c)
		})
		gateway.POST("/embeddings", textBodyLimit, func(c *gin.Context) {
			if !isOpenAIOnlyEndpointGatewayPlatform(c) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Embeddings API is not supported for this platform",
					},
				})
				return
			}
			h.OpenAIGateway.Embeddings(c)
		})
		gateway.POST("/images/generations", imagesHandler)
		gateway.POST("/images/edits", imagesHandler)
		gateway.POST("/images/generations/async", h.AsyncImage.Submit)
		gateway.POST("/images/edits/async", h.AsyncImage.Submit)
		gateway.GET("/images/tasks/:task_id", h.AsyncImage.Get)
		gateway.POST("/images/batches", h.BatchImage.Submit)
		gateway.GET("/images/batches", h.BatchImage.List)
		gateway.GET("/images/batches/models", h.BatchImage.Models)
		gateway.GET("/images/batches/:id", h.BatchImage.Get)
		gateway.GET("/images/batches/:id/items", h.BatchImage.Items)
		gateway.GET("/images/batches/:id/items/:custom_id/content", h.BatchImage.ItemContent)
		gateway.GET("/images/batches/:id/download", h.BatchImage.Download)
		gateway.POST("/images/batches/:id/cancel", h.BatchImage.Cancel)
		gateway.DELETE("/images/batches/:id", h.BatchImage.DeleteRecord)
		gateway.DELETE("/images/batches/:id/outputs", h.BatchImage.DeleteOutputs)
		gateway.POST("/videos/generations", videoGenerationHandler)
		gateway.POST("/videos/edits", videoEditHandler)
		gateway.POST("/videos/extensions", videoExtensionHandler)
		gateway.POST("/videos", videoCreateDispatch)
		gateway.GET("/videos/generations/:request_id/content", videoContentHandler)
		gateway.GET("/videos/edits/:request_id/content", videoContentHandler)
		gateway.GET("/videos/extensions/:request_id/content", videoContentHandler)
		gateway.GET("/videos/generations/:request_id", videoStatusHandler)
		gateway.GET("/videos/edits/:request_id", videoStatusHandler)
		gateway.GET("/videos/extensions/:request_id", videoStatusHandler)
		gateway.GET("/videos/:request_id", videoGetDispatch)
		gateway.GET("/videos/:request_id/content", videoContentHandler)
		voiceHandler := func(endpoint string) gin.HandlerFunc {
			return func(c *gin.Context) {
				if getGroupPlatform(c) != service.PlatformGrok {
					service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
					c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
					return
				}
				h.OpenAIGateway.GrokVoice(c, endpoint)
			}
		}
		gateway.POST("/tts", voiceHandler("tts"))
		gateway.POST("/stt", voiceHandler("stt"))
		gateway.POST("/custom-voices", voiceHandler("custom-voices"))
		customVoicePathHandler := func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokVoice(c, grokCustomVoiceEndpoint(c))
		}
		gateway.GET("/custom-voices", voiceHandler("custom-voices"))
		gateway.GET("/custom-voices/:voice_id/audio", customVoicePathHandler)
		gateway.GET("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.PATCH("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.DELETE("/custom-voices/:voice_id", customVoicePathHandler)
		gateway.GET("/realtime", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokRealtime(c)
		})
		gateway.POST("/web_search", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Web Search API is not supported for this platform"}})
				return
			}
			h.Gateway.WebSearch(c)
		})
		gateway.POST("/x_search", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "X Search API is not supported for this platform"}})
				return
			}
			h.Gateway.XSearch(c)
		})
	}

	// /v1/videos/:id 同时服务两条链路：本仓库的 Seedance 异步任务查询，
	// 以及上游 Grok 的视频状态查询。gin 不允许同一路径段用不同参数名，
	// 所以统一注册一次，在这里按分组平台分发。
	// Keep this route in a helper so the root route middleware chain remains
	// auditable while allowing video groups to use their dedicated auth guard.
	rootVideoRoute := func(method, path string, limit gin.HandlerFunc, endpoint gin.HandlerFunc) {
		r.Handle(method, path, limit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), compositeTarget, requireGroupOpenAI, endpoint)
	}

	// Gemini 原生 API 兼容层（Gemini SDK/CLI 直连）
	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	gemini.Use(groupModelAllowlist)
	// agentModelPlatform 必须早于 forceAgentPlatform：后者写入的强制平台取自
	// agentDispatchPlatform，先解析出模型所属平台才能生效。
	gemini.Use(agentModelPlatform)
	gemini.Use(forceAgentPlatform(service.PlatformGemini))
	gemini.Use(compositeGeminiTarget)
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", h.Gateway.GeminiV1BetaListModels)
		gemini.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		// Gin treats ":" as a param marker, but Gemini uses "{model}:{action}" in the same segment.
		gemini.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

	// OpenAI Responses API（不带v1前缀的别名）— auto-route based on group platform
	responsesHandler := func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.Responses(c)
			return
		}
		h.Gateway.Responses(c)
	}
	rootRoute := func(method, path string, limit gin.HandlerFunc, handler gin.HandlerFunc) {
		r.Handle(method, path, limit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), groupModelAllowlist, compositeTarget, agentModelPlatform, requireGroupAnthropic, handler)
	}
	rootRoute(http.MethodPost, "/responses", bodyLimit, responsesHandler)
	rootRoute(http.MethodPost, "/responses/*subpath", bodyLimit, guardResponsesSubpath(responsesHandler))
	rootRoute(http.MethodPost, "/alpha/search", textBodyLimit, h.OpenAIGateway.AlphaSearch)
	rootRoute(http.MethodGet, "/responses", bodyLimit, func(c *gin.Context) {
		h.OpenAIGateway.ResponsesWebSocket(c)
	})
	rootRoute(http.MethodGet, "/models", bodyLimit, modelsHandler)
	rootRoute(http.MethodPost, "/messages/count_tokens", bodyLimit, countTokensHandler)
	codexDirect := r.Group("/backend-api/codex")
	codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), groupModelAllowlist, compositeTarget, requireGroupAnthropic)
	{
		codexDirect.POST("/realtime/calls", h.OpenAIGateway.Live)
		codexDirect.GET("/:call_id", h.OpenAIGateway.LiveSideband)
		codexDirect.POST("/responses", responsesHandler)
		codexDirect.POST("/responses/*subpath", guardResponsesSubpath(responsesHandler))
		codexDirect.POST("/alpha/search", textBodyLimit, h.OpenAIGateway.AlphaSearch)
		codexDirect.GET("/responses", func(c *gin.Context) {
			setAgentRequestPlatform(c, service.PlatformOpenAI)
			h.OpenAIGateway.ResponsesWebSocket(c)
		})
		codexDirect.GET("/models", h.OpenAIGateway.CodexModels)
	}
	// OpenAI Chat Completions API（不带v1前缀的别名）— auto-route based on group platform
	rootRoute(http.MethodPost, "/chat/completions", bodyLimit, func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	})
	rootRoute(http.MethodPost, "/embeddings", textBodyLimit, func(c *gin.Context) {
		if !isOpenAIOnlyEndpointGatewayPlatform(c) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Embeddings API is not supported for this platform",
				},
			})
			return
		}
		h.OpenAIGateway.Embeddings(c)
	})
	rootVideoRoute(http.MethodPost, "/videos", bodyLimit, videoCreateDispatch)
	rootVideoRoute(http.MethodGet, "/videos/:id", bodyLimit, videoGetDispatch)
	rootVideoRoute(http.MethodGet, "/videos/:id/content", bodyLimit, videoContentHandler)
	rootVideoRoute(http.MethodGet, "/videos/generations/:request_id/content", bodyLimit, videoContentHandler)
	rootVideoRoute(http.MethodGet, "/videos/edits/:request_id/content", bodyLimit, videoContentHandler)
	rootVideoRoute(http.MethodGet, "/videos/extensions/:request_id/content", bodyLimit, videoContentHandler)
	rootVideoRoute(http.MethodGet, "/videos/generations/:request_id", bodyLimit, videoStatusHandler)
	rootVideoRoute(http.MethodGet, "/videos/edits/:request_id", bodyLimit, videoStatusHandler)
	rootVideoRoute(http.MethodGet, "/videos/extensions/:request_id", bodyLimit, videoStatusHandler)
	rootRoute(http.MethodPost, "/images/generations", bodyLimit, imagesHandler)
	rootRoute(http.MethodPost, "/images/edits", bodyLimit, imagesHandler)
	rootRoute(http.MethodPost, "/images/generations/async", bodyLimit, h.AsyncImage.Submit)
	rootRoute(http.MethodPost, "/images/edits/async", bodyLimit, h.AsyncImage.Submit)
	rootRoute(http.MethodGet, "/images/tasks/:task_id", bodyLimit, h.AsyncImage.Get)
	rootRoute(http.MethodPost, "/videos/generations", bodyLimit, videoGenerationHandler)
	rootRoute(http.MethodPost, "/videos/edits", bodyLimit, videoEditHandler)
	rootRoute(http.MethodPost, "/videos/extensions", bodyLimit, videoExtensionHandler)
	rootVoiceHandler := func(endpoint string) gin.HandlerFunc {
		return func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformGrok {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
				return
			}
			h.OpenAIGateway.GrokVoice(c, endpoint)
		}
	}
	rootRoute(http.MethodPost, "/tts", bodyLimit, rootVoiceHandler("tts"))
	rootRoute(http.MethodPost, "/stt", bodyLimit, rootVoiceHandler("stt"))
	rootRoute(http.MethodPost, "/custom-voices", bodyLimit, rootVoiceHandler("custom-voices"))
	rootCustomVoicePathHandler := func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Voice API is not supported for this platform"}})
			return
		}
		h.OpenAIGateway.GrokVoice(c, grokCustomVoiceEndpoint(c))
	}
	rootRoute(http.MethodGet, "/custom-voices", bodyLimit, rootVoiceHandler("custom-voices"))
	rootRoute(http.MethodGet, "/custom-voices/:voice_id/audio", bodyLimit, rootCustomVoicePathHandler)
	rootRoute(http.MethodGet, "/custom-voices/:voice_id", bodyLimit, rootCustomVoicePathHandler)
	rootRoute(http.MethodPatch, "/custom-voices/:voice_id", bodyLimit, rootCustomVoicePathHandler)
	rootRoute(http.MethodDelete, "/custom-voices/:voice_id", bodyLimit, rootCustomVoicePathHandler)
	rootRoute(http.MethodGet, "/realtime", bodyLimit, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformGrok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "not_found_error", "message": "Realtime API is not supported for this platform"}})
			return
		}
		h.OpenAIGateway.GrokRealtime(c)
	})
	rootRoute(http.MethodPost, "/web_search", bodyLimit, h.Gateway.WebSearch)
	rootRoute(http.MethodPost, "/x_search", bodyLimit, h.Gateway.XSearch)

	// Antigravity 模型列表
	r.GET("/antigravity/models", gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.Gateway.AntigravityModels)

	// Antigravity 专用路由（仅使用 antigravity 账户，不混合调度）
	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID)
	antigravityV1.Use(opsErrorLogger)
	antigravityV1.Use(endpointNorm)
	antigravityV1.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1.Use(gin.HandlerFunc(apiKeyAuth))
	antigravityV1.Use(groupModelAllowlist)
	antigravityV1.Use(requireGroupAnthropic)
	{
		antigravityV1.POST("/messages", h.Gateway.Messages)
		antigravityV1.POST("/messages/count_tokens", h.Gateway.CountTokens)
		antigravityV1.GET("/models", h.Gateway.AntigravityModels)
		antigravityV1.GET("/usage", h.Gateway.Usage)
	}

	antigravityV1Beta := r.Group("/antigravity/v1beta")
	antigravityV1Beta.Use(bodyLimit)
	antigravityV1Beta.Use(clientRequestID)
	antigravityV1Beta.Use(opsErrorLogger)
	antigravityV1Beta.Use(endpointNorm)
	antigravityV1Beta.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1Beta.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	antigravityV1Beta.Use(groupModelAllowlist)
	antigravityV1Beta.Use(requireGroupGoogle)
	{
		antigravityV1Beta.GET("/models", h.Gateway.GeminiV1BetaListModels)
		antigravityV1Beta.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		antigravityV1Beta.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

}

// dispatchCodexModelsGateway selects the native OpenAI Codex models endpoint
// for OpenAI groups and the generated compatibility endpoint for other groups.
func dispatchCodexModelsGateway(c *gin.Context, openAIHandler, generatedHandler gin.HandlerFunc) {
	if getGroupPlatform(c) == service.PlatformOpenAI {
		openAIHandler(c)
		return
	}
	generatedHandler(c)
}

// getGroupPlatform extracts the group platform from the API Key stored in context.
func getGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		return ""
	}
	if apiKey.Group.Platform == service.PlatformComposite {
		if platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			return platform
		}
	}
	return apiKey.Group.Platform
}

// grokCustomVoiceEndpoint derives the upstream Voice endpoint from the
// matched route template. A voice can itself be named "audio", so inspect the
// route template instead of the raw URL path when deciding whether to append
// the audio suffix.
func grokCustomVoiceEndpoint(c *gin.Context) string {
	endpoint := "custom-voices/" + c.Param("voice_id")
	if strings.HasSuffix(c.FullPath(), "/:voice_id/audio") {
		endpoint += "/audio"
	}
	return endpoint
}

// agentOrGroupPlatform 返回分发用的平台：聚合分组取按模型解析出的平台，
// 其余分组取分组自身的平台。
func agentOrGroupPlatform(c *gin.Context) string {
	if platform, ok := agentDispatchPlatform(c, service.PlatformOpenAI); ok {
		return platform
	}
	return getGroupPlatform(c)
}

func isAgentGroup(c *gin.Context) bool {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	return ok && apiKey.Group != nil && apiKey.Group.IsAgent()
}

// agentResolvedPlatformKey 保存入口中间件按请求模型解析出的平台。
const agentResolvedPlatformKey = "agent_resolved_platform"

// agentModelPlatformMiddleware 在聚合分组上按"请求模型所属平台"细化入口平台。
//
// 聚合分组用同一个凭证服务多个 provider，而协议只能给出一个默认平台
// （/v1/chat/completions 默认 openai）。若不放宽到模型维度，绑定进来的
// grok/国产账号永远不会被选中。模型未配置或查不到时保持协议默认行为，
// 由后续的目录校验给出明确报错。
func agentModelPlatformMiddleware(catalog *service.AgentModelCatalogService) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || !apiKey.Group.IsAgent() || catalog == nil {
			c.Next()
			return
		}
		if model := agentRequestModel(c); model != "" && !c.IsAborted() {
			if platform, found, err := catalog.ResolveModelPlatform(c.Request.Context(), apiKey.Group.ID, model); err == nil && found {
				c.Set(agentResolvedPlatformKey, platform)
			}
		}
		if c.IsAborted() {
			return
		}
		c.Next()
	}
}

// agentRequestModel 提取本次请求的目标模型：Gemini 走路径参数，其余读请求体
// （含 multipart，视频请求的 model 在表单字段里）。读完必须把 body 还原给下游
// handler；读失败则直接以 4xx 结束，绝不能把截断的 body 交给下游——
// multipart 拿到半个 body 会变成"文件损坏"这类误导性报错。
func agentRequestModel(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if model := compositeGeminiModelFromParams(c); model != "" {
		return model
	}
	if c.Request.Body == nil || c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		return ""
	}
	if strings.Contains(strings.ToLower(c.GetHeader("Upgrade")), "websocket") {
		return ""
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		status := http.StatusBadRequest
		message := "Failed to read request body"
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
			message = "Request body is too large"
		}
		c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
		c.Abort()
		return ""
	}
	model := strings.TrimSpace(requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body))
	requestmodel.ResetRequestBody(c.Request, body)
	return model
}

// agentDispatchPlatform 解析聚合分组本次请求应当按哪个平台分发，并把结果同时写入
// 强制平台与"已解析目标平台"：前者供调度选号（ForcePlatform 优先级最高），后者供
// handler 判断上游协议形态（与 composite 分组同一套语义）。
func agentDispatchPlatform(c *gin.Context, fallback string) (string, bool) {
	if !isAgentGroup(c) {
		return "", false
	}
	platform := fallback
	if resolved, ok := c.Get(agentResolvedPlatformKey); ok {
		if value, ok := resolved.(string); ok && value != "" {
			platform = value
		}
	}
	middleware.SetForcePlatform(c, platform)
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
	return platform, true
}

func setAgentRequestPlatform(c *gin.Context, platform string) bool {
	_, ok := agentDispatchPlatform(c, platform)
	return ok
}

func forceAgentPlatform(platform string) gin.HandlerFunc {
	return func(c *gin.Context) {
		setAgentRequestPlatform(c, platform)
		c.Next()
	}
}

func compositeTargetPlatformMiddleware(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	return func(c *gin.Context) {
		apiKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
			c.Next()
			return
		}
		if c.Request == nil || c.Request.Method == http.MethodGet {
			c.Next()
			return
		}

		body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
		if err != nil {
			status := http.StatusBadRequest
			message := "Failed to read request body"
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				status = http.StatusRequestEntityTooLarge
				message = "Request body is too large"
			}
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
			c.Abort()
			return
		}

		routePath := c.FullPath()
		model := requestmodel.FromBodyForRoute(routePath, c.GetHeader("Content-Type"), body)
		if model != "" {
			decision, err := resolver.Resolve(c.Request.Context(), apiKey.Group.ID, model, compositeRouteEndpointForPath(c.Request.URL.Path))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "server_error", "message": "Failed to resolve composite model route"}})
				c.Abort()
				return
			}
			if decision.Matched {
				c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
				if upstreamModel := strings.TrimSpace(decision.UpstreamModel); upstreamModel != "" && upstreamModel != model && gjson.ValidBytes(body) {
					if _, modelPath := requestmodel.JSONModelPathForRoute(routePath, body); modelPath != "" {
						if rewritten, rewriteErr := sjson.SetBytes(body, modelPath, upstreamModel); rewriteErr == nil {
							body = rewritten
						}
					}
				}
			}
		}
		requestmodel.ResetRequestBody(c.Request, body)
		c.Next()
	}
}

func compositeGeminiTargetPlatformMiddleware(resolver *service.CompositeRouteResolver) gin.HandlerFunc {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	return func(c *gin.Context) {
		apiKey, ok := middleware.GetAPIKeyFromContext(c)
		if ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == service.PlatformComposite {
			model := compositeGeminiModelFromParams(c)
			if model != "" {
				decision, err := resolver.Resolve(c.Request.Context(), apiKey.Group.ID, model, service.CompositeRouteEndpointGemini)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "server_error", "message": "Failed to resolve composite model route"}})
					c.Abort()
					return
				}
				if decision.Matched {
					c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
				}
			}
			if _, resolved := service.ResolvedTargetPlatformFromContext(c.Request.Context()); !resolved {
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGemini))
			}
		}
		c.Next()
	}
}

func compositeGeminiModelFromParams(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if model := strings.TrimSpace(c.Param("model")); model != "" {
		return model
	}
	modelAction := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	if modelAction == "" {
		return ""
	}
	if idx := strings.LastIndex(modelAction, ":"); idx >= 0 {
		return strings.TrimSpace(modelAction[:idx])
	}
	return modelAction
}

func compositeRouteEndpointForPath(path string) string {
	switch {
	case strings.Contains(path, "/messages/count_tokens"):
		return service.CompositeRouteEndpointCountTokens
	case strings.Contains(path, "/messages"):
		return service.CompositeRouteEndpointMessages
	case strings.Contains(path, "/responses"):
		return service.CompositeRouteEndpointResponses
	case strings.Contains(path, "/alpha/search"), strings.Contains(path, "/realtime/calls"), strings.HasSuffix(strings.TrimRight(path, "/"), "/live"):
		return service.CompositeRouteEndpointResponses
	case strings.Contains(path, "/chat/completions"):
		return service.CompositeRouteEndpointChatCompletions
	case strings.Contains(path, "/embeddings"):
		return service.CompositeRouteEndpointEmbeddings
	case strings.Contains(path, "/images/"):
		return service.CompositeRouteEndpointImages
	case strings.Contains(path, "/v1beta/"):
		return service.CompositeRouteEndpointGemini
	default:
		return service.CompositeRouteEndpointAny
	}
}

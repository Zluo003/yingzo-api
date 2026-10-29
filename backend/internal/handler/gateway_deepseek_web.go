package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// DeepSeekSearch serves model-independent search with the caller's Yingzo key.
func (h *GatewayHandler) DeepSeekSearch(c *gin.Context) { h.deepSeekWeb(c, "search") }

// DeepSeekFetch serves model-independent page retrieval with the caller's Yingzo key.
func (h *GatewayHandler) DeepSeekFetch(c *gin.Context) { h.deepSeekWeb(c, "fetch") }

func (h *GatewayHandler) deepSeekWeb(c *gin.Context, operation string) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || key.User == nil {
		h.errorResponse(c, 401, "authentication_error", "Missing user identity")
		return
	}
	if key.Group == nil {
		h.errorResponse(c, 403, "permission_error", "An active group is required")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	if err != nil {
		if _, tooLarge := extractMaxBytesError(err); tooLarge {
			h.errorResponse(c, 413, "invalid_request_error", "Web request exceeds the 64 KiB limit")
		} else {
			h.errorResponse(c, 400, "invalid_request_error", "Unable to read the web request")
		}
		return
	}
	payload, err := service.ValidateDeepSeekWebRequest(operation, body)
	if err != nil {
		h.errorResponse(c, 400, "invalid_request_error", err.Error())
		return
	}
	streamStarted := false
	release, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, false, &streamStarted)
	if err != nil {
		h.handleConcurrencyError(c, err, "user", false)
		return
	}
	release = wrapReleaseOnDone(c.Request.Context(), release)
	if release != nil {
		defer release()
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	platform := service.QuotaPlatform(c.Request.Context(), key)
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), key.User, key, key.Group, subscription, platform); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}
	result, requestID, err := h.gatewayService.RunDeepSeekWeb(c.Request.Context(), operation, payload, service.DeepSeekWebUsage{
		APIKey: key, Subscription: subscription, APIKeyService: h.apiKeyService, QuotaPlatform: platform,
		UserAgent: c.GetHeader("User-Agent"), IPAddress: c.ClientIP(),
	})
	if requestID != "" {
		c.Header("X-Request-ID", requestID)
	}
	if err != nil {
		var failure *service.DeepSeekWebError
		if errors.As(err, &failure) {
			if failure.RetryAfter != "" {
				c.Header("Retry-After", failure.RetryAfter)
			}
			h.errorResponse(c, failure.Status, failure.Code, failure.Message)
		} else {
			h.errorResponse(c, 503, "web_service_unavailable", "Web service is unavailable")
		}
		return
	}
	c.Data(http.StatusOK, "application/json", result)
}

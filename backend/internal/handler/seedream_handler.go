package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Recognize the asynchronous APIMart protocol before ordinary OpenAI forwarding.
func (h *AsyncImageHandler) TrySeedream(c *gin.Context) bool {
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		return false
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		imageTaskJSONError(c, 413, "invalid_request_error", "Unable to read image request within body limit")
		return true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var target struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &target)
	if !service.IsSeedreamModel(service.CanonicalSeedreamModel(target.Model)) {
		return false
	}
	h.Seedream(c)
	return true
}

func (h *AsyncImageHandler) Seedream(c *gin.Context) {
	c.Set(ctxKeyLocalImageTaskAdmission, true)
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil || key.GroupID == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	if key.Group.Platform != service.PlatformOpenAI && !key.Group.IsAgent() {
		imageTaskJSONError(c, 404, "not_found_error", "Seedream requires an OpenAI or Agent group")
		return
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		imageTaskJSONError(c, 403, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if h == nil || h.durable == nil {
		imageTaskError(c, service.ErrImageTaskUnavailable)
		return
	}
	replay := false
	if idem := c.GetHeader("Idempotency-Key"); idem != "" {
		_, err := h.durable.Receipt(c.Request.Context(), service.ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}, idem)
		if err == nil {
			replay = true
		} else if !errors.Is(err, service.ErrImageTaskNotFound) {
			imageTaskError(c, err)
			return
		}
	}
	if !replay && !h.durable.Enabled(c.Request.Context()) {
		imageTaskJSONError(c, 503, "image_tasks_disabled", "Enable asynchronous generated images in file storage settings")
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		imageTaskJSONError(c, 413, "invalid_request_error", "Unable to read image request within body limit")
		return
	}
	req, err := service.ParseSeedreamRequest(body)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	if key.Group.ModelAllowlistEnabled() && !key.Group.ModelAllowlist.Allows(req.Model) {
		imageTaskJSONError(c, 404, "not_found_error", "Seedream is not allowed for this group")
		return
	}
	if strings.Contains(c.Request.URL.Path, "/edits") && len(req.ImageURLs) == 0 {
		imageTaskJSONError(c, 400, "invalid_request_error", "edits requires image_urls")
		return
	}
	canonical, _ := json.Marshal(req)
	fingerprint, err := imageRequestFingerprint("/v1/images/generations", "application/json", canonical)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	if !replay && h.openAI != nil {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			imageTaskJSONError(c, 500, "api_error", "User context not found")
			return
		}
		images := make([]map[string]string, 0, len(req.ImageURLs))
		for _, u := range req.ImageURLs {
			images = append(images, map[string]string{"image_url": u})
		}
		audit, _ := json.Marshal(map[string]any{"prompt": req.Prompt, "images": images})
		decision := h.openAI.checkSecurityAudit(c, requestLogger(c, "handler.seedream.security_audit"), key, subject, service.ContentModerationProtocolOpenAIImages, req.Model, audit)
		if decision != nil && !decision.AllowNextStage {
			h.openAI.openAISecurityAuditError(c, decision)
			return
		}
	}
	var origin string
	if !replay {
		origin, err = requestPublicOrigin(c)
	}
	if err != nil {
		imageTaskJSONError(c, 503, "api_error", "Temporary image public URL is unavailable")
		return
	}
	snapshot := &service.ImageRequestSnapshot{Platform: service.PlatformOpenAI, Method: http.MethodPost, Path: "/v1/images/generations", PublicBaseURL: origin, Seedream: &service.SeedreamSnapshot{Request: *req}}
	sub, _ := middleware.GetSubscriptionFromContext(c)
	task, err := h.durable.Accept(c.Request.Context(), key, sub, service.PlatformOpenAI, req.Model, req.BillingTier(), 1, c.GetHeader("Idempotency-Key"), fingerprint, snapshot)
	if err != nil {
		if errors.Is(err, service.ErrImageIdempotencyConflict) {
			imageTaskJSONError(c, 409, "idempotency_conflict", err.Error())
		} else if errors.Is(err, service.ErrAgentImagePricingUnavailable) || errors.Is(err, service.ErrModelPricingUnavailable) {
			imageTaskJSONError(c, 400, "pricing_not_configured", "Configure and enable both Seedream 1K and 2K prices and any required reference image price")
		} else {
			imageTaskError(c, err)
		}
		return
	}
	c.Header("Preference-Applied", "respond-async")
	c.Header("Cache-Control", "no-store")
	c.Header("Location", "/v1/images/tasks/"+task.ID)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, gin.H{"id": task.ID, "task_id": task.ID, "object": task.Object, "status": task.Status, "phase": task.Phase, "billing_status": task.BillingStatus, "refund_status": task.RefundStatus, "created_at": task.CreatedAt, "expires_at": task.ExpiresAt, "deadline_at": task.DeadlineAt, "poll_url": "/v1/images/tasks/" + task.ID})
}

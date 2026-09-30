package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Midjourney always uses the durable image ledger, including admission,
// idempotency, precharge, publication and refunds. Poll the regular image task API.
func (h *AsyncImageHandler) Midjourney(c *gin.Context) {
	c.Set(ctxKeyLocalImageTaskAdmission, true)
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.Group == nil || key.GroupID == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	if key.Group.Platform != service.PlatformOpenAI && !key.Group.IsAgent() {
		imageTaskJSONError(c, 404, "not_found_error", "Midjourney requires an OpenAI or Agent group")
		return
	}
	if !service.GroupAllowsImageGeneration(key.Group) {
		imageTaskJSONError(c, 403, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if key.Group.ModelAllowlistEnabled() && !key.Group.ModelAllowlist.Allows(service.MidjourneyModel) {
		imageTaskJSONError(c, 404, "not_found_error", "Midjourney is not allowed for this group")
		return
	}
	if h == nil || h.durable == nil {
		imageTaskError(c, service.ErrImageTaskUnavailable)
		return
	}
	// Accepted requests remain replayable after the async feature is switched off.
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
		if maxErr, ok := extractMaxBytesError(err); ok {
			imageTaskJSONError(c, 413, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
		} else {
			imageTaskJSONError(c, 400, "invalid_request_error", "Failed to read request")
		}
		return
	}
	action := "imagine"
	if strings.HasSuffix(c.Request.URL.Path, "/upscale") {
		action = "upscale"
	} else if strings.HasSuffix(c.Request.URL.Path, "/edits") {
		action = "edits"
	}
	req, err := service.ParseMidjourneyRequest(body, action)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	// Canonical defaults make omitted model/version and explicit defaults equivalent.
	canonical, _ := json.Marshal(req)
	path := "/v1/midjourney/generations"
	if action != "imagine" {
		path += "/" + action
	}
	fingerprint, err := imageRequestFingerprint(path, "application/json", canonical)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	if !replay && action != "upscale" && h.openAI != nil {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			imageTaskJSONError(c, 500, "api_error", "User context not found")
			return
		}
		decision := h.openAI.checkSecurityAudit(c, requestLogger(c, "handler.midjourney.security_audit"), key, subject, service.ContentModerationProtocolOpenAIImages, service.MidjourneyModel, midjourneyAuditBody(req))
		if decision != nil && !decision.AllowNextStage {
			h.openAI.openAISecurityAuditError(c, decision)
			return
		}
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if p := c.GetHeader("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	snapshot := &service.ImageRequestSnapshot{Platform: service.PlatformOpenAI, Method: http.MethodPost, Path: path, PublicBaseURL: scheme + "://" + c.Request.Host, Midjourney: &service.MidjourneySnapshot{Request: *req, Action: action}}
	sub, _ := middleware.GetSubscriptionFromContext(c)
	task, err := h.durable.Accept(c.Request.Context(), key, sub, service.PlatformOpenAI, service.MidjourneyModel, req.PriceTier(), 1, c.GetHeader("Idempotency-Key"), fingerprint, snapshot)
	if err != nil {
		if errors.Is(err, service.ErrImageIdempotencyConflict) {
			imageTaskJSONError(c, 409, "idempotency_conflict", err.Error())
		} else if errors.Is(err, service.ErrAgentImagePricingUnavailable) || errors.Is(err, service.ErrModelPricingUnavailable) {
			imageTaskJSONError(c, 400, "pricing_not_configured", "Configure and enable the requested Midjourney operation price")
		} else {
			imageTaskError(c, err)
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Location", "/v1/images/tasks/"+task.ID)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, gin.H{"id": task.ID, "task_id": task.ID, "object": task.Object, "status": task.Status, "phase": task.Phase, "billing_status": task.BillingStatus, "refund_status": task.RefundStatus, "created_at": task.CreatedAt, "expires_at": task.ExpiresAt, "deadline_at": task.DeadlineAt, "poll_url": "/v1/images/tasks/" + task.ID})
}

// Use the existing image-audit shape so reference images participate in the
// same configured moderation policy as other image inputs.
func midjourneyAuditBody(req *service.MidjourneyRequest) []byte {
	images := make([]map[string]string, 0, len(req.ImageURLs)+1)
	for _, imageURL := range req.ImageURLs {
		images = append(images, map[string]string{"image_url": imageURL})
	}
	if req.StyleReference != "" {
		images = append(images, map[string]string{"image_url": req.StyleReference})
	}
	body, _ := json.Marshal(map[string]any{"prompt": req.Prompt, "images": images})
	return body
}

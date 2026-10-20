package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const mediaAuditPreflightKey = "_media_audit_preflight"
const mediaAuditPreflightDoneKey = "_media_audit_preflight_done"
const mediaAuditPassKey = "_media_audit_pass_hash"

// Called only after account selection. Returning false terminates this request;
// audit refusals must never enter the generation account failover loop.
func runAccountMediaAudit(c *gin.Context, svc *service.MediaAuditService, account *service.Account, model, mediaType string, body []byte) bool {
	preflight := c.GetBool(mediaAuditPreflightKey)
	if !service.AccountNeedsMediaAudit(account) {
		if preflight {
			c.Set(mediaAuditPreflightDoneKey, true)
			return false
		}
		return true
	}
	if c.GetHeader("X-Yingzo-Audit-Request") != "" {
		c.AbortWithStatusJSON(503, gin.H{"error": gin.H{"code": "media_audit_recursion", "message": "审核模型不能路由到开启媒体审核的账号"}})
		return false
	}
	sum := sha256.Sum256(body)
	fingerprint := hex.EncodeToString(sum[:])
	if c.GetString(mediaAuditPassKey) != fingerprint {
		input := accountMediaAuditInput(c, model, mediaType, body)
		if err := svc.Check(c.Request.Context(), account, input); err != nil {
			var auditErr *service.MediaAuditError
			if !errors.As(err, &auditErr) {
				auditErr = &service.MediaAuditError{}
			}
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(auditErr.StatusCode(), gin.H{"error": gin.H{"type": "content_audit_error", "code": auditErr.Code(), "message": auditErr.Error(), "event_id": auditErr.EventID}})
			return false
		}
		c.Set(mediaAuditPassKey, fingerprint)
	}
	if preflight {
		c.Set(mediaAuditPreflightDoneKey, true)
		return false
	}
	return true
}

func accountMediaAuditInput(c *gin.Context, model, mediaType string, body []byte) service.MediaAuditInput {
	input := service.ExtractMediaAuditInput(body)
	input.Model = model
	input.MediaType = mediaType
	if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil {
		input.UserID, input.APIKeyID = key.UserID, key.ID
		if key.User != nil {
			input.UserEmail = key.User.Email
		}
	}
	if id, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		input.RequestID = id
	}
	return input
}

package handler

import (
	"net/http"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// AccountProfile exposes the account associated with the authenticated API key.
// It is intentionally a Yingzo-native response rather than an OpenAI envelope.
// The endpoint is read-only and does not require billing eligibility, so a user
// can still open the account menu when a key is exhausted or expired.
// GET /v1/account
func (h *GatewayHandler) AccountProfile(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to get user info")
		return
	}
	remaining := user.Balance
	if apiKey.Quota > 0 {
		remaining = apiKey.GetQuotaRemaining()
	}
	username := strings.TrimSpace(user.Username)
	if username == "" {
		username = strings.TrimSpace(user.Email)
	}
	if username == "" {
		username = strings.TrimSpace(apiKey.Name)
	}
	if username == "" {
		username = "Yingzo 用户"
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"object":     "yingzo.account",
		"username":   username,
		"avatar_url": user.AvatarURL,
		"balance":    user.Balance,
		"remaining":  remaining,
		"unit":       "USD",
	})
}

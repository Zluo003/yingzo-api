package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMediaAuditGuardOnlyStopsSelectedEnabledAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"prompt":"a scene"}`)
	for _, enabled := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(string(body)))
		account := &service.Account{ID: 3, Extra: map[string]any{service.MediaInputAuditExtraKey: enabled}}
		continued := runAccountMediaAudit(c, nil, account, "gpt-image-2", "image", body)
		require.Equal(t, !enabled, continued)
		if enabled {
			require.True(t, c.IsAborted())
			require.Equal(t, 503, c.Writer.Status())
		}
	}
}
func TestMediaAuditPreflightNeverSubmitsEvenNormalAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"scene"}`))
	c.Set(mediaAuditPreflightKey, true)
	continued := runAccountMediaAudit(c, nil, &service.Account{ID: 1}, "gpt-image-2", "image", []byte(`{"prompt":"scene"}`))
	require.False(t, continued)
	require.True(t, c.GetBool(mediaAuditPreflightDoneKey))
	require.False(t, c.IsAborted())
}
func TestMediaAuditRecursionMarkerCannotBypassGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{}`))
	c.Request.Header.Set("X-Yingzo-Audit-Request", "1")
	continued := runAccountMediaAudit(c, nil, &service.Account{ID: 1, Extra: map[string]any{service.MediaInputAuditExtraKey: true}}, "vision", "image", []byte(`{}`))
	require.False(t, continued)
	require.True(t, c.IsAborted())
	require.Empty(t, c.GetString(mediaAuditPassKey))
	require.Equal(t, 503, c.Writer.Status())
}

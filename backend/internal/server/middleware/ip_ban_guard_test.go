//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ipBanCheckerStub struct {
	banned map[string]bool
	seen   []string
}

func (s *ipBanCheckerStub) IsBanned(_ context.Context, clientIP string) bool {
	s.seen = append(s.seen, clientIP)
	return s.banned[clientIP]
}

func newIPBanGuardRouter(checker IPBanChecker, clientIP string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		binding := &service.SessionBinding{IP: clientIP, UserAgent: "test"}
		c.Request = c.Request.WithContext(service.WithSessionBinding(c.Request.Context(), binding))
		c.Next()
	})
	r.Use(IPBanGuard(checker))
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/api/v1/settings/public", func(c *gin.Context) { c.String(http.StatusOK, "settings") })
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "home") })
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.String(http.StatusOK, "completion") })
	return r
}

func TestIPBanGuardAllowsUnbannedClients(t *testing.T) {
	checker := &ipBanCheckerStub{banned: map[string]bool{"203.0.113.7": true}}
	router := newIPBanGuardRouter(checker, "198.51.100.1")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"198.51.100.1"}, checker.seen, "the guard must use the security client IP from the session binding")
}

func TestIPBanGuardRejectsAPIRequestsWithReason(t *testing.T) {
	checker := &ipBanCheckerStub{banned: map[string]bool{"203.0.113.7": true}}
	router := newIPBanGuardRouter(checker, "203.0.113.7")

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil),
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")),
	} {
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Code)
		var body struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, "IP_BANNED", body.Reason)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}

func TestIPBanGuardServesHTMLPageToBrowsers(t *testing.T) {
	checker := &ipBanCheckerStub{banned: map[string]bool{"203.0.113.7": true}}
	router := newIPBanGuardRouter(checker, "203.0.113.7")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")
	require.Contains(t, w.Body.String(), "访问受限")
}

func TestIPBanGuardKeepsHealthCheckReachable(t *testing.T) {
	checker := &ipBanCheckerStub{banned: map[string]bool{"203.0.113.7": true}}
	router := newIPBanGuardRouter(checker, "203.0.113.7")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, checker.seen)
}

func TestIPBanGuardWithNilServiceIsANoop(t *testing.T) {
	var svc *service.IPBanService
	router := newIPBanGuardRouter(svc, "203.0.113.7")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil))

	require.Equal(t, http.StatusOK, w.Code)
}

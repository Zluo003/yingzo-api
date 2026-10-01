//go:build unit

package middleware

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMusicAuthDefersExactPriceChecksButKeepsIdentityChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, path, status string
		userActive, keyExpired     bool
		want                       int
	}{
		{"zero_balance_creation", "POST", "/v1/music/generations", service.StatusAPIKeyQuotaExhausted, true, false, 204},
		{"exhausted_key_poll", "GET", "/v1/music/tasks/musictask_test", service.StatusAPIKeyQuotaExhausted, true, false, 204},
		{"exhausted_key_recover", "GET", "/v1/music/tasks/by-idempotency/saved", service.StatusAPIKeyQuotaExhausted, true, false, 204},
		{"expired_new_request", "POST", "/v1/music/generations", service.StatusActive, true, true, 403},
		{"disabled_key_poll", "GET", "/v1/music/tasks/musictask_test", "disabled", true, false, 401},
		{"disabled_user_poll", "GET", "/v1/music/tasks/musictask_test", service.StatusActive, false, false, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &service.Group{ID: 9, Kind: "agent", SystemCode: "yingzo", Status: service.StatusActive, Hydrated: true}
			u := &service.User{ID: 1, Status: service.StatusActive, Balance: 0, Role: service.RoleUser}
			if !tc.userActive {
				u.Status = "disabled"
			}
			key := &service.APIKey{ID: 2, UserID: 1, Key: "test-music", Status: tc.status, User: u, Group: g, GroupID: &g.ID, Quota: 1, QuotaUsed: 1}
			if tc.keyExpired {
				expired := time.Now().Add(-time.Hour)
				key.ExpiresAt = &expired
			}
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
			router.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(204) })
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Authorization", "Bearer test-music")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}

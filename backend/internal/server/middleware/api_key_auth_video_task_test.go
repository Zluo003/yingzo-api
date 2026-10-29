//go:build unit

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPrepaidVideoTaskReadsRemainAvailableAfterBalanceExhaustion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformVideo, service.PlatformOpenAI, service.PlatformGrok} {
		for _, balance := range []float64{0, -59.21} {
			t.Run(fmt.Sprintf("%s/balance=%g", platform, balance), func(t *testing.T) {
				group := &service.Group{ID: 1, Platform: platform, Status: service.StatusActive, Hydrated: true}
				if platform == service.PlatformOpenAI {
					group.Kind, group.SystemCode = "agent", "yingzo"
				}
				key := &service.APIKey{ID: 1, UserID: 2, Key: "test-key", Status: service.StatusActive, GroupID: &group.ID, Group: group,
					User: &service.User{ID: 2, Status: service.StatusActive, Balance: balance}}
				repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				keys := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
				for _, tc := range []struct{ method, path string }{
					{http.MethodGet, "/v1/videos/video_123"}, {http.MethodGet, "/videos/video_123/content"},
					{http.MethodPost, "/v1/videos"}, {http.MethodPost, "/v1/videos/generations"},
					{http.MethodGet, "/v1/videos/generations/grok-task"},
				} {
					router := gin.New()
					router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(keys, nil, cfg)))
					router.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(http.StatusOK) })
					response := httptest.NewRecorder()
					request := httptest.NewRequest(tc.method, tc.path, nil)
					request.Header.Set("x-api-key", key.Key)
					router.ServeHTTP(response, request)
					want := http.StatusForbidden
					if platform != service.PlatformGrok && tc.method == http.MethodGet && (tc.path == "/v1/videos/video_123" || tc.path == "/videos/video_123/content") {
						want = http.StatusOK
					}
					require.Equal(t, want, response.Code, "%s %s: %s", tc.method, tc.path, response.Body.String())
				}
			})
		}
	}
}

//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountProfileReturnsCurrentUserConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, limit := range []int{1, 10, 30, 0, -1} {
		repo := &userHandlerRepoStub{user: &service.User{
			ID: 42, Username: "creator", Balance: 10, Concurrency: limit,
		}}
		h := &GatewayHandler{userService: service.NewUserService(repo, nil, nil, nil)}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/account", nil)
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{UserID: 42})
		// A cached auth subject may lag an administrator's quota change.
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42, Concurrency: 8})
		h.AccountProfile(c)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var payload map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, "yingzo.account", payload["object"])
		require.Equal(t, float64(limit), payload["concurrency"])
		require.Equal(t, float64(10), payload["remaining"])
	}
}

func TestAccountProfileConcurrencyRequiresAuthentication(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	(&GatewayHandler{}).AccountProfile(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

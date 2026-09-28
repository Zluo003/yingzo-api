package routes

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAsyncImageRestoresAcceptedCompositeRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformComposite}})
		c.Next()
	})
	router.Use(compositeTargetPlatformMiddleware(nil))
	router.POST("/v1/images/generations", func(c *gin.Context) {
		model, ok := service.RequestedPublicModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "all/image", model)
		require.Equal(t, service.PlatformGrok, getGroupPlatform(c))
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"grok-imagine-image","prompt":"cat"}`, string(body))
		c.Status(http.StatusNoContent)
	})
	ctx := service.WithAsyncImageExecution(context.Background(), &service.AsyncImageExecution{Route: &service.CompositeRouteDecision{Matched: true, GroupID: 1, PublicModel: "all/image", UpstreamModel: "grok-imagine-image", TargetPlatform: service.PlatformGrok, Source: service.CompositeRouteSourceDetector}})
	req := httptest.NewRequest("POST", "/v1/images/generations", bytes.NewBufferString(`{"model":"all/image","prompt":"cat"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, 204, response.Code)
}

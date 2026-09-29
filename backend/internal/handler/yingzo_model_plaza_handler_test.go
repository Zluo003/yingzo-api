package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestYingzoPlazaRequiresLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &ModelPlazaHandler{}
	r.GET("/yingzo/models", h.GetYingzo)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/yingzo/models", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestYingzoPlazaSelectsExactActiveSystemGroup(t *testing.T) {
	groups := []service.Group{
		{ID: 1, Name: "Yingzo Agent", Status: service.StatusActive},
		{ID: 2, Kind: "agent", SystemCode: "another-agent", Status: service.StatusActive},
		{ID: 3, Kind: "agent", SystemCode: "yingzo", Status: "disabled"},
	}
	require.Zero(t, yingzoPlazaGroupID(groups))
	groups = append(groups, service.Group{ID: 4, Kind: "agent", SystemCode: "yingzo", Status: service.StatusActive})
	require.Equal(t, int64(4), yingzoPlazaGroupID(groups))
}

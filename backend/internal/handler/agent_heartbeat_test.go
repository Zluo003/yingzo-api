package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentHeartbeatUsesAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("INSERT INTO yingzo_agent_heartbeats").WithArgs(int64(42), "203.0.113.42").WillReturnResult(sqlmock.NewResult(0, 1))
	h := &AgentHandler{db: db}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/heartbeat", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{UserID: 42})
		c.Next()
	}, h.Heartbeat)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/heartbeat", nil)
	request.RemoteAddr = "203.0.113.42:5170"
	request.Header.Set("X-Forwarded-For", "198.51.100.8")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

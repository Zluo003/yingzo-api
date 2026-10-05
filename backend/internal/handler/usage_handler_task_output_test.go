package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageOutputRepoStub struct {
	userUsageRepoCapture
	owner       int64
	outputLoads int
	loadError   error
}

func (r *usageOutputRepoStub) GetByID(context.Context, int64) (*service.UsageLog, error) {
	return &service.UsageLog{ID: 1, UserID: r.owner}, nil
}

func (r *usageOutputRepoStub) LoadTaskOutputs(_ context.Context, userID int64, logs []service.UsageLog) error {
	r.outputLoads++
	if userID != r.owner {
		return errors.New("incorrect owner")
	}
	if r.loadError != nil {
		return r.loadError
	}
	for i := range logs {
		logs[i].TaskOutputs = []service.UsageTaskOutput{{URL: "https://example.test/media/asset/asset.png", MediaType: "image", ExpiresAt: time.Now().Add(time.Hour)}}
	}
	return nil
}

func TestUserUsageTaskOutputsRequireOwnershipAndReachDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path    string
		owner         int64
		loadError     error
		status, loads int
	}{
		{"list", "/usage", 42, nil, http.StatusOK, 1},
		{"detail", "/usage/1", 42, nil, http.StatusOK, 1},
		{"other-user", "/usage/1", 43, nil, http.StatusForbidden, 0},
		{"unavailable", "/usage/1", 42, errors.New("asset database unavailable"), http.StatusInternalServerError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &usageOutputRepoStub{owner: tc.owner, loadError: tc.loadError}
			repo.listRows = []service.UsageLog{{ID: 1, UserID: tc.owner}}
			h := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
				c.Next()
			})
			router.GET("/usage", h.List)
			router.GET("/usage/:id", h.GetByID)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.status, response.Code)
			require.Equal(t, tc.loads, repo.outputLoads)
			if tc.status == http.StatusOK {
				var body map[string]any
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				require.Contains(t, response.Body.String(), `"task_outputs":[{"url":"https://example.test/media/asset/asset.png","media_type":"image"`)
			} else {
				require.NotContains(t, response.Body.String(), "task_outputs")
			}
		})
	}
}

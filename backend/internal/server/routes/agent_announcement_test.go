package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentAnnouncementRepo struct {
	service.AnnouncementRepository
	item service.Announcement
}

func (r *agentAnnouncementRepo) ListActive(context.Context, time.Time) ([]service.Announcement, error) {
	return []service.Announcement{r.item}, nil
}
func (r *agentAnnouncementRepo) GetByID(context.Context, int64) (*service.Announcement, error) {
	return &r.item, nil
}

type agentAnnouncementUsers struct{ service.UserRepository }

func (*agentAnnouncementUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 7, Balance: 10}, nil
}

type agentAnnouncementSubscriptions struct {
	service.UserSubscriptionRepository
}

func (*agentAnnouncementSubscriptions) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type agentAnnouncementReads struct {
	service.AnnouncementReadRepository
	marked bool
}

func (*agentAnnouncementReads) GetReadMapByUser(context.Context, int64, []int64) (map[int64]time.Time, error) {
	return map[int64]time.Time{}, nil
}
func (r *agentAnnouncementReads) MarkRead(context.Context, int64, int64, time.Time) error {
	r.marked = true
	return nil
}

func TestAgentAnnouncementsUseAuthenticatedUserAndReadStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reads := &agentAnnouncementReads{}
	announcements := service.NewAnnouncementService(
		&agentAnnouncementRepo{item: service.Announcement{
			ID: 7, Title: "公告", Content: "内容", Status: service.AnnouncementStatusActive,
			NotifyMode: service.AnnouncementNotifyModeSilent, CreatedAt: time.Now(),
		}}, reads, &agentAnnouncementUsers{}, &agentAnnouncementSubscriptions{},
	)
	r := gin.New()
	auth := func(c *gin.Context) {
		groupID := int64(9)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, Group: &service.Group{ID: groupID, Kind: "agent", SystemCode: "yingzo"}})
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	}
	RegisterAgentRoutes(r, r.Group("/api/v1"), &handler.Handlers{Announcement: handler.NewAnnouncementHandler(announcements)}, auth)

	listed := httptest.NewRecorder()
	r.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/v1/agent/announcements", nil))
	require.Equal(t, http.StatusOK, listed.Code)
	require.Contains(t, listed.Body.String(), `"title":"公告"`)
	require.NotContains(t, listed.Body.String(), `"read_at"`)

	read := httptest.NewRecorder()
	r.ServeHTTP(read, httptest.NewRequest(http.MethodPost, "/api/v1/agent/announcements/7/read", nil))
	require.Equal(t, http.StatusOK, read.Code)
	require.True(t, reads.marked)
}

func TestAgentAnnouncementsRejectOrdinaryAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterAgentRoutes(r, r.Group("/api/v1"), &handler.Handlers{Announcement: &handler.AnnouncementHandler{}}, agentAuthForTest(false))
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/agent/announcements"},
		{http.MethodPost, "/api/v1/agent/announcements/7/read"},
	} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(request.method, request.path, nil))
		require.Equal(t, http.StatusForbidden, response.Code)
	}
}

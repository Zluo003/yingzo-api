package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hours=N 滚动时间窗：用户门户"最近 24 小时"等按小时对齐的消耗视图依赖它，
// 提供时优先于 start_date/end_date/period。
func TestUserUsageModelsHoursWindow(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/models?hours=24", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	start, end := repo.modelStatsWindow()
	require.InDelta(t, (24 * time.Hour).Seconds(), end.Sub(start).Seconds(), 2)
	require.WithinDuration(t, time.Now(), end, 5*time.Second)

	var payload struct {
		Data struct {
			StartDate string `json:"start_date"`
			EndDate   string `json:"end_date"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotEmpty(t, payload.Data.StartDate)
}

func TestUserUsageHoursOverridesDateParams(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/models?hours=24&start_date=2026-01-01&end_date=2026-01-31", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	start, end := repo.modelStatsWindow()
	// hours 优先：窗口仍是滚动 24 小时，而不是 1 月整月。
	require.InDelta(t, (24 * time.Hour).Seconds(), end.Sub(start).Seconds(), 2)
}

func TestUserUsageHoursInvalidValuesRejected(t *testing.T) {
	for _, query := range []string{"hours=0", "hours=-5", "hours=abc", "hours=2161"} {
		repo := &userUsageRepoCapture{}
		router := newUserUsageRequestTypeTestRouter(repo)
		req := httptest.NewRequest(http.MethodGet, "/usage/dashboard/models?"+query, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, query)
	}
}

func TestUserUsageListHoursWindow(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?hours=168&page=1&page_size=20", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	start, end := repo.listTimeWindow()
	require.InDelta(t, (168 * time.Hour).Seconds(), end.Sub(start).Seconds(), 2)
}

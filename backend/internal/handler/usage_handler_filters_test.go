package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUserUsageListCombinedMediaFilters(t *testing.T) {
	for _, media := range []string{"text", "image", "audio", "video"} {
		t.Run(media, func(t *testing.T) {
			repo := &userUsageRepoCapture{}
			router := newUserUsageRequestTypeTestRouter(repo)
			req := httptest.NewRequest(http.MethodGet, "/usage?page=2&page_size=50&model=vendor%2Fmodel%2Bpreview&usage_type="+media+"&start_date=2026-10-03&end_date=2026-10-03&timezone=Asia%2FShanghai", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, int64(42), repo.listFilters.UserID)
			require.Equal(t, media, repo.listFilters.UsageType)
			require.Equal(t, "vendor/model+preview", repo.listFilters.Model)
			require.Nil(t, repo.listFilters.RequestType)
			require.Empty(t, repo.listFilters.BillingMode)
			require.Equal(t, 2, repo.listParams.Page)
			require.Equal(t, 50, repo.listParams.PageSize)
			require.Equal(t, time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC), repo.listFilters.StartTime.UTC())
			require.Equal(t, time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC), repo.listFilters.EndTime.UTC())
		})
	}
}

func TestUserUsageListRejectsInvalidMediaFilters(t *testing.T) {
	for _, query := range []string{"usage_type=stream", "usage_type=invalid", "start_date=2026-10-04&end_date=2026-10-03", "start_date=not-a-date"} {
		t.Run(query, func(t *testing.T) {
			repo := &userUsageRepoCapture{}
			router := newUserUsageRequestTypeTestRouter(repo)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage?"+query, nil))
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, repo.listFilters.UserID, "invalid filters must not query the repository")
		})
	}
}

func TestUserUsageFiltersApplyToStatsAndCharts(t *testing.T) {
	for _, path := range []string{"/usage/stats", "/usage/dashboard/trend", "/usage/dashboard/models", "/usage/dashboard/snapshot-v2"} {
		t.Run(path, func(t *testing.T) {
			repo := &userUsageRepoCapture{}
			router := newUserUsageRequestTypeTestRouter(repo)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?usage_type=image&model=image-model&start_date=2026-10-02&end_date=2026-10-03&timezone=Asia%2FShanghai", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			filters := repo.statsFilters
			switch path {
			case "/usage/dashboard/trend":
				filters = repo.trendFilters
			case "/usage/dashboard/models", "/usage/dashboard/snapshot-v2":
				filters = repo.modelFilters
			}
			require.Equal(t, int64(42), filters.UserID)
			require.Equal(t, "image", filters.UsageType)
			require.Equal(t, "image-model", filters.Model)
			require.Equal(t, time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC), filters.StartTime.UTC())
			require.Equal(t, time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC), filters.EndTime.UTC())
			if path == "/usage/dashboard/snapshot-v2" {
				require.Equal(t, filters, repo.trendFilters)
			}
			rec = httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?usage_type=invalid", nil))
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

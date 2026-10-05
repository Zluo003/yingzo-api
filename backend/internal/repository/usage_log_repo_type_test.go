package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageLogListMediaFiltersCountAndPagination(t *testing.T) {
	for _, media := range []string{"text", "image", "audio", "video"} {
		t.Run(media, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db}
			start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 0, 1)
			filters := usagestats.UsageLogFilters{UserID: 42, Model: "image-model", ModelFilterSource: usagestats.ModelSourceRequested, UsageType: media, StartTime: &start, EndTime: &end}
			where := "WHERE user_id = $1 AND " + resolveModelDimensionExpression(usagestats.ModelSourceRequested) + " = $2 AND " + usageLogTypeExpression + " = $3 AND created_at >= $4 AND created_at < $5"
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM usage_logs "+where)).WithArgs(int64(42), "image-model", media, start, end).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(25)))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT "+usageLogSelectColumns+" FROM usage_logs "+where+" ORDER BY id DESC LIMIT $6 OFFSET $7")).WithArgs(int64(42), "image-model", media, start, end, 20, 20).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			_, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 2, PageSize: 20}, filters)
			require.NoError(t, err)
			require.Equal(t, int64(25), page.Total)
			require.Equal(t, 2, page.Pages)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUsageLogMediaFiltersApplyToAggregations(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	filters := usagestats.UsageLogFilters{UserID: 42, Model: "image-model", ModelFilterSource: usagestats.ModelSourceRequested, UsageType: "image", StartTime: &start, EndTime: &end}
	for _, kind := range []string{"stats", "trend", "models", "groups"} {
		t.Run(kind, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := &usageLogRepository{sql: db}
			if kind == "stats" {
				mock.ExpectQuery("(?s)WITH scoped AS.*"+regexp.QuoteMeta(usageLogTypeExpression+" = $3 AND created_at >= $4 AND created_at < $5")).WithArgs(int64(42), "image-model", "image", start, end).WillReturnRows(sqlmock.NewRows([]string{"unused"}))
				_, err := repo.GetStatsWithFilters(context.Background(), filters)
				require.NoError(t, err)
			} else {
				condition := usageLogTypeExpression + " = $5"
				if kind == "groups" {
					condition, _ = appendUsageLogTypeQueryFilter("", []any{start, end, int64(42), "image-model"}, "image", "ul")
				}
				mock.ExpectQuery("(?s)SELECT.*"+regexp.QuoteMeta(condition)+".*GROUP BY").WithArgs(start, end, int64(42), "image-model", "image").WillReturnRows(sqlmock.NewRows([]string{"unused"}))
				switch kind {
				case "trend":
					_, err := repo.GetUsageTrendWithUsageFilters(context.Background(), start, end, "hour", filters)
					require.NoError(t, err)
				case "models":
					_, err := repo.GetModelStatsWithUsageFiltersBySource(context.Background(), start, end, filters, usagestats.ModelSourceRequested)
					require.NoError(t, err)
				case "groups":
					_, err := repo.GetGroupStatsWithUsageFilters(context.Background(), start, end, filters)
					require.NoError(t, err)
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

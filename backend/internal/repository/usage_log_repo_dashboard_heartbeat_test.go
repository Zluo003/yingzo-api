package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestDashboardEntityStatsCountsOnlineUsersAndIPs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC)
	today := now.Truncate(24 * time.Hour)
	mock.ExpectQuery("COUNT\\(\\*\\) as total_users").WithArgs(today).
		WillReturnRows(sqlmock.NewRows([]string{"total_users", "today_new_users"}).AddRow(8, 2))
	mock.ExpectQuery("FROM yingzo_agent_heartbeats h").WithArgs(now.Add(-150 * time.Second)).
		WillReturnRows(sqlmock.NewRows([]string{"client_ip", "users"}).
			AddRow("203.0.113.42", 2).
			AddRow("", 1))
	mock.ExpectQuery("COUNT\\(\\*\\) as total_api_keys").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"total_api_keys", "active_api_keys"}).AddRow(3, 2))
	mock.ExpectQuery("COUNT\\(\\*\\) as total_accounts").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), now, now).
		WillReturnRows(sqlmock.NewRows([]string{"total_accounts", "normal_accounts", "error_accounts", "ratelimit_accounts", "overload_accounts"}).AddRow(4, 3, 1, 0, 0))

	stats := &usagestats.DashboardStats{}
	err = (&usageLogRepository{sql: db}).fillDashboardEntityStats(context.Background(), stats, today, now)
	require.NoError(t, err)
	require.EqualValues(t, 3, stats.OnlineUsers)
	require.Equal(t, []usagestats.OnlineIPCount{{IP: "203.0.113.42", Users: 2}}, stats.OnlineIPCounts)
	require.NoError(t, mock.ExpectationsWereMet())
}

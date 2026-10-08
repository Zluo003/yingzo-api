package repository

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSetErrorOnlyRecordsMessageAfterRepeatedFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	// Match the complete assignment list: adding a status or scheduling mutation
	// would fail this test, including when the account was manually paused.
	const updateSQL = `UPDATE "accounts" SET "updated_at" = \$1, "error_message" = \$2 WHERE .*"id" = \$3`
	for _, message := range []string{"upstream unavailable", "upstream timeout", "upstream still unavailable"} {
		mock.ExpectExec(updateSQL).
			WithArgs(sqlmock.AnyArg(), message, int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`INSERT INTO scheduler_outbox`).
			WithArgs(service.SchedulerOutboxEventAccountChanged, int64(42), nil, nil, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.SetError(context.Background(), 42, message))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetErrorFailedWriteDoesNotPublishSchedulerChange(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)
	writeErr := errors.New("database unavailable")
	mock.ExpectExec(`UPDATE "accounts"`).WillReturnError(writeErr)

	require.ErrorIs(t, repo.SetError(context.Background(), 42, "upstream unavailable"), writeErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRepeatedUpstreamErrorsKeepAccountSchedulable(t *testing.T) {
	for _, platform := range []string{service.PlatformGemini, service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			repo := newAccountRepositoryWithSQL(client, nil, nil)
			rateLimits := service.NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &service.Account{
				ID: 42, Platform: platform, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true,
			}
			for range 5 {
				// Verify the real error handler reaches a diagnostic-only SQL write.
				mock.ExpectExec(`UPDATE "accounts" SET "updated_at" = \$1, "error_message" = \$2 WHERE .*"id" = \$3`).
					WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), account.ID).
					WillReturnResult(sqlmock.NewResult(0, 1))
				rateLimits.HandleUpstreamError(context.Background(), account, http.StatusForbidden, nil,
					[]byte(`{"error":{"message":"temporary upstream rejection"}}`))
				require.True(t, account.IsSchedulable())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

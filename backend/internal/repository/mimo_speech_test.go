//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMiMoSpeechAccountFilterUsesProviderWithoutChangingStoredType(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	repo := newAccountRepositoryWithSQL(client, nil, nil)
	mock.ExpectQuery(`SELECT COUNT.*FROM "accounts" WHERE`).
		WithArgs(service.PlatformOpenAI, service.AccountTypeAPIKey, service.MiMoSpeechProvider).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	count, err := repo.accountListFilteredQuery("", "speech", "", "", 0, "").Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

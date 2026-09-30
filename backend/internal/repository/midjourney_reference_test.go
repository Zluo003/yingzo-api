package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type midjourneyQuoteArgument struct{}

func (midjourneyQuoteArgument) Match(value driver.Value) bool {
	data, ok := value.([]byte)
	if !ok {
		return false
	}
	var quote service.ImageTaskQuote
	return json.Unmarshal(data, &quote) == nil && quote.EncryptedProviderReference == "encrypted reference" && quote.Model == service.MidjourneyModel
}
func TestMidjourneyReferenceSavedAtomicallyWithResponse(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	task := &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: "imgtask_x"}, LeaseToken: "lease", EncryptedResult: "encrypted images", Quote: service.ImageTaskQuote{Model: service.MidjourneyModel, EncryptedProviderReference: "encrypted reference"}}
	mock.ExpectExec(`UPDATE image_tasks SET encrypted_result=\$3,captured_usage=\$4,quote=\$5,phase='saving'`).WithArgs(task.ID, task.LeaseToken, task.EncryptedResult, sqlmock.AnyArg(), midjourneyQuoteArgument{}).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, NewImageTaskLedger(db).SaveResponse(context.Background(), task))
	require.NoError(t, mock.ExpectationsWereMet())
}

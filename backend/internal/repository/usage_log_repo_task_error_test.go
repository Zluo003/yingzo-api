//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHydrateUsageTaskErrorsBatchesOnlyFailureRefunds(t *testing.T) {
	var queries []string
	db, mock := newSQLCapturingMock(t, &queries)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	failure, settlement := "failure_refund", "settlement_refund"
	logs := []service.UsageLog{
		{ID: 1, RequestID: "image:one:failure_refund", FundsEvent: &failure, ActualCost: -0.2},
		{ID: 2, RequestID: "video:two:refund", ActualCost: -1},
		{ID: 3, RequestID: "image:three:precharge", ActualCost: 0.2},
		{ID: 4, FundsEvent: &settlement, ActualCost: -0.1},
		{ID: 5, RequestID: "video:old:refund", ActualCost: -1},
	}
	mock.ExpectQuery("SELECT ul.id").WithArgs(int64(1), int64(2), int64(5)).WillReturnRows(sqlmock.NewRows([]string{"id", "diagnostic", "legacy"}).
		AddRow(1, `{"code":451,"message":"image rejected"}`, nil).
		AddRow(2, `{"code":429,"message":"video quota exceeded"}`, nil).
		AddRow(5, nil, `{"code":"video_generation_failed","message":"生成失败"}`))
	require.NoError(t, repo.hydrateUsageTaskErrors(context.Background(), logs))
	require.Equal(t, 451, logs[0].TaskError.Code)
	require.Equal(t, 429, logs[1].TaskError.Code)
	require.Nil(t, logs[2].TaskError)
	require.Nil(t, logs[3].TaskError)
	require.Zero(t, logs[4].TaskError.Code)
	require.Equal(t, "这条历史记录未保存上游原始错误，已保存的错误信息：\n生成失败", logs[4].TaskError.Message)
	require.Len(t, queries, 1)
	for _, scope := range []string{"it.user_id = ul.user_id", "it.api_key_id = ul.api_key_id", "vt.user_id = ul.user_id", "vt.api_key_id = ul.api_key_id"} {
		require.Contains(t, queries[0], scope)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImageRefundPendingPersistsDiagnosticBeforeSettlement(t *testing.T) {
	var queries []string
	db, mock := newSQLCapturingMock(t, &queries)
	task := &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{
		ID: "imgtask_retry", Status: "processing", TaskError: &service.UsageTaskError{Code: 451, Message: "original rejection"},
	}, LeaseToken: "lease"}
	record, err := json.Marshal(task.ImageTaskRecord)
	require.NoError(t, err)
	mock.ExpectExec("WITH changed AS").WithArgs(task.ID, task.LeaseToken, nil, string(record)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, (&durableImageLedger{db: db}).MarkRefundPending(context.Background(), task))
	require.Contains(t, queries[0], "record=$4::jsonb")
	require.Contains(t, queries[0], "lease_token=$2 AND status='processing'")
	require.NoError(t, mock.ExpectationsWereMet())
}

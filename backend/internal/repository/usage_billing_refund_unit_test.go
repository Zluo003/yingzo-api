//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 退费（负数费用）回冲：与扣费对称地回补每一本账。任何一个回冲 SQL 缺失，
// 都会出现"使用记录显示已退费、实际余额/配额没回来"的静默丢失。

const (
	creditBalanceSQL            = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING balance`
	decrementSubscriptionSQL    = `(?s)UPDATE user_subscriptions us\s+SET\s+daily_usage_usd = GREATEST\(0, us\.daily_usage_usd \+ \$1\),\s+weekly_usage_usd = GREATEST\(0, us\.weekly_usage_usd \+ \$1\),\s+monthly_usage_usd = GREATEST\(0, us\.monthly_usage_usd \+ \$1\),\s+updated_at = NOW\(\)`
	decrementAPIKeyQuotaSQL     = `(?s)UPDATE api_keys\s+SET quota_used = GREATEST\(0, quota_used \+ \$1\),\s+status = CASE\s+WHEN quota > 0\s+AND status = \$3\s+AND GREATEST\(0, quota_used \+ \$1\) < quota\s+THEN \$4\s+ELSE status\s+END,\s+updated_at = NOW\(\)`
	decrementAPIKeyRateLimitSQL = `(?s)UPDATE api_keys SET\s+usage_5h = GREATEST\(0, usage_5h \+ \$1\),\s+usage_1d = GREATEST\(0, usage_1d \+ \$1\),\s+usage_7d = GREATEST\(0, usage_7d \+ \$1\),\s+updated_at = NOW\(\)`
	decrementAccountQuotaSQL    = `(?s)UPDATE accounts SET extra = \(\s+COALESCE\(extra, '\{\}'::jsonb\)\s+\|\| jsonb_build_object\('quota_used', GREATEST\(0, COALESCE\(\(extra->>'quota_used'\)::numeric, 0\) \+ \$1\)\)`
)

func newRefundTestTx(t *testing.T) (sqlmock.Sqlmock, *sql.Tx) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return mock, tx
}

func TestCreditUsageBillingBalanceAddsBack(t *testing.T) {
	mock, tx := newRefundTestTx(t)
	mock.ExpectQuery(creditBalanceSQL).
		WithArgs(4.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(104.0))

	newBalance, err := creditUsageBillingBalance(context.Background(), tx, 42, 4.0)
	require.NoError(t, err)
	require.InDelta(t, 104.0, newBalance, 0.0001)
	require.NoError(t, mock.ExpectationsWereMet())

	// 用户不存在时必须报错而不是静默成功。
	mock2, tx2 := newRefundTestTx(t)
	mock2.ExpectQuery(creditBalanceSQL).
		WithArgs(4.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	_, err = creditUsageBillingBalance(context.Background(), tx2, 42, 4.0)
	require.ErrorIs(t, err, service.ErrUserNotFound)
}

func TestDecrementUsageBillingSubscriptionClampsAtZero(t *testing.T) {
	mock, tx := newRefundTestTx(t)
	mock.ExpectExec(decrementSubscriptionSQL).
		WithArgs(-4.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, decrementUsageBillingSubscription(context.Background(), tx, 7, -4.0))
	require.NoError(t, mock.ExpectationsWereMet())

	// 订阅不存在时必须报错而不是静默成功。
	mock2, tx2 := newRefundTestTx(t)
	mock2.ExpectExec(decrementSubscriptionSQL).
		WithArgs(-4.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	require.ErrorIs(t, decrementUsageBillingSubscription(context.Background(), tx2, 7, -4.0), service.ErrSubscriptionNotFound)
}

func TestDecrementUsageBillingAPIKeyQuotaRestoresExhaustedStatus(t *testing.T) {
	mock, tx := newRefundTestTx(t)
	mock.ExpectExec(decrementAPIKeyQuotaSQL).
		WithArgs(-4.0, int64(10), service.StatusAPIKeyQuotaExhausted, service.StatusAPIKeyActive).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, decrementUsageBillingAPIKeyQuota(context.Background(), tx, 10, -4.0))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDecrementUsageBillingAPIKeyRateLimitClamps(t *testing.T) {
	mock, tx := newRefundTestTx(t)
	mock.ExpectExec(decrementAPIKeyRateLimitSQL).
		WithArgs(-4.0, int64(10)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, decrementUsageBillingAPIKeyRateLimit(context.Background(), tx, 10, -4.0))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDecrementUsageBillingAccountQuotaClamps(t *testing.T) {
	mock, tx := newRefundTestTx(t)
	mock.ExpectExec(decrementAccountQuotaSQL).
		WithArgs(-4.0, int64(30)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, decrementUsageBillingAccountQuota(context.Background(), tx, 30, -4.0))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 端到端（effects 层）：一条全字段的负数命令必须触发每一本账的回冲 SQL，
// 缺一不可；这条断言锁住"视频退费真正落到数据库"的核心保证。
func TestApplyUsageBillingEffectsRefundsEveryLedger(t *testing.T) {
	ctx := context.Background()
	mock, tx := newRefundTestTx(t)
	subID := int64(7)
	cmd := &service.UsageBillingCommand{
		RequestID:           "video:x:refund",
		APIKeyID:            10,
		UserID:              42,
		AccountID:           30,
		AccountType:         service.AccountTypeAPIKey,
		BalanceCost:         -4,
		SubscriptionCost:    -4,
		SubscriptionID:      &subID,
		APIKeyQuotaCost:     -4,
		APIKeyRateLimitCost: -4,
		AccountQuotaCost:    -4,
	}
	mock.ExpectExec(decrementSubscriptionSQL).WithArgs(-4.0, subID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(creditBalanceSQL).WithArgs(4.0, int64(42)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(104.0))
	mock.ExpectExec(decrementAPIKeyQuotaSQL).WithArgs(-4.0, int64(10), service.StatusAPIKeyQuotaExhausted, service.StatusAPIKeyActive).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(decrementAPIKeyRateLimitSQL).WithArgs(-4.0, int64(10)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(decrementAccountQuotaSQL).WithArgs(-4.0, int64(30)).WillReturnResult(sqlmock.NewResult(0, 1))

	result := &service.UsageBillingApplyResult{Applied: true}
	require.NoError(t, (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, cmd, result))
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 104.0, *result.NewBalance, 0.0001)
	require.NoError(t, mock.ExpectationsWereMet())
}

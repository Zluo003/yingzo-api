package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func affiliateWalletTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	dsn := os.Getenv("AFFILIATE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set AFFILIATE_TEST_POSTGRES_DSN to run PostgreSQL wallet checks")
	}
	base, err := dbent.Open(dialect.Postgres, dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("affiliate_wallet_%d", time.Now().UnixNano())
	_, err = base.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = base.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = base.Close()
	})
	client, err := dbent.Open(dialect.Postgres, dsn+" search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	for _, n := range []string{"130_add_user_affiliates.sql", "131_affiliate_rebate_hardening.sql", "132_affiliate_custom_settings.sql", "133_affiliate_rebate_freeze.sql", "134_affiliate_ledger_audit_snapshots.sql", "268_affiliate_wallet_and_first_recharge.sql", "269_affiliate_refund_reversals.sql"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "migrations", n))
		require.NoError(t, err)
		_, err = client.ExecContext(context.Background(), string(data))
		require.NoError(t, err, n)
	}
	return client
}
func TestAffiliateWalletPostgresConcurrentWithdrawalTransferAndRejection(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	u, err := client.User.Create().SetEmail("wallet@example.com").SetPasswordHash("test").SetRole("user").SetStatus("active").SetConcurrency(1).Save(ctx)
	require.NoError(t, err)
	repo, ok := NewAffiliateRepository(client, nil).(*affiliateRepository)
	require.True(t, ok)
	// The INSERT trigger creates the code without anyone visiting the affiliate page.
	summary, err := queryAffiliateByUserID(ctx, client, u.ID)
	require.NoError(t, err)
	require.NotEmpty(t, summary.AffCode)
	_, err = client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=100,aff_history_quota=100 WHERE user_id=$1`, u.ID)
	require.NoError(t, err)
	_, _, err = repo.TransferQuotaToBalanceMinimum(ctx, u.ID, 101)
	require.ErrorIs(t, err, service.ErrAffiliateMinimum)
	summary, err = repo.EnsureUserAffiliate(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, summary.AffQuota)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := repo.CreateAffiliateWithdrawal(ctx, u.ID, 80, 50, "test@alipay.com", "测试用户")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for e := range errs {
		if e == nil {
			successes++
		} else {
			require.ErrorIs(t, e, service.ErrAffiliateInsufficient)
		}
	}
	require.Equal(t, 1, successes)
	_, _, err = repo.TransferQuotaToBalanceMinimum(ctx, u.ID, 50)
	require.ErrorIs(t, err, service.ErrAffiliateMinimum)
	records, total, err := repo.ListAffiliateWalletRecords(ctx, u.ID, "withdrawals", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.NoError(t, repo.ProcessAffiliateWithdrawal(ctx, records[0].ID, u.ID, "rejected", "信息有误"))
	require.ErrorIs(t, repo.ProcessAffiliateWithdrawal(ctx, records[0].ID, u.ID, "rejected", "重复"), service.ErrAffiliateWithdrawalProcessed)
	transferred, balance, err := repo.TransferQuotaToBalanceMinimum(ctx, u.ID, 100)
	require.NoError(t, err)
	require.Equal(t, 100.0, transferred)
	require.Equal(t, 100.0, balance)
	_, _, err = repo.TransferQuotaToBalanceMinimum(ctx, u.ID, 0)
	require.ErrorIs(t, err, service.ErrAffiliateQuotaEmpty)
	// A paid request remains debited, and cannot later be rejected and refunded.
	_, err = client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=60 WHERE user_id=$1`, u.ID)
	require.NoError(t, err)
	record, err := repo.CreateAffiliateWithdrawal(ctx, u.ID, 60, 50, "test@alipay.com", "测试用户")
	require.NoError(t, err)
	require.NoError(t, repo.ProcessAffiliateWithdrawal(ctx, record.ID, u.ID, "paid", "transaction-1"))
	require.ErrorIs(t, repo.ProcessAffiliateWithdrawal(ctx, record.ID, u.ID, "rejected", "duplicate"), service.ErrAffiliateWithdrawalProcessed)
	summary, err = repo.EnsureUserAffiliate(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, summary.AffQuota)
}

func TestAffiliateWalletPostgresDirectRecords(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	repo, ok := NewAffiliateRepository(client, nil).(*affiliateRepository)
	require.True(t, ok)
	var ids []int64
	for _, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		u, err := client.User.Create().SetEmail(email).SetPasswordHash("test").SetRole("user").SetStatus("active").SetConcurrency(1).Save(ctx)
		require.NoError(t, err)
		ids = append(ids, u.ID)
	}
	for i := 1; i < 3; i++ {
		bound, err := repo.BindInviter(ctx, ids[i], ids[i-1])
		require.NoError(t, err)
		require.True(t, bound)
	}
	for i := 1; i < 3; i++ {
		amount := float64(1500 - i*500)
		now := time.Now()
		order, err := client.PaymentOrder.Create().SetUserID(ids[i]).SetUserEmail(fmt.Sprintf("user%d@example.com", i)).SetUserName("").SetAmount(amount * 1.1).SetPayAmount(amount).SetFeeRate(0).SetRechargeCode(fmt.Sprint(now.UnixNano())).SetOutTradeNo(fmt.Sprint(now.UnixNano())).SetPaymentType("alipay").SetPaymentTradeNo("").SetOrderType("balance").SetStatus("COMPLETED").SetPaidAt(now).SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
		require.NoError(t, err)
		applied, err := repo.AccrueQuota(ctx, ids[i-1], ids[i], amount*.1, 0, &order.ID)
		require.NoError(t, err)
		require.True(t, applied)
	}
	for _, kind := range []string{"invites", "recharges", "ledger"} {
		rows, total, err := repo.ListAffiliateWalletRecords(ctx, ids[0], kind, 1, 20)
		require.NoError(t, err, kind)
		require.EqualValues(t, 1, total, kind)
		require.Len(t, rows, 1)
		if kind == "recharges" {
			require.Equal(t, ids[1], rows[0].UserID)
			require.Equal(t, 1000.0, rows[0].PayAmount)
			require.Equal(t, 100.0, rows[0].RebateAmount)
		}
		next, total, err := repo.ListAffiliateWalletRecords(ctx, ids[0], kind, 2, 20)
		require.NoError(t, err)
		require.Empty(t, next)
		require.EqualValues(t, 1, total)
	}
	rows, total, err := repo.ListAffiliateWalletRecords(ctx, ids[1], "recharges", 1, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, ids[2], rows[0].UserID)
	require.Equal(t, 500.0, rows[0].PayAmount)
}

func TestAffiliateWalletPostgresWithdrawalRacesWithTransfer(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	repo, ok := NewAffiliateRepository(client, nil).(*affiliateRepository)
	require.True(t, ok)
	u, err := client.User.Create().SetEmail("race@example.com").SetPasswordHash("test").SetRole("user").SetStatus("active").SetConcurrency(1).Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=100,aff_history_quota=100 WHERE user_id=$1`, u.ID)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, e := repo.CreateAffiliateWithdrawal(ctx, u.ID, 100, 0, "test@alipay.com", "测试用户")
		results <- e
	}()
	go func() { <-start; _, _, e := repo.TransferQuotaToBalanceMinimum(ctx, u.ID, 0); results <- e }()
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			success++
		} else {
			require.True(t, e == service.ErrAffiliateQuotaEmpty || e == service.ErrAffiliateInsufficient, "%v", e)
		}
	}
	require.Equal(t, 1, success)
	summary, err := repo.EnsureUserAffiliate(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, summary.AffQuota)
	records, _, err := repo.ListAffiliateWalletRecords(ctx, u.ID, "withdrawals", 1, 20)
	require.NoError(t, err)
	reloaded, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	reserved := 0.0
	for _, record := range records {
		reserved += record.Amount
	}
	require.Equal(t, 100.0, reloaded.Balance+reserved)
}

func TestAffiliateWalletPostgresFreezeLifecycle(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	repo, ok := NewAffiliateRepository(client, nil).(*affiliateRepository)
	require.True(t, ok)
	settings := NewSettingRepository(client)
	for key, value := range map[string]string{
		service.SettingKeyAffiliateEnabled:           "true",
		service.SettingKeyAffiliateRebateFreezeHours: "168",
		service.SettingKeyAffiliateWithdrawMinimum:   "20",
		service.SettingKeyAffiliateTransferMinimum:   "20",
	} {
		require.NoError(t, settings.Set(ctx, key, value))
	}
	svc := service.NewAffiliateService(repo, service.NewSettingService(settings, nil), nil, nil)
	inviter, err := client.User.Create().SetEmail("freeze-inviter@example.com").SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	invitee, err := client.User.Create().SetEmail("freeze-invitee@example.com").SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	bound, err := repo.BindInviter(ctx, invitee.ID, inviter.ID)
	require.NoError(t, err)
	require.True(t, bound)
	var orderIDs []int64
	started := time.Now()
	for i, paid := range []float64{1000, 500} {
		order, err := client.PaymentOrder.Create().SetUserID(invitee.ID).SetUserEmail(invitee.Email).SetUserName("").SetAmount(paid).SetPayAmount(paid).SetFeeRate(0).SetRechargeCode(fmt.Sprintf("freeze-%d", i)).SetOutTradeNo(fmt.Sprintf("freeze-%d", i)).SetPaymentType("alipay").SetPaymentTradeNo("").SetOrderType("balance").SetStatus("COMPLETED").SetPaidAt(time.Now()).SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
		require.NoError(t, err)
		amount, err := svc.AccrueInviteRebateForOrder(ctx, invitee.ID, paid, &order.ID)
		require.NoError(t, err)
		require.Equal(t, paid*.1, amount)
		orderIDs = append(orderIDs, order.ID)
	}
	detail, err := svc.GetAffiliateDetail(ctx, inviter.ID)
	require.NoError(t, err)
	require.Zero(t, detail.AffQuota)
	require.Equal(t, 150.0, detail.AffFrozenQuota)
	require.Equal(t, 150.0, detail.AffHistoryQuota)
	require.Equal(t, 168, detail.RebateFreezeHours)
	require.NotNil(t, detail.NextThawAt)
	require.WithinDuration(t, started.Add(7*24*time.Hour), *detail.NextThawAt, 10*time.Second)
	firstThaw := *detail.NextThawAt
	for _, kind := range []string{"ledger", "recharges"} {
		rows, _, err := repo.ListAffiliateWalletRecords(ctx, inviter.ID, kind, 1, 20)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		for _, row := range rows {
			require.NotNil(t, row.FrozenUntil)
			require.True(t, row.FrozenUntil.After(time.Now()))
			if kind == "ledger" {
				require.Equal(t, "frozen", row.Status)
			}
		}
	}
	_, err = svc.RequestWithdrawal(ctx, inviter.ID, 40, "test@alipay.com", "测试用户")
	require.ErrorIs(t, err, service.ErrAffiliateInsufficient)
	_, _, err = svc.TransferAffiliateQuota(ctx, inviter.ID)
	require.ErrorIs(t, err, service.ErrAffiliateQuotaEmpty)
	// Changing policy never shortens the deadlines already stored on each entry.
	require.NoError(t, settings.Set(ctx, service.SettingKeyAffiliateRebateFreezeHours, "0"))
	detail, err = svc.GetAffiliateDetail(ctx, inviter.ID)
	require.NoError(t, err)
	require.Zero(t, detail.AffQuota)
	require.Equal(t, 150.0, detail.AffFrozenQuota)
	require.Equal(t, firstThaw, *detail.NextThawAt)
	// Simulate only the first entry reaching its deadline. A withdrawal thaws it
	// in the same transaction without requiring a prior visit to the wallet page.
	_, err = client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET frozen_until=NOW()-INTERVAL '1 second' WHERE source_order_id=$1`, orderIDs[0])
	require.NoError(t, err)
	_, err = svc.RequestWithdrawal(ctx, inviter.ID, 40, "test@alipay.com", "测试用户")
	require.NoError(t, err)
	transferred, balance, err := svc.TransferAffiliateQuota(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, 60.0, transferred)
	require.Equal(t, 60.0, balance)
	detail, err = svc.GetAffiliateDetail(ctx, inviter.ID)
	require.NoError(t, err)
	require.Zero(t, detail.AffQuota)
	require.Equal(t, 50.0, detail.AffFrozenQuota)
	// Closing the campaign also keeps unmatured funds frozen.
	require.NoError(t, settings.Set(ctx, service.SettingKeyAffiliateEnabled, "false"))
	_, _, err = svc.TransferAffiliateQuota(ctx, inviter.ID)
	require.ErrorIs(t, err, service.ErrAffiliateQuotaEmpty)
	_, err = client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET frozen_until=NOW()-INTERVAL '1 second' WHERE source_order_id=$1`, orderIDs[1])
	require.NoError(t, err)
	// Concurrent page reads release the last entry exactly once.
	results := make(chan *service.AffiliateDetail, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			d, e := svc.GetAffiliateDetail(ctx, inviter.ID)
			results <- d
			failures <- e
		}()
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, <-failures)
		d := <-results
		require.False(t, d.Enabled)
		require.Equal(t, 50.0, d.AffQuota)
		require.Zero(t, d.AffFrozenQuota)
		require.Nil(t, d.NextThawAt)
	}
	transferred, balance, err = svc.TransferAffiliateQuota(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, 50.0, transferred)
	require.Equal(t, 110.0, balance)
}

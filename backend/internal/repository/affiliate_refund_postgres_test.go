package repository

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func affiliateRefundFixture(t *testing.T, client *dbent.Client, name string) (*affiliateRepository, *dbent.User, *dbent.User, *dbent.PaymentOrder) {
	t.Helper()
	ctx := context.Background()
	repo, ok := NewAffiliateRepository(client, nil).(*affiliateRepository)
	require.True(t, ok)
	inviter, err := client.User.Create().SetEmail(name + "-inviter@example.com").SetPasswordHash("test").Save(ctx)
	require.NoError(t, err)
	invitee, err := client.User.Create().SetEmail(name + "-invitee@example.com").SetPasswordHash("test").SetBalance(1100).Save(ctx)
	require.NoError(t, err)
	_, err = repo.BindInviter(ctx, invitee.ID, inviter.ID)
	require.NoError(t, err)
	now := time.Now()
	order, err := client.PaymentOrder.Create().SetUserID(invitee.ID).SetUserEmail(invitee.Email).SetUserName("").SetAmount(1000).SetPayAmount(900).SetFeeRate(0).SetRechargeCode(name).SetOutTradeNo(name).SetPaymentType("alipay").SetPaymentTradeNo("").SetOrderType("balance").SetStatus(service.OrderStatusCompleted).SetPaidAt(now).SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(ctx)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `INSERT INTO affiliate_first_recharges(user_id,order_id,bonus_amount) VALUES($1,$2,100)`, invitee.ID, order.ID)
	require.NoError(t, err)
	return repo, inviter, invitee, order
}

func TestAffiliateRefundPostgresReversalAndRecovery(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		freeze        int
		spend, refund float64
	}{
		{"frozen_full", 168, 0, 1000}, {"frozen_partial", 168, 0, 400},
		{"available_full", 0, 0, 1000}, {"available_partial", 0, 0, 400},
		{"transferred_debt", 0, 90, 1000}, {"withdrawn_partial_debt", 0, 70, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, inviter, invitee, order := affiliateRefundFixture(t, client, tc.name)
			applied, err := repo.AccrueQuota(ctx, inviter.ID, invitee.ID, 90, tc.freeze, &order.ID)
			require.NoError(t, err)
			require.True(t, applied)
			if tc.spend == 90 {
				moved, _, err := repo.TransferQuotaToBalanceMinimum(ctx, inviter.ID, 0)
				require.NoError(t, err)
				require.Equal(t, 90.0, moved)
			} else if tc.spend > 0 {
				w, err := repo.CreateAffiliateWithdrawal(ctx, inviter.ID, tc.spend, 0, "alipay", "测试")
				require.NoError(t, err)
				require.NoError(t, repo.ProcessAffiliateWithdrawal(ctx, w.ID, inviter.ID, "paid", "paid"))
			}
			// Campaign state is deliberately irrelevant to successful refund recovery.
			adjustment, err := repo.ReverseOrderRewards(ctx, order.ID, tc.refund, 1000)
			require.NoError(t, err)
			revoked := 90 * tc.refund / 1000
			require.InDelta(t, revoked, adjustment.RebateRevoked, 1e-8)
			require.InDelta(t, tc.refund/10, adjustment.BonusRevoked, 1e-8)
			again, err := repo.ReverseOrderRewards(ctx, order.ID, tc.refund, 1000)
			require.NoError(t, err)
			require.Equal(t, &service.AffiliateRefundAdjustment{}, again)
			u, err := client.User.Get(ctx, invitee.ID)
			require.NoError(t, err)
			require.InDelta(t, 1100-tc.refund/10, u.Balance, 1e-8)
			if tc.freeze > 0 {
				require.Equal(t, revoked, adjustment.FrozenRevoked)
				_, err = client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET frozen_until=NOW()-INTERVAL '1 second' WHERE source_order_id=$1 AND action='accrue'`, order.ID)
				require.NoError(t, err)
				thawed, err := repo.ThawFrozenQuota(ctx, inviter.ID)
				require.NoError(t, err)
				require.InDelta(t, 90-revoked, thawed, 1e-8)
			}
			summary, err := repo.EnsureUserAffiliate(ctx, inviter.ID)
			require.NoError(t, err)
			net := 90 - tc.spend - revoked
			if net < 0 {
				require.Zero(t, summary.AffQuota)
				require.InDelta(t, -net, summary.AffDebtQuota, 1e-8)
				require.InDelta(t, -net, adjustment.DebtIncreased, 1e-8)
				// New available and matured frozen rebates repay debt before spending.
				_, err = repo.AccrueQuota(ctx, inviter.ID, invitee.ID, 10, 0, nil)
				require.NoError(t, err)
				_, err = repo.AccrueQuota(ctx, inviter.ID, invitee.ID, 100, 168, nil)
				require.NoError(t, err)
				_, err = client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET frozen_until=NOW()-INTERVAL '1 second' WHERE user_id=$1 AND frozen_until IS NOT NULL`, inviter.ID)
				require.NoError(t, err)
				_, err = repo.ThawFrozenQuota(ctx, inviter.ID)
				require.NoError(t, err)
				summary, err = repo.EnsureUserAffiliate(ctx, inviter.ID)
				require.NoError(t, err)
				require.InDelta(t, 110+net, summary.AffQuota, 1e-8)
				require.Zero(t, summary.AffDebtQuota)
			} else {
				require.InDelta(t, net, summary.AffQuota, 1e-8)
				require.Zero(t, summary.AffDebtQuota)
			}
			records, _, err := repo.ListAffiliateWalletRecords(ctx, inviter.ID, "recharges", 1, 20)
			require.NoError(t, err)
			require.Len(t, records, 1)
			require.InDelta(t, 90-revoked, records[0].RebateAmount, 1e-8)
			require.Equal(t, revoked, records[0].ReversedAmount)
			total, err := repo.GetAccruedRebateFromInvitee(ctx, inviter.ID, invitee.ID)
			require.NoError(t, err)
			if net >= 0 {
				require.InDelta(t, 90-revoked, total, 1e-8)
			}
		})
	}
}

func TestAffiliateRefundPostgresSuccessfulSettlementIsAtomicAndRecoverable(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	repo, inviter, invitee, order := affiliateRefundFixture(t, client, "settlement")
	_, err := repo.AccrueQuota(ctx, inviter.ID, invitee.ID, 90, 168, &order.ID)
	require.NoError(t, err)
	affiliate := service.NewAffiliateService(repo, service.NewSettingService(NewSettingRepository(client), nil), nil, nil)
	payments := service.NewPaymentService(client, nil, nil, nil, nil, nil, NewUserRepository(client, nil), nil, affiliate)
	// Force an error after gateway success. All local order/rebate/bonus changes
	// must roll back together; the initial principal deduction is already held.
	_, err = client.ExecContext(ctx, `CREATE FUNCTION fail_refund_revoke() RETURNS trigger AS $$ BEGIN IF NEW.action='refund_revoke' THEN RAISE EXCEPTION 'injected settlement failure'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql; CREATE TRIGGER fail_refund_revoke BEFORE INSERT ON user_affiliate_ledger FOR EACH ROW EXECUTE FUNCTION fail_refund_revoke()`)
	require.NoError(t, err)
	plan := &service.RefundPlan{OrderID: order.ID, Order: order, RefundAmount: 1000, GatewayAmount: 900, Reason: "test", DeductBalance: true, DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 1000}
	result, err := payments.ExecuteRefund(ctx, plan)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Warning, "待重试")
	current, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusRefundPending, current.Status)
	summary, err := repo.EnsureUserAffiliate(ctx, inviter.ID)
	require.NoError(t, err)
	require.Equal(t, 90.0, summary.AffFrozenQuota)
	u, err := client.User.Get(ctx, invitee.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, u.Balance)
	_, _, err = payments.PrepareRefund(ctx, order.ID, 1000, "again", false, true)
	require.Error(t, err)
	_, err = repo.CreateAffiliateWithdrawal(ctx, inviter.ID, 1, 0, "alipay", "姓名")
	require.ErrorIs(t, err, service.ErrAffiliateRefundPending)
	_, _, err = repo.TransferQuotaToBalanceMinimum(ctx, inviter.ID, 0)
	require.ErrorIs(t, err, service.ErrAffiliateRefundPending)
	_, err = client.ExecContext(ctx, `DROP TRIGGER fail_refund_revoke ON user_affiliate_ledger`)
	require.NoError(t, err)
	// No provider exists: this proves the retry settles locally without another
	// gateway call, and without deducting the principal twice.
	result, err = payments.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1000.0, result.BalanceDeducted)
	require.Equal(t, 90.0, result.AffiliateAdjustment.RebateRevoked)
	require.Equal(t, 100.0, result.AffiliateAdjustment.BonusRevoked)
	u, err = client.User.Get(ctx, invitee.ID)
	require.NoError(t, err)
	require.Zero(t, u.Balance)
	summary, err = repo.EnsureUserAffiliate(ctx, inviter.ID)
	require.NoError(t, err)
	require.Zero(t, summary.AffFrozenQuota)
	current, err = client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, service.OrderStatusRefunded, current.Status)
	_, err = payments.QueryAndFinalizeRefund(ctx, order.ID)
	require.Error(t, err)
	count, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestAffiliateRefundPostgresConcurrentThawTransferAndReversal(t *testing.T) {
	client := affiliateWalletTestClient(t)
	ctx := context.Background()
	repo, inviter, invitee, order := affiliateRefundFixture(t, client, "concurrent")
	_, err := repo.AccrueQuota(ctx, inviter.ID, invitee.ID, 90, 168, &order.ID)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET frozen_until=NOW()-INTERVAL '1 second' WHERE source_order_id=$1`, order.ID)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i {
			case 0:
				_, _, err := repo.TransferQuotaToBalanceMinimum(ctx, inviter.ID, 0)
				if err != nil && err != service.ErrAffiliateQuotaEmpty {
					errs <- err
				}
			case 1:
				_, err := repo.ThawFrozenQuota(ctx, inviter.ID)
				errs <- err
			default:
				_, err := repo.ReverseOrderRewards(ctx, order.ID, 1000, 1000)
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	summary, err := repo.EnsureUserAffiliate(ctx, inviter.ID)
	require.NoError(t, err)
	require.Zero(t, summary.AffQuota)
	require.Zero(t, summary.AffFrozenQuota)
	require.Zero(t, summary.AffHistoryQuota)
	u, err := client.User.Get(ctx, inviter.ID)
	require.NoError(t, err)
	require.InDelta(t, u.Balance, summary.AffDebtQuota, 1e-8)
	rows, err := client.QueryContext(ctx, `SELECT COUNT(*) FROM user_affiliate_ledger WHERE source_order_id=$1 AND action='refund_revoke'`, order.ID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next())
	var count int
	require.NoError(t, rows.Scan(&count))
	require.Equal(t, 1, count, fmt.Sprint(summary))
}

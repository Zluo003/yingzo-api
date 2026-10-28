//go:build unit

package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func affiliateBonusTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	dsn := os.Getenv("AFFILIATE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set AFFILIATE_TEST_POSTGRES_DSN to test first-recharge transactions")
	}
	ctx := context.Background()
	base, err := dbent.Open(dialect.Postgres, dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("affiliate_bonus_%d", time.Now().UnixNano())
	_, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = base.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); _ = base.Close() })
	client, err := dbent.Open(dialect.Postgres, dsn+" search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	_, err = client.ExecContext(ctx, `CREATE TABLE user_affiliates (user_id bigint PRIMARY KEY,inviter_id bigint); CREATE TABLE affiliate_first_recharges (user_id bigint PRIMARY KEY,order_id bigint UNIQUE,bonus_amount numeric(20,8) NOT NULL)`)
	require.NoError(t, err)
	return client
}
func TestAffiliateFirstRechargeBonusPostgres(t *testing.T) {
	client := affiliateBonusTestClient(t)
	ctx := context.Background()
	settings := &paymentFulfillmentSettingRepoStub{values: map[string]string{SettingKeyAffiliateEnabled: "true"}}
	svc := &PaymentService{entClient: client, affiliateService: NewAffiliateService(nil, NewSettingService(settings, nil), nil, nil)}
	for _, tc := range []struct {
		name             string
		invited, enabled bool
		want             float64
	}{{"invited", true, true, 100}, {"no_inviter", false, true, 0}, {"disabled", true, false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			settings.values[SettingKeyAffiliateEnabled] = fmt.Sprint(tc.enabled)
			u, err := client.User.Create().SetEmail(tc.name + "@example.com").SetPasswordHash("test").SetRole("user").SetStatus("active").SetConcurrency(1).Save(ctx)
			require.NoError(t, err)
			if tc.invited {
				_, err = client.ExecContext(ctx, `INSERT INTO user_affiliates (user_id,inviter_id) VALUES ($1,999)`, u.ID)
				require.NoError(t, err)
			}
			create := func(amount float64) *dbent.PaymentOrder {
				now := time.Now()
				o, e := client.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName("").SetAmount(amount).SetPayAmount(amount).SetFeeRate(0).SetRechargeCode(fmt.Sprint(now.UnixNano())).SetOutTradeNo(fmt.Sprint(now.UnixNano())).SetPaymentType("alipay").SetPaymentTradeNo("").SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPaid).SetPaidAt(now).SetExpiresAt(now.Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("localhost").SetProviderSnapshot(map[string]any{"affiliate_first_recharge_bonus_percent": float64(10)}).Save(ctx)
				require.NoError(t, e)
				return o
			}
			first := create(1000)
			second := create(500)
			// A later payment may finish fulfillment before the earlier payment.
			require.NoError(t, svc.applyAffiliateFirstRechargeBonus(ctx, second))
			var wg sync.WaitGroup
			failures := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); failures <- svc.applyAffiliateFirstRechargeBonus(ctx, first) }()
			}
			wg.Wait()
			close(failures)
			for e := range failures {
				require.NoError(t, e)
			}
			require.NoError(t, svc.applyAffiliateFirstRechargeBonus(ctx, first))
			// Enabling later never retroactively awards the first bonus or rewards a second order.
			settings.values[SettingKeyAffiliateEnabled] = "true"
			require.NoError(t, svc.applyAffiliateFirstRechargeBonus(ctx, create(500)))
			reloaded, err := client.User.Get(ctx, u.ID)
			require.NoError(t, err)
			require.InDelta(t, tc.want, reloaded.Balance, 1e-8)
		})
	}
}

func TestAffiliateRebateUsesActualPaymentAndOnlyDirectInviter(t *testing.T) {
	ctx := context.Background()
	a, b := int64(1), int64(2)
	repo := &paymentFulfillmentAffiliateRepoStub{inviteeSummary: &AffiliateSummary{UserID: 3, InviterID: &b}, inviterSummary: &AffiliateSummary{UserID: b, InviterID: &a}}
	settings := NewSettingService(&paymentFulfillmentSettingRepoStub{values: map[string]string{SettingKeyAffiliateEnabled: "true", SettingKeyAffiliateRebateRate: "10"}}, nil)
	svc := NewAffiliateService(repo, settings, nil, nil)
	order := &dbent.PaymentOrder{OrderType: payment.OrderTypeBalance, Amount: 1100, PayAmount: 1000}
	amount, err := svc.AccrueInviteRebate(ctx, 3, affiliateRebateBaseAmount(order))
	require.NoError(t, err)
	require.Equal(t, 100.0, amount)
	require.Len(t, repo.accrueCalls, 1)
	require.Equal(t, b, repo.accrueCalls[0].inviterID)
}

type affiliateWalletServiceStub struct {
	*paymentFulfillmentAffiliateRepoStub
	minimum     float64
	withdrawals int
}

func (r *affiliateWalletServiceStub) TransferQuotaToBalanceMinimum(_ context.Context, _ int64, min float64) (float64, float64, error) {
	r.minimum = min
	return 50, 50, nil
}
func (r *affiliateWalletServiceStub) CreateAffiliateWithdrawal(_ context.Context, _ int64, amount, min float64, account, name string) (*AffiliateWalletRecord, error) {
	r.minimum = min
	r.withdrawals++
	return &AffiliateWalletRecord{Amount: amount, AlipayAccount: account, AlipayName: name}, nil
}
func (r *affiliateWalletServiceStub) ProcessAffiliateWithdrawal(context.Context, int64, int64, string, string) error {
	return nil
}
func (r *affiliateWalletServiceStub) ListAffiliateWalletRecords(context.Context, int64, string, int, int) ([]AffiliateWalletRecord, int64, error) {
	return nil, 0, nil
}
func TestAffiliateWalletThresholdsAndDisabledCampaign(t *testing.T) {
	ctx := context.Background()
	repo := &affiliateWalletServiceStub{paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{}}
	settings := &paymentFulfillmentSettingRepoStub{values: map[string]string{SettingKeyAffiliateEnabled: "true", SettingKeyAffiliateWithdrawMinimum: "100", SettingKeyAffiliateTransferMinimum: "50"}}
	svc := NewAffiliateService(repo, NewSettingService(settings, nil), nil, nil)
	_, err := svc.RequestWithdrawal(ctx, 1, 99.99, "test@alipay.com", "张三")
	require.ErrorIs(t, err, ErrAffiliateMinimum)
	require.Zero(t, repo.withdrawals)
	_, err = svc.RequestWithdrawal(ctx, 1, 100.001, "test@alipay.com", "张三")
	require.Error(t, err)
	_, err = svc.RequestWithdrawal(ctx, 1, 100, " ", "张三")
	require.Error(t, err)
	record, err := svc.RequestWithdrawal(ctx, 1, 100, " test@alipay.com ", " 张三 ")
	require.NoError(t, err)
	require.Equal(t, "test@alipay.com", record.AlipayAccount)
	require.Equal(t, "张三", record.AlipayName)
	require.Equal(t, 100.0, repo.minimum)
	settings.values[SettingKeyAffiliateEnabled] = "false"
	_, err = svc.RequestWithdrawal(ctx, 1, 100, "test@alipay.com", "张三")
	require.Error(t, err)
	require.Equal(t, 1, repo.withdrawals)
	amount, _, err := svc.TransferAffiliateQuota(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 50.0, amount)
	require.Equal(t, 50.0, repo.minimum)
}

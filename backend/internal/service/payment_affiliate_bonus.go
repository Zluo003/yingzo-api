package service

import (
	"context"
	"fmt"
	"strconv"

	"entgo.io/ent/dialect"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// Redemption is already idempotent. The separate bonus claim and balance credit
// are atomic, so webhook retries and concurrent recharges cannot award twice.
func (s *PaymentService) applyAffiliateFirstRechargeBonus(ctx context.Context, o *dbent.PaymentOrder) error {
	if s.affiliateService == nil || o.OrderType != payment.OrderTypeBalance {
		return nil
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	allowed, err := lockAffiliateRewardOrder(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	// Lock the user to serialize different orders' bonus claims.
	rows, err := client.QueryContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, o.UserID)
	if err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	// Historical paid/refunded orders consume first-recharge eligibility too.
	rows, err = client.QueryContext(ctx, `SELECT id FROM payment_orders WHERE user_id=$1 AND order_type='balance' AND (paid_at IS NOT NULL OR status IN ('COMPLETED','REFUNDED','PARTIALLY_REFUNDED')) ORDER BY COALESCE(paid_at,created_at),id LIMIT 1`, o.UserID)
	if err != nil {
		return err
	}
	var firstID int64
	if rows.Next() {
		err = rows.Scan(&firstID)
	}
	if closeErr := rows.Close(); err != nil {
		return err
	} else if closeErr != nil {
		return closeErr
	}
	if firstID != o.ID {
		return nil
	}
	rate := 0.0
	if s.affiliateService.IsEnabled(ctx) {
		// Snapshot at order creation prevents later settings changes changing a quote.
		if raw, ok := o.ProviderSnapshot["affiliate_first_recharge_bonus_percent"].(float64); ok {
			rate = raw
		}
	}
	rows, err = client.QueryContext(ctx, `SELECT inviter_id FROM user_affiliates WHERE user_id=$1 AND inviter_id IS NOT NULL`, o.UserID)
	if err != nil {
		return err
	}
	invited := rows.Next()
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if !invited {
		rate = 0
	}
	if rate < 0 || rate > 100 {
		rate = 0
	}
	bonus := roundTo(o.Amount*rate/100, 8)
	res, err := client.ExecContext(ctx, `INSERT INTO affiliate_first_recharges (user_id,order_id,bonus_amount) VALUES ($1,$2,$3) ON CONFLICT (user_id) DO NOTHING`, o.UserID, o.ID, bonus)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if bonus > 0 {
		if _, err = client.User.Update().Where(user.IDEQ(o.UserID)).AddBalance(bonus).Save(ctx); err != nil {
			return err
		}
	}
	_, err = client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("AFFILIATE_FIRST_RECHARGE_BONUS").SetOperator("system").SetDetail(fmt.Sprintf(`{"bonus_amount":%g,"rate_percent":%g}`, bonus, rate)).Save(ctx)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if bonus > 0 {
		s.affiliateService.invalidateAffiliateCaches(ctx, o.UserID)
	}
	return nil
}

// Refund settlement and reward fulfillment serialize on the original order.
// A late/retried fulfillment must never recreate rewards after a refund.
func lockAffiliateRewardOrder(ctx context.Context, client *dbent.Client, orderID int64) (bool, error) {
	query := client.PaymentOrder.Query().Where(paymentorder.IDEQ(orderID))
	if paymentAuditDialect(client) == dialect.Postgres {
		query = query.ForUpdate()
	}
	order, err := query.Only(ctx)
	if err != nil {
		return false, err
	}
	return order.Status == OrderStatusPaid || order.Status == OrderStatusRecharging || order.Status == OrderStatusCompleted, nil
}

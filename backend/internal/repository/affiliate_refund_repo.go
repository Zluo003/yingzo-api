package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AffiliateRefundRepository = (*affiliateRepository)(nil)

// Called in the same transaction as the successful refund's order state and
// audit. Targets are cumulative, so retrying a settlement cannot revoke twice.
func (r *affiliateRepository) ReverseOrderRewards(ctx context.Context, orderID int64, refundAmount, orderAmount float64) (*service.AffiliateRefundAdjustment, error) {
	if orderAmount <= 0 || math.IsNaN(orderAmount) || math.IsInf(orderAmount, 0) || refundAmount <= 0 || math.IsNaN(refundAmount) || math.IsInf(refundAmount, 0) {
		return nil, fmt.Errorf("invalid affiliate refund amounts")
	}
	ratio := math.Min(1, refundAmount/orderAmount)
	result := &service.AffiliateRefundAdjustment{}
	err := r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		// Use the recorded recipient, never the invitee's current inviter.
		rows, err := client.QueryContext(ctx, `SELECT DISTINCT user_id FROM user_affiliate_ledger WHERE source_order_id=$1 AND action='accrue' ORDER BY user_id`, orderID)
		if err != nil {
			return err
		}
		var recipients []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			recipients = append(recipients, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, userID := range recipients {
			// Same lock order as thaw, withdrawal and transfer: wallet, then ledger.
			locks, err := client.QueryContext(ctx, `SELECT user_id FROM user_affiliates WHERE user_id=$1 FOR UPDATE`, userID)
			if err != nil {
				return err
			}
			if err = locks.Close(); err != nil {
				return err
			}
			rows, err := client.QueryContext(ctx, `SELECT id,amount::double precision,reversed_amount::double precision,ROUND(amount*$3::numeric,8)::double precision,frozen_until FROM user_affiliate_ledger WHERE user_id=$1 AND source_order_id=$2 AND action='accrue' ORDER BY id FOR UPDATE`, userID, orderID, ratio)
			if err != nil {
				return err
			}
			type accrual struct {
				id                       int64
				amount, reversed, target float64
				frozen                   sql.NullTime
			}
			var accruals []accrual
			for rows.Next() {
				var entry accrual
				if err := rows.Scan(&entry.id, &entry.amount, &entry.reversed, &entry.target, &entry.frozen); err != nil {
					_ = rows.Close()
					return err
				}
				accruals = append(accruals, entry)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
			for _, entry := range accruals {
				delta := math.Max(0, entry.target-entry.reversed)
				if delta < 0.000000005 {
					continue
				}
				frozen, available := 0.0, 0.0
				// Non-null means the entry has not been thawed, even if its
				// deadline passed while the refund was being processed.
				if entry.frozen.Valid {
					frozen = delta
				} else {
					available = delta
				}
				rows, err := client.QueryContext(ctx, `UPDATE user_affiliates SET aff_frozen_quota=GREATEST(0,aff_frozen_quota-$2),aff_quota=aff_quota-$3,aff_history_quota=GREATEST(0,aff_history_quota-$4),updated_at=NOW() WHERE user_id=$1 RETURNING GREATEST(0,aff_quota+$3)::double precision`, userID, frozen, available, delta)
				if err != nil {
					return err
				}
				var beforeAvailable float64
				if !rows.Next() {
					_ = rows.Close()
					return service.ErrUserNotFound
				}
				if err := rows.Scan(&beforeAvailable); err != nil {
					_ = rows.Close()
					return err
				}
				if err := rows.Close(); err != nil {
					return err
				}
				if _, err := client.ExecContext(ctx, `UPDATE user_affiliate_ledger SET reversed_amount=$2,updated_at=NOW() WHERE id=$1`, entry.id, entry.target); err != nil {
					return err
				}
				if _, err := client.ExecContext(ctx, `INSERT INTO user_affiliate_ledger (user_id,action,amount,source_order_id,source_user_id) SELECT user_id,'refund_revoke',$2,source_order_id,source_user_id FROM user_affiliate_ledger WHERE id=$1`, entry.id, delta); err != nil {
					return err
				}
				result.RebateRevoked += delta
				result.FrozenRevoked += frozen
				result.AvailableRevoked += math.Min(available, beforeAvailable)
				result.DebtIncreased += math.Max(0, available-beforeAvailable)
			}
		}
		// Lock the claim before computing the delta. No audit lookup is needed:
		// reversed_amount is the cumulative idempotency marker.
		rows, err = client.QueryContext(ctx, `SELECT user_id,reversed_amount::double precision,ROUND(bonus_amount*$2::numeric,8)::double precision FROM affiliate_first_recharges WHERE order_id=$1 FOR UPDATE`, orderID, ratio)
		if err != nil {
			return err
		}
		var bonusUserID int64
		var previous, target float64
		if rows.Next() {
			if err := rows.Scan(&bonusUserID, &previous, &target); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		bonus := math.Max(0, target-previous)
		if bonus > 0 {
			if _, err := client.ExecContext(ctx, `UPDATE affiliate_first_recharges SET reversed_amount=$2 WHERE order_id=$1`, orderID, target); err != nil {
				return err
			}
			if _, err := client.ExecContext(ctx, `UPDATE users SET balance=balance-$2,updated_at=NOW() WHERE id=$1`, bonusUserID, bonus); err != nil {
				return err
			}
			result.BonusRevoked = bonus
		}
		return nil
	})
	return result, err
}

func checkAffiliateRefundPending(ctx context.Context, client *dbent.Client, userID int64) error {
	rows, err := client.QueryContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_affiliate_ledger l JOIN payment_orders p ON p.id=l.source_order_id WHERE l.user_id=$1 AND l.action='accrue' AND l.amount>l.reversed_amount AND p.status IN ('REFUNDING','REFUND_PENDING'))`, userID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var pending bool
	if rows.Next() {
		if err := rows.Scan(&pending); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if pending {
		return service.ErrAffiliateRefundPending
	}
	return nil
}

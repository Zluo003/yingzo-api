package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AffiliateWalletRepository = (*affiliateRepository)(nil)

func (r *affiliateRepository) CreateAffiliateWithdrawal(ctx context.Context, userID int64, amount, minimum float64, account, name string) (*service.AffiliateWalletRecord, error) {
	var record service.AffiliateWalletRecord
	err := r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		if amount < minimum {
			return service.ErrAffiliateMinimum
		}
		if _, err := ensureUserAffiliateWithClient(ctx, client, userID); err != nil {
			return err
		}
		// Serialize withdrawals and transfers on the same wallet row, including thaw.
		rows, err := client.QueryContext(ctx, `SELECT user_id FROM user_affiliates WHERE user_id=$1 FOR UPDATE`, userID)
		if err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if err := checkAffiliateRefundPending(ctx, client, userID); err != nil {
			return err
		}
		if _, err = thawFrozenQuotaTx(ctx, client, userID); err != nil {
			return err
		}
		result, err := client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=aff_quota-$2, updated_at=NOW() WHERE user_id=$1 AND aff_quota >= $2`, userID, amount)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return service.ErrAffiliateInsufficient
		}
		rows, err = client.QueryContext(ctx, `INSERT INTO affiliate_withdrawals (user_id,amount,alipay_account,alipay_name) VALUES ($1,$2,$3,$4) RETURNING id,created_at`, userID, amount, account, name)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		if !rows.Next() {
			return fmt.Errorf("withdrawal insert returned no row")
		}
		if err = rows.Scan(&record.ID, &record.CreatedAt); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		_, err = client.ExecContext(ctx, `INSERT INTO user_affiliate_ledger (user_id,action,amount) VALUES ($1,'withdrawal_hold',$2)`, userID, amount)
		return err
	})
	record.UserID, record.Amount, record.AlipayAccount, record.AlipayName, record.Status = userID, amount, account, name, "pending"
	return &record, err
}

func (r *affiliateRepository) ProcessAffiliateWithdrawal(ctx context.Context, id, adminID int64, status, note string) error {
	return r.withTx(ctx, func(ctx context.Context, client *dbent.Client) error {
		rows, err := client.QueryContext(ctx, `UPDATE affiliate_withdrawals SET status=$2,admin_note=$3,processed_by=$4,processed_at=NOW() WHERE id=$1 AND status='pending' RETURNING user_id,amount`, id, status, note, adminID)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var userID int64
		var amount float64
		if !rows.Next() {
			return service.ErrAffiliateWithdrawalProcessed
		}
		if err = rows.Scan(&userID, &amount); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		action := "withdrawal_paid"
		if status == "rejected" {
			action = "withdrawal_release"
			if _, err = client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=aff_quota+$2,updated_at=NOW() WHERE user_id=$1`, userID, amount); err != nil {
				return err
			}
		}
		_, err = client.ExecContext(ctx, `INSERT INTO user_affiliate_ledger (user_id,action,amount) VALUES ($1,$2,$3)`, userID, action, amount)
		return err
	})
}

func (r *affiliateRepository) ListAffiliateWalletRecords(ctx context.Context, userID int64, kind string, page, size int) ([]service.AffiliateWalletRecord, int64, error) {
	client := clientFromContext(ctx, r.client)
	var from, fields string
	switch kind {
	case "withdrawals":
		fields = `w.id,w.user_id,u.email,w.amount,0,0,'withdrawal',w.status,w.alipay_account,w.alipay_name,w.admin_note,w.created_at,w.processed_at,NULL::timestamptz,0,NULL::bigint`
		from = `FROM affiliate_withdrawals w JOIN users u ON u.id=w.user_id WHERE ($1::bigint=0 OR w.user_id=$1)`
	case "recharges":
		fields = `p.id,p.user_id,u.email,p.amount,p.pay_amount,COALESCE((SELECT SUM(l.amount-l.reversed_amount) FROM user_affiliate_ledger l WHERE l.user_id=$1 AND l.source_order_id=p.id AND l.action='accrue'),0),'recharge',p.status,'','','',p.created_at,NULL::timestamptz,(SELECT MAX(l.frozen_until) FROM user_affiliate_ledger l WHERE l.user_id=$1 AND l.source_order_id=p.id AND l.action='accrue'),COALESCE((SELECT SUM(l.reversed_amount) FROM user_affiliate_ledger l WHERE l.user_id=$1 AND l.source_order_id=p.id AND l.action='accrue'),0),p.id`
		from = `FROM payment_orders p JOIN users u ON u.id=p.user_id JOIN user_affiliates a ON a.user_id=p.user_id WHERE a.inviter_id=$1 AND p.order_type='balance' AND (p.paid_at IS NOT NULL OR p.status IN ('COMPLETED','REFUNDED','PARTIALLY_REFUNDED'))`
	case "invites":
		fields = `a.user_id,a.user_id,u.email,0,0,COALESCE((SELECT SUM(l.amount-l.reversed_amount) FROM user_affiliate_ledger l WHERE l.user_id=$1 AND l.source_user_id=a.user_id AND l.action='accrue'),0),'invite','','','','',u.created_at,NULL::timestamptz,NULL::timestamptz,0,NULL::bigint`
		from = `FROM user_affiliates a JOIN users u ON u.id=a.user_id WHERE a.inviter_id=$1`
	default:
		fields = `l.id,l.user_id,COALESCE(u.email,''),l.amount,0,0,l.action,CASE WHEN l.action='accrue' THEN CASE WHEN l.reversed_amount>=l.amount THEN 'revoked' WHEN l.frozen_until IS NOT NULL THEN 'frozen' ELSE 'available' END ELSE '' END,'','','',l.created_at,NULL::timestamptz,l.frozen_until,l.reversed_amount,l.source_order_id`
		from = `FROM user_affiliate_ledger l LEFT JOIN users u ON u.id=l.source_user_id WHERE l.user_id=$1`
	}
	rows, err := client.QueryContext(ctx, "SELECT COUNT(*) "+from, userID)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	if rows.Next() {
		err = rows.Scan(&total)
	}
	closeErr := rows.Close()
	if err != nil {
		return nil, 0, err
	}
	if closeErr != nil {
		return nil, 0, closeErr
	}
	rows, err = client.QueryContext(ctx, "SELECT "+fields+" "+from+" ORDER BY 1 DESC LIMIT $2 OFFSET $3", userID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.AffiliateWalletRecord, 0)
	for rows.Next() {
		var v service.AffiliateWalletRecord
		if err = rows.Scan(&v.ID, &v.UserID, &v.Email, &v.Amount, &v.PayAmount, &v.RebateAmount, &v.Action, &v.Status, &v.AlipayAccount, &v.AlipayName, &v.AdminNote, &v.CreatedAt, &v.ProcessedAt, &v.FrozenUntil, &v.ReversedAmount, &v.SourceOrderID); err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}

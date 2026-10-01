package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type durableMusicLedger struct{ db *sql.DB }

func NewMusicTaskLedger(db *sql.DB) service.MusicTaskLedger { return &durableMusicLedger{db: db} }

const musicTaskColumns = `record,quote,COALESCE(idempotency_key,''),fingerprint,encrypted_request,COALESCE(encrypted_result,''),captured_usage,COALESCE(lease_token,''),phase`

func scanDurableMusic(row interface{ Scan(...any) error }) (*service.DurableMusicTask, error) {
	var r, q, u []byte
	var task service.DurableMusicTask
	var phase string
	if err := row.Scan(&r, &q, &task.IdempotencyKey, &task.Fingerprint, &task.EncryptedRequest, &task.EncryptedResult, &u, &task.LeaseToken, &phase); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrMusicTaskNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(r, &task.MusicTaskRecord); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(q, &task.Quote); err != nil {
		return nil, err
	}
	if len(u) > 0 {
		if err := json.Unmarshal(u, &task.CapturedUsage); err != nil {
			return nil, err
		}
	}
	task.Phase = phase
	return &task, nil
}
func (r *durableMusicLedger) Find(ctx context.Context, owner service.MusicTaskOwner, key string, idempotency bool) (*service.DurableMusicTask, error) {
	field := "id"
	if idempotency {
		field = "idempotency_key"
	}
	return scanDurableMusic(r.db.QueryRowContext(ctx, `SELECT `+musicTaskColumns+` FROM music_tasks WHERE user_id=$1 AND api_key_id=$2 AND `+field+`=$3`, owner.UserID, owner.APIKeyID, key))
}
func (r *durableMusicLedger) Accept(ctx context.Context, t *service.DurableMusicTask) (*service.DurableMusicTask, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, _ := json.Marshal(t.MusicTaskRecord)
	quote, _ := json.Marshal(t.Quote)
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO music_tasks(id,user_id,api_key_id,idempotency_key,fingerprint,record,quote,encrypted_request,deadline) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9) ON CONFLICT(api_key_id,idempotency_key) DO NOTHING RETURNING id`, t.ID, t.UserID, t.APIKeyID, t.IdempotencyKey, t.Fingerprint, record, quote, t.EncryptedRequest, time.Unix(t.DeadlineAt, 0)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		old, err := scanDurableMusic(tx.QueryRowContext(ctx, `SELECT `+musicTaskColumns+` FROM music_tasks WHERE api_key_id=$1 AND idempotency_key=$2`, t.APIKeyID, t.IdempotencyKey))
		if err != nil {
			return nil, false, err
		}
		if old.Fingerprint != t.Fingerprint {
			return nil, false, service.ErrMusicIdempotencyConflict
		}
		return old, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	cmd := t.Quote.Charge
	// Admission is stricter than postpaid settlement: never send an unaffordable job.
	var balance float64
	if err := tx.QueryRowContext(ctx, `SELECT balance-frozen_balance FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, t.UserID).Scan(&balance); err != nil {
		return nil, false, err
	}
	if cmd.BalanceCost > 0 && cmd.BalanceCost > balance {
		return nil, false, service.ErrInsufficientBalance
	}
	var quota, used float64
	var rateAllowed bool
	if err := tx.QueryRowContext(ctx, `SELECT quota,quota_used,
 (rate_limit_5h<=0 OR (CASE WHEN window_5h_start+INTERVAL '5 hours'<=NOW() THEN 0 ELSE usage_5h END)+$2<=rate_limit_5h) AND
 (rate_limit_1d<=0 OR (CASE WHEN window_1d_start+INTERVAL '24 hours'<=NOW() THEN 0 ELSE usage_1d END)+$2<=rate_limit_1d) AND
 (rate_limit_7d<=0 OR (CASE WHEN window_7d_start+INTERVAL '7 days'<=NOW() THEN 0 ELSE usage_7d END)+$2<=rate_limit_7d)
 FROM api_keys WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, t.APIKeyID, cmd.APIKeyRateLimitCost).Scan(&quota, &used, &rateAllowed); err != nil {
		return nil, false, err
	}
	if cmd.APIKeyRateLimitCost > 0 && !rateAllowed {
		return nil, false, infraerrors.New(429, "music_rate_quota_exceeded", "API key rate quota is insufficient for music precharge")
	}
	if cmd.APIKeyQuotaCost > 0 && quota > 0 && used+cmd.APIKeyQuotaCost > quota {
		return nil, false, infraerrors.New(403, "music_key_quota_exceeded", "API key quota is insufficient for music precharge")
	}
	if cmd.SubscriptionID != nil && cmd.SubscriptionCost > 0 {
		var allowed bool
		err := tx.QueryRowContext(ctx, `SELECT (COALESCE(g.daily_limit_usd,0)<=0 OR us.daily_usage_usd+$2<=g.daily_limit_usd) AND (COALESCE(g.weekly_limit_usd,0)<=0 OR us.weekly_usage_usd+$2<=g.weekly_limit_usd) AND (COALESCE(g.monthly_limit_usd,0)<=0 OR us.monthly_usage_usd+$2<=g.monthly_limit_usd) FROM user_subscriptions us JOIN groups g ON g.id=us.group_id WHERE us.id=$1 AND us.deleted_at IS NULL FOR UPDATE OF us`, *cmd.SubscriptionID, cmd.SubscriptionCost).Scan(&allowed)
		if err != nil {
			return nil, false, err
		}
		if !allowed {
			return nil, false, infraerrors.New(403, "music_subscription_quota_exceeded", "Subscription quota is insufficient for music precharge")
		}
	}
	cmd.Normalize()
	billing := &usageBillingRepository{db: r.db}
	if _, err = billing.claimUsageBillingKey(ctx, tx, &cmd); err != nil {
		return nil, false, err
	}
	if err = billing.applyUsageBillingEffects(ctx, tx, &cmd, &service.UsageBillingApplyResult{}); err != nil {
		return nil, false, err
	}
	if err = applyMusicPlatformQuota(ctx, tx, t, t.Quote.Usage.ActualCost, true); err != nil {
		return nil, false, err
	}
	if err = insertMusicEvent(ctx, tx, t, &t.Quote.Usage, "precharge"); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	return t, true, nil
}
func insertMusicEvent(ctx context.Context, tx *sql.Tx, t *service.DurableMusicTask, log *service.UsageLog, event string) error {
	if err := execUsageLogInsertNoResult(ctx, tx, prepareUsageLogInsert(log)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE usage_logs SET music_task_id=$1,funds_event=$2,music_task_status=$3,music_mode=$6 WHERE request_id=$4 AND api_key_id=$5`, t.ID, event, t.Status, log.RequestID, t.APIKeyID, t.Mode)
	return err
}
func (r *durableMusicLedger) Claim(ctx context.Context) (*service.DurableMusicTask, error) {
	token := uuid.NewString()
	return scanDurableMusic(r.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM music_tasks WHERE status='processing' AND (lease_until IS NULL OR lease_until<NOW()) ORDER BY COALESCE(lease_until,created_at),created_at FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE music_tasks t SET phase=CASE WHEN t.deadline<NOW() THEN 'interrupted' WHEN t.phase='submitting' THEN 'submission_unknown' WHEN t.phase='queued' THEN 'preparing' ELSE t.phase END,lease_token=$1,lease_until=NOW()+INTERVAL '45 seconds',updated_at=NOW() FROM candidate c WHERE t.id=c.id RETURNING `+musicTaskColumns, token))
}
func (r *durableMusicLedger) Pin(ctx context.Context, t *service.DurableMusicTask, accountID int64, limit int) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&id); err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tasks WHERE account_id=$1 AND status='processing'`, accountID).Scan(&count); err != nil {
		return false, err
	}
	if limit < 1 {
		limit = 1
	}
	if count >= limit {
		return false, nil
	}
	t.Quote.AccountID = accountID
	q, err := json.Marshal(t.Quote)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE music_tasks SET account_id=$3,quote=$4,phase='submitting',updated_at=NOW() WHERE id=$1 AND lease_token=$2 AND phase='preparing' AND status='processing' AND lease_until>NOW() AND deadline>NOW()`, t.ID, t.LeaseToken, accountID, q)
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return false, service.ErrMusicTaskLeaseLost
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	t.Phase = "submitting"
	return true, nil
}
func (r *durableMusicLedger) Checkpoint(ctx context.Context, t *service.DurableMusicTask, delay time.Duration) error {
	record, err := json.Marshal(t.MusicTaskRecord)
	if err != nil {
		return err
	}
	q, err := json.Marshal(t.Quote)
	if err != nil {
		return err
	}
	usage, err := json.Marshal(t.CapturedUsage)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE music_tasks SET record=$3,quote=$4,encrypted_result=NULLIF($5,''),captured_usage=$6,phase=$7,lease_until=NOW()+($8::double precision * INTERVAL '1 second'),lease_token=NULL,updated_at=NOW() WHERE id=$1 AND lease_token=$2 AND status='processing' AND lease_until>NOW() AND deadline>NOW()`, t.ID, t.LeaseToken, record, q, t.EncryptedResult, usage, t.Phase, delay.Seconds())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return service.ErrMusicTaskLeaseLost
	}
	return nil
}
func (r *durableMusicLedger) Renew(ctx context.Context, id, token string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE music_tasks SET lease_until=NOW()+INTERVAL '45 seconds' WHERE id=$1 AND lease_token=$2 AND status='processing' AND lease_until>NOW() AND deadline>NOW()`, id, token)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return service.ErrMusicTaskLeaseLost
	}
	return nil
}
func (r *durableMusicLedger) Finalize(ctx context.Context, t *service.DurableMusicTask, actual *service.UsageLog, success bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status, token string
	var leaseValid bool
	if err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(lease_token,''),COALESCE(lease_until>NOW() AND deadline>NOW(),false) FROM music_tasks WHERE id=$1 FOR UPDATE`, t.ID).Scan(&status, &token, &leaseValid); err != nil {
		return err
	}
	if status != "processing" {
		return nil
	}
	if token != t.LeaseToken || (success && !leaseValid) {
		return service.ErrMusicTaskLeaseLost
	}
	target := 0.0
	if success {
		if actual == nil {
			return errors.New("missing actual usage for music settlement")
		}
		target = actual.ActualCost
	}
	reserved := t.Quote.Usage.ActualCost
	delta := service.QuantizeUsageBillingAmount(service.QuantizeUsageBillingAmount(target) - service.QuantizeUsageBillingAmount(reserved))
	event := "settlement"
	if !success {
		event = "failure_refund"
	} else if delta < 0 {
		event = "settlement_refund"
	}
	t.Status = service.MusicTaskStatusCompleted
	t.Phase = "completed"
	t.BillingStatus = "settled"
	t.RefundStatus = "none"
	if !success {
		t.Status = service.MusicTaskStatusFailed
		t.Phase = "failed"
		t.BillingStatus = "refunded"
		t.RefundStatus = "refunded"
	}
	now := time.Now().Unix()
	t.CompletedAt = &now
	billing := &usageBillingRepository{db: r.db}
	if delta != 0 {
		cmd := t.Quote.Charge
		cmd.RequestID = "music:" + t.ID + ":" + event
		cmd.RequestFingerprint = ""
		cmd.AccountQuotaCost = 0
		cmd.BalanceCost = 0
		cmd.SubscriptionCost = 0
		if cmd.BillingType == service.BillingTypeSubscription {
			cmd.SubscriptionCost = delta
		} else {
			cmd.BalanceCost = delta
		}
		cmd.APIKeyQuotaCost = delta
		cmd.APIKeyRateLimitCost = delta
		if delta < 0 {
			// A refund after a window reset must not erase unrelated new usage.
			cmd.SubscriptionCost = 0
			cmd.APIKeyRateLimitCost = 0
		}
		cmd.Normalize()
		claimed, err := billing.claimUsageBillingKey(ctx, tx, &cmd)
		if err != nil {
			return err
		}
		if !claimed {
			return errors.New("music settlement ledger mismatch")
		}
		if delta < 0 {
			if err := refundMusicWindowQuotas(ctx, tx, t, delta); err != nil {
				return err
			}
		}
		if err := billing.applyUsageBillingEffects(ctx, tx, &cmd, &service.UsageBillingApplyResult{}); err != nil {
			return err
		}
		adjustment := service.UsageLog{UserID: t.UserID, APIKeyID: t.APIKeyID, RequestID: cmd.RequestID, Model: t.Quote.Model, RequestedModel: t.Quote.Model, GroupID: t.Quote.Usage.GroupID, SubscriptionID: cmd.SubscriptionID, BillingType: cmd.BillingType, ActualCost: delta, RateMultiplier: 1, CreatedAt: time.Now()}
		if actual != nil {
			adjustment.AccountID = actual.AccountID
		}
		if err := applyMusicPlatformQuota(ctx, tx, t, delta, false); err != nil {
			return err
		}
		if err := insertMusicEvent(ctx, tx, t, &adjustment, event); err != nil {
			return err
		}
	}
	// Upstream cost is independent of customer refunds and applied only once.
	if actual != nil && actual.AccountID > 0 {
		var accountType string
		if err := tx.QueryRowContext(ctx, `SELECT type FROM accounts WHERE id=$1`, actual.AccountID).Scan(&accountType); err != nil {
			return err
		}
		upstream := actual.TotalCost
		if actual.AccountRateMultiplier != nil {
			upstream *= *actual.AccountRateMultiplier
		}
		cmd := service.UsageBillingCommand{RequestID: "music:" + t.ID + ":upstream", APIKeyID: t.APIKeyID, UserID: t.UserID, AccountID: actual.AccountID, AccountType: accountType, AccountQuotaCost: upstream}
		cmd.Normalize()
		claimed, err := billing.claimUsageBillingKey(ctx, tx, &cmd)
		if err != nil {
			return err
		}
		if claimed {
			if err := billing.applyUsageBillingEffects(ctx, tx, &cmd, &service.UsageBillingApplyResult{}); err != nil {
				return err
			}
		}
	}
	// Keep the original debit amount. Fill in measured usage and real upstream cost
	// on that same row, so adjustment events never duplicate request/token counts.
	if actual != nil {
		_, err = tx.ExecContext(ctx, `UPDATE usage_logs SET account_id=NULLIF($3,0),input_tokens=$4,output_tokens=$5,cache_creation_tokens=$6,cache_read_tokens=$7,image_input_tokens=$8,image_output_tokens=$9,image_count=$10,total_cost=$11,account_stats_cost=$12,upstream_model=$13,upstream_request_id=$14,input_cost=$15,output_cost=$16,image_input_cost=$17,image_output_cost=$18,billing_mode=$19,account_rate_multiplier=$20,duration_ms=$21,channel_id=$22,cache_creation_cost=$23,cache_read_cost=$24 WHERE request_id=$1 AND api_key_id=$2`, t.Quote.Usage.RequestID, t.APIKeyID, actual.AccountID, actual.InputTokens, actual.OutputTokens, actual.CacheCreationTokens, actual.CacheReadTokens, actual.ImageInputTokens, actual.ImageOutputTokens, actual.ImageCount, actual.TotalCost, actual.AccountStatsCost, actual.UpstreamModel, actual.UpstreamRequestID, actual.InputCost, actual.OutputCost, actual.ImageInputCost, actual.ImageOutputCost, actual.BillingMode, actual.AccountRateMultiplier, actual.DurationMs, actual.ChannelID, actual.CacheCreationCost, actual.CacheReadCost)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE usage_logs SET music_task_status=$2 WHERE music_task_id=$1`, t.ID, t.Status); err != nil {
		return err
	}
	if !success {
		t.Result = nil
	}
	record, err := json.Marshal(t.MusicTaskRecord)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE music_tasks SET record=$2,status=$3,phase=$4,quote=$5,encrypted_request='',encrypted_result=NULL,captured_usage=NULL,lease_token=NULL,lease_until=NULL,updated_at=NOW() WHERE id=$1`, t.ID, record, t.Status, t.Phase, mustMusicQuote(t.Quote))
	if err != nil {
		return fmt.Errorf("finish music task: %w", err)
	}
	return tx.Commit()
}

func (r *durableMusicLedger) MarkRefundPending(ctx context.Context, t *service.DurableMusicTask) error {
	var usage any
	if t.CapturedUsage != nil {
		raw, err := json.Marshal(t.CapturedUsage)
		if err != nil {
			return err
		}
		usage = string(raw)
	}
	record, err := json.Marshal(t.MusicTaskRecord)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `WITH changed AS (
 UPDATE music_tasks SET phase='refunding',captured_usage=COALESCE($3::jsonb,captured_usage),record=$4::jsonb,quote=$5,updated_at=NOW()
 WHERE id=$1 AND lease_token=$2 AND status='processing' RETURNING id)
 UPDATE usage_logs SET music_task_status='refunding' WHERE music_task_id IN (SELECT id FROM changed)`, t.ID, t.LeaseToken, usage, string(record), mustMusicQuote(t.Quote))
	return err
}

func applyMusicPlatformQuota(ctx context.Context, tx *sql.Tx, t *service.DurableMusicTask, amount float64, strict bool) error {
	if t.Quote.QuotaPlatform == "" || t.Quote.Charge.BillingType == service.BillingTypeSubscription || amount == 0 {
		return nil
	}
	now := time.Now()
	day, week := timezone.StartOfDay(now), timezone.StartOfWeek(now)
	var daily, weekly, monthly float64
	var dailyLimit, weeklyLimit, monthlyLimit sql.NullFloat64
	// The first use of $3 determines its PostgreSQL parameter type. Without an
	// explicit numeric cast, GREATEST(0, $3) infers integer and rejects prices
	// such as $0.20 before admission can commit.
	err := tx.QueryRowContext(ctx, `INSERT INTO user_platform_quotas(user_id,platform,daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_window_start,weekly_window_start,monthly_window_start,created_at,updated_at)
 VALUES($1,$2,GREATEST(0,$3::numeric),GREATEST(0,$3),GREATEST(0,$3),$4,$5,$6,$6,$6)
 ON CONFLICT(user_id,platform) WHERE deleted_at IS NULL DO UPDATE SET
 daily_usage_usd=CASE WHEN user_platform_quotas.daily_window_start IS DISTINCT FROM $4 THEN GREATEST(0,$3) WHEN $3<0 AND user_platform_quotas.daily_window_start IS DISTINCT FROM $7 THEN user_platform_quotas.daily_usage_usd ELSE GREATEST(0,user_platform_quotas.daily_usage_usd+$3) END,
 weekly_usage_usd=CASE WHEN user_platform_quotas.weekly_window_start IS DISTINCT FROM $5 THEN GREATEST(0,$3) WHEN $3<0 AND user_platform_quotas.weekly_window_start IS DISTINCT FROM $8 THEN user_platform_quotas.weekly_usage_usd ELSE GREATEST(0,user_platform_quotas.weekly_usage_usd+$3) END,
 monthly_usage_usd=CASE WHEN user_platform_quotas.monthly_window_start IS NULL OR user_platform_quotas.monthly_window_start+INTERVAL '30 days'<=$6 THEN GREATEST(0,$3) WHEN $3<0 AND user_platform_quotas.monthly_window_start>$9 THEN user_platform_quotas.monthly_usage_usd ELSE GREATEST(0,user_platform_quotas.monthly_usage_usd+$3) END,
 daily_window_start=$4,weekly_window_start=$5,
 monthly_window_start=CASE WHEN user_platform_quotas.monthly_window_start IS NULL OR user_platform_quotas.monthly_window_start+INTERVAL '30 days'<=$6 THEN $6 ELSE user_platform_quotas.monthly_window_start END,updated_at=$6
 RETURNING daily_usage_usd,weekly_usage_usd,monthly_usage_usd,daily_limit_usd,weekly_limit_usd,monthly_limit_usd`, t.UserID, t.Quote.QuotaPlatform, amount, day, week, now, timezone.StartOfDay(t.Quote.PricingAt), timezone.StartOfWeek(t.Quote.PricingAt), t.Quote.PricingAt).Scan(&daily, &weekly, &monthly, &dailyLimit, &weeklyLimit, &monthlyLimit)
	if err != nil {
		return err
	}
	if strict && ((dailyLimit.Valid && daily > dailyLimit.Float64) || (weeklyLimit.Valid && weekly > weeklyLimit.Float64) || (monthlyLimit.Valid && monthly > monthlyLimit.Float64)) {
		return infraerrors.New(403, "music_platform_quota_exceeded", "Platform quota is insufficient for music precharge")
	}
	return nil
}

func refundMusicWindowQuotas(ctx context.Context, tx *sql.Tx, t *service.DurableMusicTask, delta float64) error {
	_, err := tx.ExecContext(ctx, `UPDATE api_keys SET
 usage_5h=CASE WHEN window_5h_start IS NULL OR window_5h_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,usage_5h+$2) ELSE usage_5h END,
 usage_1d=CASE WHEN window_1d_start IS NULL OR window_1d_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,usage_1d+$2) ELSE usage_1d END,
 usage_7d=CASE WHEN window_7d_start IS NULL OR window_7d_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,usage_7d+$2) ELSE usage_7d END,
 updated_at=NOW() WHERE id=$1`, t.APIKeyID, delta, t.ID)
	if err != nil {
		return err
	}
	if t.Quote.Charge.SubscriptionID != nil {
		_, err = tx.ExecContext(ctx, `UPDATE user_subscriptions SET
 daily_usage_usd=CASE WHEN daily_window_start IS NULL OR daily_window_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,daily_usage_usd+$2) ELSE daily_usage_usd END,
 weekly_usage_usd=CASE WHEN weekly_window_start IS NULL OR weekly_window_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,weekly_usage_usd+$2) ELSE weekly_usage_usd END,
 monthly_usage_usd=CASE WHEN monthly_window_start IS NULL OR monthly_window_start<=(SELECT created_at FROM music_tasks WHERE id=$3) THEN GREATEST(0,monthly_usage_usd+$2) ELSE monthly_usage_usd END,
 updated_at=NOW() WHERE id=$1`, *t.Quote.Charge.SubscriptionID, delta, t.ID)
	}
	return err
}

func mustMusicQuote(q service.MusicTaskQuote) []byte { raw, _ := json.Marshal(q); return raw }

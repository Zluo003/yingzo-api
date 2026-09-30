//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDurableImageLedger(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@image.test", PasswordHash: "hash", Balance: 100})
	var groupIDs []int64
	t.Cleanup(func() {
		// The ledger owns real transactions, so the surrounding test cannot roll
		// them back. Remove its fixtures before aggregate and user suites run.
		for _, query := range []string{
			`DELETE FROM image_tasks WHERE user_id=$1`,
			`DELETE FROM usage_logs WHERE user_id=$1`,
			`DELETE FROM user_subscriptions WHERE user_id=$1`,
			`DELETE FROM users WHERE id=$1`,
		} {
			_, err := integrationDB.ExecContext(context.Background(), query, user.ID)
			require.NoError(t, err)
		}
		for _, id := range groupIDs {
			_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id=$1`, id)
			require.NoError(t, err)
		}
	})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "async image", Quota: 1000})
	ledger := NewImageTaskLedger(integrationDB)
	makeTask := func(idem string, amount float64) *service.DurableImageTask {
		id := "imgtask_" + uuid.NewString()[:24]
		request := "image:" + id + ":precharge"
		now := time.Now()
		return &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: id, UserID: user.ID, APIKeyID: key.ID, Status: "processing", Phase: "queued", BillingStatus: "precharged", DeadlineAt: now.Add(30 * time.Minute).Unix(), CreatedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix()}, IdempotencyKey: idem, Fingerprint: "parsed-request-hash", EncryptedRequest: "encrypted fixture", Quote: service.ImageTaskQuote{Model: "gpt-image-2", Count: 1, PricingAt: now, QuotaPlatform: service.PlatformOpenAI, Charge: service.UsageBillingCommand{RequestID: request, UserID: user.ID, APIKeyID: key.ID, BalanceCost: amount, APIKeyQuotaCost: amount, APIKeyRateLimitCost: amount}, Usage: service.UsageLog{RequestID: request, UserID: user.ID, APIKeyID: key.ID, Model: "gpt-image-2", ActualCost: amount, RateMultiplier: 1}}}
	}
	balance := func() float64 {
		var v float64
		require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, user.ID).Scan(&v))
		return v
	}
	t.Run("precharge_is_atomic_and_idempotent", func(t *testing.T) {
		var wg sync.WaitGroup
		out := make(chan *service.DurableImageTask, 8)
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				task, _, err := ledger.Accept(ctx, makeTask("same-key", 2))
				if err != nil {
					errs <- err
					return
				}
				out <- task
			}()
		}
		wg.Wait()
		close(out)
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		var id string
		for task := range out {
			if id == "" {
				id = task.ID
			}
			require.Equal(t, id, task.ID)
		}
		require.InDelta(t, 98, balance(), 1e-8)
		var account sql.NullInt64
		var count int
		require.NoError(t, integrationDB.QueryRow(`SELECT account_id FROM usage_logs WHERE image_task_id=$1`, id).Scan(&account))
		require.False(t, account.Valid)
		conflict := makeTask("same-key", 2)
		conflict.Fingerprint = "other request"
		_, _, err := ledger.Accept(ctx, conflict)
		require.ErrorIs(t, err, service.ErrImageIdempotencyConflict)
		task, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, id, task.ID)
		usage := service.UsageLog{ActualCost: 2, ImageCount: 1}
		require.NoError(t, ledger.Finalize(ctx, task, &usage, true))
		require.NoError(t, ledger.Finalize(ctx, task, &usage, true))
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE image_task_id=$1`, id).Scan(&count))
		require.Equal(t, 1, count)
		_, err = ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID + 1}, id, false)
		require.ErrorIs(t, err, service.ErrImageTaskNotFound)
	})
	t.Run("fractional_image_prices_and_refunds", func(t *testing.T) {
		for _, actual := range []float64{0.2, 0.25, 0.15, 0} {
			start := balance()
			var quotaBefore float64
			require.NoError(t, integrationDB.QueryRow(`SELECT COALESCE(SUM(daily_usage_usd),0) FROM user_platform_quotas WHERE user_id=$1 AND platform='openai'`, user.ID).Scan(&quotaBefore))
			task, _, err := ledger.Accept(ctx, makeTask(fmt.Sprintf("fractional-price-%g", actual), 0.2))
			require.NoError(t, err)
			require.InDelta(t, start-0.2, balance(), 1e-8)
			found, err := ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID}, task.IdempotencyKey, true)
			require.NoError(t, err)
			require.Equal(t, task.ID, found.ID)
			claimed, err := ledger.Claim(ctx)
			require.NoError(t, err)
			require.Equal(t, task.ID, claimed.ID)
			usage := &service.UsageLog{ActualCost: actual, ImageCount: 1}
			require.NoError(t, ledger.Finalize(ctx, claimed, usage, actual > 0))
			require.NoError(t, ledger.Finalize(ctx, claimed, usage, actual > 0))
			require.InDelta(t, start-actual, balance(), 1e-8)
			var net, quotaAfter float64
			require.NoError(t, integrationDB.QueryRow(`SELECT SUM(actual_cost) FROM usage_logs WHERE image_task_id=$1`, task.ID).Scan(&net))
			require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd FROM user_platform_quotas WHERE user_id=$1 AND platform='openai'`, user.ID).Scan(&quotaAfter))
			require.InDelta(t, actual, net, 1e-8)
			require.InDelta(t, quotaBefore+actual, quotaAfter, 1e-8)
		}
	})
	t.Run("adjustments_and_refunds_preserve_net_balance", func(t *testing.T) {
		for i, actual := range []float64{3, 1, 0} {
			start := balance()
			task, _, err := ledger.Accept(ctx, makeTask(fmt.Sprintf("delta-%d", i), 2))
			require.NoError(t, err)
			claimed, err := ledger.Claim(ctx)
			require.NoError(t, err)
			require.Equal(t, task.ID, claimed.ID)
			usage := service.UsageLog{ActualCost: actual, ImageCount: 1}
			require.NoError(t, ledger.Finalize(ctx, claimed, &usage, actual != 0))
			require.NoError(t, ledger.Finalize(ctx, claimed, &usage, actual != 0))
			require.InDelta(t, start-actual, balance(), 1e-8)
			var net float64
			var events, requests int
			require.NoError(t, integrationDB.QueryRow(`SELECT SUM(actual_cost),COUNT(*),COUNT(*) FILTER(WHERE funds_event='precharge') FROM usage_logs WHERE image_task_id=$1`, task.ID).Scan(&net, &events, &requests))
			require.InDelta(t, actual, net, 1e-8)
			require.Equal(t, 2, events)
			require.Equal(t, 1, requests)
		}
	})
	t.Run("insufficient_balance_never_creates_a_task", func(t *testing.T) {
		task := makeTask("insufficient", 1000)
		_, _, err := ledger.Accept(ctx, task)
		require.ErrorIs(t, err, service.ErrInsufficientBalance)
		var count int
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM image_tasks WHERE id=$1`, task.ID).Scan(&count))
		require.Zero(t, count)
	})
	t.Run("execution_lease_loss_is_not_replayed", func(t *testing.T) {
		start := balance()
		task, _, err := ledger.Accept(ctx, makeTask("lost-execution", 2))
		require.NoError(t, err)
		first, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, task.ID, first.ID)
		require.Equal(t, "executing", first.Phase)
		_, err = integrationDB.Exec(`UPDATE image_tasks SET lease_until=NOW()-INTERVAL '1 minute' WHERE id=$1`, task.ID)
		require.NoError(t, err)
		require.ErrorIs(t, ledger.Renew(ctx, first.ID, first.LeaseToken), service.ErrImageTaskLeaseLost)
		recovered, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, "interrupted", recovered.Phase)
		first.EncryptedResult = "late result"
		require.ErrorIs(t, ledger.SaveResponse(ctx, first), service.ErrImageTaskLeaseLost)
		require.NoError(t, ledger.Finalize(ctx, recovered, nil, false))
		require.InDelta(t, start, balance(), 1e-8)
		stored, err := ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID}, task.ID, false)
		require.NoError(t, err)
		require.Equal(t, "refunded", stored.RefundStatus)
		require.Empty(t, stored.EncryptedRequest)
	})
	t.Run("saved_response_is_resumable", func(t *testing.T) {
		task, _, err := ledger.Accept(ctx, makeTask("saved-result", 2))
		require.NoError(t, err)
		claimed, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, task.ID, claimed.ID)
		claimed.EncryptedResult = "protected response"
		claimed.Quote.EncryptedProviderReference = "encrypted Midjourney account and task reference"
		claimed.CapturedUsage = &service.UsageLog{ImageCount: 1, ActualCost: 2}
		require.NoError(t, ledger.SaveResponse(ctx, claimed))
		_, err = integrationDB.Exec(`UPDATE image_tasks SET lease_until=NOW()-INTERVAL '1 minute' WHERE id=$1`, task.ID)
		require.NoError(t, err)
		recovered, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, "saving", recovered.Phase)
		require.Equal(t, "protected response", recovered.EncryptedResult)
		require.NoError(t, ledger.Finalize(ctx, recovered, recovered.CapturedUsage, true))
		stored, err := ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID}, task.ID, false)
		require.NoError(t, err)
		require.Empty(t, stored.EncryptedResult)
		require.Empty(t, stored.EncryptedRequest)
		require.Equal(t, claimed.Quote.EncryptedProviderReference, stored.Quote.EncryptedProviderReference)
	})
	t.Run("midjourney_settles_without_a_pixel_size", func(t *testing.T) {
		for _, tier := range []string{"generation", "upscale", "imagine_relax", "imagine_fast", "imagine_turbo", "upscale_fast"} {
			start := balance()
			task := makeTask("midjourney-"+tier, 0.7)
			task.Quote.Model, task.Quote.Size = service.MidjourneyModel, tier
			task.Quote.Usage.Model = service.MidjourneyBillingModel(tier)
			mode := "per_request"
			task.Quote.Usage.BillingMode = &mode
			accepted, _, err := ledger.Accept(ctx, task)
			require.NoError(t, err)
			claimed, err := ledger.Claim(ctx)
			require.NoError(t, err)
			require.Equal(t, accepted.ID, claimed.ID)
			usage := task.Quote.Usage
			usage.ImageCount = 1
			require.NoError(t, ledger.Finalize(ctx, claimed, &usage, true))
			require.NoError(t, ledger.Finalize(ctx, claimed, &usage, true))
			require.InDelta(t, start-0.7, balance(), 1e-8)
			var size sql.NullString
			var count, rows int
			require.NoError(t, integrationDB.QueryRow(`SELECT image_size,image_count FROM usage_logs WHERE image_task_id=$1`, accepted.ID).Scan(&size, &count))
			require.False(t, size.Valid)
			require.Equal(t, 1, count)
			require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE image_task_id=$1`, accepted.ID).Scan(&rows))
			require.Equal(t, 1, rows)
		}
	})
	t.Run("failed_usage_insert_rolls_back_admission", func(t *testing.T) {
		before := balance()
		task := makeTask("insert-failure", 2)
		task.Quote.Usage.Model = strings.Repeat("x", 256) // usage_logs.model has a bounded column
		_, _, err := ledger.Accept(ctx, task)
		require.Error(t, err)
		require.InDelta(t, before, balance(), 1e-8)
		_, err = ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID}, task.ID, false)
		require.ErrorIs(t, err, service.ErrImageTaskNotFound)
	})
	t.Run("failed_refund_remains_pending_and_retries_once", func(t *testing.T) {
		before := balance()
		accepted, _, err := ledger.Accept(ctx, makeTask("refund-compensation", 2))
		require.NoError(t, err)
		task, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, accepted.ID, task.ID)
		require.NoError(t, ledger.MarkRefundPending(ctx, task))
		_, err = integrationDB.Exec(`ALTER TABLE usage_logs ADD CONSTRAINT image_refund_fault CHECK (image_task_id IS DISTINCT FROM '` + task.ID + `' OR actual_cost >= 0)`)
		require.NoError(t, err)
		require.Error(t, ledger.Finalize(ctx, task, nil, false))
		require.InDelta(t, before-2, balance(), 1e-8)
		stored, err := ledger.Find(ctx, service.ImageTaskOwner{UserID: user.ID, APIKeyID: key.ID}, task.ID, false)
		require.NoError(t, err)
		require.Equal(t, "refunding", stored.Phase)
		_, err = integrationDB.Exec(`ALTER TABLE usage_logs DROP CONSTRAINT image_refund_fault`)
		require.NoError(t, err)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); copy := *stored; errs <- ledger.Finalize(ctx, &copy, nil, false) }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.InDelta(t, before, balance(), 1e-8)
		var count int
		require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM usage_logs WHERE image_task_id=$1 AND funds_event='failure_refund'`, task.ID).Scan(&count))
		require.Equal(t, 1, count)
	})
	t.Run("expired_execution_cannot_settle_success", func(t *testing.T) {
		accepted, _, err := ledger.Accept(ctx, makeTask("expired-finalize", 2))
		require.NoError(t, err)
		task, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, accepted.ID, task.ID)
		_, err = integrationDB.Exec(`UPDATE image_tasks SET deadline=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
		require.NoError(t, err)
		require.ErrorIs(t, ledger.Finalize(ctx, task, &service.UsageLog{ActualCost: 2}, true), service.ErrImageTaskLeaseLost)
		require.NoError(t, ledger.Finalize(ctx, task, nil, false))
	})

	t.Run("subscription_refund_restores_usage_not_balance", func(t *testing.T) {
		group := mustCreateGroup(t, client, &service.Group{Name: "image-sub-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
		groupIDs = append(groupIDs, group.ID)
		sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: user.ID, GroupID: group.ID})
		start := balance()
		task := makeTask("subscription-image", 2)
		task.Quote.Charge.BalanceCost = 0
		task.Quote.Charge.SubscriptionCost = 2
		task.Quote.Charge.SubscriptionID = &sub.ID
		task.Quote.Charge.BillingType = service.BillingTypeSubscription
		task.Quote.Usage.GroupID = &group.ID
		task.Quote.Usage.SubscriptionID = &sub.ID
		task.Quote.Usage.BillingType = service.BillingTypeSubscription
		_, _, err := ledger.Accept(ctx, task)
		require.NoError(t, err)
		claimed, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, task.ID, claimed.ID)
		var usage float64
		require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=$1`, sub.ID).Scan(&usage))
		require.Equal(t, 2.0, usage)
		require.NoError(t, ledger.Finalize(ctx, claimed, nil, false))
		require.NoError(t, integrationDB.QueryRow(`SELECT daily_usage_usd FROM user_subscriptions WHERE id=$1`, sub.ID).Scan(&usage))
		require.Zero(t, usage)
		require.Equal(t, start, balance())
	})
	t.Run("refund_does_not_erase_new_rate_window_usage", func(t *testing.T) {
		accepted, _, err := ledger.Accept(ctx, makeTask("rate-reset", 2))
		require.NoError(t, err)
		task, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, accepted.ID, task.ID)
		_, err = integrationDB.Exec(`UPDATE api_keys SET window_5h_start=NOW()+INTERVAL '1 second',usage_5h=5 WHERE id=$1`, key.ID)
		require.NoError(t, err)
		require.NoError(t, ledger.Finalize(ctx, task, nil, false))
		var usage float64
		require.NoError(t, integrationDB.QueryRow(`SELECT usage_5h FROM api_keys WHERE id=$1`, key.ID).Scan(&usage))
		require.Equal(t, 5.0, usage)
	})
	t.Run("endpoint_statistics_count_tasks_and_net_customer_cost", func(t *testing.T) {
		repo := newUsageLogRepositoryWithSQL(client, integrationDB)
		stats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID})
		require.NoError(t, err)
		// Include the six successful Midjourney operations at 0.7 each.
		require.Equal(t, int64(20), stats.TotalRequests)
		require.InDelta(t, 12.8, stats.TotalActualCost, 1e-8)
		require.Len(t, stats.Endpoints, 1)
		require.Equal(t, int64(20), stats.Endpoints[0].Requests)
		require.InDelta(t, 12.8, stats.Endpoints[0].ActualCost, 1e-8)
	})
}

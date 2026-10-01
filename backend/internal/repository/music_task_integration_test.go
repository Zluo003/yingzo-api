//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMusicLedger(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@music.test", PasswordHash: "hash", Balance: 100})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-" + uuid.NewString(), Name: "music", Quota: 1000})
	a := mustCreateAccount(t, client, &service.Account{Name: "Suno " + uuid.NewString(), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}, Extra: map[string]any{"music_provider": service.SunoProvider}, Concurrency: 1, Status: service.StatusActive, Schedulable: true})
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM music_tasks WHERE user_id=$1`, `DELETE FROM usage_logs WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
			_, err := integrationDB.ExecContext(ctx, q, user.ID)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, a.ID)
		require.NoError(t, err)
	})
	ledger := NewMusicTaskLedger(integrationDB)
	var agentGroup, regularGroup int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT id FROM groups WHERE kind='agent' AND system_code='yingzo' AND deleted_at IS NULL`).Scan(&agentGroup))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, "suno-isolation-"+uuid.NewString()).Scan(&regularGroup))
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, regularGroup)
		require.NoError(t, e)
	})
	_, bindingErr := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, a.ID, regularGroup)
	require.ErrorContains(t, bindingErr, "Suno accounts can only bind")
	_, bindingErr = integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, a.ID, agentGroup)
	require.NoError(t, bindingErr)
	makeTask := func(idem, mode string, price float64) *service.DurableMusicTask {
		id := "musictask_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		request := "music:" + id + ":precharge"
		now := time.Now()
		return &service.DurableMusicTask{MusicTaskRecord: service.MusicTaskRecord{ID: id, UserID: user.ID, APIKeyID: key.ID, Mode: mode, Status: "processing", Phase: "queued", BillingStatus: "precharged", DeadlineAt: now.Add(30 * time.Minute).Unix(), CreatedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix()}, IdempotencyKey: idem, Fingerprint: mode, EncryptedRequest: "protected", Quote: service.MusicTaskQuote{Model: service.SunoModel, Mode: mode, PricingAt: now, QuotaPlatform: service.PlatformOpenAI, Charge: service.UsageBillingCommand{RequestID: request, UserID: user.ID, APIKeyID: key.ID, Model: service.SunoModel, MediaType: "audio", BalanceCost: price, APIKeyQuotaCost: price, APIKeyRateLimitCost: price}, Usage: service.UsageLog{RequestID: request, UserID: user.ID, APIKeyID: key.ID, Model: service.SunoModel, ActualCost: price, RateMultiplier: 1}}}
	}
	balance := func() float64 {
		var b float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id=$1`, user.ID).Scan(&b))
		return b
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, _, err := ledger.Accept(ctx, makeTask("same", "song", 2))
			errs <- err
			if task != nil {
				ids <- task.ID
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	id := ""
	for got := range ids {
		if id == "" {
			id = got
		}
		require.Equal(t, id, got)
	}
	require.InDelta(t, 98, balance(), 1e-8)
	_, _, err := ledger.Accept(ctx, makeTask("same", "instrumental", 1))
	require.ErrorIs(t, err, service.ErrMusicIdempotencyConflict)
	_, err = ledger.Find(ctx, service.MusicTaskOwner{UserID: user.ID, APIKeyID: key.ID + 1}, id, false)
	require.ErrorIs(t, err, service.ErrMusicTaskNotFound)
	claims := make(chan *service.DurableMusicTask, 8)
	claimErrors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); claimed, e := ledger.Claim(ctx); claims <- claimed; claimErrors <- e }()
	}
	wg.Wait()
	close(claims)
	close(claimErrors)
	var first *service.DurableMusicTask
	claimedCount := 0
	for claimed := range claims {
		if claimed != nil {
			first = claimed
			claimedCount++
		}
	}
	for e := range claimErrors {
		if e != nil {
			require.ErrorIs(t, e, service.ErrMusicTaskNotFound)
		}
	}
	require.Equal(t, 1, claimedCount, "a task may have only one active execution lease")
	require.Equal(t, "preparing", first.Phase)
	pinned, err := ledger.Pin(ctx, first, a.ID, 1)
	require.NoError(t, err)
	require.True(t, pinned)
	second, _, err := ledger.Accept(ctx, makeTask("second", "instrumental", 0.2))
	require.NoError(t, err)
	second, err = ledger.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, "instrumental", second.Mode)
	pinned, err = ledger.Pin(ctx, second, a.ID, 1)
	require.NoError(t, err)
	require.False(t, pinned)
	// Losing a submission lease must not reissue a paid POST.
	_, err = integrationDB.ExecContext(ctx, `UPDATE music_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, first.ID)
	require.NoError(t, err)
	first, err = ledger.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, "submission_unknown", first.Phase)
	require.NoError(t, ledger.MarkRefundPending(ctx, first))
	require.NoError(t, ledger.Finalize(ctx, first, nil, false))
	require.NoError(t, ledger.Finalize(ctx, first, nil, false))
	require.InDelta(t, 99.8, balance(), 1e-8)
	pinned, err = ledger.Pin(ctx, second, a.ID, 1)
	require.NoError(t, err)
	require.True(t, pinned)
	second.Quote.UpstreamID = "provider-task"
	second.Phase = "polling"
	require.NoError(t, ledger.Checkpoint(ctx, second, 0))
	restored, err := ledger.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, "polling", restored.Phase)
	require.Equal(t, "provider-task", restored.Quote.UpstreamID)
	// Publication recovery keeps the provider result and frozen price.
	restored.Phase = "saving"
	restored.EncryptedResult = "protected result"
	usage := restored.Quote.Usage
	usage.TotalCost = 0.12
	restored.CapturedUsage = &usage
	credits := 1.2
	restored.Quote.UpstreamCreditsCost = &credits
	require.NoError(t, ledger.Checkpoint(ctx, restored, 0))
	restored, err = ledger.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, "saving", restored.Phase)
	require.Equal(t, "protected result", restored.EncryptedResult)
	restored.Result = json.RawMessage(`{"music":[{"audio_url":"https://gateway/one"},{"audio_url":"https://gateway/two"},{"audio_url":"https://gateway/three"}]}`)
	require.NoError(t, ledger.Finalize(ctx, restored, restored.CapturedUsage, true))
	require.NoError(t, ledger.Finalize(ctx, restored, restored.CapturedUsage, true))
	require.InDelta(t, 99.8, balance(), 1e-8)
	var quota float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used FROM api_keys WHERE id=$1`, key.ID).Scan(&quota))
	require.InDelta(t, 0.2, quota, 1e-8)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE music_task_id=$1`, restored.ID).Scan(&count))
	require.Equal(t, 1, count)
	final, err := ledger.Find(ctx, service.MusicTaskOwner{UserID: user.ID, APIKeyID: key.ID}, restored.ID, false)
	require.NoError(t, err)
	require.Equal(t, "completed", final.Status)
	require.Equal(t, credits, *final.Quote.UpstreamCreditsCost)
	// Free tasks are admitted at an empty balance; unaffordable tasks roll back entirely.
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance=0 WHERE id=$1`, user.ID)
	require.NoError(t, err)
	free, _, err := ledger.Accept(ctx, makeTask("free", "instrumental", 0))
	require.NoError(t, err)
	require.NotEmpty(t, free.ID)
	_, _, err = ledger.Accept(ctx, makeTask("unaffordable", "song", 1))
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	_, err = ledger.Find(ctx, service.MusicTaskOwner{UserID: user.ID, APIKeyID: key.ID}, "unaffordable", true)
	require.ErrorIs(t, err, service.ErrMusicTaskNotFound)
	_, err = integrationDB.ExecContext(ctx, `UPDATE music_tasks SET deadline=NOW()-INTERVAL '1 second' WHERE id=$1`, free.ID)
	require.NoError(t, err)
	expired, err := ledger.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, "interrupted", expired.Phase)
	require.NoError(t, ledger.MarkRefundPending(ctx, expired))
	require.NoError(t, ledger.Finalize(ctx, expired, nil, false))
	require.Zero(t, balance())
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT record->>'refund_status' FROM music_tasks WHERE id=$1`, free.ID).Scan(&state))
	require.Equal(t, "refunded", state)
}

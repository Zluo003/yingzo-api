package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Exercise real billing transactions and history SQL in an isolated schema of
// a disposable local database, without relying on a mock balance or ledger.
func TestUserImageTaskBillingAndHistoryPostgres(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "user_image_" + uuid.New().String()[:8]
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = db.Close() })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	scoped, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = scoped.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	require.NoError(t, client.Schema.Create(ctx))
	// The ledger and several transport fields are maintained by SQL rather
	// than Ent. Create only the schema needed by this billing regression.
	for _, name := range []string{"027_usage_billing_consistency.sql", "071_add_usage_billing_dedup.sql", "073_add_usage_billing_dedup_archive.sql", "142_user_platform_quotas.sql", "258_durable_image_tasks.sql"} {
		migration, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = scoped.Exec(string(migration))
		require.NoError(t, err)
	}
	_, err = scoped.Exec(`ALTER TABLE usage_logs
 ALTER COLUMN video_duration_seconds DROP NOT NULL,
 ALTER COLUMN video_reference_duration_seconds SET DEFAULT 0,
 ALTER COLUMN video_billable_seconds SET DEFAULT 0,
 ADD COLUMN request_type smallint NOT NULL DEFAULT 0,
 ADD COLUMN openai_ws_mode boolean NOT NULL DEFAULT false,
 ADD COLUMN image_input_tokens int NOT NULL DEFAULT 0,
 ADD COLUMN image_output_tokens int NOT NULL DEFAULT 0,
 ADD COLUMN image_input_cost numeric NOT NULL DEFAULT 0,
 ADD COLUMN image_output_cost numeric NOT NULL DEFAULT 0,
 ADD COLUMN account_stats_cost numeric,
 ADD COLUMN service_tier text,
 ADD COLUMN reasoning_effort text,
 ADD COLUMN requested_reasoning_effort text,
 ADD COLUMN inbound_endpoint text,
 ADD COLUMN upstream_endpoint text,
 ADD COLUMN upstream_request_id text,
 ADD COLUMN session_id text,
 ADD COLUMN music_task_id text,
 ADD COLUMN native_compaction_v2 boolean NOT NULL DEFAULT false;`)
	require.NoError(t, err)
	repo := &usageLogRepository{sql: scoped, client: client}
	ledger := NewImageTaskLedger(scoped)
	var userID, keyID int64
	require.NoError(t, scoped.QueryRow(`INSERT INTO users(email,password_hash,balance,created_at,updated_at) VALUES('image@test.local','hash',100,NOW(),NOW()) RETURNING id`).Scan(&userID))
	require.NoError(t, scoped.QueryRow(`INSERT INTO api_keys(user_id,key,name,created_at,updated_at) VALUES($1,'test-image-key','test',NOW(),NOW()) RETURNING id`, userID).Scan(&keyID))
	balance := func() float64 {
		var amount float64
		require.NoError(t, scoped.QueryRow(`SELECT balance FROM users WHERE id=$1`, userID).Scan(&amount))
		return amount
	}
	for i, actual := range []float64{0.2, 0.25, 0.15, 0} {
		start := balance()
		now := time.Now()
		id := fmt.Sprintf("imgtask_%d", i)
		request := "image:" + id + ":precharge"
		task := &service.DurableImageTask{
			ImageTaskRecord: service.ImageTaskRecord{ID: id, UserID: userID, APIKeyID: keyID, Status: "processing", Phase: "queued", DeadlineAt: now.Add(time.Hour).Unix(), CreatedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix()},
			IdempotencyKey:  id, Fingerprint: id, EncryptedRequest: "fixture",
			Quote: service.ImageTaskQuote{Count: 1, Model: "image", PricingAt: now, QuotaPlatform: service.PlatformGemini,
				Charge: service.UsageBillingCommand{RequestID: request, UserID: userID, APIKeyID: keyID, BalanceCost: 0.2, APIKeyQuotaCost: 0.2, APIKeyRateLimitCost: 0.2},
				Usage:  service.UsageLog{RequestID: request, UserID: userID, APIKeyID: keyID, Model: "image", ActualCost: 0.2, RateMultiplier: 1}},
		}
		accepted, created, err := ledger.Accept(ctx, task)
		require.NoError(t, err)
		require.True(t, created)
		replayed, created, err := ledger.Accept(ctx, task)
		require.NoError(t, err)
		require.False(t, created)
		require.Equal(t, accepted.ID, replayed.ID)
		require.InDelta(t, start-0.2, balance(), 1e-8)
		claimed, err := ledger.Claim(ctx)
		require.NoError(t, err)
		require.Equal(t, id, claimed.ID)
		duration := 1234
		usage := &service.UsageLog{ActualCost: actual, ImageCount: 1, DurationMs: &duration}
		require.NoError(t, ledger.Finalize(ctx, claimed, usage, actual > 0))
		require.NoError(t, ledger.Finalize(ctx, claimed, usage, actual > 0))
		require.InDelta(t, start-actual, balance(), 1e-8, "replay must not debit twice")
		var net float64
		var eventCount int
		require.NoError(t, scoped.QueryRow(`SELECT SUM(actual_cost),COUNT(*) FROM usage_logs WHERE image_task_id=$1`, id).Scan(&net, &eventCount))
		require.InDelta(t, actual, net, 1e-8)
		if actual == 0.2 {
			require.Equal(t, 1, eventCount, "one delivered image has no extra settlement debit")
		} else {
			require.Equal(t, 2, eventCount)
		}
		// A settlement outside the request's date range still belongs to its
		// original task; the user's net amount must never omit that delta.
		_, err = scoped.Exec(`UPDATE usage_logs SET created_at=created_at+INTERVAL '2 days' WHERE image_task_id=$1 AND funds_event<>'precharge'`, id)
		require.NoError(t, err)
	}
	// Financial events with the same task reference on a different key/user
	// must not contribute to this owner's logical task or its net price.
	var otherUser, otherKey, sameUserKey int64
	require.NoError(t, scoped.QueryRow(`INSERT INTO users(email,password_hash,created_at,updated_at) VALUES('other@test.local','hash',NOW(),NOW()) RETURNING id`).Scan(&otherUser))
	require.NoError(t, scoped.QueryRow(`INSERT INTO api_keys(user_id,key,name,created_at,updated_at) VALUES($1,'other-image-key','other',NOW(),NOW()) RETURNING id`, otherUser).Scan(&otherKey))
	require.NoError(t, scoped.QueryRow(`INSERT INTO api_keys(user_id,key,name,created_at,updated_at) VALUES($1,'same-user-key','other',NOW(),NOW()) RETURNING id`, userID).Scan(&sameUserKey))
	for _, owner := range []struct{ user, key int64 }{{otherUser, otherKey}, {userID, sameUserKey}} {
		log := &service.UsageLog{UserID: owner.user, APIKeyID: owner.key, RequestID: "image:imgtask_0:settlement", Model: "image", ActualCost: 9, RateMultiplier: 1}
		tx, err := scoped.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, insertImageEvent(ctx, tx, &service.DurableImageTask{ImageTaskRecord: service.ImageTaskRecord{ID: "imgtask_0", Status: "completed"}}, log, "settlement"))
		require.NoError(t, tx.Commit())
	}
	end := time.Now().Add(time.Hour)
	filters := usagestats.UsageLogFilters{UserID: userID, APIKeyID: keyID, UserTaskView: true, UsageType: "image", EndTime: &end}
	logs, page, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 2}, filters)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	require.Equal(t, int64(4), page.Total)
	require.Equal(t, 2, page.Pages)
	second, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 2, PageSize: 2}, filters)
	require.NoError(t, err)
	logs = append(logs, second...)
	var shown float64
	seen := map[string]bool{}
	for _, log := range logs {
		require.NotNil(t, log.ImageTaskID)
		require.False(t, seen[*log.ImageTaskID])
		seen[*log.ImageTaskID] = true
		require.NotNil(t, log.DurationMs)
		require.Equal(t, 1234, *log.DurationMs)
		shown += log.ActualCost
	}
	require.InDelta(t, 100-balance(), shown, 1e-8, "history equals real net debit")
	var quotaUsed, windowUsed, platformUsed float64
	require.NoError(t, scoped.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, keyID).Scan(&quotaUsed, &windowUsed))
	require.NoError(t, scoped.QueryRow(`SELECT daily_usage_usd FROM user_platform_quotas WHERE user_id=$1 AND platform='gemini'`, userID).Scan(&platformUsed))
	require.InDelta(t, shown, quotaUsed, 1e-8)
	require.InDelta(t, shown, windowUsed, 1e-8)
	require.InDelta(t, shown, platformUsed, 1e-8)
	stats, err := repo.GetStatsWithFilters(ctx, filters)
	require.NoError(t, err)
	require.Equal(t, int64(4), stats.TotalRequests)
	require.InDelta(t, shown, stats.TotalActualCost, 1e-8)
	// Admin sees original audit events. User details, including old adjustment
	// links, resolve to the canonical record with the same net cost.
	adminLogs, adminPage, err := repo.ListWithFilters(ctx, pagination.DefaultPagination(), usagestats.UsageLogFilters{UserID: userID, APIKeyID: keyID})
	require.NoError(t, err)
	require.Equal(t, int64(7), adminPage.Total)
	svc := service.NewUsageService(repo, nil, nil, nil)
	for _, raw := range adminLogs {
		detail, err := svc.GetUserByID(ctx, raw.ID, userID)
		require.NoError(t, err)
		for _, logical := range logs {
			if *logical.ImageTaskID == *raw.ImageTaskID {
				require.Equal(t, logical.ID, detail.ID)
				require.InDelta(t, logical.ActualCost, detail.ActualCost, 1e-8)
			}
		}
	}
}

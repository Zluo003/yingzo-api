//go:build integration

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyFailureCooldownMigration(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	schema := "mj_cooldown_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
CREATE TABLE accounts (
 id BIGINT, extra JSONB, status TEXT, schedulable BOOLEAN,
 temp_unschedulable_until TIMESTAMPTZ, temp_unschedulable_reason TEXT,
 rate_limit_reset_at TIMESTAMPTZ, overload_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ
);
CREATE TABLE scheduler_outbox (event_type TEXT, account_id BIGINT);
INSERT INTO accounts
SELECT id, jsonb_build_object('image_provider', CASE WHEN id=3 THEN 'other' ELSE 'discord_midjourney' END),
 CASE WHEN id=2 THEN 'disabled' ELSE 'active' END, id<>2,
 NOW()+INTERVAL '5 minutes',
 CASE WHEN id=4 THEN 'administrator cooldown' ELSE 'Discord task failed; check MJ session, subscription and channel' END,
 NOW()+INTERVAL '1 hour', NOW()+INTERVAL '2 hours', NOW(),
 CASE WHEN id=5 THEN NOW() ELSE NULL END
FROM generate_series(1,5) AS id;
`)
	require.NoError(t, err)
	raw, err := migrations.FS.ReadFile("272_clear_midjourney_task_failure_cooldown.sql")
	require.NoError(t, err)
	for attempt := 0; attempt < 2; attempt++ {
		_, err = tx.ExecContext(ctx, string(raw))
		require.NoError(t, err)
	}
	for id := 1; id <= 5; id++ {
		var until sql.NullTime
		var reason sql.NullString
		var status string
		var schedulable, otherLimitsPreserved bool
		err = tx.QueryRowContext(ctx, `SELECT status,schedulable,temp_unschedulable_until,temp_unschedulable_reason,
 rate_limit_reset_at IS NOT NULL AND overload_until IS NOT NULL
 FROM accounts WHERE id=$1`, id).Scan(&status, &schedulable, &until, &reason, &otherLimitsPreserved)
		require.NoError(t, err)
		require.True(t, otherLimitsPreserved, "unrelated limits must be retained: account=%d", id)
		if id == 1 || id == 2 {
			require.False(t, until.Valid, "old MJ failure cooldown must be cleared")
			require.False(t, reason.Valid)
		} else {
			require.True(t, until.Valid, "other providers/reasons and deleted accounts must be unchanged")
			require.True(t, reason.Valid)
		}
		if id == 2 {
			require.Equal(t, "disabled", status)
			require.False(t, schedulable, "migration must not reenable a manually disabled account")
		} else {
			require.Equal(t, "active", status)
			require.True(t, schedulable)
		}
	}
	var changed []int
	rows, err := tx.QueryContext(ctx, "SELECT account_id FROM scheduler_outbox WHERE event_type='account_changed' ORDER BY account_id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int
		require.NoError(t, rows.Scan(&id))
		changed = append(changed, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []int{1, 2}, changed, "publish each recovered account once to refresh scheduler caches")
}

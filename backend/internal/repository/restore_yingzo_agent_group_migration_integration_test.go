//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 244 负责把被误删的系统内置聚合分组找回来（守卫修复前 IsAgent() 恒为 false，
// 管理端删除一路放行）。三种状态都要覆盖：软删除行被恢复、没有行时补种、已有存活行时不重复插入。
func TestRestoreYingzoAgentGroupMigration(t *testing.T) {
	content, err := migrations.FS.ReadFile("244_restore_yingzo_agent_group.sql")
	require.NoError(t, err)
	migration := string(content)

	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "restore_agent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE groups (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(255),
			description TEXT,
			kind VARCHAR(20) NOT NULL DEFAULT 'standard',
			system_code VARCHAR(64),
			platform VARCHAR(32),
			status VARCHAR(20),
			rate_multiplier NUMERIC(20,10) DEFAULT 1,
			is_exclusive BOOLEAN DEFAULT FALSE,
			allow_image_generation BOOLEAN DEFAULT FALSE,
			image_rate_independent BOOLEAN DEFAULT FALSE,
			image_rate_multiplier NUMERIC(20,10) DEFAULT 1,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)
	`)
	require.NoError(t, err)

	// 场景 1：软删除行被恢复，并复位系统分组的固定属性。
	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups (name, kind, system_code, platform, status, is_exclusive, allow_image_generation, deleted_at)
		VALUES ('Yingzo Agent', 'agent', 'yingzo', 'video', 'inactive', TRUE, FALSE, NOW())
	`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err, "migration must be idempotent")

	var (
		liveCount  int
		platform   string
		status     string
		exclusive  bool
		imageAllow bool
	)
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM groups WHERE system_code = 'yingzo' AND deleted_at IS NULL
	`).Scan(&liveCount))
	require.Equal(t, 1, liveCount)
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT platform, status, is_exclusive, allow_image_generation
		FROM groups WHERE system_code = 'yingzo' AND deleted_at IS NULL
	`).Scan(&platform, &status, &exclusive, &imageAllow))
	// 244 is a historical restore migration and intentionally retains its
	// original openai seed. Migration 250 upgrades existing installations to
	// the composite platform marker without rewriting this checksummed file.
	require.Equal(t, "openai", platform)
	require.Equal(t, "active", status)
	require.False(t, exclusive)
	require.True(t, imageAllow, "agent 分组必须允许生图（169 的 CHECK 也要求这一点）")

	// 场景 2：行完全不存在时补种（且不产生第二行）。
	_, err = tx.ExecContext(ctx, `DELETE FROM groups WHERE system_code = 'yingzo'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM groups WHERE system_code = 'yingzo' AND deleted_at IS NULL
	`).Scan(&liveCount))
	require.Equal(t, 1, liveCount)

	compositeContent, err := migrations.FS.ReadFile("250_yingzo_agent_composite_platform.sql")
	require.NoError(t, err)
	compositeMigration := string(compositeContent)
	_, err = tx.ExecContext(ctx, compositeMigration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, compositeMigration)
	require.NoError(t, err, "composite platform migration must be idempotent")
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT platform FROM groups WHERE system_code = 'yingzo' AND deleted_at IS NULL
	`).Scan(&platform))
	require.Equal(t, "composite", platform)
}

// 245 把"系统聚合分组有且仅有一个"落成数据库不变式：不一致数据先收敛，
// 之后任何第二条存活 agent 行都写不进来。
func TestSingleLiveAgentGroupMigrationEnforcesUniqueness(t *testing.T) {
	content, err := migrations.FS.ReadFile("245_single_agent_group.sql")
	require.NoError(t, err)
	migration := string(content)

	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	schema := "single_agent_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = tx.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL search_path TO `+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		CREATE TABLE groups (
			id BIGSERIAL PRIMARY KEY,
			kind VARCHAR(20) NOT NULL DEFAULT 'standard',
			system_code VARCHAR(64),
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)
	`)
	require.NoError(t, err)

	// 两条手工造出来的存活 agent 行：迁移要保留 id 最小的一条。
	_, err = tx.ExecContext(ctx, `
		INSERT INTO groups (kind, system_code) VALUES ('agent', 'yingzo'), ('agent', 'rogue');
	`)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err, "migration must be idempotent")

	var liveCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM groups WHERE kind = 'agent' AND deleted_at IS NULL
	`).Scan(&liveCount))
	require.Equal(t, 1, liveCount, "只能留下一条存活 agent 分组")

	// 预期失败的插入会中止事务，用 SAVEPOINT 隔开后继续断言。
	_, err = tx.ExecContext(ctx, `SAVEPOINT probe`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO groups (kind, system_code) VALUES ('agent', 'second')`)
	require.Error(t, err, "唯一索引必须拒绝第二条存活 agent 分组")
	_, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT probe`)
	require.NoError(t, err)

	// 普通分组不受影响。
	_, err = tx.ExecContext(ctx, `INSERT INTO groups (kind, system_code) VALUES ('standard', NULL), ('standard', NULL)`)
	require.NoError(t, err)
}

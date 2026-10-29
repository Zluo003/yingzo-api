//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func cleanupRegistrationIPUsers(t *testing.T, prefix string) {
	t.Helper()
	ctx := context.Background()
	pattern := prefix + "%"
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM auth_identity_channels WHERE identity_id IN (SELECT ai.id FROM auth_identities ai JOIN users u ON u.id = ai.user_id WHERE u.email LIKE $1)", pattern)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM auth_identities WHERE user_id IN (SELECT id FROM users WHERE email LIKE $1)", pattern)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE email LIKE $1", pattern)
}

func registrationIPUser(email, clientIP string) *service.User {
	return &service.User{
		Email:             email,
		PasswordHash:      "test-password-hash",
		Role:              service.RoleUser,
		Status:            service.StatusActive,
		Concurrency:       1,
		RegistrationIP:    clientIP,
		RegistrationIPKey: ippkg.NetworkKey(clientIP),
	}
}

func TestUserRepositoryEnforcesRegistrationIPQuota(t *testing.T) {
	const prefix = "ipquota-seq-"
	cleanupRegistrationIPUsers(t, prefix)
	t.Cleanup(func() { cleanupRegistrationIPUsers(t, prefix) })

	ctx := context.Background()
	repo := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	quotaCtx := service.WithRegistrationIPQuota(ctx, service.RegistrationIPQuota{
		DailyLimit: 2,
		TotalLimit: 3,
		DayStart:   time.Now().Add(-time.Hour),
	})

	first := registrationIPUser(prefix+"a@example.com", "203.0.113.7")
	require.NoError(t, repo.Create(quotaCtx, first))
	require.NoError(t, repo.Create(quotaCtx, registrationIPUser(prefix+"b@example.com", "203.0.113.7")))

	stored, err := repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "203.0.113.7", stored.RegistrationIP)
	require.Equal(t, "203.0.113.7", stored.RegistrationIPKey)

	err = repo.Create(quotaCtx, registrationIPUser(prefix+"c@example.com", "203.0.113.7"))
	require.ErrorIs(t, err, service.ErrRegistrationIPDailyLimit)

	// 其它网络不受影响；同一 /64 的 IPv6 地址共享额度。
	require.NoError(t, repo.Create(quotaCtx, registrationIPUser(prefix+"d@example.com", "2001:db8:1:2::1")))
	require.NoError(t, repo.Create(quotaCtx, registrationIPUser(prefix+"e@example.com", "2001:db8:1:2::2")))
	err = repo.Create(quotaCtx, registrationIPUser(prefix+"f@example.com", "2001:db8:1:2:ffff::3"))
	require.ErrorIs(t, err, service.ErrRegistrationIPDailyLimit)

	// 超过 24 小时的账号只计入累计额度。
	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET created_at = NOW() - INTERVAL '2 days' WHERE email = $1", prefix+"a@example.com")
	require.NoError(t, err)
	require.NoError(t, repo.Create(quotaCtx, registrationIPUser(prefix+"g@example.com", "203.0.113.7")))
	err = repo.Create(quotaCtx, registrationIPUser(prefix+"h@example.com", "203.0.113.7"))
	require.ErrorIs(t, err, service.ErrRegistrationIPTotalLimit)

	// 管理员后台建号不携带额度，不受限制。
	require.NoError(t, repo.Create(ctx, registrationIPUser(prefix+"admin@example.com", "203.0.113.7")))

	total, err := repo.CountUsersByRegistrationIPKey(ctx, "203.0.113.7", nil)
	require.NoError(t, err)
	require.Equal(t, 4, total)

	// 软删除不得恢复注册额度。
	require.NoError(t, repo.Delete(ctx, first.ID))
	total, err = repo.CountUsersByRegistrationIPKey(ctx, "203.0.113.7", nil)
	require.NoError(t, err)
	require.Equal(t, 4, total)
}

func TestUserRepositorySerializesConcurrentRegistrationsPerNetwork(t *testing.T) {
	const prefix = "ipquota-race-"
	cleanupRegistrationIPUsers(t, prefix)
	t.Cleanup(func() { cleanupRegistrationIPUsers(t, prefix) })

	ctx := context.Background()
	repo := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	quotaCtx := service.WithRegistrationIPQuota(ctx, service.RegistrationIPQuota{
		DailyLimit: 1,
		DayStart:   time.Now().Add(-24 * time.Hour),
	})

	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = repo.Create(quotaCtx, registrationIPUser(fmt.Sprintf("%s%d@example.com", prefix, i), "198.51.100.42"))
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, service.ErrRegistrationIPDailyLimit):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, succeeded, "concurrent registrations from one network must not exceed the limit")
}

func TestUserRepositorySearchMatchesRegistrationIP(t *testing.T) {
	const prefix = "ipquota-search-"
	cleanupRegistrationIPUsers(t, prefix)
	t.Cleanup(func() { cleanupRegistrationIPUsers(t, prefix) })

	ctx := context.Background()
	repo := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	require.NoError(t, repo.Create(ctx, registrationIPUser(prefix+"a@example.com", "192.0.2.123")))
	require.NoError(t, repo.Create(ctx, registrationIPUser(prefix+"b@example.com", "198.51.100.9")))

	users, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, service.UserListFilters{Search: "192.0.2.123"})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, prefix+"a@example.com", users[0].Email)
	require.Equal(t, "192.0.2.123", users[0].RegistrationIP)
}

func TestIPBanRepositoryIntegration(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ip_bans")
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM ip_bans") })
	repo := NewIPBanRepository(integrationDB)

	actor := int64(7)
	stored, created, err := repo.Create(ctx, &service.IPBan{IP: "203.0.113.7", Reason: "spam", CreatedBy: &actor})
	require.NoError(t, err)
	require.True(t, created)
	require.Positive(t, stored.ID)
	require.Equal(t, int64(7), *stored.CreatedBy)

	again, created, err := repo.Create(ctx, &service.IPBan{IP: "203.0.113.7", Reason: "other"})
	require.NoError(t, err)
	require.False(t, created, "duplicate bans return the existing rule")
	require.Equal(t, stored.ID, again.ID)
	require.Equal(t, "spam", again.Reason)

	_, _, err = repo.Create(ctx, &service.IPBan{IP: "2001:db8:1:2::/64", Reason: "100%_bad"})
	require.NoError(t, err)

	all, err := repo.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	page, result, err := repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "2001:db8")
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, int64(1), result.Total)

	// LIKE 通配符按字面量匹配。
	page, _, err = repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "100%_")
	require.NoError(t, err)
	require.Len(t, page, 1)
	page, _, err = repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "%")
	require.NoError(t, err)
	require.Len(t, page, 1)

	deleted, err := repo.Delete(ctx, stored.ID)
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = repo.Delete(ctx, stored.ID)
	require.NoError(t, err)
	require.False(t, deleted)
}

func TestRegistrationIPRollingWindowIncludesPreviousCalendarDay(t *testing.T) {
	const prefix = "ipquota-rolling-"
	cleanupRegistrationIPUsers(t, prefix)
	t.Cleanup(func() { cleanupRegistrationIPUsers(t, prefix) })
	ctx := context.Background()
	repo := newUserRepositoryWithSQL(testEntClient(t), integrationDB)
	recent := registrationIPUser(prefix+"recent@example.com", "203.0.113.91")
	old := registrationIPUser(prefix+"old@example.com", "203.0.113.91")
	require.NoError(t, repo.Create(ctx, recent))
	require.NoError(t, repo.Create(ctx, old))
	_, err := integrationDB.ExecContext(ctx, "UPDATE users SET created_at = NOW() - INTERVAL '23 hours' WHERE id = $1", recent.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "UPDATE users SET created_at = NOW() - INTERVAL '25 hours' WHERE id = $1", old.ID)
	require.NoError(t, err)
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	count, err := repo.CountUsersByRegistrationIPKey(ctx, "203.0.113.91", &cutoff)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.NoError(t, repo.Delete(ctx, recent.ID))
	quotaCtx := service.WithRegistrationIPQuota(ctx, service.RegistrationIPQuota{DailyLimit: 1, DayStart: cutoff})
	err = repo.Create(quotaCtx, registrationIPUser(prefix+"new@example.com", "203.0.113.91"))
	require.ErrorIs(t, err, service.ErrRegistrationIPDailyLimit)
}

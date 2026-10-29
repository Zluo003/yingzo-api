//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type memoryIPBanRepo struct {
	mu      sync.Mutex
	bans    []IPBan
	nextID  int64
	listErr error
	lists   int
}

func (r *memoryIPBanRepo) ListAll(context.Context) ([]IPBan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return append([]IPBan(nil), r.bans...), nil
}

func (r *memoryIPBanRepo) List(_ context.Context, params pagination.PaginationParams, _ string) ([]IPBan, *pagination.PaginationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]IPBan(nil), r.bans...), &pagination.PaginationResult{Total: int64(len(r.bans)), Page: params.Page, PageSize: params.Limit()}, nil
}

func (r *memoryIPBanRepo) Create(_ context.Context, ban *IPBan) (*IPBan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.bans {
		if r.bans[i].IP == ban.IP {
			existing := r.bans[i]
			return &existing, false, nil
		}
	}
	r.nextID++
	stored := *ban
	stored.ID = r.nextID
	stored.CreatedAt = time.Now()
	r.bans = append(r.bans, stored)
	return &stored, true, nil
}

func (r *memoryIPBanRepo) Delete(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.bans {
		if r.bans[i].ID == id {
			r.bans = append(r.bans[:i], r.bans[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func TestNormalizeIPBanTarget(t *testing.T) {
	valid := map[string]string{
		"203.0.113.7":            "203.0.113.7",
		" 203.0.113.7 ":          "203.0.113.7",
		"203.0.113.7/32":         "203.0.113.7",
		"203.0.113.77/24":        "203.0.113.0/24",
		"2001:db8:1:2:3:4:5:6":   "2001:db8:1:2:3:4:5:6",
		"2001:db8:1:2:3::/64":    "2001:db8:1:2::/64",
		"::ffff:203.0.113.7":     "203.0.113.7",
		"::ffff:203.0.113.0/120": "203.0.113.0/24",
		"8.0.0.0/8":              "8.0.0.0/8",
	}
	for raw, want := range valid {
		_, normalized, err := NormalizeIPBanTarget(raw)
		require.NoError(t, err, "raw=%q", raw)
		require.Equal(t, want, normalized, "raw=%q", raw)
	}

	rejected := map[string]error{
		"":                ErrIPBanInvalidTarget,
		"not-an-ip":       ErrIPBanInvalidTarget,
		"203.0.113.7/33":  ErrIPBanInvalidTarget,
		"::ffff:0:0/90":   ErrIPBanInvalidTarget,
		"0.0.0.0/0":       ErrIPBanRangeTooBroad,
		"8.0.0.0/7":       ErrIPBanRangeTooBroad,
		"2001::/15":       ErrIPBanRangeTooBroad,
		"127.0.0.1":       ErrIPBanReservedTarget,
		"10.1.2.3":        ErrIPBanReservedTarget,
		"172.17.0.1":      ErrIPBanReservedTarget,
		"192.168.0.0/16":  ErrIPBanReservedTarget,
		"172.0.0.0/8":     ErrIPBanReservedTarget, // 与 172.16.0.0/12 重叠
		"::1":             ErrIPBanReservedTarget,
		"fd00::1":         ErrIPBanReservedTarget,
		"fe80::1":         ErrIPBanReservedTarget,
		"224.0.0.1":       ErrIPBanReservedTarget,
		"169.254.169.254": ErrIPBanReservedTarget,
	}
	for raw, want := range rejected {
		_, _, err := NormalizeIPBanTarget(raw)
		require.ErrorIs(t, err, want, "raw=%q", raw)
	}
}

func TestIPBanBanAndMatch(t *testing.T) {
	repo := &memoryIPBanRepo{}
	svc := NewIPBanService(repo, &userRepoStub{})
	ctx := context.Background()

	require.False(t, svc.IsBanned(ctx, "203.0.113.7"))

	ban, created, err := svc.Ban(ctx, "203.0.113.7", "  bulk signups  ", 42, "198.51.100.1")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "203.0.113.7", ban.IP)
	require.Equal(t, "bulk signups", ban.Reason)
	require.Equal(t, int64(42), *ban.CreatedBy)

	require.True(t, svc.IsBanned(ctx, "203.0.113.7"), "a ban applies immediately on this instance")
	require.True(t, svc.IsBanned(ctx, "203.0.113.7:51234"))
	require.True(t, svc.IsBanned(ctx, "::ffff:203.0.113.7"))
	require.False(t, svc.IsBanned(ctx, "203.0.113.8"))
	require.False(t, svc.IsBanned(ctx, ""))

	again, created, err := svc.Ban(ctx, "203.0.113.7/32", "", 42, "198.51.100.1")
	require.NoError(t, err)
	require.False(t, created, "banning the same address twice is idempotent")
	require.Equal(t, ban.ID, again.ID)
}

func TestIPBanMatchesIPv6Network(t *testing.T) {
	repo := &memoryIPBanRepo{}
	svc := NewIPBanService(repo, &userRepoStub{})
	ctx := context.Background()

	_, _, err := svc.Ban(ctx, "2001:db8:1:2::/64", "", 1, "198.51.100.1")
	require.NoError(t, err)

	match, banned := svc.Match(ctx, "2001:db8:1:2:aaaa:bbbb:cccc:dddd")
	require.True(t, banned)
	require.Equal(t, "2001:db8:1:2::/64", match.IP)
	require.False(t, svc.IsBanned(ctx, "2001:db8:1:3::1"))
}

func TestIPBanRefusesToLockOutTheActor(t *testing.T) {
	repo := &memoryIPBanRepo{}
	svc := NewIPBanService(repo, &userRepoStub{})

	_, _, err := svc.Ban(context.Background(), "203.0.113.0/24", "", 1, "203.0.113.50")
	require.ErrorIs(t, err, ErrIPBanSelf)
	require.Empty(t, repo.bans)

	_, _, err = svc.Ban(context.Background(), "2001:db8:1:2::/64", "", 1, "2001:db8:1:2::abcd")
	require.ErrorIs(t, err, ErrIPBanSelf)
}

func TestIPBanUnban(t *testing.T) {
	repo := &memoryIPBanRepo{}
	svc := NewIPBanService(repo, &userRepoStub{})
	ctx := context.Background()
	ban, _, err := svc.Ban(ctx, "203.0.113.7", "", 1, "198.51.100.1")
	require.NoError(t, err)

	require.NoError(t, svc.Unban(ctx, ban.ID))
	require.False(t, svc.IsBanned(ctx, "203.0.113.7"), "an unban applies immediately on this instance")
	require.ErrorIs(t, svc.Unban(ctx, ban.ID), ErrIPBanNotFound)
}

func TestIPBanKeepsPreviousRulesWhenReloadFails(t *testing.T) {
	repo := &memoryIPBanRepo{}
	svc := NewIPBanService(repo, &userRepoStub{})
	ctx := context.Background()
	_, _, err := svc.Ban(ctx, "203.0.113.7", "", 1, "198.51.100.1")
	require.NoError(t, err)

	repo.listErr = errors.New("db down")
	svc.reload(ctx)

	require.True(t, svc.IsBanned(ctx, "203.0.113.7"), "a database outage must not silently lift existing bans")
}

func TestIPBanFailsOpenWhenNothingCouldBeLoaded(t *testing.T) {
	repo := &memoryIPBanRepo{listErr: errors.New("db down")}
	svc := NewIPBanService(repo, &userRepoStub{})

	require.False(t, svc.IsBanned(context.Background(), "203.0.113.7"))
	require.Equal(t, 1, repo.lists)
	// 失败后的空快照有短暂有效期，期间的请求不会反复打数据库。
	require.False(t, svc.IsBanned(context.Background(), "203.0.113.7"))
	require.Equal(t, 1, repo.lists)
}

func TestIPBanServesCachedRulesWithoutDatabaseRoundTrips(t *testing.T) {
	repo := &memoryIPBanRepo{bans: []IPBan{{ID: 1, IP: "203.0.113.7"}}}
	svc := NewIPBanService(repo, &userRepoStub{})
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		require.True(t, svc.IsBanned(ctx, "203.0.113.7"))
	}
	require.Equal(t, 1, repo.lists)
}

func TestIPBanInspect(t *testing.T) {
	repo := &memoryIPBanRepo{}
	users := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{},
		totals:       map[string]int{"203.0.113.7": 6, "2001:db8:1:2::/64": 2},
		today:        map[string]int{"203.0.113.7": 4},
	}
	svc := NewIPBanService(repo, users)
	ctx := context.Background()

	inspection, err := svc.Inspect(ctx, "203.0.113.7", "198.51.100.1")
	require.NoError(t, err)
	require.Equal(t, "203.0.113.7", inspection.NetworkKey)
	require.Nil(t, inspection.Ban)
	require.Equal(t, 6, inspection.RegisteredTotal)
	require.Equal(t, 4, inspection.RegisteredToday)
	require.True(t, inspection.Bannable)

	_, _, err = svc.Ban(ctx, "203.0.113.7", "spam", 1, "198.51.100.1")
	require.NoError(t, err)
	inspection, err = svc.Inspect(ctx, "203.0.113.7", "198.51.100.1")
	require.NoError(t, err)
	require.NotNil(t, inspection.Ban)
	require.Equal(t, "spam", inspection.Ban.Reason)

	inspection, err = svc.Inspect(ctx, "2001:db8:1:2::99", "198.51.100.1")
	require.NoError(t, err)
	require.Equal(t, "2001:db8:1:2::/64", inspection.NetworkKey, "IPv6 is inspected and banned per /64")
	require.Equal(t, 2, inspection.RegisteredTotal)

	inspection, err = svc.Inspect(ctx, "10.0.0.8", "198.51.100.1")
	require.NoError(t, err)
	require.False(t, inspection.Bannable)
	require.Equal(t, "IP_BAN_RESERVED_TARGET", inspection.NotBannableReason)

	inspection, err = svc.Inspect(ctx, "198.51.100.1", "198.51.100.1")
	require.NoError(t, err)
	require.False(t, inspection.Bannable)
	require.Equal(t, "IP_BAN_SELF", inspection.NotBannableReason)

	_, err = svc.Inspect(ctx, "nonsense", "198.51.100.1")
	require.ErrorIs(t, err, ErrIPBanInvalidTarget)
}

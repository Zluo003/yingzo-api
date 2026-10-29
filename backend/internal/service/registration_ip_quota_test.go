//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// ipQuotaUserRepoStub 在通用用户仓储桩上补充按注册网络计数的能力，
// 并记录建号时 ctx 上挂载的注册额度，用于断言仓储会在锁内复核。
type ipQuotaUserRepoStub struct {
	*userRepoStub
	totals        map[string]int
	today         map[string]int
	countErr      error
	countCalls    int
	createErr     error
	createdQuotas []RegistrationIPQuota
	createdHasQ   []bool
}

func (s *ipQuotaUserRepoStub) CountUsersByRegistrationIPKey(_ context.Context, key string, since *time.Time) (int, error) {
	s.countCalls++
	if s.countErr != nil {
		return 0, s.countErr
	}
	if since != nil {
		return s.today[key], nil
	}
	return s.totals[key], nil
}

func (s *ipQuotaUserRepoStub) recordQuota(ctx context.Context) {
	quota, ok := RegistrationIPQuotaFromContext(ctx)
	s.createdQuotas = append(s.createdQuotas, quota)
	s.createdHasQ = append(s.createdHasQ, ok)
}

func (s *ipQuotaUserRepoStub) Create(ctx context.Context, user *User) error {
	s.recordQuota(ctx)
	if s.createErr != nil {
		return s.createErr
	}
	return s.userRepoStub.Create(ctx, user)
}

func (s *ipQuotaUserRepoStub) CreateWithEmailAliasGuard(ctx context.Context, user *User) error {
	s.recordQuota(ctx)
	if s.createErr != nil {
		return s.createErr
	}
	return s.userRepoStub.CreateWithEmailAliasGuard(ctx, user)
}

func newIPQuotaAuthService(repo *ipQuotaUserRepoStub, settings map[string]string) *AuthService {
	cfg := &config.Config{
		JWT:     config.JWTConfig{Secret: "test-secret", ExpireHour: 1},
		Default: config.DefaultConfig{UserBalance: 1, UserConcurrency: 1},
	}
	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)
	return NewAuthService(nil, repo, nil, nil, cfg, settingService, nil, nil, nil, nil, nil, nil, nil)
}

func registrationRequestContext(ip string) context.Context {
	return WithSessionBinding(context.Background(), &SessionBinding{IP: ip, UserAgent: "test"})
}

func ipQuotaSettings(daily, total string) map[string]string {
	return map[string]string{
		SettingKeyRegistrationEnabled:      "true",
		SettingKeyRegistrationIPDailyLimit: daily,
		SettingKeyRegistrationIPTotalLimit: total,
	}
}

func TestRegistrationIPQuotaCheck(t *testing.T) {
	cases := []struct {
		name  string
		quota RegistrationIPQuota
		total int
		today int
		want  error
	}{
		{"unlimited", RegistrationIPQuota{}, 100, 100, nil},
		{"daily under", RegistrationIPQuota{DailyLimit: 3}, 10, 2, nil},
		{"daily reached", RegistrationIPQuota{DailyLimit: 3}, 10, 3, ErrRegistrationIPDailyLimit},
		{"total reached", RegistrationIPQuota{TotalLimit: 5}, 5, 0, ErrRegistrationIPTotalLimit},
		{"total wins over daily", RegistrationIPQuota{DailyLimit: 1, TotalLimit: 5}, 5, 1, ErrRegistrationIPTotalLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.quota.Check(tc.total, tc.today)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestParseRegistrationIPLimit(t *testing.T) {
	require.Equal(t, 0, parseRegistrationIPLimit(""))
	require.Equal(t, 0, parseRegistrationIPLimit("abc"))
	require.Equal(t, 0, parseRegistrationIPLimit("-3"))
	require.Equal(t, 7, parseRegistrationIPLimit(" 7 "))
	require.Equal(t, RegistrationIPLimitMax, parseRegistrationIPLimit("999999999"))
	require.Equal(t, 0, NormalizeRegistrationIPLimit(-1))
	require.Equal(t, RegistrationIPLimitMax, NormalizeRegistrationIPLimit(RegistrationIPLimitMax+1))
}

func TestRegisterStampsRegistrationIPWithoutLimits(t *testing.T) {
	repo := &ipQuotaUserRepoStub{userRepoStub: &userRepoStub{nextID: 7}}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("0", "0"))

	_, user, err := svc.RegisterWithVerification(registrationRequestContext("2001:db8:1:2::abcd"), "a@example.com", "password123", "", "", "", "")

	require.NoError(t, err)
	require.Equal(t, "2001:db8:1:2::abcd", user.RegistrationIP)
	require.Equal(t, "2001:db8:1:2::/64", user.RegistrationIPKey)
	require.Zero(t, repo.countCalls, "no limit configured, registrations must not be counted")
	require.Equal(t, []bool{false}, repo.createdHasQ)
}

func TestRegisterRejectsWhenRegistrationIPDailyLimitReached(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		today:        map[string]int{"203.0.113.10": 2},
		totals:       map[string]int{"203.0.113.10": 2},
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("2", "0"))

	_, _, err := svc.RegisterWithVerification(registrationRequestContext("203.0.113.10"), "a@example.com", "password123", "", "", "", "")

	require.ErrorIs(t, err, ErrRegistrationIPDailyLimit)
	require.Empty(t, repo.created, "no account may be created once the daily limit is reached")
}

func TestRegisterRejectsWhenRegistrationIPTotalLimitReached(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		totals:       map[string]int{"2001:db8:1:2::/64": 5},
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("0", "5"))

	// 同一 /64 内换一个地址仍然算同一个网络。
	_, _, err := svc.RegisterWithVerification(registrationRequestContext("2001:db8:1:2:ffff::9"), "a@example.com", "password123", "", "", "", "")

	require.ErrorIs(t, err, ErrRegistrationIPTotalLimit)
	require.Empty(t, repo.created)
}

func TestRegisterAttachesQuotaForRepositoryRecheck(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		today:        map[string]int{"203.0.113.10": 1},
		totals:       map[string]int{"203.0.113.10": 4},
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("3", "10"))

	_, user, err := svc.RegisterWithVerification(registrationRequestContext("203.0.113.10"), "a@example.com", "password123", "", "", "", "")

	require.NoError(t, err)
	require.Equal(t, "203.0.113.10", user.RegistrationIP)
	require.Equal(t, []bool{true}, repo.createdHasQ)
	require.Equal(t, 3, repo.createdQuotas[0].DailyLimit)
	require.Equal(t, 10, repo.createdQuotas[0].TotalLimit)
	require.False(t, repo.createdQuotas[0].DayStart.IsZero())
}

func TestRegisterPassesThroughRepositoryRegistrationIPLimit(t *testing.T) {
	// 并发注册时仓储在锁内复核失败：错误必须原样返回，而不是被吞成 SERVICE_UNAVAILABLE。
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		createErr:    ErrRegistrationIPDailyLimit,
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("3", "0"))

	_, _, err := svc.RegisterWithVerification(registrationRequestContext("203.0.113.10"), "a@example.com", "password123", "", "", "", "")

	require.ErrorIs(t, err, ErrRegistrationIPDailyLimit)
}

func TestRegistrationIPQuotaAlsoLimitsInternalAddresses(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		today:        map[string]int{"10.0.0.5": 99},
		totals:       map[string]int{"10.0.0.5": 99},
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("1", "1"))

	_, user, err := svc.RegisterWithVerification(registrationRequestContext("10.0.0.5"), "a@example.com", "password123", "", "", "", "")

	require.ErrorIs(t, err, ErrRegistrationIPTotalLimit)
	require.Nil(t, user)
	require.Empty(t, repo.created)
	require.Positive(t, repo.countCalls)
}

func TestRegistrationIPQuotaFailsClosedWhenCountingFails(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 7},
		countErr:     errors.New("db down"),
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("1", "0"))

	_, _, err := svc.RegisterWithVerification(registrationRequestContext("203.0.113.10"), "a@example.com", "password123", "", "", "", "")

	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Empty(t, repo.created)
}

func TestSendVerifyCodeAsyncStopsBeforeEmailWhenRegistrationIPQuotaReached(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{},
		today:        map[string]int{"203.0.113.10": 1},
	}
	settings := ipQuotaSettings("1", "0")
	settings[SettingKeyEmailVerifyEnabled] = "true"
	svc := newIPQuotaAuthService(repo, settings)

	_, err := svc.SendVerifyCodeAsync(registrationRequestContext("203.0.113.10"), "a@example.com")
	require.ErrorIs(t, err, ErrRegistrationIPDailyLimit)

	// 额度未满时继续走发信流程（测试装配没有邮件队列，因此停在队列检查上）。
	repo.today["203.0.113.10"] = 0
	_, err = svc.SendVerifyCodeAsync(registrationRequestContext("203.0.113.10"), "a@example.com")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRegistrationIPDailyLimit)
}

func TestOAuthFirstLoginStampsRegistrationIP(t *testing.T) {
	repo := &ipQuotaUserRepoStub{userRepoStub: &userRepoStub{nextID: 9}}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("0", "0"))

	_, user, err := svc.LoginOrRegisterOAuth(registrationRequestContext("198.51.100.20"), "oauth@example.com", "oauth-user")

	require.NoError(t, err)
	require.Equal(t, "198.51.100.20", user.RegistrationIP)
	require.Equal(t, "198.51.100.20", user.RegistrationIPKey)
}

func TestOAuthFirstLoginHonoursRegistrationIPQuota(t *testing.T) {
	repo := &ipQuotaUserRepoStub{
		userRepoStub: &userRepoStub{nextID: 9},
		totals:       map[string]int{"198.51.100.20": 3},
	}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("0", "3"))

	_, _, err := svc.LoginOrRegisterOAuth(registrationRequestContext("198.51.100.20"), "oauth@example.com", "oauth-user")

	require.ErrorIs(t, err, ErrRegistrationIPTotalLimit)
	require.Empty(t, repo.created)
}

func TestRegistrationIPQuotaUsesRolling24Hours(t *testing.T) {
	svc := NewSettingService(&settingRepoStub{values: ipQuotaSettings("2", "0")}, &config.Config{})
	before := time.Now().UTC().Add(-24 * time.Hour)
	quota, err := svc.GetRegistrationIPQuota(context.Background())
	require.NoError(t, err)
	after := time.Now().UTC().Add(-24 * time.Hour)
	require.False(t, quota.DayStart.Before(before))
	require.False(t, quota.DayStart.After(after))
}

func TestRegistrationIPQuotaRejectsMissingSourceWhenEnabled(t *testing.T) {
	repo := &ipQuotaUserRepoStub{userRepoStub: &userRepoStub{nextID: 7}}
	svc := newIPQuotaAuthService(repo, ipQuotaSettings("2", "0"))
	_, _, err := svc.RegisterWithVerification(context.Background(), "a@example.com", "password123", "", "", "", "")
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Empty(t, repo.created)
}

func TestRegistrationIPQuotaSettingsFailureDoesNotDisableQuota(t *testing.T) {
	svc := NewSettingService(&settingRepoStub{err: errors.New("settings unavailable")}, &config.Config{})
	_, err := svc.GetRegistrationIPQuota(context.Background())
	require.ErrorIs(t, err, ErrServiceUnavailable)
}

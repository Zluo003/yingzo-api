package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

var (
	ErrRegistrationIPDailyLimit = infraerrors.TooManyRequests(
		"REGISTRATION_IP_DAILY_LIMIT",
		"too many accounts have been registered from your network in the last 24 hours, please try again later",
	)
	ErrRegistrationIPTotalLimit = infraerrors.Forbidden(
		"REGISTRATION_IP_TOTAL_LIMIT",
		"no more accounts can be registered from your network",
	)
)

// RegistrationIPLimitMax 是注册额度设置的上限，防止误填超大数字。
const RegistrationIPLimitMax = 1_000_000

// RegistrationIPQuota 自助注册时按来源网络（IPv4 地址 / IPv6 /64 网段）的注册额度，
// 0 表示不限制。
type RegistrationIPQuota struct {
	DailyLimit int
	TotalLimit int
	// DayStart is the rolling 24-hour cutoff. Field/key names retain daily for
	// compatibility; the quota never resets at midnight.
	DayStart time.Time
}

// Active 报告是否配置了任一额度。
func (q RegistrationIPQuota) Active() bool {
	return q.DailyLimit > 0 || q.TotalLimit > 0
}

// Check 根据该网络已注册的账号数（累计 / 最近 24 小时）判断能否再注册一个账号。
func (q RegistrationIPQuota) Check(total, today int) error {
	if q.TotalLimit > 0 && total >= q.TotalLimit {
		return ErrRegistrationIPTotalLimit
	}
	if q.DailyLimit > 0 && today >= q.DailyLimit {
		return ErrRegistrationIPDailyLimit
	}
	return nil
}

// RegistrationIPRepository 按注册网络键统计账号数（含软删除）；since 非 nil 时只统计
// 该时刻及之后创建的账号。
type RegistrationIPRepository interface {
	CountUsersByRegistrationIPKey(ctx context.Context, key string, since *time.Time) (int, error)
}

// CountRegistrationIPUsers 统计某注册网络的累计与最近 24 小时账号数。
func CountRegistrationIPUsers(ctx context.Context, repo RegistrationIPRepository, key string, dayStart time.Time) (total, today int, err error) {
	total, err = repo.CountUsersByRegistrationIPKey(ctx, key, nil)
	if err != nil {
		return 0, 0, err
	}
	today, err = repo.CountUsersByRegistrationIPKey(ctx, key, &dayStart)
	if err != nil {
		return 0, 0, err
	}
	return total, today, nil
}

type registrationIPQuotaContextKey struct{}

// WithRegistrationIPQuota 把注册额度挂到 ctx 上。用户仓储在创建用户时持有该网络的
// 注册锁并在同一事务内复核额度，保证并发注册无法越过额度；服务层的预检只负责
// 尽早返回明确的错误。
func WithRegistrationIPQuota(ctx context.Context, quota RegistrationIPQuota) context.Context {
	return context.WithValue(ctx, registrationIPQuotaContextKey{}, quota)
}

// RegistrationIPQuotaFromContext 读取 WithRegistrationIPQuota 挂载的注册额度。
func RegistrationIPQuotaFromContext(ctx context.Context) (RegistrationIPQuota, bool) {
	if ctx == nil {
		return RegistrationIPQuota{}, false
	}
	quota, ok := ctx.Value(registrationIPQuotaContextKey{}).(RegistrationIPQuota)
	return quota, ok
}

func isRegistrationIPLimitError(err error) bool {
	return errors.Is(err, ErrRegistrationIPDailyLimit) || errors.Is(err, ErrRegistrationIPTotalLimit)
}

func parseRegistrationIPLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0
	}
	if value > RegistrationIPLimitMax {
		return RegistrationIPLimitMax
	}
	return value
}

// NormalizeRegistrationIPLimit 把管理端提交的额度收敛到 [0, RegistrationIPLimitMax]。
func NormalizeRegistrationIPLimit(value int) int {
	if value < 0 {
		return 0
	}
	if value > RegistrationIPLimitMax {
		return RegistrationIPLimitMax
	}
	return value
}

// GetRegistrationIPQuota fails closed when settings cannot be loaded so a
// transient store failure cannot silently disable an administrator's quota.
func (s *SettingService) GetRegistrationIPQuota(ctx context.Context) (RegistrationIPQuota, error) {
	quota := RegistrationIPQuota{DayStart: time.Now().UTC().Add(-24 * time.Hour)}
	if s == nil || s.settingRepo == nil {
		return quota, ErrServiceUnavailable
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyRegistrationIPDailyLimit,
		SettingKeyRegistrationIPTotalLimit,
	})
	if err != nil {
		slog.Warn("read registration IP quota settings failed", "error", err)
		return quota, ErrServiceUnavailable
	}
	quota.DailyLimit = parseRegistrationIPLimit(values[SettingKeyRegistrationIPDailyLimit])
	quota.TotalLimit = parseRegistrationIPLimit(values[SettingKeyRegistrationIPTotalLimit])
	return quota, nil
}

// registrationClientIP 返回当前请求的客户端 IP，与审计日志、会话绑定同源：
// SessionBindingContext 中间件按可信代理 / 转发头配置解析后注入 ctx。
func registrationClientIP(ctx context.Context) string {
	if binding := SessionBindingFromContext(ctx); binding != nil {
		if addr, ok := ippkg.ParseClientAddr(binding.IP); ok {
			return addr.String()
		}
	}
	return ""
}

// stampRegistrationIP 为自助注册的新用户写入注册 IP；配置了按 IP 注册额度时先预检，
// 并把额度挂到返回的 ctx 上，由用户仓储在建号的同一把锁内复核。
// 管理员后台创建的用户不经过这里，注册 IP 保持为空。
func (s *AuthService) stampRegistrationIP(ctx context.Context, user *User) (context.Context, error) {
	clientIP := registrationClientIP(ctx)
	user.RegistrationIP = clientIP
	user.RegistrationIPKey = ippkg.NetworkKey(clientIP)

	quota, err := s.settingService.GetRegistrationIPQuota(ctx)
	if err != nil {
		return ctx, err
	}
	if !quota.Active() {
		return ctx, nil
	}
	if user.RegistrationIPKey == "" {
		return ctx, ErrServiceUnavailable
	}
	if err := s.checkRegistrationIPQuota(ctx, user.RegistrationIPKey, quota); err != nil {
		return ctx, err
	}
	return WithRegistrationIPQuota(ctx, quota), nil
}

// ensureRegistrationIPQuotaAvailable 供发送注册验证码等前置步骤调用：
// 该网络的注册额度已满时直接拒绝，不再消耗邮件配额。
func (s *AuthService) ensureRegistrationIPQuotaAvailable(ctx context.Context) error {
	clientIP := registrationClientIP(ctx)
	if s == nil || s.settingService == nil {
		return ErrServiceUnavailable
	}
	quota, err := s.settingService.GetRegistrationIPQuota(ctx)
	if err != nil {
		return err
	}
	if !quota.Active() {
		return nil
	}
	key := ippkg.NetworkKey(clientIP)
	if key == "" {
		return ErrServiceUnavailable
	}
	return s.checkRegistrationIPQuota(ctx, key, quota)
}

func (s *AuthService) checkRegistrationIPQuota(ctx context.Context, key string, quota RegistrationIPQuota) error {
	repo, ok := s.userRepo.(RegistrationIPRepository)
	if !ok {
		// 生产装配必须提供计数能力；没有数据库的 unit 测试桩保留旧路径。
		if s.entClient != nil {
			return ErrServiceUnavailable
		}
		return nil
	}
	total, today, err := CountRegistrationIPUsers(ctx, repo, key, quota.DayStart)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to count registrations for network %s: %v", key, err)
		return ErrServiceUnavailable
	}
	return quota.Check(total, today)
}

package service

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"golang.org/x/sync/singleflight"
)

var (
	ErrIPBanInvalidTarget  = infraerrors.BadRequest("IP_BAN_INVALID_TARGET", "invalid IP address or CIDR range")
	ErrIPBanReservedTarget = infraerrors.BadRequest("IP_BAN_RESERVED_TARGET", "loopback, private and other reserved addresses cannot be banned; they usually belong to the reverse proxy")
	ErrIPBanRangeTooBroad  = infraerrors.BadRequest("IP_BAN_RANGE_TOO_BROAD", "the CIDR range is too broad (IPv4 needs at least /8, IPv6 at least /16)")
	ErrIPBanSelf           = infraerrors.BadRequest("IP_BAN_SELF", "you cannot ban the network you are currently using")
	ErrIPBanNotFound       = infraerrors.NotFound("IP_BAN_NOT_FOUND", "IP ban not found")
	// ErrIPBanned 是被封禁来源访问任意接口时的响应。
	ErrIPBanned = infraerrors.Forbidden("IP_BANNED", "access from your network has been blocked")
)

const (
	ipBanReasonMaxRunes = 255

	// 封禁规则在各实例进程内缓存；本实例的增删立即生效，其它实例在 TTL 内跟进。
	ipBanCacheTTL      = 15 * time.Second
	ipBanErrorRetryTTL = 5 * time.Second
	ipBanLoadTimeout   = 3 * time.Second

	ipv4MinBanPrefixBits = 8
	ipv6MinBanPrefixBits = 16
)

// ipBanReservedPrefixes 不允许封禁的地址段：命中它们通常意味着反代未正确传递真实 IP，
// 封禁会把整条反代链路上的所有用户一起挡在门外。
var ipBanReservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// IPBan 是一条 IP / 网段封禁规则。
type IPBan struct {
	ID        int64
	IP        string // 规范化后的单个 IP 或 CIDR
	Reason    string
	CreatedBy *int64
	CreatedAt time.Time
}

// IPBanRepository 持久化封禁规则。
type IPBanRepository interface {
	ListAll(ctx context.Context) ([]IPBan, error)
	List(ctx context.Context, params pagination.PaginationParams, search string) ([]IPBan, *pagination.PaginationResult, error)
	// Create 插入规则；同一 IP / 网段已存在时不报错，返回已存在的那条（created=false）。
	Create(ctx context.Context, ban *IPBan) (stored *IPBan, created bool, err error)
	Delete(ctx context.Context, id int64) (bool, error)
}

// IPInspection 是管理端查看某个 IP 时的汇总信息。
type IPInspection struct {
	IP string
	// NetworkKey 为注册额度的计数单位，也是建议的封禁目标：IPv4 为地址本身，IPv6 为 /64 网段。
	NetworkKey      string
	Ban             *IPBan
	RegisteredTotal int
	RegisteredToday int
	Bannable        bool
	// NotBannableReason 不可封禁时的原因码（IP_BAN_RESERVED_TARGET / IP_BAN_SELF）。
	NotBannableReason string
}

type ipBanRule struct {
	prefix netip.Prefix
	ban    IPBan
}

type ipBanSnapshot struct {
	exact     map[netip.Addr]IPBan
	ranges    []ipBanRule
	expiresAt int64 // unix nano
}

func (s *ipBanSnapshot) match(addr netip.Addr) (IPBan, bool) {
	if s == nil {
		return IPBan{}, false
	}
	if ban, ok := s.exact[addr]; ok {
		return ban, true
	}
	for _, rule := range s.ranges {
		if rule.prefix.Contains(addr) {
			return rule.ban, true
		}
	}
	return IPBan{}, false
}

// IPBanService 管理 IP 封禁规则，并为每个请求提供不阻塞的封禁判定。
type IPBanService struct {
	repo       IPBanRepository
	userRepo   UserRepository
	snapshot   atomic.Pointer[ipBanSnapshot]
	refreshing atomic.Bool
	loadSF     singleflight.Group
	loadMu     sync.Mutex // serialize refreshes, including loads started before a write
}

func NewIPBanService(repo IPBanRepository, userRepo UserRepository) *IPBanService {
	return &IPBanService{repo: repo, userRepo: userRepo}
}

// IsBanned 判断客户端 IP 是否被封禁，供全局中间件在每个请求上调用。
func (s *IPBanService) IsBanned(ctx context.Context, clientIP string) bool {
	_, banned := s.Match(ctx, clientIP)
	return banned
}

// Match 返回命中客户端 IP 的封禁规则。缓存过期后后台刷新、当前请求继续使用旧规则，
// 热路径只有进程启动后的第一次判定会等待数据库。
func (s *IPBanService) Match(ctx context.Context, clientIP string) (*IPBan, bool) {
	if s == nil || s.repo == nil {
		return nil, false
	}
	addr, ok := ippkg.ParseClientAddr(clientIP)
	if !ok {
		return nil, false
	}
	ban, banned := s.currentSnapshot(ctx).match(addr)
	if !banned {
		return nil, false
	}
	return &ban, true
}

func (s *IPBanService) currentSnapshot(ctx context.Context) *ipBanSnapshot {
	current := s.snapshot.Load()
	if current != nil {
		if time.Now().UnixNano() >= current.expiresAt && s.refreshing.CompareAndSwap(false, true) {
			go func() {
				defer s.refreshing.Store(false)
				s.reload(context.Background())
			}()
		}
		return current
	}
	return s.reload(ctx)
}

// reload 从数据库重建规则快照。读取失败时保留旧快照并短暂延后重试（fail-open：
// 数据库故障不应让全站流量被拒）。
func (s *IPBanService) reload(ctx context.Context) *ipBanSnapshot {
	result, _, _ := s.loadSF.Do("ip_bans", func() (any, error) {
		s.loadMu.Lock()
		defer s.loadMu.Unlock()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ipBanLoadTimeout)
		defer cancel()
		bans, err := s.repo.ListAll(loadCtx)
		if err != nil {
			slog.Warn("load ip bans failed, keeping previous rules", "error", err)
			fallback := &ipBanSnapshot{exact: map[netip.Addr]IPBan{}}
			if previous := s.snapshot.Load(); previous != nil {
				copied := *previous
				fallback = &copied
			}
			fallback.expiresAt = time.Now().Add(ipBanErrorRetryTTL).UnixNano()
			s.snapshot.Store(fallback)
			return fallback, nil
		}
		next := buildIPBanSnapshot(bans)
		s.snapshot.Store(next)
		return next, nil
	})
	snapshot, _ := result.(*ipBanSnapshot)
	return snapshot
}

// refreshAfterWrite 在增删规则后立即重建快照。先 Forget 再加载，避免并入写入之前
// 已发起的那次加载而拿到旧规则。
func (s *IPBanService) refreshAfterWrite(ctx context.Context) {
	s.loadSF.Forget("ip_bans")
	s.reload(ctx)
}

func buildIPBanSnapshot(bans []IPBan) *ipBanSnapshot {
	snapshot := &ipBanSnapshot{
		exact:     make(map[netip.Addr]IPBan, len(bans)),
		expiresAt: time.Now().Add(ipBanCacheTTL).UnixNano(),
	}
	for _, ban := range bans {
		prefix, _, err := parseIPBanTarget(ban.IP)
		if err != nil {
			slog.Warn("skip unparsable ip ban rule", "id", ban.ID, "ip", ban.IP)
			continue
		}
		if prefix.IsSingleIP() {
			snapshot.exact[prefix.Addr()] = ban
			continue
		}
		snapshot.ranges = append(snapshot.ranges, ipBanRule{prefix: prefix, ban: ban})
	}
	return snapshot
}

// List 分页列出封禁规则。
func (s *IPBanService) List(ctx context.Context, page, pageSize int, search string) ([]IPBan, *pagination.PaginationResult, error) {
	return s.repo.List(ctx, pagination.PaginationParams{Page: page, PageSize: pageSize}, strings.TrimSpace(search))
}

// Ban 封禁一个 IP 或网段。actorIP 为操作者当前的客户端 IP，用于拒绝把自己锁在门外。
func (s *IPBanService) Ban(ctx context.Context, target, reason string, actorID int64, actorIP string) (*IPBan, bool, error) {
	prefix, normalized, err := NormalizeIPBanTarget(target)
	if err != nil {
		return nil, false, err
	}
	if actorAddr, ok := ippkg.ParseClientAddr(actorIP); ok && prefix.Contains(actorAddr) {
		return nil, false, ErrIPBanSelf
	}
	ban := &IPBan{IP: normalized, Reason: truncateRunes(strings.TrimSpace(reason), ipBanReasonMaxRunes)}
	if actorID > 0 {
		ban.CreatedBy = &actorID
	}
	stored, created, err := s.repo.Create(ctx, ban)
	if err != nil {
		return nil, false, err
	}
	s.refreshAfterWrite(ctx)
	return stored, created, nil
}

// Unban 删除一条封禁规则。
func (s *IPBanService) Unban(ctx context.Context, id int64) error {
	deleted, err := s.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrIPBanNotFound
	}
	s.refreshAfterWrite(ctx)
	return nil
}

// Inspect 汇总某个 IP 的封禁状态与注册情况（管理端用户列表点击注册 IP 时展示）。
func (s *IPBanService) Inspect(ctx context.Context, rawIP, actorIP string) (*IPInspection, error) {
	addr, ok := ippkg.ParseClientAddr(rawIP)
	if !ok {
		return nil, ErrIPBanInvalidTarget
	}
	key := ippkg.NetworkKey(addr.String())
	inspection := &IPInspection{IP: addr.String(), NetworkKey: key}

	// 管理端需要准确状态：先同步刷新本实例的规则，再判定。
	if ban, banned := s.reload(ctx).match(addr); banned {
		inspection.Ban = &ban
	}

	if repo, ok := s.userRepo.(RegistrationIPRepository); ok {
		total, today, err := CountRegistrationIPUsers(ctx, repo, key, time.Now().UTC().Add(-24*time.Hour))
		if err != nil {
			return nil, err
		}
		inspection.RegisteredTotal = total
		inspection.RegisteredToday = today
	}

	prefix, _, err := NormalizeIPBanTarget(key)
	switch {
	case err != nil:
		inspection.NotBannableReason = infraerrors.Reason(err)
	case actorContainedIn(prefix, actorIP):
		inspection.NotBannableReason = infraerrors.Reason(ErrIPBanSelf)
	default:
		inspection.Bannable = true
	}
	return inspection, nil
}

func actorContainedIn(prefix netip.Prefix, actorIP string) bool {
	actorAddr, ok := ippkg.ParseClientAddr(actorIP)
	return ok && prefix.Contains(actorAddr)
}

// NormalizeIPBanTarget 校验并规范化封禁目标：单个 IP 返回地址本身，网段返回掩码后的
// CIDR；拒绝保留地址段与过宽的网段。
func NormalizeIPBanTarget(raw string) (netip.Prefix, string, error) {
	prefix, normalized, err := parseIPBanTarget(raw)
	if err != nil {
		return netip.Prefix{}, "", err
	}
	minBits := ipv4MinBanPrefixBits
	if prefix.Addr().Is6() {
		minBits = ipv6MinBanPrefixBits
	}
	if prefix.Bits() < minBits {
		return netip.Prefix{}, "", ErrIPBanRangeTooBroad
	}
	for _, reserved := range ipBanReservedPrefixes {
		if prefix.Overlaps(reserved) {
			return netip.Prefix{}, "", ErrIPBanReservedTarget
		}
	}
	return prefix, normalized, nil
}

func parseIPBanTarget(raw string) (netip.Prefix, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return netip.Prefix{}, "", ErrIPBanInvalidTarget
	}
	var prefix netip.Prefix
	if strings.Contains(raw, "/") {
		parsed, err := netip.ParsePrefix(raw)
		if err != nil {
			return netip.Prefix{}, "", ErrIPBanInvalidTarget
		}
		addr, bits := parsed.Addr(), parsed.Bits()
		if addr.Is4In6() {
			if bits < 96 {
				return netip.Prefix{}, "", ErrIPBanInvalidTarget
			}
			addr, bits = addr.Unmap(), bits-96
		}
		prefix = netip.PrefixFrom(addr, bits).Masked()
	} else {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return netip.Prefix{}, "", ErrIPBanInvalidTarget
		}
		addr = addr.WithZone("").Unmap()
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if !prefix.IsValid() {
		return netip.Prefix{}, "", ErrIPBanInvalidTarget
	}
	if prefix.IsSingleIP() {
		return prefix, prefix.Addr().String(), nil
	}
	return prefix, prefix.String(), nil
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max])
}

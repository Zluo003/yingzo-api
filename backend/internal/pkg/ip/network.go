package ip

import "net/netip"

// NetworkKey 返回按「单个用户网络」聚合的键：IPv4 为地址本身，IPv6 为所在 /64 网段。
// 运营商通常给单个用户分配整段 /64，按单个 IPv6 地址计数或封禁可被轻易绕过。
// 非法输入返回空串。
func NetworkKey(raw string) string {
	addr, ok := parseClientAddr(raw)
	if !ok {
		return ""
	}
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

// IsPubliclyRoutable 判断地址是否为公网单播地址。回环、RFC1918/ULA 内网、链路本地、
// 未指定与组播地址返回 false：这类地址通常是反代内部转发地址，不能作为按 IP 风控
// （注册限额、封禁）的依据，否则整条反代链路的真实用户会被合并成同一个 IP。
func IsPubliclyRoutable(raw string) bool {
	addr, ok := parseClientAddr(raw)
	if !ok {
		return false
	}
	return addr.IsGlobalUnicast() && !addr.IsPrivate()
}

// ParseClientAddr 解析客户端地址（允许带端口与 IPv6 zone），IPv4-mapped IPv6 归一为 IPv4。
func ParseClientAddr(raw string) (netip.Addr, bool) {
	return parseClientAddr(raw)
}

func parseClientAddr(raw string) (netip.Addr, bool) {
	normalized := normalizeIP(raw)
	if normalized == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(normalized)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone("").Unmap(), true
}

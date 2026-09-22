package service

import (
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// TemporaryAssetRef 引用素材库里的一条素材：按 ID 或按公网 token 定位。
type TemporaryAssetRef struct {
	ID    uuid.UUID
	Token string
}

// ParseTemporaryAssetRef 从素材 URL 里解析平台素材引用，支持两种形状：
//
//   - 平台代理地址：/media/{id}/… 与 /temporary-assets/{token}。不校验 host：代理
//     地址可能来自请求 origin 或配置的公网地址，服务端无法枚举全部来源 host。
//   - 自定义域名直读地址：https://{customHost}/{prefix}{id}。host 必须与配置的自定义
//     域名精确匹配，避免把第三方站点上的同形路径当成平台素材。
//
// 解析失败（非平台素材地址）返回 false。
func ParseTemporaryAssetRef(rawURL, customHost, customPrefix string) (TemporaryAssetRef, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return TemporaryAssetRef{}, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return TemporaryAssetRef{}, false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(segments) >= 2 && segments[0] == "media":
		id, err := uuid.Parse(segments[1])
		if err != nil {
			return TemporaryAssetRef{}, false
		}
		return TemporaryAssetRef{ID: id}, true
	case len(segments) == 2 && segments[0] == "temporary-assets":
		token := strings.TrimSpace(segments[1])
		if token == "" {
			return TemporaryAssetRef{}, false
		}
		return TemporaryAssetRef{Token: token}, true
	}
	if customHost == "" || customPrefix == "" {
		return TemporaryAssetRef{}, false
	}
	if !strings.EqualFold(parsed.Hostname(), customHost) {
		return TemporaryAssetRef{}, false
	}
	id := parseCustomDomainAssetID(parsed.Path, customPrefix)
	if id == uuid.Nil {
		return TemporaryAssetRef{}, false
	}
	return TemporaryAssetRef{ID: id}, true
}

// parseCustomDomainAssetID 从自定义域名路径里解析对象 ID：路径形如 /{prefix}{id}
// （prefix 归一化后带尾斜杠），id 后允许跟更多段（例如未来带扩展名）但只认第一段。
func parseCustomDomainAssetID(path, prefix string) uuid.UUID {
	trimmed := strings.Trim(path, "/")
	prefixTrimmed := strings.Trim(prefix, "/")
	if prefixTrimmed == "" || trimmed == prefixTrimmed {
		return uuid.Nil
	}
	rest, ok := strings.CutPrefix(trimmed, prefixTrimmed+"/")
	if !ok {
		return uuid.Nil
	}
	id, err := uuid.Parse(strings.Split(rest, "/")[0])
	if err != nil {
		return uuid.Nil
	}
	return id
}

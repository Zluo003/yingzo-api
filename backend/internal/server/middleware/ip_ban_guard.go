package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// IPBanChecker 判断客户端 IP 是否命中封禁规则（由 service.IPBanService 实现）。
type IPBanChecker interface {
	IsBanned(ctx context.Context, clientIP string) bool
}

// ipBannedHTML 浏览器直接访问页面时展示的拦截页，不依赖前端资源。
var ipBannedHTML = []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width,initial-scale=1"><title>访问受限</title></head>` +
	`<body style="margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;` +
	`font-family:system-ui,-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;background:#f6f2ea;color:#3a2d25">` +
	`<main style="text-align:center;padding:24px"><h1 style="font-size:22px;margin:0 0 12px">访问受限</h1>` +
	`<p style="margin:0;color:#6b5b50">你所在的网络已被禁止访问本站，如有疑问请联系管理员。</p>` +
	`<p style="margin:8px 0 0;color:#9a8a7d;font-size:13px">Access from your network has been blocked.</p>` +
	`</main></body></html>`)

// IPBanGuard 全局中间件：拒绝来自被封禁 IP / 网段的全部请求（健康检查除外）。
// 必须挂在 SessionBindingContext 之后：客户端 IP 取自其注入的安全客户端 IP，与审计日志、
// 会话绑定、注册 IP 同源；挂在 CORS 之后，跨域部署的前端也能读到 IP_BANNED 错误码。
func IPBanGuard(checker IPBanChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if checker == nil || c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		if !checker.IsBanned(c.Request.Context(), SecurityClientIP(c)) {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		if prefersHTMLResponse(c) {
			c.Data(http.StatusForbidden, "text/html; charset=utf-8", ipBannedHTML)
			c.Abort()
			return
		}
		response.ErrorFrom(c, service.ErrIPBanned)
		c.Abort()
	}
}

// prefersHTMLResponse 浏览器地址栏导航（GET + Accept: text/html）返回拦截页，
// 其余请求（前端 fetch、网关客户端）返回 JSON 错误。
func prefersHTMLResponse(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	return strings.Contains(c.GetHeader("Accept"), "text/html")
}

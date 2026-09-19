package service

import "net/http"

// 上游报错的中文文案库。
//
// 上游（模型供应商）返回的 4xx/5xx 原始报错往往夹带账号、额度、内部端点等
// 对下游无意义或难以理解的信息，不直接透传；这里把状态码映射为简单易读的
// 中文文案，供各转发路径统一使用。管理员配置的错误透传规则
// （error_passthrough_rule）优先级最高，命中规则时不走本映射。

const (
	upstreamClientMessageAuth      = "上游账号认证失败，请联系管理员"
	upstreamClientMessageBusy      = "模型太忙了，等一会再试一次吧"
	upstreamClientMessageTimeout   = "模型生成超时了，再试一次吧"
	upstreamClientMessageCapacity  = "模型供应商算力不足，稍等一会再试"
	upstreamClientMessageDown      = "模型服务暂不可用，请稍后再试"
	upstreamClientMessageModerated = "内容未通过安全审核，请检查提示词或参考图"
	upstreamClientMessageService   = "上游模型服务暂不可用，稍等一会再试"
	upstreamClientMessageFailed    = "上游请求失败，请稍后再试"
)

// MappedUpstreamClientMessage 报告状态码是否命中中文文案库，命中时返回对应文案。
// 400 不在库里：参数类报错需要原文中的具体字段信息，下游才能修复请求。
func MappedUpstreamClientMessage(statusCode int) (string, bool) {
	switch statusCode {
	case http.StatusUnauthorized:
		return upstreamClientMessageAuth, true
	case http.StatusForbidden:
		return upstreamClientMessageCapacity, true
	case http.StatusNotFound:
		return upstreamClientMessageDown, true
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return upstreamClientMessageTimeout, true
	case http.StatusTooManyRequests, 529:
		return upstreamClientMessageBusy, true
	case http.StatusUnavailableForLegalReasons:
		return upstreamClientMessageModerated, true
	}
	if statusCode >= http.StatusInternalServerError {
		return upstreamClientMessageService, true
	}
	return "", false
}

// MapUpstreamStatusToClientError 把上游 HTTP 状态码映射为面向下游的
// (状态码, 错误类型, 中文文案)。状态码沿用网关既有语义：429 保留给下游做
// 退避重试；451 属于请求内容问题，归一为 400 invalid_request_error；其余上游
// 侧故障统一折叠为 502 upstream_error，避免下游把供应商故障当成自身问题。
func MapUpstreamStatusToClientError(statusCode int) (int, string, string) {
	switch statusCode {
	case http.StatusUnauthorized:
		return http.StatusBadGateway, "upstream_error", upstreamClientMessageAuth
	case http.StatusForbidden:
		return http.StatusBadGateway, "upstream_error", upstreamClientMessageCapacity
	case http.StatusNotFound:
		return http.StatusBadGateway, "upstream_error", upstreamClientMessageDown
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return http.StatusBadGateway, "upstream_error", upstreamClientMessageTimeout
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests, "rate_limit_error", upstreamClientMessageBusy
	case http.StatusUnavailableForLegalReasons:
		return http.StatusBadRequest, "invalid_request_error", upstreamClientMessageModerated
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", upstreamClientMessageBusy
	}
	if statusCode >= http.StatusInternalServerError {
		return http.StatusBadGateway, "upstream_error", upstreamClientMessageService
	}
	return http.StatusBadGateway, "upstream_error", upstreamClientMessageFailed
}

// 图片/视频生成的上游故障转移。
const (
	// MediaFailoverMaxAccounts 是图片/视频请求最多尝试的上游（账号）数量：
	// 首选上游生成失败后自动切换下一个满足能力要求的上游继续生成，最多尝试
	// 3 个；全部失败时向下游返回最后一个上游的错误。
	MediaFailoverMaxAccounts = 3
	// MediaFailoverMaxSwitches 是对应的最大切换次数（= MaxAccounts - 1）。
	MediaFailoverMaxSwitches = MediaFailoverMaxAccounts - 1
)

// IsMediaFailoverStatus 判断图片/视频上游的 HTTP 状态码是否值得切换下一个
// 上游重试：账号侧/容量侧故障（认证失效、欠费、算力不足、限流、超时、5xx）
// 换一个上游大概率能成功；请求内容侧故障（400 参数错误、451 内容审核）换
// 上游结果相同，直接返回错误让用户检查请求。statusCode 0 表示网络层失败。
func IsMediaFailoverStatus(statusCode int) bool {
	if statusCode == 0 {
		return true
	}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooEarly,
		http.StatusTooManyRequests:
		return true
	}
	return statusCode >= http.StatusInternalServerError
}

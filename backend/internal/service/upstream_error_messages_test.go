package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapUpstreamStatusToClientError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantStatus int
		wantType   string
		wantMsg    string
	}{
		{name: "unauthorized folds into 502", statusCode: http.StatusUnauthorized, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "上游账号认证失败，请联系管理员"},
		{name: "forbidden means capacity", statusCode: http.StatusForbidden, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "模型供应商算力不足，稍等一会再试"},
		{name: "not found means service down", statusCode: http.StatusNotFound, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "模型服务暂不可用，请稍后再试"},
		{name: "request timeout means generation timeout", statusCode: http.StatusRequestTimeout, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "模型生成超时了，再试一次吧"},
		{name: "too early means generation timeout", statusCode: http.StatusTooEarly, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "模型生成超时了，再试一次吧"},
		{name: "too many requests keeps 429", statusCode: http.StatusTooManyRequests, wantStatus: http.StatusTooManyRequests, wantType: "rate_limit_error", wantMsg: "模型太忙了，等一会再试一次吧"},
		{name: "unavailable for legal reasons folds into 400", statusCode: http.StatusUnavailableForLegalReasons, wantStatus: http.StatusBadRequest, wantType: "invalid_request_error", wantMsg: "内容未通过安全审核，请检查提示词或参考图"},
		{name: "anthropic overloaded keeps type", statusCode: 529, wantStatus: http.StatusServiceUnavailable, wantType: "overloaded_error", wantMsg: "模型太忙了，等一会再试一次吧"},
		{name: "server errors fold into 502", statusCode: http.StatusInternalServerError, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "上游模型服务暂不可用，稍等一会再试"},
		{name: "bad gateway folds into 502", statusCode: http.StatusBadGateway, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "上游模型服务暂不可用，稍等一会再试"},
		{name: "gateway timeout folds into 502", statusCode: http.StatusGatewayTimeout, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "上游模型服务暂不可用，稍等一会再试"},
		{name: "unmapped 4xx keeps generic message", statusCode: http.StatusUnprocessableEntity, wantStatus: http.StatusBadGateway, wantType: "upstream_error", wantMsg: "上游请求失败，请稍后再试"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, errType, msg := MapUpstreamStatusToClientError(tt.statusCode)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantType, errType)
			assert.Equal(t, tt.wantMsg, msg)
		})
	}
}

func TestMappedUpstreamClientMessage(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusUnavailableForLegalReasons, 529,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 530,
	} {
		_, ok := MappedUpstreamClientMessage(statusCode)
		assert.True(t, ok, "status %d should be mapped", statusCode)
	}
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusPaymentRequired, http.StatusConflict, http.StatusUnprocessableEntity, 499, 0} {
		_, ok := MappedUpstreamClientMessage(statusCode)
		assert.False(t, ok, "status %d should NOT be mapped", statusCode)
	}
}

func TestIsMediaFailoverStatus(t *testing.T) {
	for _, statusCode := range []int{
		0, // 网络层失败
		http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooEarly,
		http.StatusTooManyRequests, 529,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	} {
		assert.True(t, IsMediaFailoverStatus(statusCode), "status %d should failover", statusCode)
	}
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnavailableForLegalReasons, 200, 302} {
		assert.False(t, IsMediaFailoverStatus(statusCode), "status %d should NOT failover", statusCode)
	}
}

func TestMediaFailoverMaxAccountsBound(t *testing.T) {
	// 上限语义：最多尝试 3 个上游（首次 + 2 次切换）。
	assert.Equal(t, 3, MediaFailoverMaxAccounts)
	assert.Equal(t, 2, MediaFailoverMaxSwitches)
}

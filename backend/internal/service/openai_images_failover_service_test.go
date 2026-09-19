package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 媒体故障转移（图片）：账号侧/容量侧故障（429/5xx 等）不写响应、返回 failover
// 错误交给 handler 换下一个账号；内容类错误（451/400）为终态，直接给下游
// 中文报错信息库的文案或脱敏后的原始报错。
func TestHandleOpenAIImagesErrorResponseFailoverClassification(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Name: "img-acct"}

	t.Run("429 returns failover error without writing response", func(t *testing.T) {
		c, rec := newOpenAIUpstreamErrorTestContext(t)
		_, err := svc.handleOpenAIImagesErrorResponse(
			context.Background(),
			newOpenAIUpstreamErrorResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`),
			c, account, "gpt-image-2",
		)
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
		require.Equal(t, 0, rec.Body.Len(), "failover 前不应向客户端写出响应")
	})

	t.Run("500 returns failover error without writing response", func(t *testing.T) {
		c, rec := newOpenAIUpstreamErrorTestContext(t)
		_, err := svc.handleOpenAIImagesErrorResponse(
			context.Background(),
			newOpenAIUpstreamErrorResponse(http.StatusInternalServerError, `{"error":{"message":"boom"}}`),
			c, account, "gpt-image-2",
		)
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, 0, rec.Body.Len())
	})

	t.Run("451 writes moderation hint and does not failover", func(t *testing.T) {
		c, rec := newOpenAIUpstreamErrorTestContext(t)
		_, err := svc.handleOpenAIImagesErrorResponse(
			context.Background(),
			newOpenAIUpstreamErrorResponse(http.StatusUnavailableForLegalReasons, `{"error":{"message":"policy"}}`),
			c, account, "gpt-image-2",
		)
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "451 换上游结果相同，不应触发切换")
		require.True(t, rec.Body.Len() > 0)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "内容未通过安全审核，请检查提示词或参考图", gjson.Get(rec.Body.String(), "error.message").String())
	})

	t.Run("400 keeps actionable upstream message", func(t *testing.T) {
		c, rec := newOpenAIUpstreamErrorTestContext(t)
		_, err := svc.handleOpenAIImagesErrorResponse(
			context.Background(),
			newOpenAIUpstreamErrorResponse(http.StatusBadRequest, `{"error":{"message":"Size must be 1024x1024","type":"invalid_request_error"}}`),
			c, account, "gpt-image-2",
		)
		require.Error(t, err)
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr))
		require.True(t, rec.Body.Len() > 0)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "Size must be 1024x1024", gjson.Get(rec.Body.String(), "error.message").String())
	})
}

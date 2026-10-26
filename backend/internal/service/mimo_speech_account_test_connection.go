package service

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) testMiMoSpeechAccount(c *gin.Context, account *Account) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	s.sendEvent(c, TestEvent{Type: "test_start", Model: MiMoTTSModel})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	gateway := &OpenAIGatewayService{httpUpstream: s.httpUpstream, cfg: s.cfg}
	if _, err := gateway.FetchOpenAIModelsList(ctx, account); err != nil {
		return s.sendErrorAndEnd(c, "MiMo connection check failed: "+err.Error())
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: "MiMo connection and authentication succeeded. This read-only check does not generate paid speech; use /v1/chat/completions to test synthesis."})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

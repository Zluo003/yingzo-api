package service

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) testSunoAccount(c *gin.Context, account *Account) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	s.sendEvent(c, TestEvent{Type: "test_start", Model: SunoModel})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if _, err := sunoHTTP(ctx, &OpenAIGatewayService{httpUpstream: s.httpUpstream, cfg: s.cfg}, account, http.MethodGet, "/v1/models", nil); err != nil {
		return s.sendErrorAndEnd(c, "APIMart connection check failed: "+err.Error())
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: "APIMart connection and authentication succeeded. This read-only check does not verify paid Suno generation."})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

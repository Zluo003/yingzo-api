package service

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// A connection test is a read-only credential check; it does not create a paid grid.
func (s *AccountTestService) testMidjourneyAccount(c *gin.Context, account *Account) error {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	s.sendEvent(c, TestEvent{Type: "test_start", Model: MidjourneyModel})
	adapter := &DurableImageService{openAI: &OpenAIGatewayService{httpUpstream: s.httpUpstream, cfg: s.cfg}}
	if _, err := adapter.midjourneyHTTP(c.Request.Context(), account, http.MethodGet, "/v1/models", nil); err != nil {
		return s.sendErrorAndEnd(c, "APIMart connection check failed: "+err.Error())
	}
	s.sendEvent(c, TestEvent{Type: "content", Text: "APIMart credentials accepted. Midjourney v8.2 generation is tested through the asynchronous Imagine API."})
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}

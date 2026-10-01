package handler

import (
	"encoding/json"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type MusicTaskHandler struct {
	tasks  *service.MusicTaskService
	openAI *OpenAIGatewayHandler
}

func NewMusicTaskHandler(tasks *service.MusicTaskService, openAI *OpenAIGatewayHandler) *MusicTaskHandler {
	return &MusicTaskHandler{tasks: tasks, openAI: openAI}
}
func (h *MusicTaskHandler) Start() { h.tasks.Start() }
func (h *MusicTaskHandler) Stop()  { h.tasks.Stop() }
func musicKey(c *gin.Context) *service.APIKey {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || !service.IsYingzoMusicGroup(key.Group) {
		imageTaskJSONError(c, 403, "music_group_required", "Suno requires a Yingzo Agent API key")
		return nil
	}
	return key
}
func (h *MusicTaskHandler) Submit(c *gin.Context) {
	key := musicKey(c)
	if key == nil {
		return
	}
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			imageTaskJSONError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		imageTaskJSONError(c, 400, "invalid_request_error", "Unable to read music request")
		return
	}
	r, err := service.ParseSunoRequest(body)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	if h.openAI != nil && !c.GetBool("async_music_accepted_replay") {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			imageTaskJSONError(c, 500, "api_error", "User context unavailable")
			return
		}
		auditBody, _ := json.Marshal(map[string]any{"model": service.SunoModel, "messages": []map[string]string{{"role": "user", "content": r.Prompt + "\n" + r.Style + "\n" + r.Title + "\n" + r.NegativeTags}}})
		decision := h.openAI.checkSecurityAudit(c, requestLogger(c, "handler.music.security_audit"), key, subject, service.ContentModerationProtocolOpenAIChat, service.SunoModel, auditBody)
		if decision != nil && !decision.AllowNextStage {
			h.openAI.openAISecurityAuditError(c, decision)
			return
		}
	}
	sub, _ := middleware.GetSubscriptionFromContext(c)
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if p := c.GetHeader("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	task, err := h.tasks.Accept(c.Request.Context(), key, sub, r, c.GetHeader("Idempotency-Key"), scheme+"://"+c.Request.Host)
	if err != nil {
		musicTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Location", task.PollURL)
	c.Header("Retry-After", "3")
	c.JSON(http.StatusAccepted, task)
}
func (h *MusicTaskHandler) Get(c *gin.Context)              { h.get(c, false) }
func (h *MusicTaskHandler) GetByIdempotency(c *gin.Context) { h.get(c, true) }
func (h *MusicTaskHandler) get(c *gin.Context, idem bool) {
	key := musicKey(c)
	if key == nil {
		return
	}
	id := c.Param("task_id")
	if idem {
		id = c.Param("idempotency_key")
	}
	task, err := h.tasks.Get(c.Request.Context(), service.MusicTaskOwner{UserID: key.UserID, APIKeyID: key.ID}, id, idem)
	if err != nil {
		musicTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if task.Status == service.MusicTaskStatusProcessing {
		c.Header("Retry-After", "5")
	}
	c.JSON(http.StatusOK, task)
}

func musicTaskError(c *gin.Context, err error) {
	status, code, message := infraerrors.Code(err), infraerrors.Reason(err), infraerrors.Message(err)
	if status >= http.StatusInternalServerError {
		requestLogger(c, "handler.music").Error("music task request failed", zap.Error(err))
	}
	if code == "" || status <= 0 || status == http.StatusInternalServerError {
		status, code, message = http.StatusInternalServerError, "music_task_error", "Music task service is temporarily unavailable"
	}
	imageTaskJSONError(c, status, code, message)
}

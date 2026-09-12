package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type VideoHandler struct {
	videoService             *service.VideoService
	agentHandler             *AgentHandler
	securityAuditCoordinator *securityaudit.Coordinator
	contentModerationService *service.ContentModerationService
	// referenceMaterialFetcher 把下游提交的外部参考素材 URL 拉到本机，平台随后
	// 自己探测时长并把素材转存到素材库。
	referenceMaterialFetcher referenceMaterialDownloader
}

func NewVideoHandler(videoService *service.VideoService, agentHandler ...*AgentHandler) *VideoHandler {
	h := &VideoHandler{
		videoService:             videoService,
		referenceMaterialFetcher: service.NewReferenceMaterialFetcher(),
	}
	if len(agentHandler) > 0 {
		h.agentHandler = agentHandler[0]
	}
	return h
}

// ProvideVideoHandler is the Wire-facing constructor. NewVideoHandler takes the
// agent handler variadically so tests can build a video handler on its own,
// which Wire cannot inject; in the real graph the agent handler is required
// because multipart video requests upload their media parts through it.
func ProvideVideoHandler(videoService *service.VideoService, agentHandler *AgentHandler) *VideoHandler {
	return NewVideoHandler(videoService, agentHandler)
}

// checkSecurityAudit 让视频提示词与图片生成共用上游的审核协调器。
func (h *VideoHandler) checkSecurityAudit(c *gin.Context, reqLog *zap.Logger, apiKey *service.APIKey, subject middleware2.AuthSubject, model string, body []byte) *securityaudit.Decision {
	if h == nil {
		return nil
	}
	return runSecurityAudit(c, reqLog, h.securityAuditCoordinator, h.contentModerationService, apiKey, subject,
		service.ContentModerationProtocolVideos, model, body, "http")
}

func (h *VideoHandler) Create(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	var body []byte
	var err error
	if strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data") {
		body, err = h.readMultipartVideoRequest(c, apiKey)
	} else {
		body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	}
	if err != nil {
		if h.writeVideoRequestError(c, err) {
			return
		}
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_body_too_large", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "invalid_video_request", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "invalid_video_request", "Request body is empty")
		return
	}
	// 幂等与计费指纹按下游原始请求体计算：参考素材转存会改写 URL，而改写结果每次
	// 都是新素材，不能参与指纹，否则同一个请求无法命中幂等重放。
	inboundPayloadHash := service.HashUsageRequestPayload(body)

	var req service.VideoCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "invalid_video_request", "Failed to parse request body")
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		req.Raw = raw
	}
	setOpsRequestContext(c, req.Model, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeVideo))

	subject, hasSubject := middleware2.GetAuthSubjectFromContext(c)
	if hasSubject {
		reqLog := requestLogger(c, "handler.video", zap.Int64("api_key_id", apiKey.ID))
		if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, req.Model, body); decision != nil && !decision.AllowNextStage {
			return
		}
	}
	inboundEndpoint := GetInboundEndpoint(c)
	upstreamEndpoint := GetUpstreamEndpoint(c, service.PlatformVideo)

	// 参考素材统一转存到平台自己的公网 URL，并用平台探测出的时长覆盖下游声明。
	// 必须发生在 CreateTask 之前：参考视频时长直接参与计费估算。
	if resolved, changed, resolveErr := h.resolveVideoReferenceMaterials(c, apiKey, raw); resolveErr != nil {
		if h.writeVideoRequestError(c, resolveErr) {
			return
		}
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "reference_material_failed", "Failed to prepare reference material")
		return
	} else if changed {
		var resolvedRequest service.VideoCreateRequest
		if err := json.Unmarshal(resolved, &resolvedRequest); err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "invalid_video_request", "Failed to parse rewritten video request")
			return
		}
		resolvedRequest.Raw = raw
		req = resolvedRequest
	}

	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	resultPublicBaseURL, _ := requestPublicOrigin(c)
	createInput := &service.VideoCreateInput{
		APIKey:              apiKey,
		Subscription:        subscription,
		Request:             &req,
		RawBody:             body,
		IdempotencyKey:      c.GetHeader("Idempotency-Key"),
		RequestID:           requestID,
		RequestPayloadHash:  inboundPayloadHash,
		UserAgent:           c.GetHeader("User-Agent"),
		IPAddress:           ip.GetClientIP(c),
		InboundEndpoint:     inboundEndpoint,
		UpstreamEndpoint:    upstreamEndpoint,
		ResultPublicBaseURL: resultPublicBaseURL,
	}
	resp, err := h.videoService.CreateTask(c.Request.Context(), createInput)
	if err != nil {
		h.errorFrom(c, err)
		return
	}
	if createInput.IdempotencyReplayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	c.JSON(http.StatusOK, resp)
}

type videoMultipartRequestError struct {
	status  int
	code    string
	message string
}

func (e *videoMultipartRequestError) Error() string {
	if e == nil {
		return "invalid multipart video request"
	}
	return e.message
}

// writeVideoRequestError 把视频请求解析阶段的错误按既有错误响应格式写出。
// 返回 false 表示这个错误不属于这两类，调用方继续走其它分支。
func (h *VideoHandler) writeVideoRequestError(c *gin.Context, err error) bool {
	switch typed := err.(type) {
	case *videoMultipartRequestError:
		h.errorResponse(c, typed.status, videoErrorType(typed.status), typed.code, typed.message)
		return true
	case *videoReferenceMaterialError:
		h.errorResponse(c, typed.status, videoErrorType(typed.status), typed.code, typed.message)
		return true
	default:
		return false
	}
}

// readMultipartVideoRequest accepts a JSON request part plus repeated `file`
// parts. Media URLs in the JSON use attachment://N, where N is the zero-based
// position of the corresponding file part. This explicit ordinal contract
// preserves the user's input order through upload, URL replacement, and the
// final upstream body.
func (h *VideoHandler) readMultipartVideoRequest(c *gin.Context, apiKey *service.APIKey) ([]byte, error) {
	if h == nil {
		return nil, &videoMultipartRequestError{status: http.StatusServiceUnavailable, code: "media_upload_unavailable", message: "Video media upload is not configured"}
	}
	// Keep only small form fields in memory; large media parts are spooled to
	// temporary files by net/http. The route-level body limit remains the total
	// request-size guard.
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_multipart", message: "Failed to parse multipart video request"}
	}
	form := c.Request.MultipartForm
	if form == nil {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_multipart", message: "Multipart form is empty"}
	}
	requestJSON := ""
	for _, field := range []string{"request", "json", "body"} {
		if values := form.Value[field]; len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			requestJSON = values[0]
			break
		}
	}
	if strings.TrimSpace(requestJSON) == "" {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "video_request_part_required", message: "Multipart video requests require a JSON request part"}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(requestJSON), &raw); err != nil {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_request", message: "Failed to parse multipart video JSON request"}
	}
	var request service.VideoCreateRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_request", message: "Failed to parse multipart video JSON request"}
	}

	files := form.File["file"]
	if len(files) == 0 {
		if err := validateMultipartVideoAttachments(&request, 0); err != nil {
			return nil, err
		}
		return []byte(requestJSON), nil
	}
	if h.agentHandler == nil {
		return nil, &videoMultipartRequestError{status: http.StatusServiceUnavailable, code: "media_upload_unavailable", message: "Video media upload is not configured"}
	}
	if err := validateMultipartVideoAttachments(&request, len(files)); err != nil {
		return nil, err
	}
	urls := make([]string, len(files))
	for i, header := range files {
		file, err := header.Open()
		if err != nil {
			return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "file_open_failed", message: fmt.Sprintf("Failed to open multipart file %d", i)}
		}
		result, uploadErr := h.agentHandler.uploadTemporaryAssetPart(c, apiKey, file, header)
		_ = file.Close()
		if uploadErr != nil {
			if assetErr, ok := uploadErr.(*temporaryAssetUploadError); ok {
				message := assetErr.message
				if message == "" {
					message = assetErr.code
				}
				return nil, &videoMultipartRequestError{status: assetErr.status, code: assetErr.code, message: message}
			}
			return nil, &videoMultipartRequestError{status: http.StatusInternalServerError, code: "media_upload_failed", message: "Failed to upload multipart media"}
		}
		urls[i] = result.URL
	}

	if err := rewriteMultipartRawVideoAttachments(raw, urls); err != nil {
		return nil, err
	}
	// Marshal the normalized request after URL replacement. The VideoService
	// performs the provider-specific transformation from this canonical body.
	rewritten, err := json.Marshal(raw)
	if err != nil {
		return nil, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_request", message: "Failed to encode rewritten video request"}
	}
	return rewritten, nil
}

// rewriteMultipartRawVideoAttachments edits only the URL leaves in the raw
// JSON map, retaining provider-neutral or provider-specific fields that are
// not represented by VideoCreateRequest (for example seed or future fields).
func rewriteMultipartRawVideoAttachments(raw map[string]any, urls []string) error {
	content, ok := raw["content"].([]any)
	if !ok {
		return nil
	}
	for i, value := range content {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		var urlField string
		switch strings.ToLower(strings.TrimSpace(stringValue(item["type"]))) {
		case "image_url":
			urlField = "image_url"
		case "video_url":
			urlField = "video_url"
		case "audio_url":
			urlField = "audio_url"
		default:
			continue
		}
		urlObject, ok := item[urlField].(map[string]any)
		if !ok {
			continue
		}
		rawURL, ok := urlObject["url"].(string)
		if !ok || !strings.HasPrefix(strings.TrimSpace(rawURL), "attachment://") {
			continue
		}
		index, err := parseMultipartAttachmentOrdinal(rawURL, i, len(urls))
		if err != nil {
			return err
		}
		urlObject["url"] = urls[index]
	}
	return nil
}

func stringValue(value any) string {
	valueString, _ := value.(string)
	return valueString
}

func parseMultipartAttachmentOrdinal(rawURL string, itemIndex, fileCount int) (int, error) {
	ordinal := strings.TrimPrefix(strings.TrimSpace(rawURL), "attachment://")
	if ordinal == "" {
		return 0, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_attachment_reference", message: fmt.Sprintf("Content item %d has an empty attachment reference", itemIndex)}
	}
	index := 0
	for _, ch := range ordinal {
		if ch < '0' || ch > '9' {
			return 0, &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_attachment_reference", message: fmt.Sprintf("Content item %d must reference attachment://N", itemIndex)}
		}
		index = index*10 + int(ch-'0')
		if index >= fileCount {
			return 0, &videoMultipartRequestError{status: http.StatusBadRequest, code: "attachment_reference_out_of_range", message: fmt.Sprintf("Content item %d references missing attachment %s", itemIndex, ordinal)}
		}
	}
	return index, nil
}

func rewriteMultipartVideoAttachments(request *service.VideoCreateRequest, urls []string) error {
	if request == nil {
		return &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_request", message: "Video request is empty"}
	}
	for i := range request.Content {
		item := &request.Content[i]
		var rawURL *service.VideoContentURL
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "image_url":
			rawURL = item.ImageURL
		case "video_url":
			rawURL = item.VideoURL
		case "audio_url":
			rawURL = item.AudioURL
		default:
			continue
		}
		if rawURL == nil || !strings.HasPrefix(strings.TrimSpace(rawURL.URL), "attachment://") {
			continue
		}
		index, err := parseMultipartAttachmentOrdinal(rawURL.URL, i, len(urls))
		if err != nil {
			return err
		}
		rawURL.URL = urls[index]
	}
	return nil
}

func validateMultipartVideoAttachments(request *service.VideoCreateRequest, fileCount int) error {
	if request == nil {
		return &videoMultipartRequestError{status: http.StatusBadRequest, code: "invalid_video_request", message: "Video request is empty"}
	}
	for i := range request.Content {
		item := &request.Content[i]
		var rawURL *service.VideoContentURL
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "image_url":
			rawURL = item.ImageURL
		case "video_url":
			rawURL = item.VideoURL
		case "audio_url":
			rawURL = item.AudioURL
		default:
			continue
		}
		if rawURL == nil || !strings.HasPrefix(strings.TrimSpace(rawURL.URL), "attachment://") {
			continue
		}
		if _, err := parseMultipartAttachmentOrdinal(rawURL.URL, i, fileCount); err != nil {
			return err
		}
	}
	return nil
}

// videoPublicIDParam 从路径参数中取出视频任务 id。
//
// 同一组路由注册了两种参数名：/v1/videos/:request_id（网关前缀）与
// /videos/:id（引擎根路径）。只认其中一个，另一条路径就会恒定 404 —— 下游拿
// 创建响应里的 id 去 /v1/videos/<id> 查询时正是这种情况（issue: 任务已完成却查不到）。
func videoPublicIDParam(c *gin.Context) string {
	if publicID := strings.TrimSpace(c.Param("request_id")); publicID != "" {
		return publicID
	}
	return strings.TrimSpace(c.Param("id"))
}

func (h *VideoHandler) Get(c *gin.Context) {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	setOpsRequestContext(c, "", false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeVideo))

	resp, err := h.videoService.GetTask(c.Request.Context(), videoPublicIDParam(c), apiKey)
	if err != nil {
		h.errorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *VideoHandler) errorFrom(c *gin.Context, err error) {
	if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(retryAfter))
	}
	status, body := infraerrors.ToHTTP(err)
	code := strings.TrimSpace(body.Reason)
	if code == "" {
		code = "video_service_unavailable"
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		message = "Video service is temporarily unavailable. Please retry later."
	}
	h.errorResponse(c, status, videoErrorType(status), code, message)
}

func (h *VideoHandler) errorResponse(c *gin.Context, status int, errType, code, message string) {
	code, message = service.SanitizeVideoClientError(code, message)
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"code":    code,
			"message": message,
		},
	})
}

func videoErrorType(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

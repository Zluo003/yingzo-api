package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func ProvideAsyncImageHandler(tasks *service.ImageTaskService, openAI *OpenAIGatewayHandler, durable *service.DurableImageService) *AsyncImageHandler {
	h := NewAsyncImageHandler(tasks, openAI)
	h.durable = durable
	return h
}
func (h *AsyncImageHandler) Start(router http.Handler) {
	if h == nil || h.durable == nil {
		return
	}
	h.durable.Start(func(ctx context.Context, snapshot *service.ImageRequestSnapshot, key *service.APIKey, capture *service.AsyncImageExecution) (int, json.RawMessage, error) {
		req, err := http.NewRequestWithContext(ctx, snapshot.Method, snapshot.Path, bytes.NewReader(snapshot.Body))
		if err != nil {
			return 0, nil, err
		}
		req.Host = snapshot.Host
		req.RemoteAddr = snapshot.RemoteAddr
		req.Header = snapshot.Header.Clone()
		req.Header.Set("Authorization", "Bearer "+key.Key)
		req.Header.Set("x-goog-api-key", key.Key)
		req.Header.Del("Prefer")
		req.Header.Del("Idempotency-Key")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder.Code, json.RawMessage(recorder.Body.Bytes()), ctx.Err()
	})
}
func (h *AsyncImageHandler) Prefer(c *gin.Context, platform string) bool {
	if h == nil || h.durable == nil || service.IsAsyncImageExecution(c.Request.Context()) {
		return false
	}
	wants := false
	for _, v := range strings.Split(c.GetHeader("Prefer"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(v, ";", 2)[0]), "respond-async") {
			wants = true
		}
	}
	if !wants {
		return false
	}
	replay := c.GetBool(middleware2.ContextKeyAsyncImageTaskReplay)
	if platform != service.PlatformOpenAI && platform != service.PlatformGrok && platform != service.PlatformGemini {
		if replay {
			imageTaskJSONError(c, http.StatusConflict, "idempotency_conflict", "accepted image task cannot be replayed through a different platform")
			return true
		}
		return false
	}
	enabled := h.durable.Enabled(c.Request.Context())
	if !enabled {
		key, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || key == nil || c.GetHeader("Idempotency-Key") == "" {
			return false
		}
		if _, err := h.durable.Receipt(c.Request.Context(), service.ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}, c.GetHeader("Idempotency-Key")); err != nil {
			if errors.Is(err, service.ErrImageTaskNotFound) {
				return false
			}
			// An unavailable ledger cannot prove that this request is new.
			imageTaskError(c, err)
			return true
		}
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", "Failed to read image request")
		return true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	if asyncImageRequestStreams(c.GetHeader("Content-Type"), body) && !replay {
		return false
	}
	if platform == service.PlatformGemini {
		if !strings.HasSuffix(c.Request.URL.Path, ":generateContent") {
			return false
		}
		var native struct {
			GenerationConfig struct {
				ResponseModalities []string `json:"responseModalities"`
			} `json:"generationConfig"`
		}
		_ = json.Unmarshal(body, &native)
		imageModel := strings.Contains(strings.ToLower(c.Param("modelAction")), "image")
		for _, m := range native.GenerationConfig.ResponseModalities {
			if strings.EqualFold(m, "IMAGE") {
				imageModel = true
			}
		}
		if !imageModel && !replay {
			return false
		}
	}
	if !enabled {
		key, _ := middleware2.GetAPIKeyFromContext(c)
		h.submitDurable(c, key, platform, body)
		return true
	}

	c.Set("async_image_platform", platform)
	h.Submit(c)
	return true
}
func (h *AsyncImageHandler) submitDurable(c *gin.Context, key *service.APIKey, platform string, body []byte) {
	model, size, count := "", "", 1
	switch platform {
	case service.PlatformGemini:
		model = strings.TrimSuffix(strings.TrimPrefix(c.Param("modelAction"), "/"), ":generateContent")
		var native struct {
			GenerationConfig struct {
				CandidateCount int `json:"candidateCount"`
				ImageConfig    struct {
					ImageSize string `json:"imageSize"`
				} `json:"imageConfig"`
			} `json:"generationConfig"`
		}
		if json.Unmarshal(body, &native) != nil {
			imageTaskJSONError(c, 400, "invalid_request_error", "invalid Gemini request")
			return
		}
		size = native.GenerationConfig.ImageConfig.ImageSize
		if native.GenerationConfig.CandidateCount > 0 {
			count = native.GenerationConfig.CandidateCount
		}
	case service.PlatformGrok:
		parsed := service.ParseGrokMediaRequest(c.GetHeader("Content-Type"), body)
		model, size, count = parsed.Model, parsed.SizeTier, parsed.N
	default:
		parsed, err := h.openAI.gatewayService.ParseOpenAIImagesRequest(c, body)
		if err != nil {
			imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
			return
		}
		model, size, count = parsed.Model, parsed.SizeTier, parsed.N
	}
	if count == 0 {
		count = 1
	}
	path := strings.TrimSuffix(c.Request.URL.Path, "/async")
	var route *service.CompositeRouteDecision
	if public, ok := service.RequestedPublicModelFromContext(c.Request.Context()); ok && key.Group.Platform == service.PlatformComposite {
		upstream, _ := service.ResolvedUpstreamModelFromContext(c.Request.Context())
		source, _ := service.CompositeRouteSourceFromContext(c.Request.Context())
		route = &service.CompositeRouteDecision{Matched: true, GroupID: key.Group.ID, PublicModel: public, TargetPlatform: platform, UpstreamModel: upstream, Source: source}
		// Snapshot the client's model for allowlist checks and stable idempotency.
		if gjson.ValidBytes(body) && platform != service.PlatformGemini {
			body, _ = sjson.SetBytes(body, "model", public)
		}
	}
	fingerprint, err := imageRequestFingerprint(path, c.GetHeader("Content-Type"), body)
	if err != nil {
		imageTaskJSONError(c, 400, "invalid_request_error", err.Error())
		return
	}
	headers := make(http.Header)
	// Copy only protocol data. Cookies and caller credentials never enter a task snapshot.
	for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Forwarded-Proto", "X-Forwarded-For", "X-Real-IP"} {
		if value := c.GetHeader(name); value != "" {
			headers.Set(name, value)
		}
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if p := c.GetHeader("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	snapshot := &service.ImageRequestSnapshot{Route: route, Platform: platform, Method: http.MethodPost, Path: path, Host: c.Request.Host, Header: headers, Body: body, RemoteAddr: c.Request.RemoteAddr, PublicBaseURL: scheme + "://" + c.Request.Host}
	sub, _ := middleware2.GetSubscriptionFromContext(c)
	task, err := h.durable.Accept(c.Request.Context(), key, sub, platform, model, size, count, c.GetHeader("Idempotency-Key"), fingerprint, snapshot)
	if err != nil {
		if errors.Is(err, service.ErrImageIdempotencyConflict) {
			imageTaskJSONError(c, http.StatusConflict, "idempotency_conflict", err.Error())
		} else {
			imageTaskError(c, err)
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Location", "/v1/images/tasks/"+task.ID)
	c.Header("Retry-After", "3")
	c.Header("Preference-Applied", "respond-async")
	c.JSON(http.StatusAccepted, gin.H{"id": task.ID, "task_id": task.ID, "object": task.Object, "status": task.Status, "phase": task.Phase, "billing_status": task.BillingStatus, "refund_status": task.RefundStatus, "created_at": task.CreatedAt, "expires_at": task.ExpiresAt, "deadline_at": task.DeadlineAt, "poll_url": "/v1/images/tasks/" + task.ID})
}
func (h *AsyncImageHandler) GetByIdempotency(c *gin.Context) {
	if h == nil || h.durable == nil {
		imageTaskError(c, service.ErrImageTaskNotFound)
		return
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	task, err := h.durable.Get(c.Request.Context(), service.ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}, c.Param("idempotency_key"), true)
	if err != nil {
		imageTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Retry-After", "3")
	c.JSON(http.StatusOK, task)
}

// Multipart boundaries and JSON member order are transport details. Attachment
// order within a field remains significant (the first reference can be special).
func imageRequestFingerprint(path, contentType string, body []byte) (string, error) {
	var canonical []byte
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", err
	}
	if media == "multipart/form-data" {
		values := map[string][]string{}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				return "", err
			}
			digest := sha256.Sum256(data)
			values[part.FormName()] = append(values[part.FormName()], hex.EncodeToString(digest[:]))
		}
		canonical, err = json.Marshal(values)
	} else {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err = decoder.Decode(&value); err == nil {
			var extra any
			if e := decoder.Decode(&extra); e != io.EOF {
				return "", errors.New("request must contain one JSON object")
			}
			canonical, err = json.Marshal(canonicalImageParameters(value))
		}
	}
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(path+"\n"), canonical...))
	return hex.EncodeToString(digest[:]), nil
}

func canonicalImageParameters(value any) any {
	switch v := value.(type) {
	case json.Number:
		if n, err := decimal.NewFromString(v.String()); err == nil {
			return json.Number(n.String())
		}
	case map[string]any:
		for k, item := range v {
			v[k] = canonicalImageParameters(item)
		}
	case []any:
		for i, item := range v {
			v[i] = canonicalImageParameters(item)
		}
	}
	return value
}

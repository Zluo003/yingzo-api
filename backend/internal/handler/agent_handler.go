package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

type AgentHandler struct {
	db             *sql.DB
	dataDir        string
	styleDir       string
	objectStore    service.BackupObjectStore
	fileStorage    *service.FileStorageService
	billingService *service.BillingService
	videoService   *service.VideoService
	agentModels    *service.AgentModelCatalogService
	cleanupMu      sync.Mutex
	cleanupStop    context.CancelFunc
	cleanupDone    chan struct{}
}

func NewAgentHandler(
	db *sql.DB,
	cfg *config.Config,
	fileStorage *service.FileStorageService,
	billingService *service.BillingService,
	videoService *service.VideoService,
	agentModels *service.AgentModelCatalogService,
) *AgentHandler {
	dir := filepath.Join(cfg.Pricing.DataDir, "agent-assets")
	styleDir := filepath.Join(cfg.Pricing.DataDir, "style-library")
	if fileStorage != nil {
		dir = fileStorage.LocalPath()
	}
	_ = os.MkdirAll(dir, 0700)
	_ = os.MkdirAll(styleDir, 0700)
	h := &AgentHandler{
		db:             db,
		dataDir:        dir,
		styleDir:       styleDir,
		billingService: billingService,
		videoService:   videoService,
		agentModels:    agentModels,
		fileStorage:    fileStorage,
	}
	h.StartCleanupWorker(time.Hour)
	return h
}

// ModelCatalog 暴露 Yingzo Agent 的模型目录服务，供网关入口中间件按请求模型
// 解析实际平台（聚合分组一个凭证要服务多个 provider）。
func (h *AgentHandler) ModelCatalog() *service.AgentModelCatalogService {
	if h == nil {
		return nil
	}
	return h.agentModels
}

// Models 返回 Yingzo Agent 当前可用的聚合模型目录。Agent 分组只保留一个
// 入口凭证，实际 provider 由模型目录和路由中间件按请求模型解析。
func (h *AgentHandler) Models(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil || !apiKey.Group.IsAgent() {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "agent_credential_required", "message": "This endpoint requires an Agent credential"}})
		return
	}
	if h.agentModels == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Agent model catalog is unavailable"}})
		return
	}
	entries, err := h.agentModels.ListAvailable(c.Request.Context(), apiKey.Group.ID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": err.Error()}})
		return
	}
	// The desktop client consumes the model catalogue as a routing contract,
	// rather than as a plain provider model list. Keep the list envelope for the
	// existing /v1/models surface, but publish the aggregate Agent capabilities
	// that let the client select the correct native provider route per model.
	config, configErr := h.agentModels.GetConfig(c.Request.Context(), apiKey.Group.ID)
	if configErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Agent model catalog is unavailable"}})
		return
	}
	models := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		models = append(models, agentCatalogModel(entry, config))
	}
	body := gin.H{
		"object":                   "list",
		"catalog_schema_version":   service.AgentModelCatalogSchemaVersion,
		"gateway_contract_version": service.AgentAPIContractVersion,
		"provenance": gin.H{
			"catalog_source":     service.AgentModelCatalogSource,
			"capability_sources": []string{"gateway"},
		},
		"data": models,
	}
	raw, _ := json.Marshal(body)
	digest := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=15")
	c.Header("X-Model-Catalog-Schema-Version", strconv.Itoa(service.AgentModelCatalogSchemaVersion))
	c.Header("X-Gateway-Contract-Version", service.AgentAPIContractVersion)
	if strings.TrimSpace(c.GetHeader("If-None-Match")) == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.JSON(http.StatusOK, body)
}

// agentCatalogModel translates the persisted Agent model routing record into
// the capability vocabulary consumed by the desktop kernel. It intentionally
// describes only routes that the existing gateway handlers already implement;
// no upstream protocol handler is changed here.
func agentCatalogModel(entry service.AgentModelCatalogEntry, config *service.AgentModelCatalogConfig) gin.H {
	input := []string{}
	output := []string{}
	operations := []string{}
	streaming := true
	asynchronous := false
	capabilities := gin.H{
		"input_modalities":  input,
		"output_modalities": output,
		"operations":        operations,
		"media_types":       entry.MediaTypes,
		"platforms":         entry.Platforms,
		"interfaces":        entry.Interfaces,
		"streaming":         streaming,
		"asynchronous":      asynchronous,
	}

	if containsAgentMediaType(entry.MediaTypes, service.AgentMediaTypeText) {
		input = appendUniqueStrings(input, "text")
		output = appendUniqueStrings(output, "text")
		operations = appendUniqueStrings(operations, "text.generate")
	}
	if containsAgentMediaType(entry.MediaTypes, service.AgentMediaTypeImage) {
		input = appendUniqueStrings(input, "text", "image")
		output = appendUniqueStrings(output, "image")
		operations = appendUniqueStrings(operations, "image.generate", "image.edit")
		streaming = false
		capabilities["max_input_images"] = 16
		capabilities["supported_aspect_ratios"] = []string{"1:1", "16:9", "9:16"}
		// 与视频分辨率同一原则：只下发管理端配过价（=启用）的尺寸档位，未配置
		// 的档位不下发，避免下游拿到上游不支持、也无法计费的能力参数。
		capabilities["supported_image_sizes"] = configuredAgentModelResolutions(entry, config, service.AgentMediaTypeImage)
	}
	if containsAgentMediaType(entry.MediaTypes, service.AgentMediaTypeVideo) {
		input = appendUniqueStrings(input, "text", "image", "video", "audio")
		output = appendUniqueStrings(output, "video")
		operations = appendUniqueStrings(operations, "video.generate")
		streaming = false
		asynchronous = true
		capabilities["supported_video_resolutions"] = configuredAgentModelResolutions(entry, config, service.AgentMediaTypeVideo)
		// Duration is a per-model upstream constraint, not a fixed three-tier
		// menu: the 2.0 family accepts 4-15s and 2.5 accepts 4-30s. Deriving it
		// from the video spec keeps the catalog aligned with request validation.
		capabilities["supported_video_durations_sec"] = service.SupportedVideoDurations(entry.ID)
		capabilities["supports_video_audio"] = true
		// 参考音频能否单独成篇是按模型声明的能力：所有视频模型都要求参考音频
		// 搭配至少一张图或一段视频，因此对每个模型显式声明为 false，而不是靠
		// 字段缺失让客户端自己猜。
		capabilities["supports_audio_only_reference"] = service.SupportsAudioOnlyReference(entry.ID)
		capabilities["cancellation"] = []string{"remoteJob"}
	}
	if containsAgentInterface(entry.Interfaces, service.AgentInterfaceOpenAIEmbeddings) {
		operations = removeString(operations, "text.generate")
		input = []string{"text"}
		output = []string{"text"}
		streaming = false
	}
	if len(input) == 0 {
		input = []string{"text"}
	}
	if len(output) == 0 {
		output = []string{"text"}
	}
	capabilities["input_modalities"] = input
	capabilities["output_modalities"] = output
	capabilities["operations"] = operations
	capabilities["streaming"] = streaming
	capabilities["asynchronous"] = asynchronous

	return gin.H{
		"id":                entry.ID,
		"object":            "model",
		"created":           0,
		"owned_by":          "yingzo-agent",
		"source":            "gateway",
		"availability":      "advertised",
		"capability_source": "gateway",
		"display_name":      entry.ID,
		"media_types":       entry.MediaTypes,
		"platforms":         entry.Platforms,
		"interfaces":        entry.Interfaces,
		"capabilities":      capabilities,
	}
}

func containsAgentMediaType(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsAgentInterface(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

// configuredAgentModelResolutions 返回该模型在管理端显式配过价的分辨率/尺寸
// 档位。价格档位就是能力开关：配置了 = 启用并下发；留空 = 未定价也不下发，
// 下游永远不会拿到没有计费、上游可能不支持的能力参数。
func configuredAgentModelResolutions(entry service.AgentModelCatalogEntry, config *service.AgentModelCatalogConfig, mediaType string) []string {
	// The public aggregate entry intentionally omits pricing internals. When a
	// configured model record is available, expose its explicitly priced tiers;
	// the client will consequently never select an unpriced resolution.
	seen := map[string]struct{}{}
	result := make([]string, 0)
	if config != nil {
		for _, model := range config.Models {
			if model.ModelCode != entry.ID || model.MediaType != mediaType || !model.Enabled || !model.Available || model.Excluded {
				continue
			}
			for _, price := range model.Prices {
				resolution := strings.TrimSpace(price.Resolution)
				if resolution != "" {
					if _, ok := seen[resolution]; !ok {
						seen[resolution] = struct{}{}
						result = append(result, resolution)
					}
				}
			}
		}
	}
	return result
}

func (h *AgentHandler) StartCleanupWorker(interval time.Duration) {
	if h == nil || h.db == nil || interval <= 0 {
		return
	}
	h.cleanupMu.Lock()
	defer h.cleanupMu.Unlock()
	if h.cleanupStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.cleanupStop = cancel
	h.cleanupDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = h.CleanupExpired(ctx)
			}
		}
	}()
}

func (h *AgentHandler) StopCleanupWorker() {
	if h == nil {
		return
	}
	h.cleanupMu.Lock()
	cancel, done := h.cleanupStop, h.cleanupDone
	h.cleanupStop = nil
	h.cleanupDone = nil
	h.cleanupMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashToken(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }

func requestPublicOrigin(c *gin.Context) (string, error) {
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	if forwarded := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	parsed, err := url.Parse(scheme + "://" + c.Request.Host)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("request origin is invalid")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return "", errors.New("request origin must use HTTPS outside local development")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

type agentGenerationEstimateRequest struct {
	Kind     string          `json:"kind" binding:"required"`
	Platform string          `json:"platform"`
	Model    string          `json:"model" binding:"required"`
	Count    int             `json:"count" binding:"required"`
	Request  json.RawMessage `json:"request" binding:"required"`
}

type agentGenerationQuote struct {
	QuoteID        string         `json:"quote_id"`
	Kind           string         `json:"kind"`
	Model          string         `json:"model"`
	RequestHash    string         `json:"request_hash"`
	PricingVersion string         `json:"pricing_version"`
	UnitKind       string         `json:"unit_kind"`
	Units          float64        `json:"units"`
	Count          int            `json:"count"`
	UnitPrice      float64        `json:"unit_price"`
	TotalPrice     float64        `json:"total_price"`
	ActualPrice    float64        `json:"actual_price"`
	Currency       string         `json:"currency"`
	Details        map[string]any `json:"details"`
	CreatedAt      time.Time      `json:"created_at"`
	ValidUntil     time.Time      `json:"valid_until"`
	Active         bool           `json:"active"`
}

func (h *AgentHandler) EstimateGeneration(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil || apiKey.GroupID == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "invalid_api_key", "message": "Invalid API key"}})
		return
	}
	var input agentGenerationEstimateRequest
	if err := c.ShouldBindJSON(&input); err != nil || input.Count <= 0 || input.Count > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_estimate_request", "message": "kind, model, request, and count from 1 to 100 are required"}})
		return
	}
	input.Kind = strings.TrimSpace(strings.ToLower(input.Kind))
	input.Platform = strings.TrimSpace(strings.ToLower(input.Platform))
	input.Model = strings.TrimSpace(input.Model)
	quote, err := h.calculateGenerationQuote(c.Request.Context(), apiKey, input)
	if err != nil {
		status, body := infraerrors.ToHTTP(err)
		if status < 400 || status > 599 {
			status = http.StatusBadRequest
		}
		code := strings.TrimSpace(body.Reason)
		if code == "" {
			code = "generation_estimate_failed"
		}
		message := strings.TrimSpace(body.Message)
		if message == "" {
			message = err.Error()
		}
		c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
		return
	}
	detailsJSON, err := json.Marshal(quote.Details)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "generation_quote_failed", "message": "Failed to serialize quote"}})
		return
	}
	_, err = h.db.ExecContext(c, `
		INSERT INTO agent_generation_quotes(
			id,api_key_id,group_id,kind,model,request_hash,pricing_version,unit_kind,
			units,count,unit_price,total_price,actual_price,currency,details,created_at,expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, quote.QuoteID, apiKey.ID, apiKey.Group.ID, quote.Kind, quote.Model, quote.RequestHash,
		quote.PricingVersion, quote.UnitKind, quote.Units, quote.Count, quote.UnitPrice,
		quote.TotalPrice, quote.ActualPrice, quote.Currency, detailsJSON, quote.CreatedAt, quote.ValidUntil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "generation_quote_failed", "message": "Failed to persist quote"}})
		return
	}
	c.JSON(http.StatusOK, quote)
}

func (h *AgentHandler) calculateGenerationQuote(ctx context.Context, apiKey *service.APIKey, input agentGenerationEstimateRequest) (*agentGenerationQuote, error) {
	now := time.Now().UTC()
	quote := &agentGenerationQuote{
		QuoteID:    "quote_" + uuid.NewString(),
		Kind:       input.Kind,
		Model:      input.Model,
		Count:      input.Count,
		Currency:   "credit",
		CreatedAt:  now,
		ValidUntil: now.Add(10 * time.Minute),
		Active:     true,
	}
	switch input.Kind {
	case "image":
		if h.billingService == nil || h.agentModels == nil {
			return nil, infraerrors.ServiceUnavailable("generation_estimate_failed", "Image pricing service is unavailable")
		}
		var request struct {
			Model string `json:"model"`
			Size  string `json:"size"`
		}
		if err := json.Unmarshal(input.Request, &request); err != nil {
			return nil, infraerrors.BadRequest("invalid_image_request", "Invalid image request")
		}
		if request.Model != "" && request.Model != input.Model {
			return nil, infraerrors.BadRequest("estimate_model_mismatch", "Request model does not match estimate model")
		}
		tier := service.NormalizeImageBillingTierOrDefault(request.Size)
		platform, unitPrice, err := h.resolveAgentImageQuotePrice(ctx, apiKey.Group.ID, input.Platform, input.Model, tier)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("generation_estimate_failed", "Unable to resolve current image pricing").WithCause(err)
		}
		cost, err := h.billingService.CalculateConfiguredAgentImageCost(unitPrice, input.Count)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("generation_estimate_failed", "Unable to resolve current image pricing").WithCause(err)
		}
		quote.UnitKind = "image"
		quote.Units = float64(input.Count)
		quote.UnitPrice = cost.TotalCost / float64(input.Count)
		quote.TotalPrice = cost.TotalCost
		quote.ActualPrice = cost.ActualCost
		quote.Details = map[string]any{"platform": platform, "image_size": tier, "count": input.Count}
		quote.RequestHash = hashJSON(map[string]any{"kind": input.Kind, "platform": platform, "model": input.Model, "image_size": tier, "count": input.Count})
	case "video":
		if h.videoService == nil {
			return nil, infraerrors.ServiceUnavailable("generation_estimate_failed", "Video pricing service is unavailable")
		}
		var request service.VideoCreateRequest
		if err := json.Unmarshal(input.Request, &request); err != nil {
			return nil, infraerrors.BadRequest("invalid_video_request", "Invalid video request")
		}
		if request.Model != "" && request.Model != input.Model {
			return nil, infraerrors.BadRequest("estimate_model_mismatch", "Request model does not match estimate model")
		}
		request.Model = input.Model
		if input.Platform != "" && input.Platform != service.PlatformVideo {
			return nil, infraerrors.BadRequest("estimate_platform_mismatch", "Video generation requires the seedance platform")
		}
		var raw map[string]any
		if err := json.Unmarshal(input.Request, &raw); err == nil {
			raw["model"] = input.Model
			request.Raw = raw
		}
		estimate, err := h.videoService.EstimateGenerationCost(ctx, apiKey, &request, input.Count)
		if err != nil {
			return nil, err
		}
		quote.UnitKind = "second"
		quote.Units = float64(estimate.BillableSeconds)
		quote.UnitPrice = estimate.CreditsPerSecond
		quote.TotalPrice = estimate.TotalCost
		quote.ActualPrice = estimate.ActualCost
		quote.Details = map[string]any{
			"count": input.Count, "resolution": estimate.Resolution, "ability_code": estimate.AbilityCode,
			"generated_seconds": estimate.GeneratedSeconds, "reference_video_seconds": estimate.ReferenceVideoSeconds,
			"billable_seconds": estimate.BillableSeconds, "reference_video_multiplier": estimate.ReferenceVideoMultiplier,
		}
		quote.RequestHash = hashJSON(map[string]any{
			"kind": input.Kind, "model": estimate.Model, "resolution": estimate.Resolution,
			"ability_code": estimate.AbilityCode, "count": input.Count,
			"generated_seconds": estimate.GeneratedSeconds, "reference_video_seconds": estimate.ReferenceVideoSeconds,
		})
	default:
		return nil, infraerrors.BadRequest("invalid_generation_kind", "Generation kind must be image or video")
	}
	quote.PricingVersion = "price_" + hashJSON(map[string]any{
		"request_hash": quote.RequestHash, "unit_price": quote.UnitPrice, "total_price": quote.TotalPrice,
		"actual_price": quote.ActualPrice, "group_updated_at": apiKey.Group.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})[:24]
	return quote, nil
}

func (h *AgentHandler) resolveAgentImageQuotePrice(
	ctx context.Context,
	groupID int64,
	requestedPlatform string,
	model string,
	resolution string,
) (string, float64, error) {
	platforms := []string{service.PlatformOpenAI, service.PlatformGemini}
	if requestedPlatform != "" {
		platforms = []string{requestedPlatform}
	}
	type match struct {
		platform string
		price    float64
	}
	matches := make([]match, 0, 1)
	for _, platform := range platforms {
		price, _, err := h.agentModels.ResolveMediaUnitPrice(
			ctx,
			groupID,
			platform,
			service.AgentMediaTypeImage,
			resolution,
			model,
		)
		if err == nil {
			matches = append(matches, match{platform: platform, price: price})
		}
	}
	if len(matches) == 0 {
		return "", 0, service.ErrAgentImagePricingUnavailable
	}
	if len(matches) > 1 {
		return "", 0, errors.New("image model exists on multiple platforms; platform is required")
	}
	return matches[0].platform, matches[0].price, nil
}

func (h *AgentHandler) GetGenerationEstimate(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "invalid_api_key", "message": "Invalid API key"}})
		return
	}
	var quote agentGenerationQuote
	var detailsJSON []byte
	err := h.db.QueryRowContext(c, `
		SELECT id,kind,model,request_hash,pricing_version,unit_kind,units,count,unit_price,
			total_price,actual_price,currency,details,created_at,expires_at
		FROM agent_generation_quotes WHERE id=$1 AND api_key_id=$2
	`, c.Param("id"), apiKey.ID).Scan(
		&quote.QuoteID, &quote.Kind, &quote.Model, &quote.RequestHash, &quote.PricingVersion,
		&quote.UnitKind, &quote.Units, &quote.Count, &quote.UnitPrice, &quote.TotalPrice,
		&quote.ActualPrice, &quote.Currency, &detailsJSON, &quote.CreatedAt, &quote.ValidUntil,
	)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "generation_quote_not_found", "message": "Generation quote was not found"}})
		return
	}
	if err != nil || json.Unmarshal(detailsJSON, &quote.Details) != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "generation_quote_failed", "message": "Failed to read quote"}})
		return
	}
	quote.Active = quote.ValidUntil.After(time.Now())
	c.JSON(http.StatusOK, quote)
}

func hashJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type trustedMediaMetadata struct {
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	FPS             float64 `json:"fps,omitempty"`
	Container       string  `json:"container,omitempty"`
	VideoCodec      string  `json:"video_codec,omitempty"`
	AudioCodec      string  `json:"audio_codec,omitempty"`
	Encoding        string  `json:"encoding,omitempty"`
	Probe           string  `json:"probe"`
}

type mediaPolicy struct {
	kind       string
	limit      int64
	extensions map[string]bool
}

var mediaPolicies = map[string]mediaPolicy{
	"image/jpeg":      {kind: "image", limit: 30 << 20, extensions: map[string]bool{".jpg": true, ".jpeg": true}},
	"image/png":       {kind: "image", limit: 30 << 20, extensions: map[string]bool{".png": true}},
	"image/webp":      {kind: "image", limit: 30 << 20, extensions: map[string]bool{".webp": true}},
	"image/gif":       {kind: "image", limit: 30 << 20, extensions: map[string]bool{".gif": true}},
	"image/bmp":       {kind: "image", limit: 30 << 20, extensions: map[string]bool{".bmp": true}},
	"image/tiff":      {kind: "image", limit: 30 << 20, extensions: map[string]bool{".tif": true, ".tiff": true}},
	"image/heic":      {kind: "image", limit: 30 << 20, extensions: map[string]bool{".heic": true}},
	"image/heif":      {kind: "image", limit: 30 << 20, extensions: map[string]bool{".heif": true}},
	"video/mp4":       {kind: "video", limit: 200 << 20, extensions: map[string]bool{".mp4": true}},
	"video/quicktime": {kind: "video", limit: 200 << 20, extensions: map[string]bool{".mov": true}},
	"audio/wav":       {kind: "audio", limit: 15 << 20, extensions: map[string]bool{".wav": true}},
	"audio/x-wav":     {kind: "audio", limit: 15 << 20, extensions: map[string]bool{".wav": true}},
	"audio/mpeg":      {kind: "audio", limit: 15 << 20, extensions: map[string]bool{".mp3": true}},
}

func inspectMedia(file multipart.File, header *multipart.FileHeader) (mediaPolicy, string, bool) {
	declared := strings.ToLower(strings.TrimSpace(strings.Split(header.Header.Get("Content-Type"), ";")[0]))
	policy, ok := mediaPolicies[declared]
	if !ok || !policy.extensions[strings.ToLower(filepath.Ext(header.Filename))] {
		return mediaPolicy{}, "", false
	}
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return mediaPolicy{}, "", false
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return mediaPolicy{}, "", false
	}
	detected := detectMediaMIME(buf[:n])
	if detected != declared {
		// Browsers commonly report WAV with either registered MIME spelling.
		if policy.kind != "audio" || (detected != "audio/wav" && detected != "audio/x-wav") {
			return mediaPolicy{}, detected, false
		}
	}
	return policy, declared, true
}

func detectMediaMIME(header []byte) string {
	if len(header) >= 4 {
		if bytes.Equal(header[:4], []byte{'I', 'I', 0x2a, 0x00}) || bytes.Equal(header[:4], []byte{'M', 'M', 0x00, 0x2a}) {
			return "image/tiff"
		}
	}
	if len(header) >= 12 && bytes.Equal(header[4:8], []byte("ftyp")) {
		brand := string(header[8:12])
		switch brand {
		case "heic", "heix", "hevc", "hevx":
			return "image/heic"
		case "mif1", "msf1", "heim", "heis":
			return "image/heif"
		case "qt  ":
			return "video/quicktime"
		default:
			return "video/mp4"
		}
	}
	return strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(header), ";")[0]))
}

func probeTrustedMedia(ctx context.Context, filePath string, policy mediaPolicy, contentType string) (trustedMediaMetadata, error) {
	if policy.kind == "image" {
		file, err := os.Open(filePath)
		if err == nil {
			config, _, decodeErr := image.DecodeConfig(file)
			_ = file.Close()
			if decodeErr == nil && config.Width > 0 && config.Height > 0 {
				return trustedMediaMetadata{Width: config.Width, Height: config.Height, Probe: "go-image"}, nil
			}
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(probeCtx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration,format_name:stream=codec_type,codec_name,width,height,r_frame_rate,duration",
		"-of", "json",
		filePath,
	)
	output, err := command.Output()
	if err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return trustedMediaMetadata{}, errors.New("media probe timed out")
		}
		if errors.Is(err, exec.ErrNotFound) {
			// 运行镜像缺少 ffprobe 时给出可诊断的报错，而不是含糊的"无法解码"。
			return trustedMediaMetadata{}, errors.New("trusted media probe (ffprobe) is not installed")
		}
		return trustedMediaMetadata{}, errors.New("media cannot be decoded by the trusted probe")
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FrameRate string `json:"r_frame_rate"`
			Duration  string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			Name     string `json:"format_name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		return trustedMediaMetadata{}, errors.New("trusted probe returned invalid metadata")
	}
	metadata := trustedMediaMetadata{Probe: "ffprobe"}
	metadata.DurationSeconds, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			metadata.Width = stream.Width
			metadata.Height = stream.Height
			metadata.VideoCodec = normalizeProbeCodec(stream.CodecName)
			metadata.FPS = parseFrameRate(stream.FrameRate)
			if metadata.DurationSeconds <= 0 {
				metadata.DurationSeconds, _ = strconv.ParseFloat(stream.Duration, 64)
			}
		case "audio":
			metadata.AudioCodec = normalizeProbeCodec(stream.CodecName)
			metadata.Encoding = metadata.AudioCodec
			if metadata.DurationSeconds <= 0 {
				metadata.DurationSeconds, _ = strconv.ParseFloat(stream.Duration, 64)
			}
		}
	}
	switch contentType {
	case "video/mp4":
		metadata.Container = "mp4"
	case "video/quicktime":
		metadata.Container = "mov"
	default:
		metadata.Container = strings.Split(probe.Format.Name, ",")[0]
	}
	switch policy.kind {
	case "image":
		if metadata.Width <= 0 || metadata.Height <= 0 {
			return trustedMediaMetadata{}, errors.New("trusted image dimensions are unavailable")
		}
	case "video":
		if metadata.Width <= 0 || metadata.Height <= 0 || metadata.DurationSeconds <= 0 || metadata.FPS <= 0 || metadata.VideoCodec == "" {
			return trustedMediaMetadata{}, errors.New("trusted video dimensions, duration, FPS, or codec are unavailable")
		}
	case "audio":
		if metadata.DurationSeconds <= 0 || metadata.AudioCodec == "" {
			return trustedMediaMetadata{}, errors.New("trusted audio duration or codec is unavailable")
		}
	}
	return metadata, nil
}

func parseFrameRate(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) == 2 {
		numerator, numeratorErr := strconv.ParseFloat(parts[0], 64)
		denominator, denominatorErr := strconv.ParseFloat(parts[1], 64)
		if numeratorErr == nil && denominatorErr == nil && denominator > 0 {
			return numerator / denominator
		}
	}
	rate, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return rate
}

func normalizeProbeCodec(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "h264":
		return "h264"
	case "hevc", "h265":
		return "h265"
	default:
		return strings.ToLower(strings.TrimSpace(codec))
	}
}

func temporaryAssetQuota() (int64, int64) {
	maxCount, maxBytes := int64(100), int64(2<<30)
	if raw := strings.TrimSpace(os.Getenv("AGENT_ASSETS_DAILY_MAX_COUNT")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			maxCount = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_ASSETS_DAILY_MAX_BYTES")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			maxBytes = parsed
		}
	}
	return maxCount, maxBytes
}

// assetLocalDir 返回当前生效的素材根目录。配置了 local_dir 时用配置值（Docker 部署
// 指向宿主机 bind mount 进来的真实目录），否则回落到构造时的默认目录。
func (h *AgentHandler) assetLocalDir(ctx context.Context) (string, error) {
	if h == nil {
		return "", errors.New("agent handler is unavailable")
	}
	if h.fileStorage != nil {
		return h.fileStorage.EffectiveLocalPath(ctx)
	}
	if strings.TrimSpace(h.dataDir) == "" {
		return "", errors.New("asset data directory is unavailable")
	}
	if err := os.MkdirAll(h.dataDir, 0700); err != nil {
		return "", err
	}
	return h.dataDir, nil
}

func (h *AgentHandler) temporaryAssetStorageRuntime(ctx context.Context) (*service.FileStorageRuntime, error) {
	if h.fileStorage != nil {
		return h.fileStorage.Runtime(ctx)
	}
	maxCount, maxBytes := temporaryAssetQuota()
	backend := "local"
	if h.objectStore != nil {
		backend = "s3"
	}
	return &service.FileStorageRuntime{
		Config: service.FileStorageConfig{
			SchemaVersion:  1,
			Backend:        backend,
			RetentionHours: 24,
			DailyMaxCount:  maxCount,
			DailyMaxBytes:  maxBytes,
			S3:             service.BackupS3Config{Prefix: "agent-assets/"},
		},
		Store: h.objectStore,
	}, nil
}

func (h *AgentHandler) fileStorageObjectStore(ctx context.Context) (service.BackupObjectStore, error) {
	if h.objectStore != nil {
		return h.objectStore, nil
	}
	if h.fileStorage == nil {
		return nil, nil //nolint:nilnil // local-only deployments do not have an object store
	}
	runtime, err := h.fileStorage.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Store, nil
}

func temporaryAssetPublicURL(publicBaseURL string, id uuid.UUID, contentType string) string {
	return strings.TrimRight(publicBaseURL, "/") + "/media/" + id.String() + "/asset" + canonicalMediaExtension(contentType)
}

// temporaryAssetCustomURL 构造自定义域名直读地址：对象 key 直接挂在域名后。对象存储
// 返回响应时使用上传时写入的 Content-Type，路径里无需扩展名。
func temporaryAssetCustomURL(base, storageKey string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(storageKey, "/")
}

type temporaryAssetUploadResult struct {
	ID          uuid.UUID            `json:"id"`
	URL         string               `json:"url"`
	ContentType string               `json:"content_type"`
	Size        int64                `json:"size"`
	SHA256      string               `json:"sha256"`
	Metadata    trustedMediaMetadata `json:"metadata"`
	CreatedAt   time.Time            `json:"created_at"`
	ExpiresAt   time.Time            `json:"expires_at"`
	LeaseUntil  time.Time            `json:"lease_until"`
}

type temporaryAssetUploadError struct {
	status  int
	code    string
	message string
	extra   gin.H
}

func (e *temporaryAssetUploadError) Error() string {
	if e == nil {
		return "temporary asset upload failed"
	}
	if e.message != "" {
		return e.message
	}
	return e.code
}

// UploadTemporaryAsset publishes one trusted multipart asset for later use as
// a public reference URL in a video generation request.
func (h *AgentHandler) UploadTemporaryAsset(c *gin.Context) {
	h.uploadTemporaryAsset(c, http.StatusCreated)
}

// UploadTemporaryAssetCompat is the legacy /v1/files adapter used by older
// desktop clients. It reuses the Agent asset pipeline verbatim and only
// changes the successful status code expected by that client.
func (h *AgentHandler) UploadTemporaryAssetCompat(c *gin.Context) {
	h.uploadTemporaryAsset(c, http.StatusOK)
}

func (h *AgentHandler) uploadTemporaryAsset(c *gin.Context, successStatus int) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code":    "invalid_api_key",
			"message": "Invalid API key",
		}})
		return
	}

	started := time.Now()
	file, header, digest, err := h.receiveTemporaryAsset(c)
	if err != nil {
		status, code := http.StatusBadRequest, "file_required"
		if uploadErr, ok := err.(*temporaryAssetUploadError); ok {
			status, code = uploadErr.status, uploadErr.code
		}
		c.JSON(status, gin.H{"error": gin.H{"code": code, "message": err.Error()}})
		return
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	received := time.Since(started)
	result, err := h.storeTemporaryAssetPart(c, apiKey, file, header, digest)
	c.Header("Server-Timing", fmt.Sprintf("receive;dur=%.3f, validate;dur=%.3f, store;dur=%.3f, total;dur=%.3f", float64(received.Microseconds())/1000, c.GetFloat64("assetValidateMs"), c.GetFloat64("assetStoreMs"), float64(time.Since(started).Microseconds())/1000))
	if err != nil {
		if uploadErr, ok := err.(*temporaryAssetUploadError); ok {
			message := uploadErr.message
			if message == "" {
				message = uploadErr.code
			}
			body := gin.H{"code": uploadErr.code, "message": message}
			for key, value := range uploadErr.extra {
				body[key] = value
			}
			c.JSON(uploadErr.status, gin.H{"error": body})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code":    "media_upload_failed",
			"message": "Failed to upload media",
		}})
		return
	}

	c.JSON(successStatus, result)
}

// uploadTemporaryAssetPart stores one multipart file for the standalone Agent
// upload endpoint and the one-shot multipart video request path.
func (h *AgentHandler) uploadTemporaryAssetPart(c *gin.Context, key *service.APIKey, file multipart.File, header *multipart.FileHeader) (*temporaryAssetUploadResult, error) {
	return h.storeTemporaryAssetPart(c, key, file, header, "")
}

func (h *AgentHandler) storeTemporaryAssetPart(c *gin.Context, key *service.APIKey, file multipart.File, header *multipart.FileHeader, digest string) (*temporaryAssetUploadResult, error) {
	validateStarted := time.Now()
	if h == nil || key == nil || file == nil || header == nil {
		return nil, &temporaryAssetUploadError{status: http.StatusBadRequest, code: "invalid_upload"}
	}
	runtime, err := h.temporaryAssetStorageRuntime(c.Request.Context())
	if err != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "file_storage_unavailable"}
	}
	requestOrigin, err := requestPublicOrigin(c)
	if err != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "temporary_asset_public_url_invalid"}
	}
	publicBaseURL := requestOrigin
	if h.fileStorage != nil {
		publicBaseURL, err = h.fileStorage.EffectivePublicBaseURL(c.Request.Context(), requestOrigin)
		if err != nil {
			return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "temporary_asset_public_url_invalid"}
		}
	}
	policy, contentType, ok := inspectMedia(file, header)
	if !ok {
		return nil, &temporaryAssetUploadError{status: http.StatusBadRequest, code: "unsupported_media"}
	}
	if header.Size <= 0 || header.Size > policy.limit {
		return nil, &temporaryAssetUploadError{status: http.StatusRequestEntityTooLarge, code: "media_too_large", extra: gin.H{"limit_bytes": policy.limit}}
	}
	maxCount, maxBytes := runtime.Config.DailyMaxCount, runtime.Config.DailyMaxBytes
	var currentCount, currentBytes int64
	// 只统计参考素材：这套每日配额是给下游上传用的（界面文案也是这么写的），上游
	// 回捞的生成产物不该消耗它，否则生成越多、这个凭据能上传的素材反而越少。
	err = h.db.QueryRowContext(c, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM temporary_assets WHERE api_key_id=$1 AND user_id=$2 AND purpose=$3 AND created_at>NOW()-INTERVAL '24 hours' AND deleted_at IS NULL`, key.ID, key.UserID, service.TemporaryAssetPurposeReference).Scan(&currentCount, &currentBytes)
	if err != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "database_error"}
	}
	if currentCount >= maxCount || currentBytes+header.Size > maxBytes {
		return nil, &temporaryAssetUploadError{status: http.StatusTooManyRequests, code: "temporary_asset_quota_exceeded", extra: gin.H{
			"max_count_24h":    maxCount,
			"max_bytes_24h":    maxBytes,
			"retry_after_hint": "when prior uploads leave the rolling 24-hour window",
		}}
	}
	id := uuid.New()
	token, tokenErr := randomToken(32)
	if tokenErr != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "storage_error"}
	}
	root, err := h.assetLocalDir(c.Request.Context())
	if err != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "file_storage_unavailable"}
	}
	dir := filepath.Join(root, id.String())
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "storage_error"}
	}
	target := filepath.Join(dir, "object")
	n := header.Size
	if digest != "" {
		spool, ok := file.(*os.File)
		if !ok {
			_ = os.RemoveAll(dir)
			return nil, errors.New("invalid spool")
		}
		if err = os.Rename(spool.Name(), target); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	} else {
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		hash := sha256.New()
		n, err = io.Copy(io.MultiWriter(out, hash), io.LimitReader(file, policy.limit+1))
		closeErr := out.Close()
		if err != nil || closeErr != nil || n > policy.limit || n != header.Size {
			_ = os.RemoveAll(dir)
			return nil, &temporaryAssetUploadError{status: http.StatusBadRequest, code: "upload_failed"}
		}
		digest = hex.EncodeToString(hash.Sum(nil))
	}
	metadata, probeErr := probeTrustedMedia(c.Request.Context(), target, policy, contentType)
	if probeErr != nil {
		_ = os.RemoveAll(dir)
		return nil, &temporaryAssetUploadError{status: http.StatusUnprocessableEntity, code: "media_probe_failed", message: probeErr.Error()}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "media_probe_failed", message: "Failed to serialize trusted media metadata"}
	}
	// 总容量上限：先按"最早失效优先"提前驱逐未租用素材腾出空间，避免容量满时
	// 新上传直接被拒。驱逐不足时明确区分"容量被租约占满"和"存储不可用"。
	if h.fileStorage != nil {
		if _, capacityErr := h.fileStorage.EnforceTemporaryAssetCapacity(c.Request.Context(), service.TemporaryAssetPurposeReference, n); capacityErr != nil {
			_ = os.RemoveAll(dir)
			if errors.Is(capacityErr, service.ErrTemporaryAssetCapacityExhausted) {
				return nil, &temporaryAssetUploadError{status: http.StatusInsufficientStorage, code: "temporary_asset_capacity_exceeded"}
			}
			return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "file_storage_unavailable"}
		}
	}
	c.Set("assetValidateMs", float64(time.Since(validateStarted).Microseconds())/1000)
	storeStarted := time.Now()
	defer func() { c.Set("assetStoreMs", float64(time.Since(storeStarted).Microseconds())/1000) }()
	expires := time.Now().Add(time.Duration(runtime.Config.RetentionHours) * time.Hour)
	backend, storageKey := "local", target
	if runtime.Config.Backend == "s3" {
		if runtime.Store == nil {
			_ = os.RemoveAll(dir)
			return nil, &temporaryAssetUploadError{status: http.StatusServiceUnavailable, code: "file_storage_unavailable"}
		}
		f, openErr := os.Open(target)
		if openErr != nil {
			_ = os.RemoveAll(dir)
			return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "storage_error"}
		}
		storageKey = runtime.Config.S3.Prefix + id.String()
		_, err = uploadMediaFile(c, runtime.Store, storageKey, f, contentType, n)
		_ = f.Close()
		if err != nil {
			_ = os.RemoveAll(dir)
			return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "storage_error"}
		}
		backend = "s3"
		_ = os.RemoveAll(dir)
	}
	leaseUntil := time.Now().UTC().Add(10 * time.Minute)
	_, err = h.db.ExecContext(c, `INSERT INTO temporary_assets(id,user_id,api_key_id,group_id,public_token_hash,storage_backend,storage_key,original_filename,media_type,mime_type,size_bytes,sha256,metadata,expires_at,lease_until,purpose) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, key.UserID, key.ID, key.GroupID, hashToken(token), backend, storageKey, filepath.Base(header.Filename), policy.kind, contentType, n, digest, metadataJSON, expires, leaseUntil, service.TemporaryAssetPurposeReference)
	if err != nil {
		if backend == "s3" {
			_ = runtime.Store.Delete(context.Background(), storageKey)
		} else {
			_ = os.RemoveAll(dir)
		}
		return nil, &temporaryAssetUploadError{status: http.StatusInternalServerError, code: "database_error"}
	}
	assetURL := temporaryAssetPublicURL(publicBaseURL, id, contentType)
	if backend == "s3" {
		// 配置了自定义域名时，参考素材返回给下游的是对象存储直读地址：上游直接从
		// 对象存储取文件，不经过平台代理。
		if base := runtime.Config.S3.CustomAssetBase(); base != "" {
			assetURL = temporaryAssetCustomURL(base, storageKey)
		}
	}
	return &temporaryAssetUploadResult{
		ID:          id,
		URL:         assetURL,
		ContentType: contentType,
		Size:        n,
		SHA256:      digest,
		Metadata:    metadata,
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   expires.UTC(),
		LeaseUntil:  leaseUntil,
	}, nil
}

func (h *AgentHandler) ServeTemporaryAsset(c *gin.Context) {
	var backend, file, name, ct string
	var size int64
	var expires time.Time
	err := h.db.QueryRowContext(c, `SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,GREATEST(expires_at,lease_until) AS expires_at FROM temporary_assets WHERE public_token_hash=$1 AND deleted_at IS NULL`, hashToken(c.Param("token"))).Scan(&backend, &file, &name, &ct, &size, &expires)
	if err != nil || time.Now().After(expires) {
		c.Status(404)
		return
	}
	if !h.serveTemporaryAssetContent(c, backend, file, name, ct, size) {
		return
	}
	_, _ = h.db.ExecContext(context.Background(), `UPDATE temporary_assets SET last_accessed_at=NOW() WHERE public_token_hash=$1`, hashToken(c.Param("token")))
}

func (h *AgentHandler) ServeCleanTemporaryAsset(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	var backend, file, name, ct string
	var size int64
	var expires time.Time
	err = h.db.QueryRowContext(c, `SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,GREATEST(expires_at,lease_until) AS expires_at FROM temporary_assets WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&backend, &file, &name, &ct, &size, &expires)
	if err != nil || time.Now().After(expires) || c.Param("filename") != "asset"+canonicalMediaExtension(ct) {
		c.Status(http.StatusNotFound)
		return
	}
	if !h.serveTemporaryAssetContent(c, backend, file, name, ct, size) {
		return
	}
	_, _ = h.db.ExecContext(context.Background(), `UPDATE temporary_assets SET last_accessed_at=NOW() WHERE id=$1`, id)
}

func (h *AgentHandler) serveTemporaryAssetContent(c *gin.Context, backend, file, name, contentType string, size int64) bool {
	if backend == "s3" {
		return h.serveS3Media(c, file, name, contentType, size)
	}
	f, err := os.Open(file)
	if err != nil {
		c.Status(http.StatusNotFound)
		return false
	}
	defer func() { _ = f.Close() }()
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	http.ServeContent(c.Writer, c.Request, name, time.Time{}, f)
	return true
}

func canonicalMediaExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mp4":
		return ".m4a"
	default:
		return ".bin"
	}
}

func (h *AgentHandler) CleanupExpired(ctx context.Context) (int64, error) {
	tx, err := h.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id,storage_backend,storage_key FROM temporary_assets WHERE GREATEST(expires_at,lease_until)<=NOW() AND deleted_at IS NULL ORDER BY GREATEST(expires_at,lease_until) LIMIT 200 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	type expiredAsset struct {
		id      uuid.UUID
		backend string
		key     string
	}
	assets := make([]expiredAsset, 0, 200)
	for rows.Next() {
		var asset expiredAsset
		if err := rows.Scan(&asset.id, &asset.backend, &asset.key); err != nil {
			_ = rows.Close()
			return 0, err
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var n int64
	store, storeErr := h.fileStorageObjectStore(ctx)
	for _, asset := range assets {
		if asset.backend == "s3" {
			if storeErr != nil || store == nil {
				continue
			}
			err = store.Delete(ctx, asset.key)
		} else {
			err = os.RemoveAll(filepath.Dir(asset.key))
		}
		if err != nil {
			continue
		}
		res, execErr := tx.ExecContext(ctx, `UPDATE temporary_assets SET deleted_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, asset.id)
		if execErr == nil {
			x, _ := res.RowsAffected()
			n += x
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	_, _ = h.db.ExecContext(ctx, `DELETE FROM agent_generation_quotes WHERE expires_at<NOW()-INTERVAL '24 hours'`)
	return n, nil
}

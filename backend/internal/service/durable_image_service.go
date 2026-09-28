package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type DurableImageService struct {
	ledger    ImageTaskLedger
	encryptor SecretEncryptor
	files     *FileStorageService
	legacy    *ImageStorageSettingService
	publisher *TemporaryAssetPublisher
	billing   *BillingService
	resolver  *ModelPricingResolver
	rates     UserGroupRateRepository
	keys      *APIKeyService
	cache     *BillingCacheService
	openAI    *OpenAIGatewayService
	gateway   *GatewayService
	once      sync.Once
	cancel    context.CancelFunc
}

func NewDurableImageService(ledger ImageTaskLedger, encryptor SecretEncryptor, files *FileStorageService, legacy *ImageStorageSettingService, publisher *TemporaryAssetPublisher, billing *BillingService, resolver *ModelPricingResolver, rates UserGroupRateRepository, keys *APIKeyService, cache *BillingCacheService, openAI *OpenAIGatewayService, gateway *GatewayService) *DurableImageService {
	s := &DurableImageService{ledger: ledger, encryptor: encryptor, files: files, legacy: legacy, publisher: publisher, billing: billing, resolver: resolver, rates: rates, keys: keys, cache: cache, openAI: openAI, gateway: gateway}
	files.legacyImages = legacy
	keys.imageTaskLookup = func(ctx context.Context, keyID int64, idem string) bool {
		key, err := keys.GetByID(ctx, keyID)
		if err != nil {
			return false
		}
		_, err = ledger.Find(ctx, ImageTaskOwner{key.UserID, key.ID}, idem, true)
		return err == nil
	}
	return s
}
func (s *DurableImageService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *DurableImageService) Enabled(ctx context.Context) bool {
	if s == nil || s.ledger == nil || s.files == nil {
		return false
	}
	cfg, _, err := s.files.loadEffectiveConfig(ctx)
	if err != nil {
		return false
	}
	if cfg.Generated != nil {
		return cfg.Generated.AsyncImagesEnabled
	}
	if s.legacy == nil {
		return false
	}
	_, enabled := s.legacy.resolve()
	return enabled
}
func (s *DurableImageService) quote(ctx context.Context, key *APIKey, sub *UserSubscription, platform, model, size string, count int) (ImageTaskQuote, error) {
	requestedModel := model
	if public, ok := RequestedPublicModelFromContext(ctx); ok && key.Group != nil && key.Group.Platform == PlatformComposite {
		requestedModel = public
		concrete := model
		if resolved, ok := ResolvedUpstreamModelFromContext(ctx); ok {
			concrete = resolved
		}
		model = s.gateway.compositeBillableModel(ctx, key, public, concrete)
	}
	q := ImageTaskQuote{QuotaPlatform: platform, Model: model, Size: NormalizeImageBillingTierOrDefault(size), Count: count, PricingAt: time.Now(), Multiplier: 1}
	if count < 1 || count > 10 {
		return q, errors.New("image count must be between 1 and 10")
	}
	if key.Group == nil || key.GroupID == nil {
		return q, errors.New("image group context required")
	}
	var reserved *CostBreakdown
	if key.Group.IsAgent() {
		price, _, err := s.resolver.ResolveAgentMediaUnitPrice(ctx, key.Group.ID, platform, AgentMediaTypeImage, q.Size, model)
		if err != nil {
			return q, err
		}
		reserved, _ = s.billing.CalculateConfiguredAgentImageCost(price, count)
		q.PerImage, _ = s.billing.CalculateConfiguredAgentImageCost(price, 1)
	} else {
		multiplier := key.Group.RateMultiplier
		if s.rates != nil {
			m, err := s.rates.GetByUserAndGroup(ctx, key.UserID, *key.GroupID)
			if err != nil {
				return q, err
			}
			if m != nil {
				multiplier = *m
			}
		}
		tokenMultiplier, imageMultiplier := computePeakAwareMultipliers(key, multiplier, q.PricingAt)
		q.Multiplier = tokenMultiplier
		resolved := s.resolver.Resolve(ctx, PricingInput{Model: model, GroupID: key.GroupID, Group: key.Group})
		if resolved != nil && resolved.Source == PricingSourceChannel && resolved.Mode == BillingModeToken {
			q.TokenPricing = resolved
			q.ChannelPricing = resolved.channelPricing
			q.LongContext = resolved.longContextPricingEnabled
			var err error
			reserved, _, err = s.billing.EstimateImageGenerationCost(ctx, key, s.rates, model, q.Size, count)
			if err != nil {
				return q, err
			}
		} else {
			q.Multiplier = imageMultiplier
			if platform == PlatformGemini {
				q.PerImage = s.gateway.calculateImageCost(ctx, &ForwardResult{ImageCount: 1, ImageSize: q.Size}, key, model, imageMultiplier)
			} else {
				q.PerImage = s.openAI.calculateOpenAIImageCost(ctx, model, key, &OpenAIForwardResult{ImageCount: 1, ImageSize: q.Size}, imageMultiplier)
			}
			if q.PerImage == nil {
				return q, errors.New("image price is unavailable")
			}
			c := *q.PerImage
			applyCostBreakdownMultiplier(&c, float64(count))
			reserved = &c
		}
	}
	if reserved == nil {
		return q, errors.New("image quote is unavailable")
	}
	reserved.ActualCost = QuantizeUsageBillingAmount(reserved.ActualCost)
	q.Charge = UsageBillingCommand{APIKeyID: key.ID, UserID: key.UserID, Model: model, MediaType: "image", APIKeyQuotaCost: reserved.ActualCost, APIKeyRateLimitCost: reserved.ActualCost}
	if sub != nil && key.Group.IsSubscriptionType() {
		q.Charge.SubscriptionID = &sub.ID
		q.Charge.SubscriptionCost = reserved.ActualCost
		q.Charge.BillingType = BillingTypeSubscription
	} else {
		q.Charge.BalanceCost = reserved.ActualCost
	}
	q.Usage = UsageLog{UserID: key.UserID, APIKeyID: key.ID, Model: model, RequestedModel: requestedModel, GroupID: key.GroupID, SubscriptionID: q.Charge.SubscriptionID, BillingType: q.Charge.BillingType, ActualCost: reserved.ActualCost, ImageSize: &q.Size, RateMultiplier: q.Multiplier, CreatedAt: q.PricingAt}
	mode := string(BillingModeImage)
	if q.TokenPricing != nil {
		mode = string(BillingModeToken)
	}
	q.Usage.BillingMode = &mode
	return q, nil
}
func (s *DurableImageService) Accept(ctx context.Context, key *APIKey, sub *UserSubscription, platform, model, size string, count int, idempotency, fingerprint string, snapshot *ImageRequestSnapshot) (*ImageTask, error) {
	if len(idempotency) > 200 {
		return nil, errors.New("Idempotency-Key exceeds 200 bytes")
	}
	owner := ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}
	if idempotency != "" {
		old, err := s.ledger.Find(ctx, owner, idempotency, true)
		if err == nil {
			if old.Fingerprint != fingerprint {
				return nil, ErrImageIdempotencyConflict
			}
			return imageTaskToPublic(&old.ImageTaskRecord), nil
		}
		if !errors.Is(err, ErrImageTaskNotFound) {
			return nil, err
		}
	}
	q, err := s.quote(ctx, key, sub, platform, model, size, count)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	encrypted, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cfg, _, err := s.files.loadEffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	retention := cfg.ResultRetentionHours
	id := "imgtask_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	q.Charge.RequestID = "image:" + id + ":precharge"
	q.Usage.RequestID = q.Charge.RequestID
	t := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: id, UserID: key.UserID, APIKeyID: key.ID, Status: ImageTaskStatusProcessing, Phase: "queued", BillingStatus: "precharged", RefundStatus: "none", CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(retention) * time.Hour).Unix(), DeadlineAt: now.Add(30 * time.Minute).Unix()}, Quote: q, IdempotencyKey: idempotency, Fingerprint: fingerprint, EncryptedRequest: encrypted}
	t, _, err = s.ledger.Accept(ctx, t)
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, t)
	return imageTaskToPublic(&t.ImageTaskRecord), nil
}

// Receipt lookup does not resolve artifact URLs. An expired artifact must never
// turn a replay of an accepted request into a new synchronous generation.
func (s *DurableImageService) Receipt(ctx context.Context, owner ImageTaskOwner, idempotency string) (*ImageTask, error) {
	t, err := s.ledger.Find(ctx, owner, idempotency, true)
	if err != nil {
		return nil, err
	}
	return imageTaskToPublic(&t.ImageTaskRecord), nil
}

func (s *DurableImageService) Get(ctx context.Context, owner ImageTaskOwner, key string, idempotency bool) (*ImageTask, error) {
	t, err := s.ledger.Find(ctx, owner, key, idempotency)
	if err != nil {
		return nil, err
	}
	if t.Phase == "interrupted" || t.Phase == "refunding" {
		t.Status = ImageTaskStatusFailed
		t.RefundStatus = "pending"
		t.BillingStatus = "refunding"
	}
	if t.Status == ImageTaskStatusCompleted {
		result, err := s.refreshResult(ctx, t.Result, owner)
		if err != nil {
			return nil, err
		}
		t.Result = result
	}
	return imageTaskToPublic(&t.ImageTaskRecord), nil
}
func (s *DurableImageService) invalidate(ctx context.Context, t *DurableImageTask) {
	if s.cache != nil {
		_ = s.cache.InvalidateUserBalance(ctx, t.UserID)
		s.cache.RollbackUserPlatformQuotaUsage(ctx, t.UserID, t.Quote.QuotaPlatform, 0)
		_ = s.cache.InvalidateAPIKeyRateLimit(ctx, t.APIKeyID)
		if t.Quote.Usage.GroupID != nil {
			_ = s.cache.InvalidateSubscription(ctx, t.UserID, *t.Quote.Usage.GroupID)
		}
	}
	if s.keys != nil {
		if key, err := s.keys.GetByID(ctx, t.APIKeyID); err == nil {
			s.keys.InvalidateAuthCacheByKey(ctx, key.Key)
		}
	}
}
func (s *DurableImageService) Start(execute ImageTaskExecutor) {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		for i := 0; i < 8; i++ {
			go s.worker(ctx, execute)
		}
	})
}
func (s *DurableImageService) worker(ctx context.Context, execute ImageTaskExecutor) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			task, err := s.ledger.Claim(ctx)
			if errors.Is(err, ErrImageTaskNotFound) {
				continue
			}
			if err != nil {
				slog.Warn("image task claim failed", "error", err)
				continue
			}
			s.run(ctx, task, execute)
		}
	}
}
func (s *DurableImageService) run(parent context.Context, t *DurableImageTask, execute ImageTaskExecutor) {
	ctx, cancel := context.WithDeadline(parent, time.Unix(t.DeadlineAt, 0))
	defer cancel()
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go func() {
		timer := time.NewTicker(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := s.ledger.Renew(ctx, t.ID, t.LeaseToken); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		if v := recover(); v != nil {
			slog.Error("image task worker panic", "task_id", t.ID, "panic", v)
			s.fail(t, "image task interrupted")
		}
	}()
	if t.Phase == "interrupted" || t.Phase == "refunding" {
		s.fail(t, "upstream execution could not be confirmed before its lease or deadline expired")
		return
	}
	raw, err := s.encryptor.Decrypt(t.EncryptedRequest)
	if err != nil {
		s.fail(t, "request snapshot unavailable")
		return
	}
	var snapshot ImageRequestSnapshot
	if json.Unmarshal([]byte(raw), &snapshot) != nil {
		s.fail(t, "invalid request snapshot")
		return
	}
	key, err := s.keys.GetByID(ctx, t.APIKeyID)
	if err != nil {
		s.fail(t, "task owner is unavailable")
		return
	}
	// Legacy image tasks retain their S3 location until first unified save. This
	// override is also used by publication inside the existing sync handlers.
	cfg, _, err := s.files.loadEffectiveConfig(ctx)
	if err != nil {
		s.fail(t, "storage configuration unavailable")
		return
	}
	if cfg.Generated == nil && s.legacy != nil {
		old, e := s.legacy.effectiveConfig(ctx)
		if e != nil {
			s.fail(t, "legacy storage configuration unavailable")
			return
		}
		cfg.Generated = &GeneratedStorageConfig{Backend: "s3", PresignExpiryHours: old.PresignExpiry, S3: BackupS3Config{Endpoint: old.Endpoint, Region: old.Region, Bucket: old.Bucket, Prefix: old.Prefix, AccessKeyID: old.AccessKeyID, SecretAccessKey: old.SecretAccessKey, ForcePathStyle: old.ForcePathStyle, CustomDomain: old.PublicBaseURL}}
	}
	ctx = context.WithValue(ctx, generatedStorageOverrideKey{}, cfg.Generated)
	var result json.RawMessage
	if t.EncryptedResult == "" {
		capture := &AsyncImageExecution{TaskID: t.ID, Route: snapshot.Route, Platform: snapshot.Platform, Quote: &t.Quote}
		execCtx := WithAsyncImageExecution(ctx, capture)
		status, body, err := execute(execCtx, &snapshot, key, capture)
		t.CapturedUsage = capture.Usage
		if err != nil || status < 200 || status >= 300 || !json.Valid(body) {
			s.fail(t, "image generation failed")
			return
		}
		if capture.Usage == nil {
			s.fail(t, "upstream usage could not be confirmed")
			return
		}
		t.EncryptedResult, err = s.encryptor.Encrypt(string(body))
		if err != nil {
			s.fail(t, "could not protect generated response")
			return
		}
		if err = s.ledger.SaveResponse(ctx, t); err != nil {
			return
		} // lease recovery compensates; never re-generate
		result = body
	} else {
		decoded, e := s.encryptor.Decrypt(t.EncryptedResult)
		if e != nil {
			s.fail(t, "saved response unavailable")
			return
		}
		result = json.RawMessage(decoded)
	}
	normalized, count, err := s.publishResult(ctx, t, key, snapshot.PublicBaseURL, result)
	if err != nil {
		s.fail(t, "failed to validate or store generated images")
		return
	}
	usage, err := s.settlement(ctx, t, count)
	if err != nil {
		s.fail(t, "could not settle image usage")
		return
	}
	if ctx.Err() != nil {
		s.fail(t, "image task exceeded its deadline")
		return
	}
	t.Result = normalized
	t.HTTPStatus = http.StatusOK
	finalizeCtx, c := context.WithTimeout(context.WithoutCancel(parent), 20*time.Second)
	defer c()
	if err := s.ledger.Finalize(finalizeCtx, t, usage, true); err != nil {
		slog.Warn("image settlement will be retried", "task_id", t.ID, "error", err)
		return
	}
	s.invalidate(finalizeCtx, t)
}
func (s *DurableImageService) fail(t *DurableImageTask, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = s.ledger.MarkRefundPending(ctx, t)
	t.Error = imageTaskErrorJSON("image_generation_failed", message)
	t.HTTPStatus = http.StatusBadGateway
	t.Result = nil
	if err := s.ledger.Finalize(ctx, t, t.CapturedUsage, false); err != nil {
		slog.Warn("image refund will be retried", "task_id", t.ID, "error", err)
		return
	}
	s.invalidate(ctx, t)
}
func (s *DurableImageService) settlement(ctx context.Context, t *DurableImageTask, count int) (*UsageLog, error) {
	if t.CapturedUsage == nil {
		return nil, errors.New("missing upstream usage")
	}
	log := *t.CapturedUsage
	log.ImageCount = count
	cost, err := calculateFrozenImageCost(ctx, s.billing, s.resolver, &t.Quote, UsageTokens{InputTokens: log.InputTokens, OutputTokens: log.OutputTokens, ImageInputTokens: log.ImageInputTokens, ImageOutputTokens: log.ImageOutputTokens, CacheCreationTokens: log.CacheCreationTokens, CacheReadTokens: log.CacheReadTokens}, count)
	if err != nil {
		return nil, err
	}

	log.ActualCost = cost.ActualCost // upstream statistical cost remains captured separately
	return &log, nil
}

func (s *DurableImageService) publishResult(ctx context.Context, t *DurableImageTask, key *APIKey, base string, result json.RawMessage) (json.RawMessage, int, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(result, &top); err != nil {
		return nil, 0, err
	}
	var items []map[string]json.RawMessage
	if data, ok := top["data"]; ok {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, 0, err
		}
	} else {
		var native struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text       string `json:"text"`
						InlineData *struct {
							Data     string `json:"data"`
							MimeType string `json:"mimeType"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(result, &native); err != nil {
			return nil, 0, err
		}
		texts := []string{}
		for _, c := range native.Candidates {
			for _, p := range c.Content.Parts {
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
				if p.InlineData != nil {
					b, _ := json.Marshal(p.InlineData.Data)
					items = append(items, map[string]json.RawMessage{"b64_json": b})
				}
			}
		}
		delete(top, "candidates")
		if len(texts) > 0 {
			top["text"], _ = json.Marshal(texts)
		}
	}
	if len(items) == 0 {
		return nil, 0, errors.New("upstream returned no images")
	}
	owner := TemporaryAssetOwner{UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID}
	fetcher := NewImageResultUploader(nil, "", maxPublishedGeneratedImageBytes, newGeneratedVideoHTTPClient())
	for i, item := range items {
		var rawURL string
		_ = json.Unmarshal(item["url"], &rawURL)
		assetID := ""
		if parsed, e := url.Parse(rawURL); e == nil {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) == 3 && parts[0] == "media" {
				assetID = parts[1]
			}
		}
		if assetID != "" {
			if _, err := s.assetURL(ctx, assetID, ImageTaskOwner{key.UserID, key.ID}, base); err != nil {
				assetID = ""
			}
		}
		if assetID == "" {
			if rawURL != "" && !strings.HasPrefix(rawURL, "data:") {
				if err := validateGeneratedVideoURL(ctx, rawURL, false); err != nil {
					return nil, 0, err
				}
			}
			data, _, err := fetcher.fetchImageBytes(ctx, item)
			if err != nil {
				return nil, 0, err
			}
			stableID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d", t.ID, i)))
			publishCtx := context.WithValue(ctx, generatedAssetIDKey{}, stableID)
			stableURL, err := s.publisher.PublishGeneratedImage(publishCtx, owner, base, base64.StdEncoding.EncodeToString(data), "")
			if err != nil {
				return nil, 0, err
			}
			assetID = stableID.String()
			rawURL = stableURL
		}
		item["asset_id"], _ = json.Marshal(assetID)
		item["url"], _ = json.Marshal(rawURL)
		delete(item, "b64_json")
	}
	top["data"], _ = json.Marshal(items)
	if _, ok := top["model"]; !ok {
		top["model"], _ = json.Marshal(t.Quote.Model)
	}
	normalized, err := json.Marshal(top)
	return normalized, len(items), err
}
func (s *DurableImageService) assetURL(ctx context.Context, id string, owner ImageTaskOwner, base string) (string, error) {
	var backend, key, mime string
	err := s.files.db.QueryRowContext(ctx, `SELECT storage_backend,storage_key,mime_type FROM temporary_assets WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND purpose='generated' AND deleted_at IS NULL AND expires_at>NOW()`, id, owner.UserID, owner.APIKeyID).Scan(&backend, &key, &mime)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrImageTaskExpired
	}
	if err != nil {
		return "", err
	}
	if backend == "s3" {
		direct, err := s.files.GeneratedObjectURL(ctx, key)
		if err != nil || direct != "" {
			return direct, err
		}
	}
	extension := ".png"
	switch mime {
	case "image/jpeg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	case "image/gif":
		extension = ".gif"
	}
	return strings.TrimRight(base, "/") + "/media/" + id + "/asset" + extension, nil
}
func (s *DurableImageService) refreshResult(ctx context.Context, result json.RawMessage, owner ImageTaskOwner) (json.RawMessage, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(result, &top); err != nil {
		return nil, err
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(top["data"], &items); err != nil {
		return nil, err
	}
	for _, item := range items {
		var id, old string
		_ = json.Unmarshal(item["asset_id"], &id)
		_ = json.Unmarshal(item["url"], &old)
		if id == "" {
			continue
		}
		u, _ := url.Parse(old)
		base := ""
		if u != nil {
			base = u.Scheme + "://" + u.Host
		}
		fresh, err := s.assetURL(ctx, id, owner, base)
		if err != nil {
			return nil, err
		}
		item["url"], _ = json.Marshal(fresh)
	}
	top["data"], _ = json.Marshal(items)
	return json.Marshal(top)
}

func calculateFrozenImageCost(ctx context.Context, billing *BillingService, resolver *ModelPricingResolver, quote *ImageTaskQuote, tokens UsageTokens, count int) (*CostBreakdown, error) {
	if quote.TokenPricing != nil {
		resolved := *quote.TokenPricing
		resolved.channelPricing = quote.ChannelPricing
		resolved.longContextPricingEnabled = quote.LongContext
		return billing.CalculateCostUnified(CostInput{Ctx: ctx, Model: quote.Model, Resolved: &resolved, Resolver: resolver, Tokens: tokens, RateMultiplier: quote.Multiplier, PricingAt: quote.PricingAt})
	}
	if quote.PerImage == nil {
		return nil, errors.New("missing image quote")
	}
	cost := *quote.PerImage
	applyCostBreakdownMultiplier(&cost, float64(count))
	return &cost, nil
}

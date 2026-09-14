package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	videoProviderAigod    = "aigod"
	videoProviderNewtoken = "newtoken"
	videoProviderMikuapi  = "mikuapi"
	videoProviderJingyu   = "jingyu"
	// videoAigodSubjectType：aigod 要求参考图/参考视频统一声明主体类型。
	// 该上游把参考素材一律按真人链路处理，与下游是否传 subject_type 无关。
	videoAigodSubjectType       = "person"
	videoDefaultBaseURL         = "https://api.aigod.one"
	videoDefaultNewtokenBaseURL = "https://newtoken.club"
	videoDefaultMikuapiBaseURL  = "https://mikuapi.org"
	videoDefaultAPIPath         = "/v1/videos"
	videoDefaultJingyuBaseURL   = "https://api.jingyuapi.art"
	videoDefaultJingyuAPIPath   = "/v1/video/generations"
	videoJingyuSeedance20Model  = "yu-video-2-pro"
	videoJingyuSeedance25Model  = "yu-video-2.5-pro"
	// newtoken encodes the output resolution into the upstream model id, so the
	// adapter routes on (downstream model, resolution) instead of a static map.
	videoNewtokenSeedance20720PModel     = "sd2.0-720p-official"
	videoNewtokenSeedance201080PModel    = "sd2.0-1080p-official"
	videoNewtokenSeedance20Fast720PModel = "sd2.0-720p-fast-official"
	videoNewtokenSeedance25720PModel     = "sd2.5-720p-official"
	videoNewtokenSeedance251080PModel    = "sd2.5-1080p-official"
	videoDefaultPollInterval             = 2 * time.Second
	// videoDefaultPollTimeout：Seedance 视频任务的轮询超时，aigod 与 newtoken 统一
	// 使用 15 分钟。上游最长链条是 aigod 的"真人过白（≤10 分钟）+ 生成"，
	// 15 分钟覆盖该窗口并留出生成余量。账号 extra 里的 poll_timeout_ms 仍可覆盖。
	videoDefaultPollTimeout    = 15 * time.Minute
	videoDefaultRequestTimeout = 60 * time.Second
	videoDefaultConnectTimeout = 15 * time.Second
	videoNewtokenPollInterval  = 5 * time.Second
	// mikuapi 的视频创建立即返回任务 id，成片要自己轮询；按 5 秒一轮，
	// 与 newtoken 一样不去打秒级轮询。
	videoMikuapiPollInterval     = 5 * time.Second
	videoJingyuPollInterval      = 5 * time.Second
	videoJingyuPollTimeout       = 30 * time.Minute
	videoJingyuRequestTimeout    = 30 * time.Minute
	videoJingyuConnectTimeout    = 60 * time.Second
	videoNewtokenRequestTimeout  = 5 * time.Minute
	videoNewtokenConnectTimeout  = 15 * time.Second
	videoMinDurationSeconds      = 4
	videoMaxDurationSeconds      = 15
	videoSeedance25MaxDuration   = 30
	videoMaxReferenceVideoTotal  = 15
	videoSeedance25MaxReferences = 50
	videoPublicIDPrefix          = "video_"
	videoObject                  = "video"
	videoAbilityTextToVideo      = "video_text_to_video"
	videoAbilityImageToVideo     = "video_image_to_video"
	videoAbilityStartEndToVideo  = "video_start_end_to_video"
	videoAbilityReferenceToVideo = "video_reference_to_video"
)

type VideoService struct {
	accountRepo          VideoAccountRepository
	taskRepo             VideoTaskRepository
	pricingRepo          VideoGroupPricingRuleRepository
	usageLogRepo         UsageLogRepository
	usageBillingRepo     UsageBillingRepository
	userRepo             UserRepository
	userSubRepo          UserSubscriptionRepository
	apiKeyService        APIKeyQuotaUpdater
	billingCache         *BillingCacheService
	deferredService      *DeferredService
	balanceNotify        *BalanceNotifyService
	quotaRepo            UserPlatformQuotaRepository
	httpUpstream         HTTPUpstream
	cfg                  *config.Config
	agentModels          *AgentModelCatalogService
	videoResultPublisher VideoResultPublisher
	apiKeyLoader         *APIKeyService

	startLifecycleFunc func(VideoTaskLifecycleInput)
}

func (s *VideoService) SetAgentModelCatalog(catalog *AgentModelCatalogService) {
	if s != nil {
		s.agentModels = catalog
	}
}

func (s *VideoService) SetVideoResultPublisher(publisher VideoResultPublisher) {
	if s != nil {
		s.videoResultPublisher = publisher
	}
}

func NewVideoService(
	accountRepo VideoAccountRepository,
	taskRepo VideoTaskRepository,
	pricingRepo VideoGroupPricingRuleRepository,
	usageLogRepo UsageLogRepository,
	usageBillingRepo UsageBillingRepository,
	userRepo UserRepository,
	userSubRepo UserSubscriptionRepository,
	apiKeyService *APIKeyService,
	billingCache *BillingCacheService,
	deferredService *DeferredService,
	balanceNotify *BalanceNotifyService,
	quotaRepo UserPlatformQuotaRepository,
	httpUpstream HTTPUpstream,
	cfg *config.Config,
) *VideoService {
	return &VideoService{
		accountRepo:      accountRepo,
		taskRepo:         taskRepo,
		pricingRepo:      pricingRepo,
		usageLogRepo:     usageLogRepo,
		usageBillingRepo: usageBillingRepo,
		userRepo:         userRepo,
		userSubRepo:      userSubRepo,
		apiKeyService:    apiKeyService,
		apiKeyLoader:     apiKeyService,
		billingCache:     billingCache,
		deferredService:  deferredService,
		balanceNotify:    balanceNotify,
		quotaRepo:        quotaRepo,
		httpUpstream:     httpUpstream,
		cfg:              cfg,
	}
}

func (s *VideoService) CreateTask(ctx context.Context, input *VideoCreateInput) (*VideoResponse, error) {
	if input == nil || input.APIKey == nil || input.APIKey.User == nil || input.APIKey.Group == nil || input.Request == nil {
		return nil, videoBadRequest("invalid_video_request", "Invalid video request")
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return s.createTask(ctx, input)
	}

	coordinator := DefaultIdempotencyCoordinator()
	if coordinator == nil {
		return nil, ErrIdempotencyStoreUnavail
	}
	actorScope := "api_key:" + strconv.FormatInt(input.APIKey.ID, 10)
	result, err := coordinator.Execute(ctx, IdempotencyExecuteOptions{
		// The repository uniqueness key is (scope, key hash), so the API key is
		// part of the scope as well as the fingerprint to prevent cross-key collisions.
		Scope:          "openai.videos.create." + actorScope,
		ActorScope:     actorScope,
		Method:         http.MethodPost,
		Route:          videoDefaultAPIPath,
		IdempotencyKey: input.IdempotencyKey,
		Payload:        videoIdempotencyPayload(input),
		TTL:            DefaultWriteIdempotencyTTL(),
	}, func(execCtx context.Context) (any, error) {
		return s.createTask(execCtx, input)
	})
	if err != nil {
		return nil, err
	}
	input.IdempotencyReplayed = result.Replayed
	return decodeIdempotentVideoResponse(result.Data)
}

func (s *VideoService) createTask(ctx context.Context, input *VideoCreateInput) (*VideoResponse, error) {
	if input == nil || input.APIKey == nil || input.APIKey.User == nil || input.APIKey.Group == nil || input.Request == nil {
		return nil, videoBadRequest("invalid_video_request", "Invalid video request")
	}
	if input.APIKey.Group.Platform != PlatformVideo && !input.APIKey.Group.IsAgent() {
		return nil, videoBadRequest("video_platform_required", "Video API is not available for this API key group")
	}

	normalized, rule, estimate, err := s.estimateGenerationCost(ctx, input.APIKey, input.Request, 1)
	if err != nil {
		return nil, err
	}
	totalCost := estimate.TotalCost
	actualCost := estimate.ActualCost
	if err := validateVideoEstimatedCost(input.APIKey, input.Subscription, actualCost); err != nil {
		return nil, err
	}

	account, err := s.selectAccountForRequest(ctx, input.APIKey.Group.ID, normalized, input.APIKey.Group.IsAgent())
	if err != nil {
		return nil, err
	}
	upstreamModel := videoUpstreamModelForAccount(account, normalized)
	upstreamBody := videoUpstreamBodyForAccount(account, normalized, upstreamModel)
	upstreamEndpoint := normalizedVideoEndpoint(input.UpstreamEndpoint)
	if accountEndpoint := videoAccountAPIPath(account); accountEndpoint != "" {
		upstreamEndpoint = accountEndpoint
	}

	publicID := generateVideoPublicID()
	task, err := s.taskRepo.Create(ctx, &VideoTaskCreateInput{
		PublicID:                 publicID,
		RequestID:                optionalTrimmedPtr(input.RequestID),
		UserID:                   input.APIKey.User.ID,
		APIKeyID:                 input.APIKey.ID,
		GroupID:                  input.APIKey.Group.ID,
		AccountID:                account.ID,
		Model:                    normalized.Model,
		UpstreamModel:            upstreamModel,
		Resolution:               normalized.Resolution,
		DurationSeconds:          normalized.GeneratedSeconds,
		ReferenceDurationSeconds: normalized.ReferenceVideoSeconds,
		BillableSeconds:          estimate.BillableSeconds,
		CostPerSecond:            rule.CreditsPerSecond,
		TotalCost:                totalCost,
		ActualCost:               actualCost,
		Status:                   VideoTaskStatusQueued,
	})
	if err != nil {
		return nil, err
	}
	inboundEndpoint := normalizedVideoEndpoint(input.InboundEndpoint)
	if err := s.billCreatedTask(ctx, task, input.APIKey, input.Subscription, account, input.RequestPayloadHash, input.UserAgent, input.IPAddress, inboundEndpoint, upstreamEndpoint); err != nil {
		return nil, err
	}

	lifecycleInput := VideoTaskLifecycleInput{
		PublicID:            task.PublicID,
		Account:             account,
		APIKey:              input.APIKey,
		Subscription:        input.Subscription,
		UpstreamBody:        upstreamBody,
		RequestPayloadHash:  input.RequestPayloadHash,
		UserAgent:           input.UserAgent,
		IPAddress:           input.IPAddress,
		InboundEndpoint:     inboundEndpoint,
		UpstreamEndpoint:    upstreamEndpoint,
		ResultPublicBaseURL: input.ResultPublicBaseURL,
	}
	if s.startLifecycleFunc != nil {
		s.startLifecycleFunc(lifecycleInput)
	} else {
		s.startLifecycle(lifecycleInput)
	}
	return videoResponseFromTask(task), nil
}

func videoIdempotencyPayload(input *VideoCreateInput) any {
	if input == nil {
		return map[string]string{"request_sha256": ""}
	}
	hash := strings.TrimSpace(input.RequestPayloadHash)
	if hash == "" {
		hash = HashUsageRequestPayload(input.RawBody)
	}
	if hash != "" {
		return map[string]string{"request_sha256": hash}
	}
	return input.Request
}

func decodeIdempotentVideoResponse(data any) (*VideoResponse, error) {
	if response, ok := data.(*VideoResponse); ok && response != nil {
		return response, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, ErrIdempotencyStoreUnavail.WithCause(err)
	}
	var response VideoResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, ErrIdempotencyStoreUnavail.WithCause(err)
	}
	if strings.TrimSpace(response.ID) == "" {
		return nil, ErrIdempotencyStoreUnavail
	}
	return &response, nil
}

// EstimateGenerationCost is the authoritative pre-charge estimate used by
// Yingzo confirmation quotes. It intentionally shares normalization and the
// pricing rule lookup with CreateTask.
func (s *VideoService) EstimateGenerationCost(
	ctx context.Context,
	apiKey *APIKey,
	request *VideoCreateRequest,
	count int,
) (*VideoCostEstimate, error) {
	_, _, estimate, err := s.estimateGenerationCost(ctx, apiKey, request, count)
	return estimate, err
}

func (s *VideoService) HasEnabledPricing(ctx context.Context, groupID int64, model string) (bool, error) {
	if s == nil || s.pricingRepo == nil || groupID <= 0 {
		return false, nil
	}
	rules, err := s.pricingRepo.ListByGroupID(ctx, groupID)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		if rule.Enabled && rule.ModelCode == model && IsSupportedVideoResolution(model, rule.Resolution) {
			return true, nil
		}
	}
	return false, nil
}

func (s *VideoService) estimateGenerationCost(
	ctx context.Context,
	apiKey *APIKey,
	request *VideoCreateRequest,
	count int,
) (*normalizedVideoRequest, *VideoGroupPricingRule, *VideoCostEstimate, error) {
	if apiKey == nil || apiKey.Group == nil || request == nil {
		return nil, nil, nil, videoBadRequest("invalid_video_request", "Invalid video request")
	}
	if count <= 0 {
		return nil, nil, nil, videoBadRequest("invalid_video_count", "Video count must be positive")
	}
	var normalized *normalizedVideoRequest
	var err error
	if apiKey.Group.IsAgent() {
		normalized, err = normalizeAgentVideoCreateRequest(request)
	} else {
		normalized, err = normalizeVideoCreateRequest(request)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var rule *VideoGroupPricingRule
	if apiKey.Group.IsAgent() {
		if s.agentModels == nil {
			return nil, nil, nil, videoBadRequest("video_pricing_rule_not_found", "Video pricing rule is not configured")
		}
		unitPrice, modelCode, priceErr := s.agentModels.ResolveMediaUnitPrice(
			ctx,
			apiKey.Group.ID,
			PlatformVideo,
			AgentMediaTypeVideo,
			normalized.Resolution,
			normalized.Model,
		)
		if priceErr != nil {
			return nil, nil, nil, videoBadRequest("video_pricing_rule_not_found", "Video pricing rule is not configured")
		}
		rule = &VideoGroupPricingRule{
			GroupID:          apiKey.Group.ID,
			ModelCode:        modelCode,
			Resolution:       normalized.Resolution,
			CreditsPerSecond: unitPrice,
			Enabled:          true,
		}
	} else {
		rule, err = s.pricingRepo.GetEnabledRule(ctx, apiKey.Group.ID, normalized.Model, normalized.Resolution)
		if err != nil {
			if errors.Is(err, ErrVideoPricingRuleNotFound) {
				return nil, nil, nil, videoBadRequest("video_pricing_rule_not_found", "Video pricing rule is not configured")
			}
			return nil, nil, nil, err
		}
	}
	referenceMultiplier := 1.0
	billableSecondsPerOutput := normalized.BillableSeconds
	perOutputCost := float64(billableSecondsPerOutput) * rule.CreditsPerSecond
	rateMultiplier := apiKey.Group.RateMultiplier
	if rateMultiplier <= 0 {
		rateMultiplier = 1
	}
	if apiKey.Group.IsAgent() {
		referenceMultiplier = 0
		billableSecondsPerOutput = normalized.GeneratedSeconds
		perOutputCost = float64(billableSecondsPerOutput) * rule.CreditsPerSecond
		rateMultiplier = 1
	}
	totalCost := perOutputCost * float64(count)
	estimate := &VideoCostEstimate{
		Model:                    normalized.Model,
		Resolution:               normalized.Resolution,
		AbilityCode:              normalized.AbilityCode,
		Count:                    count,
		GeneratedSeconds:         normalized.GeneratedSeconds,
		ReferenceVideoSeconds:    normalized.ReferenceVideoSeconds,
		BillableSeconds:          billableSecondsPerOutput * count,
		CreditsPerSecond:         rule.CreditsPerSecond,
		ReferenceVideoMultiplier: referenceMultiplier,
		RateMultiplier:           rateMultiplier,
		TotalCost:                totalCost,
		ActualCost:               totalCost * rateMultiplier,
		PricingRuleUpdatedAt:     rule.UpdatedAt,
	}
	return normalized, rule, estimate, nil
}

func (s *VideoService) GetTask(ctx context.Context, publicID string, apiKey *APIKey) (*VideoResponse, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" || apiKey == nil {
		return nil, ErrVideoTaskNotFound
	}
	task, err := s.taskRepo.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if task.APIKeyID != apiKey.ID || task.UserID != apiKey.UserID {
		return nil, ErrVideoTaskNotFound
	}
	return videoResponseFromTask(task), nil
}

func (s *VideoService) selectAccountForRequest(ctx context.Context, groupID int64, normalized *normalizedVideoRequest, agentGroup bool) (*Account, error) {
	if normalized == nil {
		return nil, ErrVideoAccountNotFound
	}
	model := normalized.Model
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, groupID, PlatformVideo)
	if err != nil {
		return nil, err
	}
	candidates := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Platform != PlatformVideo || account.Type != AccountTypeAPIKey || !account.IsSchedulable() {
			continue
		}
		if strings.TrimSpace(account.GetCredential("api_key")) == "" {
			continue
		}
		if !account.IsModelSupported(model) {
			continue
		}
		// 账号级分辨率白名单必须无条件生效，不能塞进下面按 provider 判定的兼容性
		// 检查：那段只对 newtoken 运行，aigod 账号会整条漏过，于是配了分辨率限制
		// 的 aigod 账号仍会被调度到它其实生成不了的档位上。
		if !videoAccountSupportsResolution(&account, model, normalized.Resolution) {
			continue
		}
		if agentGroup {
			current := agentDiscoverySet(discoverAgentModels([]Account{account}))
			if _, supported := current[agentModelKey(PlatformVideo, model)]; !supported {
				continue
			}
			if videoProviderNeedsRequestCompatibility(videoAccountProvider(&account)) && !isVideoAccountCompatibleForRequest(&account, normalized) {
				continue
			}
		} else if !isVideoAccountCompatibleForRequest(&account, normalized) {
			continue
		}
		candidates = append(candidates, account)
	}
	if len(candidates) == 0 {
		return nil, ErrVideoAccountNotFound
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.LastUsedAt == nil && b.LastUsedAt != nil {
			return true
		}
		if a.LastUsedAt != nil && b.LastUsedAt == nil {
			return false
		}
		if a.LastUsedAt == nil && b.LastUsedAt == nil {
			return a.ID < b.ID
		}
		if !a.LastUsedAt.Equal(*b.LastUsedAt) {
			return a.LastUsedAt.Before(*b.LastUsedAt)
		}
		return a.ID < b.ID
	})
	selected := candidates[0]
	// 记录"已使用"，让同优先级账号按 last_used_at 轮转。放在这里而不是调用方：
	// 轮转状态是"选中"这个动作的固有副作用，任何调用方都不该有机会漏掉它。
	// 失败只记日志——轮转信息缺失不该让一次本可成功的生成失败。
	if err := s.accountRepo.UpdateLastUsed(ctx, selected.ID); err != nil {
		slog.Warn("failed to record video account last-used time", "account_id", selected.ID, "error", err)
	}
	return &selected, nil
}

func (s *VideoService) createUpstreamTask(ctx context.Context, account *Account, body map[string]any) (*videoUpstreamCreateResult, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint, err := videoAccountEndpoint(account)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, videoAccountDuration(account, "request_timeout_ms", videoAccountDefaultDuration(account, "request_timeout_ms")))
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.doUpstream(req, account)
	if err != nil {
		return nil, &videoUpstreamError{StatusCode: 0, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody}
	}
	var payload map[string]any
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody, Err: err}
	}
	id := videoTaskIDFromPayload(payload)
	if id == "" {
		id = videoTaskIDFromLocation(resp.Header.Get("Location"))
	}
	if id == "" {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody, Err: errors.New("missing upstream task id")}
	}
	return &videoUpstreamCreateResult{ID: id}, nil
}

func (s *VideoService) pollUpstreamTask(ctx context.Context, account *Account, upstreamTaskID string) (*videoPollResult, error) {
	baseEndpoint, err := videoAccountEndpoint(account)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(baseEndpoint, "/") + "/" + url.PathEscape(upstreamTaskID)
	reqCtx, cancel := context.WithTimeout(ctx, videoAccountDuration(account, "request_timeout_ms", videoAccountDefaultDuration(account, "request_timeout_ms")))
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	resp, err := s.doUpstream(req, account)
	if err != nil {
		return nil, &videoUpstreamError{StatusCode: 0, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody}
	}
	var payload map[string]any
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody, Err: err}
	}
	rawStatus := stringFromMap(payload, "status")
	status := normalizeVideoUpstreamStatus(rawStatus)
	result := &videoPollResult{Status: status}
	if status == VideoTaskStatusCompleted {
		// 成片地址由适配器决定：多数上游把地址放在状态响应里，mikuapi 只提供
		// /v1/videos/{id}/content，需要按任务 id 拼出来。注意这里传的是**基础**
		// endpoint（.../v1/videos），任务 id 由适配器自己拼，避免拼成
		// /v1/videos/{id}/{id}/content。
		result.VideoURL = videoAbsoluteResultURL(baseEndpoint, videoResultURLForAccount(account, baseEndpoint, upstreamTaskID, payload))
		if result.VideoURL == "" {
			return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody, Err: errors.New("missing video result URL")}
		}
	}
	return result, nil
}

func (s *VideoService) doUpstream(req *http.Request, account *Account) (*http.Response, error) {
	if s.httpUpstream == nil {
		return http.DefaultClient.Do(req)
	}
	proxyURL := ""
	if account != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	concurrency := 1
	if account != nil {
		concurrency = account.Concurrency
	}
	return s.httpUpstream.Do(req, proxyURL, account.ID, concurrency)
}

func (s *VideoService) startLifecycle(input VideoTaskLifecycleInput) {
	if s == nil || input.Account == nil || input.APIKey == nil || strings.TrimSpace(input.PublicID) == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("video lifecycle panic", "task_id", input.PublicID, "panic", r)
			}
		}()

		created, err := s.createUpstreamTask(context.Background(), input.Account, input.UpstreamBody)
		if err != nil {
			clientErr := mapVideoUpstreamError(err, false)
			s.recordVideoAccountFailure(context.Background(), input.Account, clientErr, err)
			task, _ := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
				Status:    stringPtr(VideoTaskStatusFailed),
				ErrorJSON: videoErrorJSON(clientErr.VideoClientError),
			})
			_ = s.refundFailedTask(context.Background(), task, input.APIKey, input.Subscription, input.Account, input.RequestPayloadHash, input.UserAgent, input.IPAddress, input.InboundEndpoint, input.UpstreamEndpoint)
			return
		}

		processingStatus := VideoTaskStatusProcessing
		if _, err := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
			Status:         &processingStatus,
			UpstreamTaskID: &created.ID,
		}); err != nil {
			slog.Warn("video task submit state update failed", "task_id", input.PublicID, "error", err)
			return
		}
		s.pollLifecycle(input, created.ID)
	}()
}

func (s *VideoService) pollLifecycle(input VideoTaskLifecycleInput, upstreamTaskID string) {
	intervalFallback := videoAccountDefaultDuration(input.Account, "poll_interval_ms")
	timeoutFallback := videoAccountDefaultDuration(input.Account, "poll_timeout_ms")
	interval := videoAccountDuration(input.Account, "poll_interval_ms", intervalFallback)
	timeout := videoAccountDuration(input.Account, "poll_timeout_ms", timeoutFallback)
	if interval <= 0 {
		interval = intervalFallback
	}
	if timeout <= 0 {
		timeout = timeoutFallback
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 轮询容错：上游（尤其 mikuapi）偶发查不到任务或短暂 5xx 时不能一次就判失败，
	// 否则用户被扣费却拿不到成片。容忍次数由适配器声明，见 shouldAbandonVideoPoll。
	pollTolerance := videoPollFailureTolerance(input.Account)
	consecutiveFailures := 0
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			clientErr := videoClientError("video_service_unavailable", "Video service is temporarily unavailable. Please retry later.")
			task, _ := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
				Status:    stringPtr(VideoTaskStatusFailed),
				ErrorJSON: videoErrorJSON(clientErr),
			})
			_ = s.refundFailedTask(context.Background(), task, input.APIKey, input.Subscription, input.Account, input.RequestPayloadHash, input.UserAgent, input.IPAddress, input.InboundEndpoint, input.UpstreamEndpoint)
			return
		case <-ticker.C:
			result, err := s.pollUpstreamTask(ctx, input.Account, upstreamTaskID)
			if err != nil {
				consecutiveFailures++
				clientErr := mapVideoUpstreamError(err, true)
				s.recordVideoAccountFailure(context.Background(), input.Account, clientErr, err)
				if !shouldAbandonVideoPoll(clientErr.Retryable, consecutiveFailures, pollTolerance) {
					if !clientErr.Retryable {
						// 上游暂时查不到任务/抖动：在容忍次数内继续轮询，不判失败。
						slog.Warn("video poll failure tolerated",
							"task_id", input.PublicID,
							"provider", videoAccountProvider(input.Account),
							"consecutive_failures", consecutiveFailures,
							"tolerance", pollTolerance,
							"error", err)
					}
					continue
				}
				task, _ := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
					Status:    stringPtr(VideoTaskStatusFailed),
					ErrorJSON: videoErrorJSON(clientErr.VideoClientError),
				})
				_ = s.refundFailedTask(context.Background(), task, input.APIKey, input.Subscription, input.Account, input.RequestPayloadHash, input.UserAgent, input.IPAddress, input.InboundEndpoint, input.UpstreamEndpoint)
				return
			}
			consecutiveFailures = 0
			switch result.Status {
			case VideoTaskStatusQueued, VideoTaskStatusProcessing:
				_, _ = s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
					Status: &result.Status,
				})
			case VideoTaskStatusCompleted:
				publishedURL, publishErr := s.publishVideoResult(ctx, input, result.VideoURL)
				if publishErr != nil {
					slog.Warn("video result publication failed", "task_id", input.PublicID, "error", publishErr)
					continue
				}
				now := time.Now().UTC()
				task, updateErr := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
					Status:         &result.Status,
					ResultVideoURL: &publishedURL,
					CompletedAt:    &now,
				})
				if updateErr == nil {
					_ = s.recordCompletedTask(context.Background(), task, input.APIKey, input.Subscription, input.Account, input.UserAgent, input.IPAddress, input.InboundEndpoint, input.UpstreamEndpoint)
				}
				return
			case VideoTaskStatusFailed, VideoTaskStatusCancelled:
				clientErr := videoClientError("video_generation_failed", "Video generation failed. Please retry with a different prompt or input.")
				task, _ := s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
					Status:    &result.Status,
					ErrorJSON: videoErrorJSON(clientErr),
				})
				_ = s.refundFailedTask(context.Background(), task, input.APIKey, input.Subscription, input.Account, input.RequestPayloadHash, input.UserAgent, input.IPAddress, input.InboundEndpoint, input.UpstreamEndpoint)
				return
			default:
				_, _ = s.taskRepo.UpdateByPublicID(context.Background(), input.PublicID, VideoTaskUpdate{
					Status: stringPtr(VideoTaskStatusProcessing),
				})
			}
		}
	}
}

func (s *VideoService) publishVideoResult(ctx context.Context, input VideoTaskLifecycleInput, upstreamURL string) (string, error) {
	if s == nil || s.videoResultPublisher == nil {
		return "", errors.New("video result publisher is unavailable")
	}
	if input.APIKey == nil || input.APIKey.User == nil || input.APIKey.Group == nil {
		return "", errors.New("video result owner is unavailable")
	}
	owner := TemporaryAssetOwner{
		UserID:   input.APIKey.User.ID,
		APIKeyID: input.APIKey.ID,
		GroupID:  input.APIKey.Group.ID,
	}
	// 有的上游成片地址是受保护的下载端点（mikuapi 的 /content），回捞时要带 key；
	// 其余上游给的是预签名/公开地址，保持原样不带授权头。
	var publishedURL string
	var err error
	if authorization := videoProviderAdapterForAccount(input.Account).ResultAuthorization(input.Account); authorization != "" {
		authenticated, ok := s.videoResultPublisher.(AuthenticatedVideoResultPublisher)
		if !ok {
			return "", errors.New("video result publisher does not support authenticated downloads")
		}
		publishedURL, err = authenticated.PublishGeneratedVideoWithAuth(
			ctx, owner, input.ResultPublicBaseURL, upstreamURL, authorization,
		)
	} else {
		publishedURL, err = s.videoResultPublisher.PublishGeneratedVideo(
			ctx, owner, input.ResultPublicBaseURL, upstreamURL,
		)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(publishedURL) == "" {
		return "", errors.New("video result publisher returned an empty URL")
	}
	return publishedURL, nil
}

func (s *VideoService) billCreatedTask(ctx context.Context, task *VideoTask, apiKey *APIKey, subscription *UserSubscription, account *Account, payloadHash, userAgent, ipAddress, inboundEndpoint, upstreamEndpoint string) error {
	if task == nil || apiKey == nil || apiKey.User == nil || apiKey.Group == nil || account == nil {
		return nil
	}
	if task.BilledAt != nil {
		return nil
	}
	usageLog, isSubscriptionBill := buildVideoUsageLog(task, apiKey, subscription, account, videoUsageLogOptions{
		RequestID:        "video:" + task.PublicID,
		UserAgent:        userAgent,
		IPAddress:        ipAddress,
		InboundEndpoint:  inboundEndpoint,
		UpstreamEndpoint: upstreamEndpoint,
		CreatedAt:        time.Now().UTC(),
	})
	if usageLog == nil {
		return nil
	}
	if err := s.applyVideoUsageBilling(ctx, usageLog, task, apiKey, subscription, account, payloadHash, isSubscriptionBill); err != nil {
		slog.Error("video prebilling failed", "task_id", task.PublicID, "error", err)
		return err
	}
	if s.usageLogRepo != nil {
		if _, err := s.usageLogRepo.Create(ctx, usageLog); err != nil {
			slog.Warn("video usage log write failed", "task_id", task.PublicID, "error", err)
		}
	}
	if _, err := s.taskRepo.MarkBilled(ctx, task.PublicID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func (s *VideoService) recordCompletedTask(ctx context.Context, task *VideoTask, apiKey *APIKey, subscription *UserSubscription, account *Account, userAgent, ipAddress, inboundEndpoint, upstreamEndpoint string) error {
	if task == nil || apiKey == nil || account == nil || task.ResultVideoURL == nil || s.usageLogRepo == nil {
		return nil
	}
	updater, ok := s.usageLogRepo.(VideoUsageResultUpdater)
	if !ok {
		return nil
	}
	if err := updater.UpdateVideoResult(ctx, "video:"+task.PublicID, apiKey.ID, VideoUsageResultUpdate{
		ResultURL:                *task.ResultVideoURL,
		DurationMs:               videoTaskDurationMs(task),
		InboundEndpoint:          normalizedVideoEndpoint(inboundEndpoint),
		UpstreamEndpoint:         normalizedVideoEndpoint(upstreamEndpoint),
		VideoTaskID:              task.PublicID,
		VideoResolution:          task.Resolution,
		VideoDurationSeconds:     task.DurationSeconds,
		ReferenceDurationSeconds: task.ReferenceDurationSeconds,
		BillableSeconds:          task.BillableSeconds,
	}); err != nil {
		slog.Warn("video completion usage log update failed", "task_id", task.PublicID, "error", err)
		return err
	}
	return nil
}

func (s *VideoService) refundFailedTask(ctx context.Context, task *VideoTask, apiKey *APIKey, subscription *UserSubscription, account *Account, payloadHash, userAgent, ipAddress, inboundEndpoint, upstreamEndpoint string) error {
	if task == nil || apiKey == nil || apiKey.User == nil || apiKey.Group == nil || account == nil {
		return nil
	}
	if s.taskRepo != nil {
		if latest, err := s.taskRepo.GetByPublicID(ctx, task.PublicID); err == nil && latest != nil {
			task = latest
		}
	}
	if task.BilledAt == nil || task.RefundedAt != nil || task.ActualCost <= 0 {
		return nil
	}
	durationMs := videoTaskDurationMs(task)
	inboundEndpoint = normalizedVideoEndpoint(inboundEndpoint)
	upstreamEndpoint = normalizedVideoEndpoint(upstreamEndpoint)
	if updater, ok := s.usageLogRepo.(VideoUsageResultUpdater); ok {
		if err := updater.UpdateVideoResult(ctx, "video:"+task.PublicID, apiKey.ID, VideoUsageResultUpdate{
			DurationMs:       durationMs,
			InboundEndpoint:  inboundEndpoint,
			UpstreamEndpoint: upstreamEndpoint,
		}); err != nil {
			slog.Warn("video failed usage log update failed", "task_id", task.PublicID, "error", err)
		}
	}
	usageLog, isSubscriptionBill := buildVideoUsageLog(task, apiKey, subscription, account, videoUsageLogOptions{
		RequestID:        "video:" + task.PublicID + ":refund",
		UserAgent:        userAgent,
		IPAddress:        ipAddress,
		InboundEndpoint:  inboundEndpoint,
		UpstreamEndpoint: upstreamEndpoint,
		DurationMs:       durationMs,
		CreatedAt:        time.Now().UTC(),
		TotalCost:        -task.TotalCost,
		ActualCost:       -task.ActualCost,
		OutputCost:       -task.TotalCost,
	})
	if usageLog == nil {
		return nil
	}
	if err := s.applyVideoUsageBilling(ctx, usageLog, task, apiKey, subscription, account, payloadHash+":refund", isSubscriptionBill); err != nil {
		slog.Error("video refund billing failed", "task_id", task.PublicID, "error", err)
		return err
	}
	if s.usageLogRepo != nil {
		if _, err := s.usageLogRepo.Create(ctx, usageLog); err != nil {
			slog.Warn("video refund usage log write failed", "task_id", task.PublicID, "error", err)
		}
	}
	if _, err := s.taskRepo.MarkRefunded(ctx, task.PublicID, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

type videoUsageLogOptions struct {
	RequestID        string
	UserAgent        string
	IPAddress        string
	InboundEndpoint  string
	UpstreamEndpoint string
	CreatedAt        time.Time
	DurationMs       *int
	TotalCost        float64
	ActualCost       float64
	OutputCost       float64
	ResultVideoURL   *string
}

func buildVideoUsageLog(task *VideoTask, apiKey *APIKey, subscription *UserSubscription, account *Account, opts videoUsageLogOptions) (*UsageLog, bool) {
	if task == nil || apiKey == nil || apiKey.User == nil || apiKey.Group == nil || account == nil {
		return nil, false
	}
	billingMode := string(BillingModeVideoDuration)
	createdAt := opts.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	totalCost := opts.TotalCost
	actualCost := opts.ActualCost
	outputCost := opts.OutputCost
	if totalCost == 0 && actualCost == 0 && outputCost == 0 && opts.ResultVideoURL == nil {
		totalCost = task.TotalCost
		actualCost = task.ActualCost
		outputCost = task.TotalCost
	}
	requestID := strings.TrimSpace(opts.RequestID)
	if requestID == "" {
		requestID = "video:" + task.PublicID
	}
	rateMultiplier := apiKey.Group.RateMultiplier
	if apiKey.Group.IsAgent() {
		rateMultiplier = 1
	}
	usageLog := &UsageLog{
		UserID:                        apiKey.User.ID,
		APIKeyID:                      apiKey.ID,
		AccountID:                     account.ID,
		RequestID:                     requestID,
		Model:                         task.Model,
		RequestedModel:                task.Model,
		UpstreamModel:                 optionalNonEqualStringPtr(task.UpstreamModel, task.Model),
		GroupID:                       &task.GroupID,
		InputCost:                     0,
		OutputCost:                    outputCost,
		TotalCost:                     totalCost,
		ActualCost:                    actualCost,
		RateMultiplier:                rateMultiplier,
		AccountRateMultiplier:         videoFloat64Ptr(account.BillingRateMultiplier()),
		BillingType:                   BillingTypeBalance,
		RequestType:                   RequestTypeVideo,
		Stream:                        false,
		OpenAIWSMode:                  false,
		DurationMs:                    opts.DurationMs,
		UserAgent:                     optionalTrimmedPtr(opts.UserAgent),
		IPAddress:                     optionalTrimmedPtr(opts.IPAddress),
		InboundEndpoint:               optionalTrimmedPtr(normalizedVideoEndpoint(opts.InboundEndpoint)),
		UpstreamEndpoint:              optionalTrimmedPtr(normalizedVideoEndpoint(opts.UpstreamEndpoint)),
		BillingMode:                   &billingMode,
		VideoTaskID:                   &task.PublicID,
		VideoResolution:               &task.Resolution,
		VideoDurationSeconds:          &task.DurationSeconds,
		VideoReferenceDurationSeconds: task.ReferenceDurationSeconds,
		VideoBillableSeconds:          task.BillableSeconds,
		VideoResultURL:                opts.ResultVideoURL,
		CreatedAt:                     createdAt,
	}
	isSubscriptionBill := false
	if subscription != nil && apiKey.Group.IsSubscriptionType() {
		isSubscriptionBill = true
		usageLog.BillingType = BillingTypeSubscription
		usageLog.SubscriptionID = &subscription.ID
	}
	return usageLog, isSubscriptionBill
}

func (s *VideoService) applyVideoUsageBilling(ctx context.Context, usageLog *UsageLog, task *VideoTask, apiKey *APIKey, subscription *UserSubscription, account *Account, payloadHash string, isSubscriptionBill bool) error {
	if usageLog == nil || task == nil || apiKey == nil || apiKey.User == nil || account == nil {
		return nil
	}
	if usageLog.ActualCost < 0 && s.usageBillingRepo == nil {
		return errors.New("video refund requires usage billing repository")
	}
	billingMode := string(BillingModeVideoDuration)
	applied, billingErr := applyUsageBilling(ctx, usageLog.RequestID, usageLog, &postUsageBillingParams{
		Cost:                  &CostBreakdown{OutputCost: usageLog.OutputCost, TotalCost: usageLog.TotalCost, ActualCost: usageLog.ActualCost, BillingMode: billingMode},
		User:                  apiKey.User,
		APIKey:                apiKey,
		Account:               account,
		Subscription:          subscription,
		RequestPayloadHash:    payloadHash,
		IsSubscriptionBill:    isSubscriptionBill,
		AccountRateMultiplier: account.BillingRateMultiplier(),
		APIKeyService:         s.apiKeyService,
		Platform:              PlatformVideo,
	}, s.billingDeps(), s.usageBillingRepo)
	if billingErr != nil {
		return billingErr
	}
	if applied {
		s.finalizeVideoRefundCache(ctx, usageLog, task, apiKey, subscription, isSubscriptionBill)
	}
	return nil
}

func (s *VideoService) finalizeVideoRefundCache(ctx context.Context, usageLog *UsageLog, task *VideoTask, apiKey *APIKey, subscription *UserSubscription, isSubscriptionBill bool) {
	if s == nil || s.billingCache == nil || usageLog == nil || usageLog.ActualCost >= 0 || apiKey == nil || apiKey.User == nil {
		return
	}
	refund := -usageLog.ActualCost
	if isSubscriptionBill {
		if subscription != nil && apiKey.GroupID != nil {
			s.billingCache.QueueUpdateSubscriptionUsage(apiKey.User.ID, *apiKey.GroupID, -refund)
		}
		return
	}
	_ = s.billingCache.InvalidateUserBalance(ctx, apiKey.User.ID)
	if apiKey.HasRateLimits() {
		s.billingCache.QueueUpdateAPIKeyRateLimitUsage(apiKey.ID, -refund)
	}
	if task != nil {
		s.billingCache.RollbackUserPlatformQuotaUsage(ctx, apiKey.User.ID, PlatformVideo, refund)
	}
}

func (s *VideoService) billingDeps() *billingDeps {
	return &billingDeps{
		accountRepo:           s.accountRepo,
		userRepo:              s.userRepo,
		userSubRepo:           s.userSubRepo,
		billingCacheService:   s.billingCache,
		deferredService:       s.deferredService,
		balanceNotifyService:  s.balanceNotify,
		userPlatformQuotaRepo: s.quotaRepo,
		cfg:                   s.cfg,
	}
}

func normalizedVideoEndpoint(endpoint string) string {
	if strings.TrimSpace(endpoint) == "" {
		return ""
	}
	return videoDefaultAPIPath
}

func videoTaskDurationMs(task *VideoTask) *int {
	if task == nil || task.CreatedAt.IsZero() {
		return nil
	}
	end := task.UpdatedAt
	if task.CompletedAt != nil {
		end = *task.CompletedAt
	}
	if end.IsZero() || end.Before(task.CreatedAt) {
		return nil
	}
	ms := int(end.Sub(task.CreatedAt).Milliseconds())
	if ms < 0 {
		return nil
	}
	return &ms
}

type normalizedVideoRequest struct {
	Model                 string
	Prompt                string
	Content               []VideoContent
	Ratio                 string
	RatioProvided         bool
	Duration              float64
	RequestedDuration     float64
	GeneratedSeconds      int
	Resolution            string
	GenerateAudio         *bool
	SafetyIdentifier      string
	AbilityCode           string
	ReferenceVideoSeconds int
	BillableSeconds       int
	Raw                   map[string]any
}

func normalizeVideoCreateRequest(req *VideoCreateRequest) (*normalizedVideoRequest, error) {
	return normalizeVideoCreateRequestWithDynamicModel(req, false)
}

func normalizeAgentVideoCreateRequest(req *VideoCreateRequest) (*normalizedVideoRequest, error) {
	return normalizeVideoCreateRequestWithDynamicModel(req, true)
}

func normalizeVideoCreateRequestWithDynamicModel(req *VideoCreateRequest, dynamicModel bool) (*normalizedVideoRequest, error) {
	model := strings.TrimSpace(req.Model)
	spec, knownModel := videoSpecForModel(model)
	if model == "" || (!dynamicModel && !knownModel) {
		return nil, videoBadRequest("invalid_video_model", "Invalid video model")
	}
	resolution := strings.TrimSpace(req.Resolution)
	if resolution == "" {
		resolution = spec.DefaultResolution
	}
	if dynamicModel {
		var err error
		resolution, err = normalizeAgentPriceResolution(AgentMediaTypeVideo, resolution)
		if err != nil {
			return nil, videoBadRequest("invalid_video_resolution", "Invalid video resolution")
		}
		// Agent groups build their catalog from each account's model_mapping, so
		// models we do not know stay unconstrained. Known ones still have to land
		// inside the upstream allow-list.
		if knownModel && !IsSupportedVideoResolution(model, resolution) {
			return nil, videoBadRequest("invalid_video_resolution", "Invalid video resolution")
		}
	} else if !IsSupportedVideoResolution(model, resolution) {
		return nil, videoBadRequest("invalid_video_resolution", "Invalid video resolution")
	}
	requestedDuration := req.Duration
	duration := requestedDuration
	if duration == -1 && spec.AutoDurationSeconds > 0 {
		duration = float64(spec.AutoDurationSeconds)
	}
	if duration != math.Trunc(duration) || duration < float64(spec.MinSeconds) || duration > float64(spec.MaxSeconds) {
		return nil, videoBadRequest("invalid_video_duration", "Invalid video duration")
	}
	generatedSeconds := int(math.Ceil(duration))
	if generatedSeconds <= 0 {
		return nil, videoBadRequest("invalid_video_duration", "Invalid video duration")
	}
	prompt := strings.TrimSpace(req.Prompt)
	content := normalizeVideoContent(req.Content)
	stats := inspectVideoContent(content)
	if prompt == "" {
		for _, item := range content {
			if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
				prompt = strings.TrimSpace(item.Text)
				break
			}
		}
	}
	ability := strings.TrimSpace(req.AbilityCode)
	if ability == "" {
		ability = inferVideoAbility(stats)
	}
	if !isValidVideoAbility(ability) {
		return nil, videoBadRequest("invalid_video_ability", "Invalid video ability")
	}
	if err := validateVideoAbilityInput(model, ability, prompt, stats); err != nil {
		return nil, err
	}
	referenceSeconds, err := referenceVideoSeconds(model, content)
	if err != nil {
		return nil, err
	}
	ratio := firstNonEmptyVideoString(req.Ratio, req.AspectRatio, req.AspectRatioCamel)
	ratioProvided := ratio != ""
	if model == VideoModelSeedance25 {
		ratio = strings.ToLower(strings.TrimSpace(ratio))
		if ratio == "adaptive" {
			ratio = "auto"
		}
		if ratio == "" && (ability == videoAbilityImageToVideo || ability == videoAbilityStartEndToVideo) {
			ratio = "auto"
		}
		if ratio != "" && !isSeedance25Ratio(ratio) {
			return nil, videoBadRequest("invalid_video_ratio", "Invalid video ratio")
		}
	}
	if ratio == "" {
		ratio = "16:9"
	}
	return &normalizedVideoRequest{
		Model:                 model,
		Prompt:                prompt,
		Content:               content,
		Ratio:                 ratio,
		RatioProvided:         ratioProvided,
		Duration:              duration,
		RequestedDuration:     requestedDuration,
		GeneratedSeconds:      generatedSeconds,
		Resolution:            resolution,
		GenerateAudio:         req.GenerateAudio,
		SafetyIdentifier:      strings.TrimSpace(req.SafetyIdentifier),
		AbilityCode:           ability,
		ReferenceVideoSeconds: referenceSeconds,
		BillableSeconds:       generatedSeconds + referenceSeconds,
		Raw:                   cloneMap(req.Raw),
	}, nil
}

func (r *normalizedVideoRequest) UpstreamBody(upstreamModel string) map[string]any {
	body := map[string]any{
		"model":    upstreamModel,
		"prompt":   r.Prompt,
		"content":  videoContentForUpstream(r.Content, r.Prompt),
		"ratio":    r.Ratio,
		"duration": r.GeneratedSeconds,
	}
	if r.GenerateAudio != nil {
		body["generate_audio"] = *r.GenerateAudio
	}
	if r.SafetyIdentifier != "" {
		body["safety_identifier"] = r.SafetyIdentifier
	}
	return body
}

func normalizeVideoContent(content []VideoContent) []VideoContent {
	out := make([]VideoContent, 0, len(content))
	for _, item := range content {
		item.Type = strings.TrimSpace(strings.ToLower(item.Type))
		item.Role = strings.TrimSpace(strings.ToLower(item.Role))
		item.Text = strings.TrimSpace(item.Text)
		if item.SubjectType == "" && (item.Type == "image_url" || item.Type == "video_url") {
			item.SubjectType = "person"
		}
		out = append(out, item)
	}
	return out
}

type videoContentStats struct {
	ImageCount          int
	VideoCount          int
	AudioCount          int
	FirstFrameCount     int
	LastFrameCount      int
	ReferenceImageCount int
	ReferenceVideoCount int
	ReferenceAudioCount int
	HasReference        bool
}

func inspectVideoContent(content []VideoContent) videoContentStats {
	var stats videoContentStats
	for _, item := range content {
		switch item.Type {
		case "image_url":
			stats.ImageCount++
			switch item.Role {
			case "first_frame":
				stats.FirstFrameCount++
			case "last_frame":
				stats.LastFrameCount++
			case "reference_image":
				stats.ReferenceImageCount++
				stats.HasReference = true
			}
		case "video_url":
			stats.VideoCount++
			if item.Role == "reference_video" || item.Role == "" {
				stats.ReferenceVideoCount++
				stats.HasReference = true
			}
		case "audio_url":
			stats.AudioCount++
			if item.Role == "reference_audio" || item.Role == "" {
				stats.ReferenceAudioCount++
				stats.HasReference = true
			}
		}
	}
	return stats
}

func inferVideoAbility(stats videoContentStats) string {
	if stats.HasReference || stats.VideoCount > 0 || stats.AudioCount > 0 || stats.ReferenceImageCount > 0 {
		return videoAbilityReferenceToVideo
	}
	if stats.FirstFrameCount == 1 && stats.LastFrameCount == 1 && stats.ImageCount == 2 {
		return videoAbilityStartEndToVideo
	}
	if stats.FirstFrameCount == 1 && stats.ImageCount == 1 {
		return videoAbilityImageToVideo
	}
	return videoAbilityTextToVideo
}

func isValidVideoAbility(ability string) bool {
	switch ability {
	case videoAbilityTextToVideo, videoAbilityImageToVideo, videoAbilityStartEndToVideo, videoAbilityReferenceToVideo:
		return true
	default:
		return false
	}
}

func validateVideoAbilityInput(model, ability, prompt string, stats videoContentStats) error {
	switch ability {
	case videoAbilityTextToVideo:
		if strings.TrimSpace(prompt) == "" {
			return videoBadRequest("invalid_video_prompt", "Video prompt is required")
		}
		if stats.ImageCount > 0 || stats.VideoCount > 0 || stats.AudioCount > 0 {
			return videoBadRequest("invalid_video_content", "Text-to-video requests cannot include media references")
		}
	case videoAbilityImageToVideo:
		if stats.ImageCount != 1 || stats.FirstFrameCount != 1 {
			return videoBadRequest("invalid_video_content", "Image-to-video requires exactly one first frame image")
		}
		if stats.VideoCount > 0 || stats.AudioCount > 0 {
			return videoBadRequest("invalid_video_content", "Image-to-video cannot include video or audio references")
		}
	case videoAbilityStartEndToVideo:
		if stats.ImageCount != 2 || stats.FirstFrameCount != 1 || stats.LastFrameCount != 1 {
			return videoBadRequest("invalid_video_content", "Start-end video requires exactly one first frame and one last frame image")
		}
		if stats.VideoCount > 0 || stats.AudioCount > 0 {
			return videoBadRequest("invalid_video_content", "Start-end video cannot include video or audio references")
		}
	case videoAbilityReferenceToVideo:
		spec, _ := videoSpecForModel(model)
		referenceCount := stats.ImageCount + stats.VideoCount + stats.AudioCount
		if stats.ImageCount > spec.MaxRefImages || stats.VideoCount > spec.MaxRefVideos || stats.AudioCount > spec.MaxRefAudios ||
			(spec.MaxRefTotal > 0 && referenceCount > spec.MaxRefTotal) {
			return videoBadRequest("invalid_video_content", videoReferenceLimitMessage(spec))
		}
		if spec.AudioNeedsVisual {
			// Audio on its own has nothing to animate, so these models need at
			// least one image or video alongside it.
			if stats.ImageCount+stats.VideoCount == 0 {
				return videoBadRequest("invalid_video_content", "Reference video requests require at least one image or video reference")
			}
		} else if referenceCount == 0 {
			return videoBadRequest("invalid_video_content", "Reference video requests require at least one image, video, or audio reference")
		}
	}
	return nil
}

func videoReferenceLimitMessage(spec videoModelSpec) string {
	message := fmt.Sprintf("Reference video requests support up to %d images, %d videos, and %d audio files",
		spec.MaxRefImages, spec.MaxRefVideos, spec.MaxRefAudios)
	if spec.MaxRefTotal > 0 {
		message += fmt.Sprintf(", and %d total media references", spec.MaxRefTotal)
	}
	return message
}

// referenceVideoSeconds 汇总参考视频时长，用于把参考素材的时长计入计费。
//
// 时长一律来自平台自己的 ffprobe 探测结果（素材转存阶段写入 DurationSeconds），
// 下游声明的值在那一步就被丢掉了。缺失时按 0 秒计入而不再拒绝请求——参考素材是否
// 可用由上游判定，最坏情况只是这一段参考时长不计费。
func referenceVideoSeconds(model string, content []VideoContent) (int, error) {
	spec, _ := videoSpecForModel(model)
	maxDuration := float64(spec.MaxRefVideoSeconds)
	maxTotal := spec.MaxRefVideoTotalSeconds
	total := 0
	for _, item := range content {
		if item.Type != "video_url" || (item.Role != "" && item.Role != "reference_video") {
			continue
		}
		// 时长缺省时按 0 秒计入（先挡掉 nil 再解引用）；正常情况下素材转存阶段
		// 已经填好探测结果，这里只是防御。
		if item.DurationSeconds == nil || *item.DurationSeconds <= 0 {
			continue
		}
		if *item.DurationSeconds < 2 || *item.DurationSeconds > maxDuration {
			return 0, videoBadRequest("invalid_reference_video_duration", "Invalid reference video duration")
		}
		total += int(math.Ceil(*item.DurationSeconds))
		if total > maxTotal {
			return 0, videoBadRequest("invalid_reference_video_duration", "Invalid reference video duration")
		}
	}
	return total, nil
}

// isSeedance25Ratio 用于参数层的 2.5 画幅校验：六个画幅 + auto/adaptive 哨兵值。
func isSeedance25Ratio(ratio string) bool {
	switch strings.TrimSpace(strings.ToLower(ratio)) {
	case "auto", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9":
		return true
	default:
		return false
	}
}

// videoRequestRatioAllowed 是渠道闸门共用的"当前 Seedance 渠道可服务的画幅"判定。
//
// 画幅、时长、分辨率都是**渠道能力**：同一个模型在不同渠道可能支持的范围不同，
// 因此判定必须发生在按渠道实现的适配器里（能服务就参与调度，不能就换渠道），
// 而不是在参数层一刀切——那会把未来新渠道才支持的取值提前拒掉。
// 目前 aigod 与 newtoken 的取值一致，所以共用这一份；将来某渠道不同，直接在该
// 渠道的 CompatibleRequest 里写自己的逻辑即可。
func videoRequestRatioAllowed(model, ratio string) bool {
	if isSeedanceAspectRatio(ratio) {
		return true
	}
	// auto 是 2.5"由模型决定画幅"的哨兵值，不是画幅本身。
	return model == VideoModelSeedance25 && strings.EqualFold(strings.TrimSpace(ratio), "auto")
}

// videoContentForUpstream 组装 aigod 上游的 content 数组（仅 aigod 的
// UpstreamBody 调用）。参考图与参考视频一律带 subject_type=person。
func videoContentForUpstream(content []VideoContent, prompt string) []map[string]any {
	out := make([]map[string]any, 0, len(content)+1)
	hasText := false
	for _, item := range content {
		entry := map[string]any{"type": item.Type}
		switch item.Type {
		case "text":
			if item.Text == "" {
				continue
			}
			entry["text"] = item.Text
			hasText = true
		case "image_url":
			if item.ImageURL == nil || strings.TrimSpace(item.ImageURL.URL) == "" {
				continue
			}
			entry["image_url"] = map[string]any{"url": strings.TrimSpace(item.ImageURL.URL)}
			if item.Role != "" {
				entry["role"] = item.Role
			}
			// aigod 的参考图统一声明为 person，不跟随下游传入值。
			entry["subject_type"] = videoAigodSubjectType
		case "video_url":
			if item.VideoURL == nil || strings.TrimSpace(item.VideoURL.URL) == "" {
				continue
			}
			entry["video_url"] = map[string]any{"url": strings.TrimSpace(item.VideoURL.URL)}
			if item.Role != "" {
				entry["role"] = item.Role
			}
			// 参考视频同样统一为 person；audio_url 不是参考图/视频，不设置。
			entry["subject_type"] = videoAigodSubjectType
		case "audio_url":
			if item.AudioURL == nil || strings.TrimSpace(item.AudioURL.URL) == "" {
				continue
			}
			entry["audio_url"] = map[string]any{"url": strings.TrimSpace(item.AudioURL.URL)}
			if item.Role != "" {
				entry["role"] = item.Role
			}
		default:
			continue
		}
		out = append(out, entry)
	}
	if !hasText && strings.TrimSpace(prompt) != "" {
		out = append([]map[string]any{{"type": "text", "text": strings.TrimSpace(prompt)}}, out...)
	}
	return out
}

func validateVideoEstimatedCost(apiKey *APIKey, subscription *UserSubscription, actualCost float64) error {
	if actualCost <= 0 || apiKey == nil || apiKey.User == nil || apiKey.Group == nil {
		return nil
	}
	if apiKey.Quota > 0 && apiKey.QuotaUsed+actualCost > apiKey.Quota {
		return infraerrors.TooManyRequests("API_KEY_QUOTA_EXHAUSTED", "API key quota is exhausted")
	}
	if apiKey.HasRateLimits() {
		if apiKey.RateLimit5h > 0 && apiKey.EffectiveUsage5h()+actualCost > apiKey.RateLimit5h {
			return infraerrors.TooManyRequests("API_KEY_RATE_5H_EXCEEDED", "API key rate limit is exhausted")
		}
		if apiKey.RateLimit1d > 0 && apiKey.EffectiveUsage1d()+actualCost > apiKey.RateLimit1d {
			return infraerrors.TooManyRequests("API_KEY_RATE_1D_EXCEEDED", "API key rate limit is exhausted")
		}
		if apiKey.RateLimit7d > 0 && apiKey.EffectiveUsage7d()+actualCost > apiKey.RateLimit7d {
			return infraerrors.TooManyRequests("API_KEY_RATE_7D_EXCEEDED", "API key rate limit is exhausted")
		}
	}
	if apiKey.Group.IsSubscriptionType() {
		if subscription == nil {
			return infraerrors.Forbidden("SUBSCRIPTION_NOT_FOUND", "No active subscription found for this group")
		}
		daily, weekly, monthly := subscription.CheckAllLimits(apiKey.Group, actualCost)
		if !daily || !weekly || !monthly {
			return infraerrors.TooManyRequests("USAGE_LIMIT_EXCEEDED", "Usage limit exceeded")
		}
		return nil
	}
	if apiKey.User.Balance < actualCost {
		return infraerrors.Forbidden("INSUFFICIENT_BALANCE", "Insufficient account balance")
	}
	return nil
}

func SeedanceUpstreamModel(model, resolution string) string {
	return strings.TrimSpace(model) + "-" + strings.TrimSpace(resolution)
}

func videoUpstreamModelForAccount(account *Account, normalized *normalizedVideoRequest) string {
	return videoProviderAdapterForAccount(account).UpstreamModel(account, normalized)
}

func videoUpstreamBodyForAccount(account *Account, normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	return videoProviderAdapterForAccount(account).BuildCreateBody(normalized, upstreamModel)
}

func isVideoAccountCompatibleForRequest(account *Account, normalized *normalizedVideoRequest) bool {
	return videoProviderAdapterForAccount(account).CompatibleRequest(normalized)
}

// videoProviderNeedsRequestCompatibility reports whether a provider constrains
// requests beyond the (model, resolution) pair the agent discovery set already
// checks — duration, aspect ratio and reference-media limits. Those providers
// must still run CompatibleRequest during agent-group scheduling so an
// unsupported request is routed to another upstream instead of failing there.
func videoProviderNeedsRequestCompatibility(provider string) bool {
	switch provider {
	case videoProviderAigod, videoProviderNewtoken, videoProviderMikuapi, videoProviderJingyu:
		return true
	default:
		return false
	}
}

type videoUpstreamCreateResult struct {
	ID string
}

type videoPollResult struct {
	Status   string
	VideoURL string
}

type videoUpstreamError struct {
	PollRetryable bool // Set only by provider-specific query handling.
	StatusCode    int
	Body          []byte
	Err           error
}

func (e *videoUpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("video upstream status %d", e.StatusCode)
}

type mappedVideoClientError struct {
	VideoClientError
	StatusCode int
	Retryable  bool
}

func mapVideoUpstreamError(err error, polling bool) mappedVideoClientError {
	var upstreamErr *videoUpstreamError
	if errors.As(err, &upstreamErr) {
		switch upstreamErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return mappedVideoClientError{VideoClientError: videoClientError("video_provider_unavailable", "Video service is temporarily unavailable. Please contact support if the issue persists."), StatusCode: upstreamErr.StatusCode}
		case http.StatusTooManyRequests:
			return mappedVideoClientError{VideoClientError: videoClientError("video_service_busy", "Video service is busy. Please retry later."), StatusCode: upstreamErr.StatusCode, Retryable: polling}
		}
		if upstreamErr.StatusCode >= 500 || upstreamErr.StatusCode == 0 || (polling && upstreamErr.PollRetryable) {
			return mappedVideoClientError{VideoClientError: videoClientError("video_service_unavailable", "Video service is temporarily unavailable. Please retry later."), StatusCode: upstreamErr.StatusCode, Retryable: polling}
		}
		return mappedVideoClientError{VideoClientError: videoClientError("video_service_unavailable", "Video service is temporarily unavailable. Please retry later."), StatusCode: upstreamErr.StatusCode}
	}
	return mappedVideoClientError{VideoClientError: videoClientError("video_service_unavailable", "Video service is temporarily unavailable. Please retry later.")}
}

func (s *VideoService) recordVideoAccountFailure(ctx context.Context, account *Account, mapped mappedVideoClientError, cause error) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	message := mapped.Message
	if message == "" {
		message = "Video service request failed"
	}
	switch mapped.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		_ = s.accountRepo.SetError(ctx, account.ID, message)
	case http.StatusTooManyRequests:
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, time.Now().UTC().Add(1*time.Minute))
	case 0:
		reason := "video service temporary failure"
		if cause != nil {
			reason = "video service temporary failure: " + cause.Error()
		}
		_ = s.accountRepo.SetTempUnschedulable(ctx, account.ID, time.Now().UTC().Add(1*time.Minute), reason)
	}
}

// shouldAbandonVideoPoll 决定一次轮询失败是否直接判任务失败。
//
//   - 可重试错误（5xx / 网络错误 / 429）在轮询阶段一律继续重试；
//   - 不可重试错误（例如上游暂时查不到任务返回 404）只有连续失败到 tolerance 次
//     才放弃，避免上游抖动被当成终态；
//   - tolerance <= 1 时不可重试错误一次即失败，与 aigod / newtoken 的既有行为一致。
func shouldAbandonVideoPoll(retryable bool, consecutiveFailures, tolerance int) bool {
	if retryable {
		return false
	}
	if tolerance <= 1 {
		return true
	}
	return consecutiveFailures >= tolerance
}

func normalizeVideoUpstreamStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued":
		return VideoTaskStatusQueued
	case "processing", "in_progress", "in_process", "progress", "running":
		return VideoTaskStatusProcessing
	case "completed", "succeeded", "success":
		return VideoTaskStatusCompleted
	case "failed", "error":
		return VideoTaskStatusFailed
	case "cancelled", "canceled":
		return VideoTaskStatusCancelled
	default:
		return VideoTaskStatusProcessing
	}
}

func videoResponseFromTask(task *VideoTask) *VideoResponse {
	if task == nil {
		return nil
	}
	resp := &VideoResponse{
		ID:           task.PublicID,
		Object:       videoObject,
		Model:        task.Model,
		Status:       task.Status,
		RefundStatus: videoRefundStatus(task),
		CreatedAt:    task.CreatedAt.Unix(),
	}
	if task.Status == VideoTaskStatusCompleted {
		resp.VideoURL = task.ResultVideoURL
		if task.CompletedAt != nil {
			completed := task.CompletedAt.Unix()
			resp.CompletedAt = &completed
		}
	}
	if task.Status == VideoTaskStatusFailed {
		resp.Error = videoErrorFromJSON(task.ErrorJSON)
		if resp.Error == nil {
			err := videoClientError("video_generation_failed", "Video generation failed. Please retry later.")
			resp.Error = &err
		}
	}
	return resp
}

func videoRefundStatus(task *VideoTask) string {
	if task == nil {
		return VideoRefundStatusNotApplicable
	}
	if task.RefundedAt != nil {
		return VideoRefundStatusRefunded
	}
	if (task.Status == VideoTaskStatusFailed || task.Status == VideoTaskStatusCancelled) && task.BilledAt != nil && task.ActualCost > 0 {
		return VideoRefundStatusPending
	}
	return VideoRefundStatusNotApplicable
}

func videoBadRequest(code, message string) error {
	return infraerrors.BadRequest(code, message)
}

func videoClientError(code, message string) VideoClientError {
	return VideoClientError{Code: code, Message: message}
}

func SanitizeVideoClientError(code, message string) (string, string) {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	if code == "" {
		code = "video_service_unavailable"
	}
	if message == "" {
		message = "Video service is temporarily unavailable. Please retry later."
	}
	joined := strings.ToLower(code + " " + message)
	forbidden := []string{
		"aigod",
		"api.aigod.one",
		"newtoken",
		"newtoken.club",
		"jingyu",
		"jingyuapi",
		"api.jingyuapi.art",
		// Covers every newtoken upstream model id (sd2.0-720p-official, ...).
		"-official",
		"upstream",
		"upstream_task",
		"upstream_task_id",
	}
	for _, token := range forbidden {
		if strings.Contains(joined, token) {
			return "video_service_unavailable", "Video service is temporarily unavailable. Please retry later."
		}
	}
	return code, message
}

func videoErrorJSON(err VideoClientError) map[string]any {
	return map[string]any{"code": err.Code, "message": err.Message}
}

func videoErrorFromJSON(raw map[string]any) *VideoClientError {
	if raw == nil {
		return nil
	}
	code := strings.TrimSpace(stringFromMap(raw, "code"))
	message := strings.TrimSpace(stringFromMap(raw, "message"))
	if code == "" || message == "" {
		return nil
	}
	code, message = SanitizeVideoClientError(code, message)
	return &VideoClientError{Code: code, Message: message}
}

func videoAccountEndpoint(account *Account) (string, error) {
	baseURL := strings.TrimSpace(accountExtraString(account, "base_url"))
	if baseURL == "" {
		baseURL = videoDefaultBaseURLForProvider(videoAccountProvider(account))
	}
	apiPath := videoAccountAPIPath(account)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ErrVideoAccountNotFound
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(apiPath, "/")
	return parsed.String(), nil
}

func videoAccountAPIPath(account *Account) string {
	apiPath := strings.TrimSpace(accountExtraString(account, "api_path"))
	if apiPath == "" {
		return videoDefaultAPIPathForProvider(videoAccountProvider(account))
	}
	return apiPath
}

func videoAccountProvider(account *Account) string {
	provider := strings.ToLower(strings.TrimSpace(accountExtraString(account, "video_provider")))
	switch provider {
	case videoProviderNewtoken:
		return videoProviderNewtoken
	case videoProviderMikuapi:
		return videoProviderMikuapi
	case videoProviderJingyu:
		return videoProviderJingyu
	default:
		return videoProviderAigod
	}
}

func videoDefaultBaseURLForProvider(provider string) string {
	return videoProviderAdapterByName(provider).DefaultBaseURL()
}

func videoDefaultAPIPathForProvider(provider string) string {
	return videoProviderAdapterByName(provider).DefaultAPIPath()
}

func videoAccountDuration(account *Account, key string, fallback time.Duration) time.Duration {
	ms := accountExtraInt(account, key)
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func videoAccountDefaultDuration(account *Account, key string) time.Duration {
	switch videoAccountProvider(account) {
	case videoProviderJingyu:
		switch key {
		case "poll_interval_ms":
			return videoJingyuPollInterval
		case "poll_timeout_ms":
			return videoJingyuPollTimeout
		case "request_timeout_ms":
			return videoJingyuRequestTimeout
		case "connect_timeout_ms":
			return videoJingyuConnectTimeout
		}
	case videoProviderMikuapi:
		switch key {
		case "poll_interval_ms":
			return videoMikuapiPollInterval
		}
	case videoProviderNewtoken:
		switch key {
		case "poll_interval_ms":
			return videoNewtokenPollInterval
		case "request_timeout_ms":
			return videoNewtokenRequestTimeout
		case "connect_timeout_ms":
			return videoNewtokenConnectTimeout
		}
	}
	switch key {
	case "poll_interval_ms":
		return videoDefaultPollInterval
	case "poll_timeout_ms":
		return videoDefaultPollTimeout
	case "connect_timeout_ms":
		return videoDefaultConnectTimeout
	default:
		return videoDefaultRequestTimeout
	}
}

func accountExtraString(account *Account, key string) string {
	if account == nil {
		return ""
	}
	if account.Extra != nil {
		if s, ok := account.Extra[key].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(account.GetCredential(key))
}

func accountExtraInt(account *Account, key string) int {
	if account == nil {
		return 0
	}
	value, ok := account.Extra[key]
	if !ok || value == nil {
		value, ok = account.Credentials[key]
		if !ok || value == nil {
			return 0
		}
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := strconv.Atoi(v.String())
		return i
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(v))
		return i
	default:
		return 0
	}
}

func firstNonEmptyVideoString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// videoTaskIDFromPayload accepts the documented top-level id and the common
// data/task/result envelopes used by OpenAI-compatible async providers.
func videoTaskIDFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if id := firstNonEmptyVideoString(stringFromMap(payload, "task_id"), stringFromMap(payload, "id")); id != "" {
		return id
	}
	for _, key := range []string{"data", "task", "result"} {
		if nested, ok := mapFromAny(payload[key]); ok {
			if id := videoTaskIDFromPayload(nested); id != "" {
				return id
			}
		}
	}
	return ""
}

func videoTaskIDFromLocation(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-2] != "videos" {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// videoAbsoluteResultURL resolves a relative result URL against the polled
// endpoint. Upstreams may document the finished video as a direct link but
// express it as a path, so both forms must publish.
func videoAbsoluteResultURL(endpoint, resultURL string) string {
	resultURL = strings.TrimSpace(resultURL)
	if resultURL == "" {
		return ""
	}
	target, err := url.Parse(resultURL)
	if err != nil {
		return ""
	}
	if target.IsAbs() {
		return resultURL
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return resultURL
	}
	return base.ResolveReference(target).String()
}

func videoResultURLFromPayload(payload map[string]any) string {
	resultURL := firstNonEmptyVideoString(
		stringFromMap(payload, "download_url"),
		stringFromMap(payload, "result_asset_url"),
		stringFromMap(payload, "url"),
		stringFromMap(payload, "video_url"),
		stringFromMap(payload, "result_url"),
	)
	if resultURL != "" {
		return resultURL
	}
	metadata, ok := mapFromAny(payload["metadata"])
	if ok {
		if metadataURL := strings.TrimSpace(stringFromMap(metadata, "url")); metadataURL != "" {
			return metadataURL
		}
	}
	// 上游文档把 output[0].url 与 video_url/url 并列为结果地址的读取位置。
	if outputs, ok := payload["output"].([]any); ok {
		for _, item := range outputs {
			entry, ok := mapFromAny(item)
			if !ok {
				continue
			}
			if entryURL := strings.TrimSpace(stringFromMap(entry, "url")); entryURL != "" {
				return entryURL
			}
		}
	}
	// 上游常把成品地址包在 data/task/result 信封里（与 videoTaskIDFromPayload
	// 保持一致）。不展开信封会在任务其实已完成时报“取不到结果地址”，而该错误被
	// mapVideoUpstreamError 归为非重试，任务会被直接判失败并退费。
	for _, key := range []string{"data", "task", "result"} {
		if nested, ok := mapFromAny(payload[key]); ok {
			if nestedURL := videoResultURLFromPayload(nested); nestedURL != "" {
				return nestedURL
			}
		}
	}
	return ""
}

func stringFromMap(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	value := raw[key]
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func mapFromAny(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	default:
		return nil, false
	}
}

func optionalTrimmedPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func videoFloat64Ptr(value float64) *float64 {
	return &value
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]any, len(input))
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return input
	}
	return out
}

func generateVideoPublicID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return videoPublicIDPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return videoPublicIDPrefix + hex.EncodeToString(b[:])
}

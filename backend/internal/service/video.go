package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	VideoModelSeedance20     = "seedance-2.0"
	VideoModelSeedance20Fast = "seedance-2.0-fast"
	VideoModelSeedance25     = "seedance-2.5"

	// 经 mikuapi 渠道提供的 Grok Imagine 与可灵视频，下游模型名与上游一致。
	// 两者与 Seedance 是三套完全独立的接口（端点、字段、状态值、成片机制都
	// 不同），渠道侧的分支见 mikuapi 适配器。
	VideoModelGrokImagineVideo15Preview = "grok-imagine-video-1.5-preview"
	VideoModelKlingVideoV3Omni          = "kling-video-v3-omni"

	VideoResolution480P  = "480p"
	VideoResolution720P  = "720p"
	VideoResolution768P  = "768p"
	VideoResolution1080P = "1080p"
	VideoResolution2K    = "2K"
	VideoResolution4K    = "4K"

	VideoTaskStatusQueued     = "queued"
	VideoTaskStatusProcessing = "processing"
	VideoTaskStatusCompleted  = "completed"
	VideoTaskStatusFailed     = "failed"
	VideoTaskStatusCancelled  = "cancelled"

	VideoRefundStatusNotApplicable = "not-applicable"
	VideoRefundStatusPending       = "pending"
	VideoRefundStatusRefunded      = "refunded"
)

// videoModelSpec collects the downstream limits of one video model. The
// upstream clamps out-of-range parameters to the nearest allowed value instead
// of rejecting them, so anything we let through silently produces a video that
// differs from the one we billed for. Every limit therefore has to be enforced
// here, not left to the provider.
type videoModelSpec struct {
	Resolutions       []string
	DefaultResolution string
	// AutoDurationSeconds is the duration substituted for the "auto" sentinel
	// (-1). Zero means the model has no auto duration and rejects the sentinel.
	AutoDurationSeconds int
	MinSeconds          int
	MaxSeconds          int
	MaxRefImages        int
	MaxRefVideos        int
	MaxRefAudios        int
	// MaxRefTotal caps images+videos+audios together. Zero means no aggregate
	// cap beyond the per-kind ones.
	MaxRefTotal int
	// MaxRefVideoSeconds caps a single reference clip, MaxRefVideoTotalSeconds
	// caps their sum.
	MaxRefVideoSeconds      int
	MaxRefVideoTotalSeconds int
	// AudioNeedsVisual rejects reference sets whose only media is audio.
	AudioNeedsVisual bool
}

// videoModelSpecs is the single source of truth for per-model video limits.
// Models absent from the table are only reachable through agent groups, where
// the catalog comes from each account's model_mapping; those fall back to
// videoDefaultModelSpec.
var videoModelSpecs = map[string]videoModelSpec{
	VideoModelSeedance20: {
		Resolutions:             []string{VideoResolution480P, VideoResolution720P, VideoResolution1080P, VideoResolution4K},
		DefaultResolution:       VideoResolution720P,
		MinSeconds:              videoMinDurationSeconds,
		MaxSeconds:              videoMaxDurationSeconds,
		MaxRefImages:            9,
		MaxRefVideos:            3,
		MaxRefAudios:            3,
		MaxRefVideoSeconds:      videoMaxDurationSeconds,
		MaxRefVideoTotalSeconds: videoMaxReferenceVideoTotal,
		AudioNeedsVisual:        true,
	},
	VideoModelSeedance20Fast: {
		Resolutions:             []string{VideoResolution480P, VideoResolution720P},
		DefaultResolution:       VideoResolution720P,
		MinSeconds:              videoMinDurationSeconds,
		MaxSeconds:              videoMaxDurationSeconds,
		MaxRefImages:            9,
		MaxRefVideos:            3,
		MaxRefAudios:            3,
		MaxRefVideoSeconds:      videoMaxDurationSeconds,
		MaxRefVideoTotalSeconds: videoMaxReferenceVideoTotal,
		AudioNeedsVisual:        true,
	},
	VideoModelSeedance25: {
		Resolutions:             []string{VideoResolution480P, VideoResolution720P, VideoResolution1080P},
		DefaultResolution:       VideoResolution720P,
		AutoDurationSeconds:     5,
		MinSeconds:              videoMinDurationSeconds,
		MaxSeconds:              videoSeedance25MaxDuration,
		MaxRefImages:            30,
		MaxRefVideos:            10,
		MaxRefAudios:            10,
		MaxRefTotal:             videoSeedance25MaxReferences,
		MaxRefVideoSeconds:      videoSeedance25MaxDuration,
		MaxRefVideoTotalSeconds: videoSeedance25MaxDuration,
		// 2.5 与 2.0 系列同规则：参考音频必须搭配至少一张图或一段视频。
		AudioNeedsVisual: true,
	},
	// grok-imagine-video-1.5-preview（mikuapi）：时长 1-15 秒，清晰度 480p/720p/
	// 1080p；参考素材只有图片（参考图模式实测 7 张），没有尾帧语义。上游差异
	// （创建端点、首帧模式互斥、画幅拉伸等）全部由 mikuapi 适配器吸收，下游
	// 协议不变。
	VideoModelGrokImagineVideo15Preview: {
		Resolutions:       []string{VideoResolution480P, VideoResolution720P, VideoResolution1080P},
		DefaultResolution: VideoResolution720P,
		MinSeconds:        1,
		MaxSeconds:        videoMaxDurationSeconds,
		MaxRefImages:      7,
		AudioNeedsVisual:  true,
	},
	// kling-video-v3-omni（mikuapi 可灵）：时长 3-15 秒，清晰度 720p/1080p/4K，
	// 参考图至多 7 张，没有尾帧语义（首尾帧插值是 kling-video-v3 的能力，omni
	// 只做参考图）。画幅只有 16:9/9:16/1:1，由 mikuapi 适配器的渠道闸门收紧。
	VideoModelKlingVideoV3Omni: {
		Resolutions:       []string{VideoResolution720P, VideoResolution1080P, VideoResolution4K},
		DefaultResolution: VideoResolution720P,
		MinSeconds:        3,
		MaxSeconds:        videoMaxDurationSeconds,
		MaxRefImages:      7,
		AudioNeedsVisual:  true,
	},
}

// videoDefaultModelSpec mirrors the limits that applied to every non-2.5 model
// before videoModelSpecs existed. It keeps agent groups, whose model catalog is
// account-defined, on exactly the validation they had before.
var videoDefaultModelSpec = videoModelSpec{
	DefaultResolution:       VideoResolution720P,
	MinSeconds:              videoMinDurationSeconds,
	MaxSeconds:              videoMaxDurationSeconds,
	MaxRefImages:            9,
	MaxRefVideos:            3,
	MaxRefAudios:            3,
	MaxRefVideoSeconds:      videoMaxDurationSeconds,
	MaxRefVideoTotalSeconds: videoMaxReferenceVideoTotal,
	AudioNeedsVisual:        true,
}

// videoSpecForModel returns the model's row, or the legacy defaults for models
// the gateway does not know about. The bool reports whether the model is one of
// the built-in ones.
func videoSpecForModel(model string) (videoModelSpec, bool) {
	spec, ok := videoModelSpecs[strings.TrimSpace(model)]
	if !ok {
		return videoDefaultModelSpec, false
	}
	return spec, true
}

func IsSupportedVideoModel(model string) bool {
	_, ok := videoModelSpecs[strings.TrimSpace(model)]
	return ok
}

// SupportedVideoModels 是本仓库对外提供的视频模型清单：Seedance 三档 + 经
// mikuapi 渠道提供的 Grok Imagine 与可灵。
// 新增可对外提供的模型时必须同时改这里和 videoModelSpecs，否则分组候选模型不会包含它。
func SupportedVideoModels() []string {
	return []string{
		VideoModelSeedance20,
		VideoModelSeedance20Fast,
		VideoModelSeedance25,
		VideoModelGrokImagineVideo15Preview,
		VideoModelKlingVideoV3Omni,
	}
}

// SupportedVideoResolutions returns a copy of the model's allow-list so callers
// cannot mutate the shared spec table.
func SupportedVideoResolutions(model string) []string {
	spec, ok := videoSpecForModel(model)
	if !ok {
		return nil
	}
	return append([]string(nil), spec.Resolutions...)
}

// SupportedVideoDurations returns every integer duration (seconds) the model
// accepts. The bounds come from the same spec that validates create requests,
// so the advertised catalog can never drift from what the gateway enforces:
// the seedance-2.0 family accepts 4-15s, seedance-2.5 accepts 4-30s.
func SupportedVideoDurations(model string) []int {
	spec, _ := videoSpecForModel(model)
	if spec.MaxSeconds < spec.MinSeconds {
		return nil
	}
	durations := make([]int, 0, spec.MaxSeconds-spec.MinSeconds+1)
	for seconds := spec.MinSeconds; seconds <= spec.MaxSeconds; seconds++ {
		durations = append(durations, seconds)
	}
	return durations
}

// SupportsAudioOnlyReference reports whether a reference-to-video request may
// consist solely of reference audio. Audio on its own has nothing to animate,
// so every current model sets AudioNeedsVisual and rejects it; the catalog still
// declares the verdict explicitly for each model instead of leaving clients to
// infer it from an absent field.
func SupportsAudioOnlyReference(model string) bool {
	spec, _ := videoSpecForModel(model)
	return !spec.AudioNeedsVisual
}

func IsSupportedVideoResolution(model, resolution string) bool {
	spec, ok := videoSpecForModel(model)
	if !ok {
		return false
	}
	resolution = strings.TrimSpace(resolution)
	for _, allowed := range spec.Resolutions {
		if allowed == resolution {
			return true
		}
	}
	return false
}

var (
	ErrVideoTaskNotFound        = infraerrors.NotFound("VIDEO_TASK_NOT_FOUND", "Video task not found")
	ErrVideoPricingRuleNotFound = infraerrors.BadRequest("video_pricing_rule_not_found", "Video pricing rule is not configured")
	ErrVideoInvalidRequest      = infraerrors.BadRequest("invalid_video_request", "Invalid video request")
	ErrVideoAccountNotFound     = infraerrors.ServiceUnavailable("video_service_unavailable", "Video service is temporarily unavailable. Please retry later.")
)

type VideoGroupPricingRule struct {
	ID                       int64     `json:"id"`
	GroupID                  int64     `json:"group_id"`
	ModelCode                string    `json:"model_code"`
	Resolution               string    `json:"resolution"`
	CreditsPerSecond         float64   `json:"credits_per_second"`
	ReferenceVideoMultiplier float64   `json:"reference_video_multiplier"`
	Enabled                  bool      `json:"enabled"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type VideoTask struct {
	ID                       int64
	PublicID                 string
	RequestID                *string
	UserID                   int64
	APIKeyID                 int64
	GroupID                  int64
	AccountID                int64
	Model                    string
	UpstreamModel            string
	Resolution               string
	DurationSeconds          int
	ReferenceDurationSeconds int
	BillableSeconds          int
	CostPerSecond            float64
	TotalCost                float64
	ActualCost               float64
	Status                   string
	UpstreamTaskID           *string
	RequestJSON              map[string]any
	UpstreamResponseJSON     map[string]any
	ErrorJSON                map[string]any
	ResultVideoURL           *string
	CreatedAt                time.Time
	UpdatedAt                time.Time
	CompletedAt              *time.Time
	BilledAt                 *time.Time
	RefundedAt               *time.Time
}

type VideoClientError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type VideoCreateRequest struct {
	Model            string         `json:"model"`
	Prompt           string         `json:"prompt"`
	Content          []VideoContent `json:"content"`
	Ratio            string         `json:"ratio"`
	AspectRatio      string         `json:"aspect_ratio"`
	AspectRatioCamel string         `json:"aspectRatio"`
	Duration         float64        `json:"duration"`
	Resolution       string         `json:"resolution"`
	GenerateAudio    *bool          `json:"generate_audio"`
	SafetyIdentifier string         `json:"safety_identifier"`
	AbilityCode      string         `json:"ability_code"`
	Raw              map[string]any `json:"-"`
}

type VideoContent struct {
	Type     string           `json:"type"`
	Text     string           `json:"text"`
	ImageURL *VideoContentURL `json:"image_url"`
	VideoURL *VideoContentURL `json:"video_url"`
	AudioURL *VideoContentURL `json:"audio_url"`
	Role     string           `json:"role"`
	// SubjectType 目前由网关强制覆盖（参考图/视频统一声明 person），不跟随下游。
	SubjectType string `json:"subject_type,omitempty"`
	// DurationSeconds 是平台用 ffprobe 探测出的参考视频时长，参与参考时长计费。
	// 下游传进来的同名字段会在素材转存阶段被丢弃，不会被采信。
	DurationSeconds *float64       `json:"duration_seconds,omitempty"`
	Extra           map[string]any `json:"-"`
}

type VideoContentURL struct {
	URL string `json:"url"`
}

type VideoCreateInput struct {
	APIKey              *APIKey
	Subscription        *UserSubscription
	Request             *VideoCreateRequest
	RawBody             []byte
	IdempotencyKey      string
	IdempotencyReplayed bool
	RequestID           string
	RequestPayloadHash  string
	UserAgent           string
	IPAddress           string
	InboundEndpoint     string
	UpstreamEndpoint    string
	ResultPublicBaseURL string
}

type VideoResponse struct {
	ID           string            `json:"id"`
	Object       string            `json:"object"`
	Model        string            `json:"model"`
	Status       string            `json:"status"`
	VideoURL     *string           `json:"video_url,omitempty"`
	Error        *VideoClientError `json:"error,omitempty"`
	RefundStatus string            `json:"refund_status"`
	CreatedAt    int64             `json:"created_at"`
	CompletedAt  *int64            `json:"completed_at,omitempty"`
}

type VideoCostEstimate struct {
	Model                    string    `json:"model"`
	Resolution               string    `json:"resolution"`
	AbilityCode              string    `json:"ability_code"`
	Count                    int       `json:"count"`
	GeneratedSeconds         int       `json:"generated_seconds"`
	ReferenceVideoSeconds    int       `json:"reference_video_seconds"`
	BillableSeconds          int       `json:"billable_seconds"`
	CreditsPerSecond         float64   `json:"credits_per_second"`
	ReferenceVideoMultiplier float64   `json:"reference_video_multiplier"`
	RateMultiplier           float64   `json:"rate_multiplier"`
	TotalCost                float64   `json:"total_cost"`
	ActualCost               float64   `json:"actual_cost"`
	PricingRuleUpdatedAt     time.Time `json:"pricing_rule_updated_at"`
}

type VideoTaskUpdateInput struct {
	PublicID string
	Status   string
	Error    *VideoClientError
}

type VideoTaskLifecycleInput struct {
	PublicID            string
	Account             *Account
	APIKey              *APIKey
	Subscription        *UserSubscription
	UpstreamBody        map[string]any
	RequestPayloadHash  string
	UserAgent           string
	IPAddress           string
	InboundEndpoint     string
	UpstreamEndpoint    string
	ResultPublicBaseURL string
}

type VideoTaskCreateInput struct {
	PublicID                 string
	RequestID                *string
	UserID                   int64
	APIKeyID                 int64
	GroupID                  int64
	AccountID                int64
	Model                    string
	UpstreamModel            string
	Resolution               string
	DurationSeconds          int
	ReferenceDurationSeconds int
	BillableSeconds          int
	CostPerSecond            float64
	TotalCost                float64
	ActualCost               float64
	Status                   string
	UpstreamTaskID           *string
}

type VideoTaskUpdate struct {
	Status         *string
	UpstreamTaskID *string
	ErrorJSON      map[string]any
	ResultVideoURL *string
	CompletedAt    *time.Time
	BilledAt       *time.Time
	RefundedAt     *time.Time
}

type VideoTaskRepository interface {
	Create(ctx context.Context, input *VideoTaskCreateInput) (*VideoTask, error)
	GetByPublicID(ctx context.Context, publicID string) (*VideoTask, error)
	UpdateByPublicID(ctx context.Context, publicID string, update VideoTaskUpdate) (*VideoTask, error)
	MarkProcessingByPublicID(ctx context.Context, publicID string, upstreamTaskID string) (*VideoTask, bool, error)
	TransitionTerminalByPublicID(ctx context.Context, publicID string, update VideoTaskUpdate) (*VideoTask, bool, error)
	MarkBilled(ctx context.Context, publicID string, billedAt time.Time) (bool, error)
	MarkRefunded(ctx context.Context, publicID string, refundedAt time.Time) (bool, error)
}

type VideoGroupPricingRuleRepository interface {
	ListByGroupID(ctx context.Context, groupID int64) ([]VideoGroupPricingRule, error)
	ReplaceForGroup(ctx context.Context, groupID int64, rules []VideoGroupPricingRule) error
	GetEnabledRule(ctx context.Context, groupID int64, modelCode string, resolution string) (*VideoGroupPricingRule, error)
}

type VideoAccountRepository interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
	SetError(ctx context.Context, id int64, errorMsg string) error
	SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error
	SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error
	IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error
	// UpdateLastUsed 记录账号最近一次被选中。selectAccountForRequest 的"同优先级
	// 轮转"完全依赖 last_used_at：缺了这个写入，所有候选的时间戳都是空的，排序会
	// 一路落到 id 升序，同优先级的其他账号永远分不到流量。
	UpdateLastUsed(ctx context.Context, id int64) error
}

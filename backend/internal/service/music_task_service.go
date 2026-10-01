package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type musicSnapshot struct {
	GroupID       int64       `json:"group_id"`
	PublicBaseURL string      `json:"public_base_url"`
	Request       SunoRequest `json:"request"`
}
type MusicTrack struct {
	AudioID       string  `json:"audio_id,omitempty"`
	Status        string  `json:"status,omitempty"`
	Title         string  `json:"title,omitempty"`
	Lyrics        string  `json:"lyrics,omitempty"`
	Tags          string  `json:"tags,omitempty"`
	DisplayTags   string  `json:"display_tags,omitempty"`
	Duration      float64 `json:"duration"`
	AudioURL      string  `json:"audio_url"`
	ImageURL      string  `json:"image_url,omitempty"`
	ImageLargeURL string  `json:"image_large_url,omitempty"`
}
type MusicResult struct {
	Music []MusicTrack `json:"music"`
}
type sunoTaskResponse struct {
	Code int `json:"code"`
	Data struct {
		Status      string      `json:"status"`
		Progress    float64     `json:"progress"`
		Cost        *float64    `json:"cost"`
		CreditsCost *float64    `json:"credits_cost"`
		Result      MusicResult `json:"result"`
		Error       struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"data"`
}

type MusicTaskService struct {
	ledger    MusicTaskLedger
	encryptor SecretEncryptor
	files     *FileStorageService
	publisher *TemporaryAssetPublisher
	resolver  *ModelPricingResolver
	keys      *APIKeyService
	cache     *BillingCacheService
	gateway   *OpenAIGatewayService
	once      sync.Once
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func NewMusicTaskService(ledger MusicTaskLedger, encryptor SecretEncryptor, files *FileStorageService, publisher *TemporaryAssetPublisher, resolver *ModelPricingResolver, keys *APIKeyService, cache *BillingCacheService, gateway *OpenAIGatewayService) *MusicTaskService {
	s := &MusicTaskService{ledger: ledger, encryptor: encryptor, files: files, publisher: publisher, resolver: resolver, keys: keys, cache: cache, gateway: gateway}
	keys.musicTaskLookup = func(ctx context.Context, id int64, idem string) bool {
		key, err := keys.GetByID(ctx, id)
		if err != nil {
			return false
		}
		_, err = ledger.Find(ctx, MusicTaskOwner{key.UserID, key.ID}, idem, true)
		return err == nil
	}
	return s
}
func (s *MusicTaskService) Enabled(ctx context.Context) bool {
	if s == nil || s.files == nil || s.ledger == nil {
		return false
	}
	cfg, _, err := s.files.loadEffectiveConfig(ctx)
	return err == nil && cfg.Generated != nil && cfg.Generated.AsyncMusicEnabled
}
func (s *MusicTaskService) Accept(ctx context.Context, key *APIKey, sub *UserSubscription, r *SunoRequest, idem, base string) (*MusicTask, error) {
	if !IsYingzoMusicGroup(key.Group) || key.GroupID == nil {
		return nil, infraerrors.New(403, "music_group_required", "Suno requires the Yingzo Agent group")
	}
	if len(idem) > 200 {
		return nil, infraerrors.BadRequest("invalid_request_error", "Idempotency-Key exceeds 200 bytes")
	}
	body, _ := json.Marshal(r)
	sum := sha256.Sum256(body)
	fingerprint := hex.EncodeToString(sum[:])
	owner := MusicTaskOwner{key.UserID, key.ID}
	if idem != "" {
		old, err := s.ledger.Find(ctx, owner, idem, true)
		if err == nil {
			if old.Fingerprint != fingerprint {
				return nil, ErrMusicIdempotencyConflict
			}
			return musicTaskToPublic(&old.MusicTaskRecord), nil
		}
		if !errors.Is(err, ErrMusicTaskNotFound) {
			return nil, err
		}
	}
	if !s.Enabled(ctx) {
		return nil, infraerrors.New(503, "music_tasks_disabled", "Enable music generation in generated file storage settings")
	}
	if key.Group.ModelAllowlistEnabled() && !key.Group.ModelAllowlist.Allows(SunoModel) {
		return nil, infraerrors.New(403, "model_not_allowed", "suno-v6 is not allowed")
	}
	price, _, err := s.resolver.ResolveAgentMediaUnitPrice(ctx, *key.GroupID, PlatformOpenAI, AgentMediaTypeAudio, r.Mode(), SunoModel)
	if err != nil {
		return nil, infraerrors.BadRequest("pricing_not_configured", "Configure and enable the requested Suno generation mode price")
	}
	accounts, err := s.accounts(ctx, *key.GroupID)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, infraerrors.New(503, "music_account_unavailable", "No available Suno account")
	}
	price = QuantizeUsageBillingAmount(price)
	now := time.Now()
	id := "musictask_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	q := MusicTaskQuote{Model: SunoModel, Mode: r.Mode(), QuotaPlatform: PlatformOpenAI, PricingAt: now}
	q.Charge = UsageBillingCommand{RequestID: "music:" + id + ":precharge", APIKeyID: key.ID, UserID: key.UserID, Model: SunoModel, MediaType: AgentMediaTypeAudio, APIKeyQuotaCost: price, APIKeyRateLimitCost: price}
	if sub != nil && key.Group.IsSubscriptionType() {
		q.Charge.SubscriptionID = &sub.ID
		q.Charge.SubscriptionCost = price
		q.Charge.BillingType = BillingTypeSubscription
	} else {
		q.Charge.BalanceCost = price
	}
	mode := string(BillingModePerRequest)
	tier := r.Mode()
	endpoint := "/v1/music/generations"
	q.Usage = UsageLog{UserID: key.UserID, APIKeyID: key.ID, RequestID: q.Charge.RequestID, Model: SunoModel, RequestedModel: SunoModel, GroupID: key.GroupID, SubscriptionID: q.Charge.SubscriptionID, BillingType: q.Charge.BillingType, ActualCost: price, RateMultiplier: 1, BillingMode: &mode, BillingTier: &tier, InboundEndpoint: &endpoint, CreatedAt: now}
	snapshot, _ := json.Marshal(musicSnapshot{GroupID: *key.GroupID, PublicBaseURL: base, Request: *r})
	encrypted, err := s.encryptor.Encrypt(string(snapshot))
	if err != nil {
		return nil, err
	}
	cfg, _, err := s.files.loadEffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	t := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: id, UserID: key.UserID, APIKeyID: key.ID, Mode: r.Mode(), Status: MusicTaskStatusProcessing, Phase: "queued", BillingStatus: "precharged", RefundStatus: "none", CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(cfg.ResultRetentionHours) * time.Hour).Unix(), DeadlineAt: now.Add(30 * time.Minute).Unix()}, Quote: q, IdempotencyKey: idem, Fingerprint: fingerprint, EncryptedRequest: encrypted}
	t, _, err = s.ledger.Accept(ctx, t)
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, t)
	return musicTaskToPublic(&t.MusicTaskRecord), nil
}

func (s *MusicTaskService) Get(ctx context.Context, owner MusicTaskOwner, id string, idem bool) (*MusicTask, error) {
	t, err := s.ledger.Find(ctx, owner, id, idem)
	if err != nil {
		return nil, err
	}
	if t.Phase == "refunding" || t.Phase == "interrupted" {
		t.Status = MusicTaskStatusFailed
		t.BillingStatus = "refunding"
		t.RefundStatus = "pending"
	}
	if t.Status == MusicTaskStatusCompleted {
		if time.Now().Unix() >= t.ExpiresAt {
			return nil, ErrMusicTaskExpired
		}
		var result MusicResult
		if json.Unmarshal(t.Result, &result) != nil {
			return nil, ErrMusicTaskUnavailable
		}
		assetOwner := TemporaryAssetOwner{UserID: owner.UserID, APIKeyID: owner.APIKeyID}
		for i := range result.Music {
			track := &result.Music[i]
			for index, v := range []*string{&track.AudioURL, &track.ImageURL, &track.ImageLargeURL} {
				if *v != "" {
					*v, err = s.publisher.RefreshGeneratedURL(ctx, assetOwner, *v)
					if err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							if index > 0 { // An expired optional cover does not hide playable music.
								*v = ""
								continue
							}
							return nil, ErrMusicTaskExpired
						}
						return nil, ErrMusicTaskUnavailable
					}
				}
			}
		}
		t.Result, _ = json.Marshal(result)
	}
	return musicTaskToPublic(&t.MusicTaskRecord), nil
}
func (s *MusicTaskService) accounts(ctx context.Context, group int64) ([]Account, error) {
	items, err := s.gateway.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, group, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	out := []Account{}
	for _, a := range items {
		if a.IsSuno() && a.IsSchedulable() {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
func (s *MusicTaskService) invalidate(ctx context.Context, t *DurableMusicTask) {
	if s.cache != nil {
		_ = s.cache.InvalidateUserBalance(ctx, t.UserID)
		_ = s.cache.InvalidateAPIKeyRateLimit(ctx, t.APIKeyID)
		s.cache.RollbackUserPlatformQuotaUsage(ctx, t.UserID, t.Quote.QuotaPlatform, 0)
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
func (s *MusicTaskService) Start() {
	s.once.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		for i := 0; i < 8; i++ {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						t, err := s.ledger.Claim(ctx)
						if errors.Is(err, ErrMusicTaskNotFound) {
							continue
						}
						if err != nil {
							slog.Warn("music task claim failed", "error", err)
							continue
						}
						s.run(ctx, t)
					}
				}
			}()
		}
	})
}
func (s *MusicTaskService) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait()
	}
}
func (s *MusicTaskService) checkpoint(ctx context.Context, t *DurableMusicTask, delay time.Duration) {
	if err := s.ledger.Checkpoint(ctx, t, delay); err != nil {
		slog.Warn("music checkpoint failed", "task_id", t.ID, "error", err)
	}
}

// Each lease executes a single durable stage. Only preparing can issue a POST;
// Pin commits submitting before that POST, so an ambiguous submission is never replayed.
func (s *MusicTaskService) run(parent context.Context, t *DurableMusicTask) {
	ctx, cancel := context.WithDeadline(parent, time.Unix(t.DeadlineAt, 0))
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.ledger.Renew(ctx, t.ID, t.LeaseToken) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		if v := recover(); v != nil {
			slog.Error("music worker panic", "task_id", t.ID, "panic", v)
		}
	}()
	if t.Phase == "interrupted" || t.Phase == "refunding" {
		s.fail(t, "music_task_failed", "Music generation failed or exceeded its deadline")
		return
	}
	if t.Phase == "submission_unknown" {
		s.checkpoint(ctx, t, 10*time.Second)
		return
	}
	raw, err := s.encryptor.Decrypt(t.EncryptedRequest)
	if err != nil {
		s.fail(t, "music_snapshot_unavailable", "Music request unavailable")
		return
	}
	var snapshot musicSnapshot
	if json.Unmarshal([]byte(raw), &snapshot) != nil {
		s.fail(t, "music_snapshot_invalid", "Music request unavailable")
		return
	}
	if t.Phase == "preparing" {
		s.submit(ctx, t, &snapshot)
		return
	}
	if t.Phase == "polling" {
		s.poll(ctx, t)
		return
	}
	if t.Phase == "saving" {
		s.publish(ctx, t, &snapshot)
		return
	}
	s.fail(t, "music_task_invalid", "Invalid music task phase")
}
func (s *MusicTaskService) submit(ctx context.Context, t *DurableMusicTask, snapshot *musicSnapshot) {
	key, err := s.keys.GetByID(ctx, t.APIKeyID)
	if err != nil {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	if key.GroupID == nil || *key.GroupID != snapshot.GroupID || !IsYingzoMusicGroup(key.Group) || !key.Group.IsActive() || key.User == nil || !key.User.IsActive() || key.IsExpired() || (!key.IsActive() && key.Status != StatusAPIKeyQuotaExhausted) {
		s.fail(t, "music_owner_unavailable", "Task owner is no longer authorized")
		return
	}
	accounts, err := s.accounts(ctx, snapshot.GroupID)
	if err != nil {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	var selected *Account
	for i := range accounts {
		ok, e := s.ledger.Pin(ctx, t, accounts[i].ID, accounts[i].Concurrency)
		if e != nil {
			return
		}
		if ok {
			selected = &accounts[i]
			break
		}
	}
	if selected == nil {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body, err := sunoHTTP(callCtx, s.gateway, selected, http.MethodPost, "/v1/music/generations", snapshot.Request.UpstreamBody())
	if err != nil {
		var upstream *sunoHTTPError
		if errors.As(err, &upstream) && upstream.status >= 400 && upstream.status < 500 && upstream.status != 408 {
			s.fail(t, "music_upstream_rejected", "Suno rejected the generation request")
			return
		}
		t.Phase = "submission_unknown"
		s.checkpoint(ctx, t, 10*time.Second)
		return
	}
	var receipt struct {
		Code int `json:"code"`
		Data []struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &receipt) != nil || receipt.Code != 200 || len(receipt.Data) != 1 || receipt.Data[0].TaskID == "" {
		t.Phase = "submission_unknown"
		s.checkpoint(ctx, t, 10*time.Second)
		return
	}
	t.Quote.UpstreamID = receipt.Data[0].TaskID
	t.Phase = "polling"
	s.checkpoint(ctx, t, 3*time.Second)
	_ = s.gateway.accountRepo.UpdateLastUsed(ctx, selected.ID)
}
func (s *MusicTaskService) poll(ctx context.Context, t *DurableMusicTask) {
	a, err := s.gateway.accountRepo.GetByID(ctx, t.Quote.AccountID)
	if err != nil {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, err := sunoHTTP(callCtx, s.gateway, a, http.MethodGet, "/v1/music/tasks/"+url.PathEscape(t.Quote.UpstreamID), nil)
	if err != nil {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	var response sunoTaskResponse
	if json.Unmarshal(body, &response) != nil || response.Code != 200 {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	t.Progress = math.Max(0, math.Min(100, response.Data.Progress))
	if response.Data.Status != "completed" && response.Data.Status != "failed" {
		s.checkpoint(ctx, t, 5*time.Second)
		return
	}
	usage := t.Quote.Usage
	usage.AccountID = a.ID
	rate := a.BillingRateMultiplier()
	usage.AccountRateMultiplier = &rate
	if response.Data.Cost != nil && *response.Data.Cost >= 0 {
		usage.TotalCost = *response.Data.Cost
	}
	usage.UpstreamModel = new(string)
	*usage.UpstreamModel = "suno"
	usage.UpstreamRequestID = &t.Quote.UpstreamID
	elapsed := int(time.Since(time.Unix(t.CreatedAt, 0)).Milliseconds())
	usage.DurationMs = &elapsed
	t.CapturedUsage = &usage
	t.Quote.UpstreamCreditsCost = response.Data.CreditsCost
	if response.Data.Status == "failed" {
		s.fail(t, "music_upstream_failed", "Suno generation failed")
		return
	}
	tracks := []MusicTrack{}
	for _, track := range response.Data.Result.Music {
		if track.AudioURL != "" && (track.Status == "" || track.Status == "complete" || track.Status == "completed") {
			tracks = append(tracks, track)
		}
	}
	if len(tracks) == 0 {
		s.fail(t, "music_empty_result", "Suno returned no playable audio")
		return
	}
	result, _ := json.Marshal(MusicResult{Music: tracks})
	t.EncryptedResult, err = s.encryptor.Encrypt(string(result))
	if err != nil {
		return
	}
	t.Phase = "saving"
	s.checkpoint(ctx, t, 0)
}
func (s *MusicTaskService) publish(ctx context.Context, t *DurableMusicTask, snapshot *musicSnapshot) {
	raw, err := s.encryptor.Decrypt(t.EncryptedResult)
	if err != nil {
		s.fail(t, "music_result_unavailable", "Saved music result unavailable")
		return
	}
	var result MusicResult
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Music) == 0 {
		s.fail(t, "music_empty_result", "Saved music result invalid")
		return
	}
	owner := TemporaryAssetOwner{UserID: t.UserID, APIKeyID: t.APIKeyID, GroupID: snapshot.GroupID}
	for i := range result.Music {
		track := &result.Music[i]
		stable := uuid.NewSHA1(uuid.NameSpaceOID, []byte(t.ID+":"+strconv.Itoa(i)+":audio"))
		track.AudioURL, err = s.publisher.PublishGeneratedMusic(ctx, owner, snapshot.PublicBaseURL, track.AudioURL, stable)
		if err != nil {
			s.checkpoint(ctx, t, 5*time.Second)
			return
		}
		for j, cover := range []*string{&track.ImageURL, &track.ImageLargeURL} {
			if *cover != "" {
				stable = uuid.NewSHA1(uuid.NameSpaceOID, []byte(t.ID+":"+strconv.Itoa(i)+":cover:"+strconv.Itoa(j)))
				*cover, err = s.publisher.PublishGeneratedMusicCover(ctx, owner, snapshot.PublicBaseURL, *cover, stable)
				if err != nil {
					*cover = ""
				}
			}
		}
	}
	t.Result, _ = json.Marshal(result)
	t.Progress = 100
	t.HTTPStatus = 200
	if t.CapturedUsage == nil {
		s.fail(t, "music_usage_unavailable", "Music usage unavailable")
		return
	}
	if err = s.ledger.Finalize(ctx, t, t.CapturedUsage, true); err != nil {
		slog.Warn("music settlement will be retried", "task_id", t.ID, "error", err)
		return
	}
	s.invalidate(ctx, t)
}
func (s *MusicTaskService) fail(t *DurableMusicTask, code, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if len(t.Error) == 0 {
		t.Error, _ = json.Marshal(map[string]string{"code": code, "message": message})
	}
	t.HTTPStatus = 502
	t.TaskError = NewUsageTaskError(502, nil, message)
	if err := s.ledger.MarkRefundPending(ctx, t); err != nil {
		slog.Warn("music refund checkpoint failed", "task_id", t.ID, "error", err)
		return
	}
	if err := s.ledger.Finalize(ctx, t, t.CapturedUsage, false); err != nil {
		slog.Warn("music refund will be retried", "task_id", t.ID, "error", err)
		return
	}
	s.invalidate(ctx, t)
}

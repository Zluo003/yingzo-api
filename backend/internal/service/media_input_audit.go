package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type MediaAuditService struct {
	db          *sql.DB
	settings    SettingRepository
	encryptor   SecretEncryptor
	email       mediaAuditMailer
	client      *http.Client
	imageClient *http.Client
	configMu    sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
}

type mediaAuditMailer interface {
	SendEmail(context.Context, string, string, string) error
}

func NewMediaAuditService(db *sql.DB, settings SettingRepository, encryptor SecretEncryptor, email *EmailService) *MediaAuditService {
	// Never follow API redirects: credentials must not be forwarded to a different host.
	client := newSSRFSafeHTTPClient(180 * time.Second)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &MediaAuditService{db: db, settings: settings, encryptor: encryptor, client: client, imageClient: NewReferenceMaterialFetcher().client}
	if email != nil {
		s.email = email
	}
	return s
}
func ProvideMediaAuditService(db *sql.DB, settings SettingRepository, encryptor SecretEncryptor, email *EmailService) *MediaAuditService {
	s := NewMediaAuditService(db, settings, encryptor, email)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.processNotifications(ctx)
			}
		}
	}()
	return s
}
func (s *MediaAuditService) Stop() {
	if s != nil && s.cancel != nil {
		s.cancel()
		<-s.done
	}
}

func (s *MediaAuditService) Check(ctx context.Context, account *Account, in MediaAuditInput) error {
	if !AccountNeedsMediaAudit(account) {
		return nil
	}
	if s == nil || s.settings == nil {
		return &MediaAuditError{}
	}
	if in.RequestedAt.IsZero() {
		in.RequestedAt = time.Now().UTC()
	}
	in.AccountID = account.ID
	in.AccountName = account.Name
	cfg, err := s.loadConfig(ctx)
	result := &MediaAuditResult{Decision: "error", Category: "audit_error", Reason: "审核配置不可用", PolicyVersion: mediaAuditPolicyVersion}
	prepared := &mediaAuditPrepared{Input: in, Normalized: normalizeMediaAuditText(in.Prompt)}
	attempts := []MediaAuditAttempt{}
	addAttempt := func(ep MediaAuditEndpoint, callErr error) {
		a := MediaAuditAttempt{Reviewer: ep.Protocol + ":" + ep.Model}
		if callErr != nil {
			a.Error = callErr.Error()
		}
		attempts = append(attempts, a)
	}
	if err == nil {
		auditCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		defer cancel()
		prepared, err = s.prepareInput(auditCtx, in)
		if err == nil {
			ep := cfg.Primary
			if err = validateMediaAuditEndpoint(&ep, true); err == nil {
				result, err = s.call(auditCtx, cfg, ep, prepared)
				addAttempt(ep, err)
			}
			if err != nil && cfg.Fallback.Domain != "" && auditCtx.Err() == nil {
				fallback := cfg.Fallback
				if validationErr := validateMediaAuditEndpoint(&fallback, true); validationErr == nil {
					result, err = s.call(auditCtx, cfg, fallback, prepared)
					addAttempt(fallback, err)
					if result != nil {
						result.UsedFallback = true
					}
				} else {
					err = validationErr
				}
			}
		}
		if err != nil {
			result = &MediaAuditResult{Decision: "error", Category: "audit_error", Reason: err.Error(), PolicyVersion: mediaAuditPolicyVersion}
		}
	}
	if err == nil && result != nil && result.Decision == "allow" {
		return nil
	}
	if result == nil {
		result = &MediaAuditResult{Decision: "error", Category: "audit_error", Reason: "审核结果无效", PolicyVersion: mediaAuditPolicyVersion}
	}
	result.Attempts = attempts
	// Evidence survives client disconnects; failure to persist never opens generation.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	id, saveErr := s.record(saveCtx, cfg, prepared, result)
	if saveErr != nil {
		slog.Error("media_input_audit.evidence_failed", "account_id", account.ID, "request_id", in.RequestID)
		return &MediaAuditError{}
	}
	return &MediaAuditError{Blocked: result.Decision == "block", EventID: id}
}

type MediaAuditEvent struct {
	ID             string              `json:"id"`
	RequestID      string              `json:"request_id"`
	UserID         int64               `json:"user_id"`
	UserEmail      string              `json:"user_email"`
	APIKeyID       int64               `json:"api_key_id"`
	AccountID      int64               `json:"account_id"`
	AccountName    string              `json:"account_name"`
	Model          string              `json:"model"`
	MediaType      string              `json:"media_type"`
	Decision       string              `json:"decision"`
	Category       string              `json:"category"`
	Reason         string              `json:"reason"`
	Reviewer       string              `json:"reviewer"`
	CreatedAt      time.Time           `json:"created_at"`
	ExpiresAt      time.Time           `json:"expires_at"`
	NotifiedAt     *time.Time          `json:"notified_at"`
	NotifyAttempts int                 `json:"notify_attempts"`
	NotifyPending  bool                `json:"notify_pending"`
	Evidence       *MediaAuditEvidence `json:"evidence,omitempty"`
}
type MediaAuditEvidence struct {
	Input            MediaAuditInput   `json:"input"`
	NormalizedPrompt string            `json:"normalized_prompt"`
	Assets           []MediaAuditAsset `json:"assets"`
	Result           *MediaAuditResult `json:"result"`
	Files            []string          `json:"files,omitempty"`
}

func (s *MediaAuditService) record(ctx context.Context, cfg MediaAuditConfig, p *mediaAuditPrepared, result *MediaAuditResult) (string, error) {
	if s.db == nil || s.encryptor == nil {
		return "", errors.New("evidence storage unavailable")
	}
	evidence := MediaAuditEvidence{Input: p.Input, NormalizedPrompt: p.Normalized, Assets: p.Assets, Result: result, Files: []string{}}
	evidence.Input.Images = append([]string(nil), p.Input.Images...)
	for i, source := range evidence.Input.Images {
		if strings.HasPrefix(source, "data:") {
			evidence.Input.Images[i] = "[inline image]"
		}
	}
	for _, asset := range p.Assets {
		evidence.Files = append(evidence.Files, base64.StdEncoding.EncodeToString(asset.Data))
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	encrypted, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return "", err
	}
	days := cfg.RetentionDays
	if days < 1 || days > 365 {
		days = 30
	}
	id := uuid.NewString()
	_, err = s.db.ExecContext(ctx, `INSERT INTO media_input_audit_events
 (id,request_id,user_id,user_email,api_key_id,account_id,account_name,model,media_type,decision,category,reason,reviewer,encrypted_payload,expires_at,notify_pending)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, id, p.Input.RequestID, p.Input.UserID, p.Input.UserEmail, p.Input.APIKeyID, p.Input.AccountID, p.Input.AccountName, p.Input.Model, p.Input.MediaType, result.Decision, result.Category, result.Reason, result.Reviewer, encrypted, time.Now().Add(time.Duration(days)*24*time.Hour), len(cfg.AdminEmails) > 0)
	return id, err
}

const mediaAuditEventColumns = `id::text,request_id,user_id,user_email,api_key_id,account_id,account_name,model,media_type,decision,category,reason,reviewer,created_at,expires_at,notified_at,notify_attempts,notify_pending`

func scanMediaAuditEvent(row interface{ Scan(...any) error }) (MediaAuditEvent, error) {
	var e MediaAuditEvent
	err := row.Scan(&e.ID, &e.RequestID, &e.UserID, &e.UserEmail, &e.APIKeyID, &e.AccountID, &e.AccountName, &e.Model, &e.MediaType, &e.Decision, &e.Category, &e.Reason, &e.Reviewer, &e.CreatedAt, &e.ExpiresAt, &e.NotifiedAt, &e.NotifyAttempts, &e.NotifyPending)
	return e, err
}
func (s *MediaAuditService) ListEvents(ctx context.Context, page int) ([]MediaAuditEvent, error) {
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+mediaAuditEventColumns+` FROM media_input_audit_events WHERE expires_at>NOW() ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET $1`, (page-1)*20)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []MediaAuditEvent{}
	for rows.Next() {
		e, err := scanMediaAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
func (s *MediaAuditService) event(ctx context.Context, id string, files bool) (*MediaAuditEvent, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, infraerrors.NotFound("AUDIT_EVENT_NOT_FOUND", "审核事件不存在")
	}
	e, err := scanMediaAuditEvent(s.db.QueryRowContext(ctx, `SELECT `+mediaAuditEventColumns+` FROM media_input_audit_events WHERE id=$1 AND expires_at>NOW()`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("AUDIT_EVENT_NOT_FOUND", "审核事件不存在或已过期")
	}
	if err != nil {
		return nil, err
	}
	var encrypted string
	if err = s.db.QueryRowContext(ctx, `SELECT encrypted_payload FROM media_input_audit_events WHERE id=$1 AND expires_at>NOW()`, id).Scan(&encrypted); err != nil {
		return nil, err
	}
	raw, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, errors.New("审核证据解密失败")
	}
	var evidence MediaAuditEvidence
	if json.Unmarshal([]byte(raw), &evidence) != nil {
		return nil, errors.New("审核证据格式无效")
	}
	for i := range evidence.Assets {
		evidence.Assets[i].BackupURL = fmt.Sprintf("/api/v1/admin/risk-control/media-input/events/%s/assets/%d", id, i)
	}
	if !files {
		evidence.Files = nil
	}
	e.Evidence = &evidence
	return &e, nil
}
func (s *MediaAuditService) GetEvent(ctx context.Context, id string) (*MediaAuditEvent, error) {
	return s.event(ctx, id, false)
}
func (s *MediaAuditService) EvidenceAsset(ctx context.Context, id string, index int) ([]byte, string, error) {
	e, err := s.event(ctx, id, true)
	if err != nil {
		return nil, "", err
	}
	if index < 0 || index >= len(e.Evidence.Files) || index >= len(e.Evidence.Assets) {
		return nil, "", infraerrors.NotFound("AUDIT_ASSET_NOT_FOUND", "证据素材不存在")
	}
	data, err := base64.StdEncoding.DecodeString(e.Evidence.Files[index])
	return data, e.Evidence.Assets[index].MIME, err
}
func (s *MediaAuditService) processNotifications(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	// Retention is independent of temporary asset eviction; expired cases cannot be served.
	if _, err := s.db.ExecContext(runCtx, `DELETE FROM media_input_audit_events WHERE id IN (SELECT id FROM media_input_audit_events WHERE expires_at<=NOW() LIMIT 100)`); err != nil {
		return
	}
	if s.email == nil {
		return
	}
	cfg, err := s.loadConfig(runCtx)
	if err != nil || len(cfg.AdminEmails) == 0 {
		return
	}
	// Lease before delivery: concurrent nodes do not send the same pending item together.
	rows, err := s.db.QueryContext(runCtx, `UPDATE media_input_audit_events SET notify_after=NOW()+INTERVAL '5 minutes',notify_attempts=notify_attempts+1
 WHERE id IN (SELECT id FROM media_input_audit_events WHERE notify_pending AND notify_after<=NOW() AND expires_at>NOW() ORDER BY created_at LIMIT 3 FOR UPDATE SKIP LOCKED) RETURNING id::text`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		e, err := s.GetEvent(runCtx, id)
		if err != nil {
			continue
		}
		link := "/admin/risk-control?media_event=" + id
		values, _ := s.settings.GetMultiple(runCtx, []string{"frontend_url"})
		if base := strings.TrimRight(values["frontend_url"], "/"); strings.HasPrefix(base, "https://") {
			link = base + link
		}
		body := fmt.Sprintf(`<p>账号级媒体输入审核事件（%s）。本事件未封禁用户。</p><p>用户：%s（UID %d），API Key ID：%d</p><p>上游账号：%s（%d），模型：%s，通道：%s</p><p>时间：%s；类别：%s；原因：%s</p><p>请求 ID：%s</p><p><a href="%s">登录管理后台查看提示词和私有素材证据</a></p><p>审核事件 ID：%s</p>`, html.EscapeString(e.Decision), html.EscapeString(e.UserEmail), e.UserID, e.APIKeyID, html.EscapeString(e.AccountName), e.AccountID, html.EscapeString(e.Model), html.EscapeString(e.Evidence.Input.VideoChannelID), e.CreatedAt.UTC().Format(time.RFC3339), html.EscapeString(e.Category), html.EscapeString(e.Reason), html.EscapeString(e.RequestID), html.EscapeString(link), id)
		sent := true
		for _, recipient := range cfg.AdminEmails {
			if s.email.SendEmail(runCtx, recipient, "[媒体前置审核] "+e.Decision+" "+id, body) != nil {
				sent = false
			}
		}
		if sent {
			_, _ = s.db.ExecContext(runCtx, `UPDATE media_input_audit_events SET notify_pending=FALSE,notified_at=NOW() WHERE id=$1`, id)
		}
	}
}

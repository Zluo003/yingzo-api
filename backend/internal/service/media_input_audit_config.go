package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MediaInputAuditExtraKey = "media_input_audit_enabled"
const mediaAuditSettingKey = "account_media_input_audit_v1"

// Endpoint keys are plaintext only during a write or an outbound call. Read APIs mask them.
type MediaAuditEndpoint struct {
	Protocol       string `json:"protocol"`
	Domain         string `json:"domain"`
	APIKey         string `json:"api_key,omitempty"`
	ClearKey       bool   `json:"clear_key,omitempty"`
	KeyConfigured  bool   `json:"key_configured"`
	Model          string `json:"model"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type MediaAuditConfig struct {
	Primary           MediaAuditEndpoint `json:"primary"`
	Fallback          MediaAuditEndpoint `json:"fallback"`
	AdminEmails       []string           `json:"admin_emails"`
	RetentionDays     int                `json:"retention_days"`
	PoliticalNames    bool               `json:"political_names"`
	PublicFigureNames bool               `json:"public_figure_names"`
	Nudity            bool               `json:"nudity"`
}

func defaultMediaAuditConfig() MediaAuditConfig {
	return MediaAuditConfig{Primary: MediaAuditEndpoint{Protocol: "openai", TimeoutSeconds: 30}, Fallback: MediaAuditEndpoint{Protocol: "gemini", TimeoutSeconds: 30}, RetentionDays: 30, PoliticalNames: true, PublicFigureNames: true, Nudity: true, AdminEmails: []string{}}
}
func AccountNeedsMediaAudit(account *Account) bool {
	if account == nil {
		return false
	}
	enabled, _ := account.Extra[MediaInputAuditExtraKey].(bool)
	return enabled
}
func (s *MediaAuditService) loadConfig(ctx context.Context) (MediaAuditConfig, error) {
	cfg := defaultMediaAuditConfig()
	values, err := s.settings.GetMultiple(ctx, []string{mediaAuditSettingKey})
	if err != nil {
		return cfg, err
	}
	if raw := values[mediaAuditSettingKey]; raw != "" {
		err = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg, err
}
func (s *MediaAuditService) GetConfig(ctx context.Context) (MediaAuditConfig, error) {
	cfg, err := s.loadConfig(ctx)
	for _, ep := range []*MediaAuditEndpoint{&cfg.Primary, &cfg.Fallback} {
		ep.KeyConfigured = ep.APIKey != ""
		ep.APIKey = ""
		ep.ClearKey = false
	}
	return cfg, err
}
func validateMediaAuditEndpoint(ep *MediaAuditEndpoint, required bool) error {
	ep.Domain = strings.TrimSpace(ep.Domain)
	ep.Model = strings.TrimSpace(ep.Model)
	if ep.Domain == "" && ep.Model == "" && ep.APIKey == "" && !required {
		return nil
	}
	if ep.Protocol != "openai" && ep.Protocol != "gemini" {
		return errors.New("审核协议必须为 openai 或 gemini")
	}
	if !strings.Contains(ep.Domain, "://") {
		ep.Domain = "https://" + ep.Domain
	}
	u, err := url.Parse(ep.Domain)
	if err != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("审核地址只填写 HTTPS 域名，不包含接口路径、查询参数或凭据")
	}
	ep.Domain = strings.TrimRight(ep.Domain, "/")
	if ep.Model == "" || len(ep.Model) > 200 || !isSafeUpstreamPathSegment(ep.Model) {
		return errors.New("审核模型名称无效")
	}
	if ep.TimeoutSeconds < 5 || ep.TimeoutSeconds > 180 {
		return errors.New("审核超时须为 5–180 秒")
	}
	if ep.APIKey == "" {
		return errors.New("请配置审核 API Key")
	}
	return nil
}
func (s *MediaAuditService) SaveConfig(ctx context.Context, in MediaAuditConfig) (MediaAuditConfig, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if !in.PoliticalNames && !in.PublicFigureNames && !in.Nudity {
		return MediaAuditConfig{}, infraerrors.BadRequest("INVALID_MEDIA_AUDIT_CONFIG", "至少启用一项审核规则")
	}
	old, err := s.loadConfig(ctx)
	if err != nil {
		return MediaAuditConfig{}, err
	}
	if in.RetentionDays < 1 || in.RetentionDays > 365 {
		return MediaAuditConfig{}, infraerrors.BadRequest("INVALID_MEDIA_AUDIT_CONFIG", "证据保留天数须为 1–365")
	}
	if len(in.AdminEmails) == 0 || len(in.AdminEmails) > 20 {
		return MediaAuditConfig{}, infraerrors.BadRequest("INVALID_MEDIA_AUDIT_CONFIG", "请配置 1–20 个管理员收件邮箱")
	}
	for i, v := range in.AdminEmails {
		a, e := mail.ParseAddress(strings.TrimSpace(v))
		if e != nil || a.Address != strings.TrimSpace(v) {
			return MediaAuditConfig{}, infraerrors.BadRequest("INVALID_MEDIA_AUDIT_CONFIG", "管理员邮箱格式无效")
		}
		in.AdminEmails[i] = a.Address
	}
	for i, ep := range []*MediaAuditEndpoint{&in.Primary, &in.Fallback} {
		prior := []MediaAuditEndpoint{old.Primary, old.Fallback}[i]
		if ep.ClearKey {
			ep.APIKey = ""
		} else if strings.TrimSpace(ep.APIKey) == "" {
			ep.APIKey = prior.APIKey
		} else {
			if s.encryptor == nil {
				return MediaAuditConfig{}, errors.New("secret encryption unavailable")
			}
			ep.APIKey, err = s.encryptor.Encrypt(strings.TrimSpace(ep.APIKey))
			if err != nil {
				return MediaAuditConfig{}, err
			}
		}
		if err = validateMediaAuditEndpoint(ep, i == 0); err != nil {
			return MediaAuditConfig{}, infraerrors.BadRequest("INVALID_MEDIA_AUDIT_CONFIG", err.Error())
		}
		ep.ClearKey = false
		ep.KeyConfigured = false
	}
	raw, err := json.Marshal(in)
	if err == nil {
		err = s.settings.Set(ctx, mediaAuditSettingKey, string(raw))
	}
	if err != nil {
		return MediaAuditConfig{}, err
	}
	return s.GetConfig(ctx)
}
func (s *MediaAuditService) endpointKey(ep MediaAuditEndpoint) (string, error) {
	if s.encryptor == nil || ep.APIKey == "" {
		return "", errors.New("审核密钥未配置")
	}
	return s.encryptor.Decrypt(ep.APIKey)
}
func (s *MediaAuditService) Probe(ctx context.Context, slot, prompt string, images []string) (*MediaAuditResult, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	ep := cfg.Primary
	if slot == "fallback" {
		ep = cfg.Fallback
	} else if slot != "primary" {
		return nil, infraerrors.BadRequest("INVALID_ENDPOINT", "请选择 primary 或 fallback")
	}
	if err = validateMediaAuditEndpoint(&ep, true); err != nil {
		return nil, infraerrors.BadRequest("INVALID_ENDPOINT", err.Error())
	}
	input, err := s.prepareInput(ctx, MediaAuditInput{Prompt: prompt, Images: images})
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_AUDIT_INPUT", err.Error())
	}
	result, err := s.call(ctx, cfg, ep, input)
	if err != nil {
		return nil, infraerrors.BadRequest("MEDIA_AUDIT_PROBE_FAILED", err.Error())
	}
	return result, nil
}

// Audit decisions are terminal, separate from provider failover and account bans.
type MediaAuditError struct {
	Blocked bool
	EventID string
}

func (e *MediaAuditError) Error() string {
	if e.Blocked {
		return "内容未通过前置审核"
	}
	return "内容审核暂不可用，请稍后重试"
}
func (e *MediaAuditError) StatusCode() int {
	if e.Blocked {
		return 403
	}
	return 503
}
func (e *MediaAuditError) Code() string {
	if e.Blocked {
		return "content_policy_violation"
	}
	return "media_audit_unavailable"
}

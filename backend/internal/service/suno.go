package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SunoModel = "suno-v6"
const SunoProvider = "apimart_suno"
const AgentMediaTypeAudio = "audio"
const AgentInterfaceMusic = "music.generations"

type SunoRequest struct {
	Operation     string         `json:"operation,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
	Version       string         `json:"version,omitempty"`
	CustomModelID string         `json:"custom_model_id,omitempty"`
	PersonaID     string         `json:"persona_id,omitempty"`
	AudioWeight   *float64       `json:"audio_weight,omitempty"`
	Variety       string         `json:"variety,omitempty"`
	MaxMode       bool           `json:"max_mode,omitempty"`

	Model        string   `json:"model"`
	Custom       bool     `json:"custom"`
	Instrumental *bool    `json:"instrumental"`
	Prompt       string   `json:"prompt"`
	Style        string   `json:"style,omitempty"`
	Title        string   `json:"title,omitempty"`
	AutoLyrics   bool     `json:"auto_lyrics"`
	StyleWeight  *float64 `json:"style_weight,omitempty"`
	Weirdness    *float64 `json:"weirdness_constraint,omitempty"`
	VocalGender  string   `json:"vocal_gender,omitempty"`
	NegativeTags string   `json:"negative_tags,omitempty"`
	Duration     *int     `json:"duration,omitempty"`
	AudioFormat  string   `json:"audio_format"`
}

func (r *SunoRequest) Mode() string {
	op := r.Operation
	if op == "" || op == "generation" {
		op = "song"
		if r.Instrumental != nil && *r.Instrumental {
			op = "instrumental"
		}
	}
	op = sunoBillingOperation(op)
	f := r.Fields()
	if _, ok := f["custom_model_id"]; ok {
		return op + ":custom"
	}
	if v, _ := f["version"].(string); v != "" && v != "v6" {
		return op + ":" + v
	}
	return op
}
func IsSunoPriceTier(mode string) bool {
	for _, tier := range SunoPriceTiers() {
		if mode == tier {
			return true
		}
	}
	return false
}
func IsYingzoMusicGroup(g *Group) bool { return g != nil && g.IsAgent() && g.SystemCode == "yingzo" }

func ParseSunoRequest(body []byte) (*SunoRequest, error) {
	return ParseSunoOperation("generation", body)
}
func (r *SunoRequest) UpstreamBody() []byte {
	payload := r.Fields()
	if r.Operation != "uploadTask" {
		payload["model"] = "suno"
	} else {
		delete(payload, "model")
	}
	body, _ := json.Marshal(payload)
	return body
}

func (a *Account) IsSuno() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey && a.Extra["music_provider"] == SunoProvider
}

func normalizeSunoAccount(a *Account) error {
	if a.Extra["music_provider"] != SunoProvider {
		return nil
	}
	if !a.IsSuno() || strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return infraerrors.BadRequest("INVALID_SUNO_ACCOUNT", "Suno requires an OpenAI API-key account and APIMart API key")
	}
	base := strings.TrimSpace(a.GetCredential("base_url"))
	if base == "" {
		base = "https://api.apimart.ai"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return infraerrors.BadRequest("INVALID_SUNO_ACCOUNT", "Suno base URL must be an HTTPS origin or /v1 URL")
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" && p != "/v1" {
		return infraerrors.BadRequest("INVALID_SUNO_ACCOUNT", "Suno base URL must not include an endpoint path")
	}
	if a.Extra["image_provider"] != nil || a.IsImageAccount() {
		return infraerrors.BadRequest("INVALID_SUNO_ACCOUNT", "Suno must use a dedicated music account")
	}
	a.Credentials["base_url"] = strings.TrimRight(base, "/")
	a.Credentials["model_mapping"] = map[string]any{SunoModel: SunoModel}
	return nil
}

func (s *adminServiceImpl) validateSunoGroups(ctx context.Context, a *Account, ids []int64) error {
	if !a.IsSuno() {
		return nil
	}
	if len(ids) == 0 {
		return infraerrors.BadRequest("INVALID_SUNO_GROUP", "Bind Suno to the Yingzo Agent group")
	}
	for _, id := range ids {
		g, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if !IsYingzoMusicGroup(g) {
			return infraerrors.BadRequest("INVALID_SUNO_GROUP", "Suno only supports the Yingzo Agent group")
		}
	}
	return nil
}

func normalizeSunoPrices(mediaType string, prices []AgentModelPrice) ([]AgentModelPrice, error) {
	if mediaType != AgentMediaTypeAudio {
		return nil, errors.New("suno-v6 is an audio model")
	}
	out := make([]AgentModelPrice, 0, len(prices))
	seen := map[string]bool{}
	for _, p := range prices {
		if !IsSunoPriceTier(p.Resolution) || seen[p.Resolution] || p.UnitPrice < 0 || math.IsNaN(p.UnitPrice) || math.IsInf(p.UnitPrice, 0) {
			return nil, errors.New("suno requires unique supported operation prices with finite non-negative amounts")
		}
		seen[p.Resolution] = true
		p.BillingUnit = "request"
		out = append(out, p)
	}
	return out, nil
}

type sunoHTTPError struct{ status int }

func (e *sunoHTTPError) Error() string { return fmt.Sprintf("Suno upstream HTTP %d", e.status) }

func sunoHTTP(ctx context.Context, gateway *OpenAIGatewayService, a *Account, method, path string, body []byte) ([]byte, error) {
	base := strings.TrimSuffix(strings.TrimRight(a.GetCredential("base_url"), "/"), "/v1")
	base, err := gateway.validateUpstreamBaseURL(base)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.GetCredential("api_key"))
	req.Header.Set("Content-Type", "application/json")
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	resp, err := gateway.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &sunoHTTPError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2*1024*1024 {
		return nil, errors.New("suno response too large")
	}
	return data, nil
}

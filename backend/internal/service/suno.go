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
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SunoModel = "suno-v6"
const SunoProvider = "apimart_suno"
const AgentMediaTypeAudio = "audio"
const AgentInterfaceMusic = "music.generations"

type SunoRequest struct {
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
	if r.Custom {
		return "song"
	}
	return "instrumental"
}
func IsSunoPriceTier(mode string) bool { return mode == "instrumental" || mode == "song" }
func IsYingzoMusicGroup(g *Group) bool { return g != nil && g.IsAgent() && g.SystemCode == "yingzo" }

func ParseSunoRequest(body []byte) (*SunoRequest, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, errors.New("request must be a JSON object")
	}
	allowed := map[string]bool{}
	for _, name := range []string{"model", "custom", "instrumental", "prompt", "style", "title", "auto_lyrics", "style_weight", "weirdness_constraint", "vocal_gender", "negative_tags", "duration", "audio_format"} {
		allowed[name] = true
	}
	for k, v := range fields {
		if !allowed[k] {
			return nil, fmt.Errorf("unsupported field %q", k)
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, fmt.Errorf("%s must not be null", k)
		}
	}
	var r SunoRequest
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("request must contain one JSON object")
	}
	if r.Model != "" && r.Model != SunoModel {
		return nil, errors.New("only suno-v6 is supported")
	}
	r.Model = SunoModel
	wantInstrumental := !r.Custom
	if r.Instrumental != nil && *r.Instrumental != wantInstrumental {
		return nil, errors.New("custom=false requires instrumental=true; custom=true requires instrumental=false")
	}
	r.Instrumental = &wantInstrumental
	if r.AutoLyrics {
		return nil, errors.New("auto_lyrics must be false")
	}
	limit := 3000
	if r.Custom {
		limit = 5000
	}
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > limit {
		return nil, fmt.Errorf("prompt must contain 1-%d Unicode characters", limit)
	}
	if utf8.RuneCountInString(r.Style) > 1000 || utf8.RuneCountInString(r.Title) > 80 {
		return nil, errors.New("style allows 1000 characters and title allows 80 characters")
	}
	if !r.Custom {
		for _, name := range []string{"style", "title", "vocal_gender", "negative_tags", "duration"} {
			if _, ok := fields[name]; ok {
				return nil, fmt.Errorf("%s is only supported in song mode", name)
			}
		}
	} else if strings.TrimSpace(r.Style) == "" {
		return nil, errors.New("song mode requires style")
	}
	for _, value := range []*float64{r.StyleWeight, r.Weirdness} {
		if value != nil && (*value < 0 || *value > 1 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return nil, errors.New("style_weight and weirdness_constraint must be between 0 and 1")
		}
	}
	if r.Duration != nil && (*r.Duration < 10 || *r.Duration > 360) {
		return nil, errors.New("duration must be between 10 and 360 seconds")
	}
	switch strings.ToLower(r.VocalGender) {
	case "":
	case "male", "m":
		r.VocalGender = "Male"
	case "female", "f":
		r.VocalGender = "Female"
	default:
		return nil, errors.New("vocal_gender must be Male or Female")
	}
	if r.AudioFormat == "" {
		r.AudioFormat = "wav"
	}
	if r.AudioFormat != "mp3" && r.AudioFormat != "m4a" && r.AudioFormat != "wav" {
		return nil, errors.New("audio_format must be mp3, m4a or wav")
	}
	return &r, nil
}

func (r *SunoRequest) UpstreamBody() []byte {
	body, _ := json.Marshal(r)
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	payload["model"], payload["version"] = "suno", "v6"
	if !r.Custom {
		delete(payload, "auto_lyrics")
	}
	body, _ = json.Marshal(payload)
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
			return nil, errors.New("Suno requires unique instrumental/song prices with finite non-negative amounts")
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &sunoHTTPError{resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2*1024*1024 {
		return nil, errors.New("Suno response too large")
	}
	return data, nil
}

package service

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	MiMoSpeechProvider   = "mimo"
	MiMoTTSModel         = "mimo-v2.5-tts"
	MiMoVoiceDesignModel = "mimo-v2.5-tts-voicedesign"
	MiMoBaseURL          = "https://api.xiaomimimo.com/v1"
)

var MiMoSpeechVoices = []string{"mimo_default", "冰糖", "茉莉", "苏打", "白桦", "Mia", "Chloe", "Milo", "Dean"}

func IsMiMoSpeechModel(model string) bool {
	return model == MiMoTTSModel || model == MiMoVoiceDesignModel
}

func (a *Account) IsMiMoSpeech() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeAPIKey && a.Extra["speech_provider"] == MiMoSpeechProvider
}

// UsesAgentTokenPricing includes speech synthesis priced by upstream token usage.
// Suno retains its operation-specific per-request pricing.
func UsesAgentTokenPricing(mediaType, model string) bool {
	return mediaType == AgentMediaTypeText || (mediaType == AgentMediaTypeAudio && IsMiMoSpeechModel(model))
}

// Speech uses assistant messages as caller-supplied input. Present both the
// voice instructions and spoken text to audits that normally inspect a user turn.
// Only the audit copy changes; the upstream body keeps its original roles.
func MiMoSpeechAuditBody(body []byte, model string) []byte {
	if !IsMiMoSpeechModel(model) {
		return body
	}
	var parts []string
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if content := message.Get("content").String(); content != "" {
			parts = append(parts, content)
		}
	}
	updated, err := sjson.SetBytes(body, "messages", []map[string]string{{"role": "user", "content": strings.Join(parts, "\n\n")}})
	if err != nil {
		return body
	}
	return updated
}

func normalizeMiMoSpeechAccount(a *Account) error {
	provider, _ := a.Extra["speech_provider"].(string)
	speech, _ := a.Extra["speech_account"].(bool)
	if provider == "" && !speech {
		return nil
	}
	invalid := func(message string) error { return infraerrors.BadRequest("INVALID_SPEECH_ACCOUNT", message) }
	if provider != MiMoSpeechProvider || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return invalid("MiMo speech requires a dedicated OpenAI-compatible API key account")
	}
	if a.IsImageAccount() || a.GetExtraString("image_provider") != "" || a.GetExtraString("music_provider") != "" {
		return invalid("Speech accounts cannot also be image or music accounts")
	}
	if strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return invalid("MiMo API key is required")
	}
	base := strings.TrimRight(strings.TrimSpace(a.GetCredential("base_url")), "/")
	if base == "" {
		base = MiMoBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/v1") {
		return invalid("Speech Base URL must be an HTTPS origin, optionally ending in /v1")
	}
	a.Extra["speech_account"] = true
	a.Credentials["base_url"] = base
	a.Credentials["model_mapping"] = map[string]any{MiMoTTSModel: MiMoTTSModel, MiMoVoiceDesignModel: MiMoVoiceDesignModel}
	a.Extra["openai_responses_supported"] = false
	a.Credentials[openAIEndpointCapabilitiesCredentialKey] = []string{string(OpenAIEndpointCapabilityChatCompletions)}
	return nil
}

// ValidateMiMoSpeechRequest preserves MiMo's assistant-as-input contract. It is
// also called after account/channel mapping, before sending a paid request.
func ValidateMiMoSpeechRequest(body []byte, model string) error {
	if !IsMiMoSpeechModel(model) {
		return nil
	}
	root := gjson.ParseBytes(body)
	messages := root.Get("messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return errors.New("messages must be a non-empty array")
	}
	user, assistant := false, false
	for _, message := range messages.Array() {
		role, content := message.Get("role"), message.Get("content")
		if !message.IsObject() || (role.String() != "user" && role.String() != "assistant") || content.Type != gjson.String || strings.TrimSpace(content.String()) == "" {
			return errors.New("MiMo speech messages require role user or assistant and non-empty string content")
		}
		user = user || role.String() == "user"
		assistant = assistant || role.String() == "assistant"
	}
	audio := root.Get("audio")
	if audio.Exists() && !audio.IsObject() {
		return errors.New("audio must be an object")
	}
	optimize := audio.Get("optimize_text_preview")
	if optimize.Exists() && optimize.Type != gjson.True && optimize.Type != gjson.False {
		return errors.New("audio.optimize_text_preview must be a boolean")
	}
	if model == MiMoVoiceDesignModel {
		if !user {
			return errors.New("voice design requires a user message describing the voice")
		}
		if audio.Get("voice").Exists() {
			return errors.New("voice design does not accept audio.voice; describe the voice in a user message")
		}
	} else {
		if optimize.Exists() {
			return errors.New("audio.optimize_text_preview is only supported by voice design")
		}
		if voice := audio.Get("voice"); voice.Exists() {
			valid := false
			for _, id := range MiMoSpeechVoices {
				valid = valid || (voice.Type == gjson.String && voice.String() == id)
			}
			if !valid {
				return errors.New("audio.voice must be a supported MiMo preset voice")
			}
		}
	}
	if !assistant && !(model == MiMoVoiceDesignModel && optimize.Bool()) {
		return errors.New("speech text is required in an assistant message")
	}
	format := audio.Get("format")
	if format.Exists() {
		if format.Type != gjson.String {
			return errors.New("audio.format must be a string")
		}
		switch format.String() {
		case "wav", "mp3", "pcm", "pcm16":
		default:
			return fmt.Errorf("unsupported audio.format %q", format.String())
		}
		if root.Get("stream").Bool() && format.String() != "pcm" && format.String() != "pcm16" {
			return errors.New("streaming speech requires audio.format pcm16 or pcm")
		}
	}
	for _, key := range []string{"tools", "tool_choice", "input", "response_format"} {
		if root.Get(key).Exists() {
			return fmt.Errorf("MiMo speech does not support %s", key)
		}
	}
	return nil
}

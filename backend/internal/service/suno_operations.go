package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

//go:embed suno_operations.json
var sunoOperationJSON []byte

type SunoFieldSpec struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}
type SunoOperationSpec struct {
	Path   string                   `json:"path"`
	Doc    string                   `json:"doc"`
	Fields map[string]SunoFieldSpec `json:"fields"`
}

var sunoOperations = func() map[string]SunoOperationSpec {
	var specs map[string]SunoOperationSpec
	if err := json.Unmarshal(sunoOperationJSON, &specs); err != nil {
		panic(err)
	}
	return specs
}()

func SunoOperations() map[string]SunoOperationSpec {
	var specs map[string]SunoOperationSpec
	_ = json.Unmarshal(sunoOperationJSON, &specs)
	return specs
}

// Billing keys are downstream identifiers, independent of provider endpoint names.
func sunoBillingOperation(operation string) string {
	switch operation {
	case "uploadCover":
		return "upload_cover"
	case "createModel":
		return "create_model"
	case "uploadTask":
		return "upload"
	default:
		return operation
	}
}

func SunoPriceTiers() []string {
	tiers := []string{"instrumental", "song"}
	names := []string{}
	for name := range sunoOperations {
		// generation folds into song/instrumental; uploadTask is bundled into
		// the prices of sample/uploadCover/createModel and never billed alone.
		if name != "generation" && name != "uploadTask" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	tiers = append(tiers, names...)
	base := append([]string(nil), tiers...)
	for _, name := range base {
		op := name
		if op == "song" || op == "instrumental" {
			op = "generation"
		}
		spec := sunoOperations[op]
		if _, ok := spec.Fields["custom_model_id"]; ok {
			tiers = append(tiers, name+":custom")
		}
	}
	for i, tier := range tiers {
		parts := strings.SplitN(tier, ":", 2)
		tiers[i] = sunoBillingOperation(parts[0])
		if len(parts) == 2 {
			tiers[i] += ":" + parts[1]
		}
	}
	return tiers
}
func (r *SunoRequest) Endpoint() string {
	op := r.Operation
	if op == "" {
		op = "generation"
	}
	return sunoOperations[op].Path
}
func (r *SunoRequest) Fields() map[string]any {
	if r.Payload != nil {
		data, _ := json.Marshal(r.Payload)
		var f map[string]any
		_ = json.Unmarshal(data, &f)
		return f
	}
	data, _ := json.Marshal(r)
	var f map[string]any
	_ = json.Unmarshal(data, &f)
	delete(f, "operation")
	delete(f, "payload")
	if r.Version == "" && r.CustomModelID == "" {
		f["version"] = "v6"
	}
	return f
}
func (r *SunoRequest) AuditText() string {
	f := r.Fields()
	parts := []string{}
	for _, name := range []string{"prompt", "gpt_description", "style", "tags", "title", "negative_tags", "infill_lyrics", "name", "describe", "styles"} {
		if value, ok := f[name].(string); ok {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}
func (r *SunoRequest) PriceMultiplier() float64 {
	if r.Fields()["max_mode"] == true {
		return 2
	}
	return 1
}
func ParseSunoOperation(operation string, body []byte) (*SunoRequest, error) {
	if operation == "" {
		operation = "generation"
	}
	legacyWAV := operation == "wav"
	if legacyWAV {
		operation = "download"
	}
	spec, ok := sunoOperations[operation]
	if !ok {
		return nil, fmt.Errorf("unsupported Suno operation %q", operation)
	}
	var f map[string]any
	if json.Unmarshal(body, &f) != nil || f == nil {
		return nil, fmt.Errorf("request must be a JSON object")
	}
	if legacyWAV {
		f["formats"] = []any{"wav"}
	}
	for name, value := range f {
		field, exists := spec.Fields[name]
		if name == "model" {
			field.Type = "string"
			exists = true
		}
		if !exists {
			return nil, fmt.Errorf("unsupported field %q for %s", name, operation)
		}
		if value == nil {
			return nil, fmt.Errorf("%s must not be null", name)
		}
		valid := false
		switch field.Type {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number", "integer":
			n, ok := value.(float64)
			valid = ok && !math.IsInf(n, 0) && !math.IsNaN(n) && (field.Type != "integer" || n == math.Trunc(n))
		case "string[]", "integer[]", "array":
			arr, ok := value.([]any)
			valid = ok
			for _, v := range arr {
				if field.Type == "integer[]" {
					n, ok := v.(float64)
					valid = valid && ok && n >= 1 && n == math.Trunc(n)
				} else {
					_, ok := v.(string)
					valid = valid && ok
				}
			}
		}
		if !valid {
			return nil, fmt.Errorf("%s must be %s", name, field.Type)
		}
	}
	for name, field := range spec.Fields {
		value, exists := f[name]
		if field.Required && (!exists || value == "") {
			return nil, fmt.Errorf("%s is required", name)
		}
		if field.Required {
			if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("%s is required", name)
			}
		}
	}
	if m, ok := f["model"]; ok && m != SunoModel {
		return nil, fmt.Errorf("only suno-v6 is supported")
	}
	if _, has := f["custom_model_id"]; has {
		if _, ok := f["version"]; ok {
			return nil, fmt.Errorf("custom_model_id and version are mutually exclusive")
		}
		if _, ok := f["persona_id"]; ok {
			return nil, fmt.Errorf("custom_model_id and persona_id are mutually exclusive")
		}
		modelID, ok := f["custom_model_id"].(string)
		if !ok {
			return nil, fmt.Errorf("custom_model_id must be a UUID")
		}
		if _, err := uuid.Parse(modelID); err != nil {
			return nil, fmt.Errorf("custom_model_id must be a UUID")
		}
	} else if _, ok := spec.Fields["version"]; ok {
		if _, set := f["version"]; !set {
			f["version"] = "v6"
		}
	}
	enums := map[string]string{"version": "v6", "variety": "off|normal|high|extra|max", "audio_format": "mp3|m4a|wav", "variation_category": "subtle|normal|high", "lyrics_model": "classic|remi", "type": "one-shot|loop"}
	for field, choices := range enums {
		if v, ok := f[field].(string); ok && !strings.Contains("|"+choices+"|", "|"+v+"|") {
			return nil, fmt.Errorf("invalid %s", field)
		}
	}
	for _, name := range []string{"style_weight", "weirdness", "weirdness_constraint", "audio_weight"} {
		if v, ok := f[name].(float64); ok && (v < 0 || v > 1) {
			return nil, fmt.Errorf("%s must be between 0 and 1", name)
		}
	}
	if _, ok := f["weirdness"]; ok {
		if _, also := f["weirdness_constraint"]; also {
			return nil, fmt.Errorf("send only one weirdness field")
		}
	}
	if v, ok := f["vocal_gender"].(string); ok {
		switch strings.ToLower(v) {
		case "male", "m":
			f["vocal_gender"] = "Male"
		case "female", "f":
			f["vocal_gender"] = "Female"
		default:
			return nil, fmt.Errorf("vocal_gender must be Male or Female")
		}
	}
	custom, _ := f["custom"].(bool)
	if _, supported := spec.Fields["custom"]; supported {
		if _, set := f["custom"]; !set {
			if operation == "extend" {
				custom = true
			} else if operation != "generation" {
				custom = nonemptySuno(f, "prompt") || (!nonemptySuno(f, "gpt_description") && (nonemptySuno(f, "tags") || nonemptySuno(f, "title")))
			}
			f["custom"] = custom
		}
		if operation == "generation" {
			// Preserve the original downstream defaults; callers can now explicitly choose either vocal mode.
			if _, set := f["instrumental"]; !set {
				f["instrumental"] = !custom
			}
			if _, set := f["audio_format"]; !set {
				f["audio_format"] = "wav"
			}
			if (!custom || f["instrumental"] != true) && !nonemptySuno(f, "prompt") {
				return nil, fmt.Errorf("prompt is required")
			}
		} else {
			if !custom && operation != "extend" && !nonemptySuno(f, "gpt_description") {
				return nil, fmt.Errorf("gpt_description is required in inspiration mode")
			}
			if custom && (operation == "sample" || operation == "mashup" || operation == "uploadCover") && f["instrumental"] != true && !nonemptySuno(f, "prompt") {
				return nil, fmt.Errorf("prompt is required for vocals")
			}
		}
		if !custom && (f["max_mode"] == true || f["duration"] != nil || f["duration_s"] != nil) {
			return nil, fmt.Errorf("max and target duration require custom=true")
		}
	}
	limits := map[string]int{"prompt": 5000, "gpt_description": 3000, "style": 1000, "tags": 1000, "title": 80, "infill_lyrics": 5000}
	if operation == "generation" && !custom {
		limits["prompt"] = 3000
	}
	for field, limit := range limits {
		if v, ok := f[field].(string); ok && utf8.RuneCountInString(v) > limit {
			return nil, fmt.Errorf("%s exceeds %d Unicode characters", field, limit)
		}
	}
	for _, name := range []string{"duration", "duration_s"} {
		if v, ok := f[name].(float64); ok {
			if operation == "fadeIn" || operation == "fadeOut" {
				if v <= 0 {
					return nil, fmt.Errorf("fade duration must be positive")
				}
			} else if v < 10 || v > 360 || v != math.Trunc(v) {
				return nil, fmt.Errorf("target duration must be an integer between 10 and 360")
			}
		}
	}
	if v, ok := f["audio_index"].(float64); ok && v < 1 {
		return nil, fmt.Errorf("audio_index must be positive")
	}
	if _, ok := spec.Fields["task_id"]; ok {
		if _, set := f["audio_index"]; !set {
			f["audio_index"] = float64(1)
		}
	}
	if v, ok := f["continue_at"].(float64); ok && v < 1 {
		return nil, fmt.Errorf("continue_at must be at least 1")
	}
	if v, ok := f["speed"].(float64); ok && (v < 0.25 || v > 4) {
		return nil, fmt.Errorf("speed must be between 0.25 and 4")
	}
	if v, ok := f["bpm"].(float64); ok && (v < 1 || v > 300) {
		return nil, fmt.Errorf("bpm must be between 1 and 300")
	}
	if v, ok := f["key"].(string); ok {
		key := strings.TrimSuffix(v, "m")
		if !strings.Contains("|C|C#|D|D#|E|F|F#|G|G#|A|A#|B|", "|"+key+"|") || key == "" {
			return nil, fmt.Errorf("invalid musical key")
		}
	}
	for _, pair := range [][2]string{{"start_s", "end_s"}, {"vocal_start_s", "vocal_end_s"}} {
		start, sok := f[pair[0]].(float64)
		end, eok := f[pair[1]].(float64)
		if (sok && start < 0) || (eok && end <= start) {
			return nil, fmt.Errorf("invalid %s/%s range", pair[0], pair[1])
		}
	}
	if operation == "mashup" {
		ids, ok := f["task_ids"].([]any)
		if !ok || len(ids) != 2 {
			return nil, fmt.Errorf("task_ids requires exactly two tasks")
		}
		if indexes, ok := f["audio_indexes"].([]any); ok && len(indexes) != 2 {
			return nil, fmt.Errorf("audio_indexes requires exactly two indexes")
		}
	}
	for _, field := range []string{"audio_url", "audioFilePath"} {
		if v, ok := f[field].(string); ok && !sunoPublicURL(v) {
			return nil, fmt.Errorf("%s must be an HTTP(S) audio URL", field)
		}
	}
	if values, ok := f["audio_urls"].([]any); ok {
		min, max := 1, 4
		if operation == "createModel" {
			min, max = 6, 24
		}
		if len(values) < min || len(values) > max {
			return nil, fmt.Errorf("audio_urls requires %d-%d URLs", min, max)
		}
		for _, v := range values {
			audioURL, ok := v.(string)
			if !ok || !sunoPublicURL(audioURL) {
				return nil, fmt.Errorf("invalid audio URL")
			}
		}
	}
	if operation == "download" {
		_, plural := f["formats"]
		_, single := f["format"]
		if plural == single {
			return nil, fmt.Errorf("provide exactly one of format or formats")
		}
		values, _ := f["formats"].([]any)
		if single {
			values = []any{f["format"]}
			delete(f, "format")
		}
		out := []any{}
		seen := map[string]bool{}
		for _, v := range values {
			format, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("invalid download format")
			}
			value := strings.ToLower(format)
			if value != "mp3" && value != "wav" && value != "m4a" {
				return nil, fmt.Errorf("invalid download format")
			}
			if !seen[value] {
				out = append(out, value)
				seen[value] = true
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("formats must not be empty")
		}
		f["formats"] = out
	}
	data, _ := json.Marshal(f)
	var r SunoRequest
	_ = json.Unmarshal(data, &r)
	r.Model = SunoModel
	if operation == "generation" && r.Version == "v6" {
		r.Version = ""
	}
	if operation != "generation" {
		r.Operation = operation
		r.Payload = f
	}
	return &r, nil
}
func nonemptySuno(f map[string]any, name string) bool {
	s, _ := f[name].(string)
	return strings.TrimSpace(s) != ""
}
func sunoPublicURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

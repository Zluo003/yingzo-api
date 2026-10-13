package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Preserve all provider result fields and original music indexes, including zero weights.
func validateSunoResult(operation string, raw json.RawMessage) error {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || len(result) == 0 {
		return fmt.Errorf("empty result")
	}
	switch operation {
	case "", "generation", "extend", "uploadExtend", "coverSong", "uploadCover", "addVocals", "addInstrumental", "addStem", "sample", "mashup", "inspo", "sounds", "remaster", "replaceMusic", "crop", "removeSection", "adjustSpeed", "fadeIn", "fadeOut", "concat":
		tracks, _ := result["music"].([]any)
		for _, item := range tracks {
			track, _ := item.(map[string]any)
			if url, _ := track["audio_url"].(string); url != "" {
				return nil
			}
		}
		return fmt.Errorf("no playable audio")
	case "uploadTask":
		tracks, _ := result["music"].([]any)
		if len(tracks) == 0 {
			return fmt.Errorf("no uploaded track")
		}
	case "createModel":
		if !nonemptySuno(result, "model_id") {
			return fmt.Errorf("missing model_id")
		}
	case "persona":
		if !nonemptySuno(result, "persona_id") {
			return fmt.Errorf("missing persona_id")
		}
	}
	return nil
}

func transformSunoResult(raw json.RawMessage, transform func(path, kind, value string) (string, error)) (json.RawMessage, error) {
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return nil, fmt.Errorf("invalid result")
	}
	var walk func(any, string, string) error
	walk = func(node any, path, parent string) error {
		switch v := node.(type) {
		case map[string]any:
			for key, value := range v {
				child := path + "/" + key
				if str, ok := value.(string); ok && (strings.HasPrefix(str, "https://") || strings.HasPrefix(str, "http://")) {
					lower := strings.ToLower(key)
					kind := ""
					switch {
					case strings.Contains(lower, "image") || strings.Contains(lower, "cover"):
						kind = "image"
					case strings.Contains(lower, "video"):
						kind = "video"
					case strings.Contains(lower, "audio") || lower == "wavurl" || lower == "mp3url" || lower == "m4aurl":
						kind = "audio"
					case lower == "url" && strings.Contains(parent, "videos"):
						kind = "video"
					case lower == "url" && (strings.Contains(parent, "files") || strings.Contains(parent, "stems") || strings.Contains(parent, "music")):
						kind = "audio"
					}
					if kind != "" {
						replacement, err := transform(child, kind, str)
						if err != nil {
							if kind == "image" {
								v[key] = ""
								continue
							}
							return err
						}
						v[key] = replacement
					}
				} else if err := walk(value, child, path); err != nil {
					return err
				}
			}
		case []any:
			for i, item := range v {
				if err := walk(item, fmt.Sprintf("%s/%d", path, i), path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(result, "", ""); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

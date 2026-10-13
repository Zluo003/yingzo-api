package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type musicResourceFinder interface {
	FindResource(context.Context, MusicTaskOwner, string, string) (*DurableMusicTask, error)
}

// Resolve only this key's completed tasks, and keep every dependency on one account.
// The public request is fingerprinted before this translation takes place.
func (s *MusicTaskService) resolveSunoSources(ctx context.Context, owner MusicTaskOwner, request *SunoRequest) (*SunoRequest, int64, error) {
	f := request.Fields()
	account := int64(0)
	bind := func(t *DurableMusicTask) error {
		if t.Status != MusicTaskStatusCompleted || t.Quote.UpstreamID == "" || t.Quote.AccountID == 0 {
			return fmt.Errorf("source task must be completed")
		}
		if time.Now().Unix() >= t.ExpiresAt {
			return ErrMusicTaskExpired
		}
		if account != 0 && account != t.Quote.AccountID {
			return fmt.Errorf("all sources must belong to the same upstream account")
		}
		account = t.Quote.AccountID
		return nil
	}
	resolve := func(id string, index int) (string, error) {
		t, err := s.ledger.Find(ctx, owner, id, false)
		if err != nil {
			return "", err
		}
		if err = bind(t); err != nil {
			return "", err
		}
		// Only array position is used for addressing; never compact successful tracks.
		var tracks struct{ Music []map[string]any }
		if json.Unmarshal(t.Result, &tracks) != nil || index < 1 || index > len(tracks.Music) {
			return "", fmt.Errorf("source audio_index is out of range")
		}
		duration, _ := tracks.Music[index-1]["duration"].(float64)
		for _, field := range []string{"end_s", "vocal_end_s", "continue_at"} {
			if v, ok := f[field].(float64); ok && duration > 0 && (v > duration || (field == "continue_at" && v >= duration)) {
				return "", fmt.Errorf("%s exceeds source duration", field)
			}
		}
		if request.Operation == "fadeIn" || request.Operation == "fadeOut" {
			if v, _ := f["duration_s"].(float64); duration > 0 && v > duration {
				return "", fmt.Errorf("fade duration exceeds source duration")
			}
		}
		switch request.Operation {
		case "addVocals", "addInstrumental", "sample":
			if t.Operation != "uploadTask" {
				return "", fmt.Errorf("source must be an uploadTask result")
			}
		case "concat":
			if t.Operation != "extend" && t.Operation != "uploadExtend" {
				return "", fmt.Errorf("source must be an extension result")
			}
		}
		return t.Quote.UpstreamID, nil
	}
	if id, ok := f["task_id"].(string); ok {
		index, _ := f["audio_index"].(float64)
		if index == 0 {
			index = 1
		}
		upstream, err := resolve(id, int(index))
		if err != nil {
			return nil, 0, sunoSourceError(err)
		}
		f["task_id"] = upstream
	}
	if ids, ok := f["task_ids"].([]any); ok {
		indexes, _ := f["audio_indexes"].([]any)
		for i, id := range ids {
			index := 1
			if len(indexes) > i {
				index = int(indexes[i].(float64))
			}
			upstream, err := resolve(id.(string), index)
			if err != nil {
				return nil, 0, sunoSourceError(err)
			}
			ids[i] = upstream
		}
	}
	for field, resultField := range map[string]string{"custom_model_id": "model_id", "persona_id": "persona_id"} {
		if id, ok := f[field].(string); ok && id != "" {
			finder, ok := s.ledger.(musicResourceFinder)
			if !ok {
				return nil, 0, ErrMusicTaskUnavailable
			}
			t, err := finder.FindResource(ctx, owner, resultField, id)
			if err != nil {
				return nil, 0, err
			}
			if err = bind(t); err != nil {
				return nil, 0, sunoSourceError(err)
			}
		}
	}
	copy := *request
	copy.Payload = f
	return &copy, account, nil
}
func sunoSourceError(err error) error {
	if err == ErrMusicTaskNotFound || err == ErrMusicTaskExpired {
		return err
	}
	return infraerrors.BadRequest("invalid_source", err.Error())
}

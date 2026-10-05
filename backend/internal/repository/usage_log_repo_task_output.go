package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type usageOutputCandidate struct {
	log     *service.UsageLog
	assetID uuid.UUID
	output  service.UsageTaskOutput
}

// LoadTaskOutputs uses two bounded queries per page, never one query per row or
// a remote probe. Both the task and asset must belong to the usage record's key.
func (r *usageLogRepository) LoadTaskOutputs(ctx context.Context, userID int64, logs []service.UsageLog) error {
	args := []any{userID}
	var holders []string
	byID := make(map[int64]*service.UsageLog)
	for i := range logs {
		log := &logs[i]
		log.TaskOutputs = nil
		if log.UserID != userID || log.ActualCost < 0 ||
			(log.FundsEvent != nil && (*log.FundsEvent == "failure_refund" || *log.FundsEvent == "settlement_refund")) ||
			strings.HasSuffix(log.RequestID, ":refund") || strings.HasSuffix(log.RequestID, ":failure_refund") {
			continue
		}
		if log.ImageTaskID == nil && log.MusicTaskID == nil && log.VideoTaskID == nil &&
			!strings.HasPrefix(log.RequestID, "image:") && !strings.HasPrefix(log.RequestID, "music:") && !strings.HasPrefix(log.RequestID, "video:") {
			continue
		}
		args = append(args, log.ID)
		holders = append(holders, fmt.Sprintf("$%d", len(args)))
		byID[log.ID] = log
	}
	if len(holders) == 0 {
		return nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT ul.id, it.record->'result', mt.record->'result', COALESCE(vt.result_video_url, '')
 FROM usage_logs ul
 LEFT JOIN image_tasks it ON it.id = COALESCE(NULLIF(ul.image_task_id, ''), CASE WHEN ul.request_id LIKE 'image:%' THEN split_part(ul.request_id, ':', 2) END)
 AND it.user_id = ul.user_id AND it.api_key_id = ul.api_key_id AND it.status = 'completed'
 LEFT JOIN music_tasks mt ON mt.id = COALESCE(NULLIF(ul.music_task_id, ''), CASE WHEN ul.request_id LIKE 'music:%' THEN split_part(ul.request_id, ':', 2) END)
 AND mt.user_id = ul.user_id AND mt.api_key_id = ul.api_key_id AND mt.status = 'completed'
 LEFT JOIN video_tasks vt ON vt.public_id = COALESCE(NULLIF(ul.video_task_id, ''), CASE WHEN ul.request_id LIKE 'video:%' THEN split_part(ul.request_id, ':', 2) END)
 AND vt.user_id = ul.user_id AND vt.api_key_id = ul.api_key_id AND vt.status = 'completed'
 WHERE ul.user_id = $1 AND ul.id IN (`+strings.Join(holders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	var candidates []usageOutputCandidate
	for rows.Next() {
		var id int64
		var imageResult, musicResult []byte
		var videoURL string
		if err := rows.Scan(&id, &imageResult, &musicResult, &videoURL); err != nil {
			_ = rows.Close()
			return err
		}
		if log := byID[id]; log != nil {
			candidates = append(candidates, usageOutputCandidates(log, imageResult, musicResult, videoURL)...)
		}
	}
	readErr := rows.Err()
	closeErr := rows.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(candidates) == 0 {
		return nil
	}
	args = nil
	holders = nil
	for i, candidate := range candidates {
		n := len(args)
		holders = append(holders, fmt.Sprintf("($%d::int, $%d::uuid, $%d::bigint, $%d::bigint, $%d::text)", n+1, n+2, n+3, n+4, n+5))
		args = append(args, i, candidate.assetID.String(), candidate.log.UserID, candidate.log.APIKeyID, candidate.output.MediaType)
	}
	assets, err := r.sql.QueryContext(ctx, `SELECT requested.ordinal, GREATEST(a.expires_at, a.lease_until)
 FROM (VALUES `+strings.Join(holders, ",")+`) AS requested(ordinal, asset_id, user_id, api_key_id, media_type)
 JOIN temporary_assets a ON a.id = requested.asset_id AND a.user_id = requested.user_id AND a.api_key_id = requested.api_key_id
 AND a.media_type = requested.media_type AND a.purpose = 'generated'
 WHERE a.deleted_at IS NULL AND GREATEST(a.expires_at, a.lease_until) > NOW()
 ORDER BY requested.ordinal`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = assets.Close() }()
	for assets.Next() {
		var index int
		var expires time.Time
		if err := assets.Scan(&index, &expires); err != nil {
			return err
		}
		candidate := candidates[index]
		candidate.output.ExpiresAt = expires
		candidate.log.TaskOutputs = append(candidate.log.TaskOutputs, candidate.output)
	}
	return assets.Err()
}

func usageOutputCandidates(log *service.UsageLog, imageResult, musicResult []byte, videoURL string) []usageOutputCandidate {
	var candidates []usageOutputCandidate
	seen := make(map[uuid.UUID]bool)
	add := func(rawURL, media, title string) {
		ref, ok := service.ParseTemporaryAssetRef(rawURL, "", "")
		if !ok || ref.ID == uuid.Nil || seen[ref.ID] {
			return
		}
		seen[ref.ID] = true
		candidates = append(candidates, usageOutputCandidate{log: log, assetID: ref.ID, output: service.UsageTaskOutput{
			URL: rawURL, MediaType: media, Title: title,
		}})
	}
	var images struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(imageResult, &images) == nil {
		for _, item := range images.Data {
			add(item.URL, "image", "")
		}
	}
	var music service.MusicResult
	if json.Unmarshal(musicResult, &music) == nil {
		for _, track := range music.Music {
			add(track.AudioURL, "audio", track.Title)
		}
	}
	add(videoURL, "video", "")
	return candidates
}

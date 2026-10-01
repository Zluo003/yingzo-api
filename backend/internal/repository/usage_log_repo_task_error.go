package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Load diagnostics in one query per page, only for failure refunds. The owner
// and API-key joins prevent a malformed task reference crossing account bounds.
func (r *usageLogRepository) hydrateUsageTaskErrors(ctx context.Context, logs []service.UsageLog) (err error) {
	if err := r.hydrateMusicUsage(ctx, logs); err != nil {
		return err
	}
	var args []any
	var placeholders []string
	byID := make(map[int64]*service.UsageLog)
	for i := range logs {
		log := &logs[i]
		if strings.HasPrefix(log.RequestID, "music:") {
			continue
		}
		event := ""
		if log.FundsEvent != nil {
			event = *log.FundsEvent
		}
		imageRefund := event == "failure_refund" || strings.HasSuffix(log.RequestID, ":failure_refund") ||
			(log.ImageTaskID != nil && log.ActualCost < 0 && event != "settlement_refund" && log.ImageTaskStatus != nil && *log.ImageTaskStatus == "failed")
		videoRefund := strings.HasPrefix(log.RequestID, "video:") && (strings.HasSuffix(log.RequestID, ":refund") || log.ActualCost < 0)
		if !imageRefund && !videoRefund {
			continue
		}
		args = append(args, log.ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		byID[log.ID] = log
	}
	if len(args) == 0 {
		return nil
	}
	query := `SELECT ul.id,
 COALESCE(it.record->'task_error', vt.error_json->'task_error'),
 COALESCE(it.record->'error', vt.error_json)
 FROM usage_logs ul
 LEFT JOIN image_tasks it ON it.id = COALESCE(NULLIF(ul.image_task_id, ''), CASE WHEN ul.request_id LIKE 'image:%' THEN split_part(ul.request_id, ':', 2) END)
 AND it.user_id = ul.user_id AND it.api_key_id = ul.api_key_id
 LEFT JOIN video_tasks vt ON vt.public_id = COALESCE(NULLIF(ul.video_task_id, ''), CASE WHEN ul.request_id LIKE 'video:%' THEN split_part(ul.request_id, ':', 2) END)
 AND vt.user_id = ul.user_id AND vt.api_key_id = ul.api_key_id
 WHERE ul.id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		var id int64
		var diagnostic, legacy []byte
		if err := rows.Scan(&id, &diagnostic, &legacy); err != nil {
			return err
		}
		log := byID[id]
		if log == nil {
			continue
		}
		var taskError service.UsageTaskError
		if json.Unmarshal(diagnostic, &taskError) == nil && (taskError.Code != 0 || taskError.Message != "") {
			log.TaskError = service.NewUsageTaskError(taskError.Code, nil, taskError.Message)
		} else {
			log.TaskError = service.NewUsageTaskError(0, legacy, "")
			if log.TaskError != nil && log.TaskError.Code == 0 {
				log.TaskError.Message = "这条历史记录未保存上游原始错误，已保存的错误信息：\n" + log.TaskError.Message
			}
		}
	}
	return rows.Err()
}

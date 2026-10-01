package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Music metadata is hydrated only for music events, preserving legacy query contracts.
func (r *usageLogRepository) hydrateMusicUsage(ctx context.Context, logs []service.UsageLog) error {
	args := []any{}
	holders := []string{}
	byID := map[int64]*service.UsageLog{}
	for i := range logs {
		l := &logs[i]
		if strings.HasPrefix(l.RequestID, "music:") {
			args = append(args, l.ID)
			holders = append(holders, fmt.Sprintf("$%d", len(args)))
			byID[l.ID] = l
		}
	}
	if len(args) == 0 {
		return nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT ul.id,ul.music_task_id,ul.music_task_status,ul.music_mode,mt.record->'task_error' FROM usage_logs ul JOIN music_tasks mt ON mt.id=ul.music_task_id AND mt.user_id=ul.user_id AND mt.api_key_id=ul.api_key_id WHERE ul.id IN (`+strings.Join(holders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var task, status, mode string
		var diagnostic []byte
		if err = rows.Scan(&id, &task, &status, &mode, &diagnostic); err != nil {
			return err
		}
		if l := byID[id]; l != nil {
			l.MusicTaskID = &task
			l.MusicTaskStatus = &status
			l.MusicMode = &mode
			if l.FundsEvent != nil && *l.FundsEvent == "failure_refund" {
				var taskErr service.UsageTaskError
				if json.Unmarshal(diagnostic, &taskErr) == nil {
					l.TaskError = &taskErr
				}
			}
		}
	}
	return rows.Err()
}

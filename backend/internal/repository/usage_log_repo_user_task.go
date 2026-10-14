package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const userImageTaskKey = `COALESCE(NULLIF(ul.image_task_id, ''), CASE WHEN ul.request_id LIKE 'image:%' THEN NULLIF(split_part(ul.request_id, ':', 2), '') END)`

// Collapse before filtering/paging. Financial deltas are included even if they
// settle outside the requested time range. Never modify the underlying audit
// rows: their sum must continue to equal the real balance/quota movement.
func userTaskUsageSource(userPlaceholder string) string {
	columns := strings.Split(usageLogSelectColumns, ", ")
	for i, col := range columns {
		switch col {
		case "actual_cost":
			columns[i] = "logical.net_cost AS actual_cost"
		case "funds_event":
			columns[i] = "CASE WHEN logical.image_task_status='failed' AND logical.net_cost=0 THEN 'failure_refund' ELSE logical.funds_event END AS funds_event"
		case "image_count":
			columns[i] = "CASE WHEN it.status='completed' AND it.quote->>'Count'='1' THEN 1 ELSE logical.image_count END AS image_count"
		default:
			columns[i] = "logical." + col
		}
	}
	return fmt.Sprintf(`(SELECT %s, logical.task_key, logical.music_task_id
 FROM (
 SELECT ul.*, %s AS task_key,
 SUM(actual_cost) OVER task_events AS net_cost,
 ROW_NUMBER() OVER (PARTITION BY user_id, api_key_id, COALESCE(%s, 'usage:' || id::text)
 ORDER BY CASE WHEN funds_event='precharge' OR request_id LIKE 'image:%%:precharge' THEN 0 ELSE 1 END, id) AS task_row
 FROM usage_logs ul WHERE user_id=%s
 WINDOW task_events AS (PARTITION BY user_id, api_key_id, COALESCE(%s, 'usage:' || id::text))
 ) logical
 LEFT JOIN image_tasks it ON it.id=logical.task_key AND it.user_id=logical.user_id AND it.api_key_id=logical.api_key_id
 WHERE logical.task_row=1) AS usage_logs`, strings.Join(columns, ", "), userImageTaskKey, userImageTaskKey, userPlaceholder, userImageTaskKey)
}

func (r *usageLogRepository) listUserTaskUsage(ctx context.Context, where string, args []any, params pagination.PaginationParams) ([]service.UsageLog, *pagination.PaginationResult, error) {
	// ListWithFilters always binds the authenticated UserID first.
	source := userTaskUsageSource("$1")
	var total int64
	if err := scanSingleRow(ctx, r.sql, "SELECT COUNT(*) FROM "+source+" "+where, args, &total); err != nil {
		return nil, nil, err
	}
	query := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s LIMIT $%d OFFSET $%d", usageLogSelectColumns, source, where, usageLogOrderBy(params), len(args)+1, len(args)+2)
	listArgs := append(append([]any{}, args...), params.Limit(), params.Offset())
	logs, err := r.queryUsageLogs(ctx, query, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	return logs, paginationResultFromTotal(total, params), nil
}

// An old link to a settlement row resolves to the same canonical task as its
// precharge link. Ownership is checked by the service before this projection.
func (r *usageLogRepository) ResolveUserTaskUsage(ctx context.Context, log *service.UsageLog) (*service.UsageLog, error) {
	taskID := ""
	if log.ImageTaskID != nil {
		taskID = *log.ImageTaskID
	}
	if taskID == "" && strings.HasPrefix(log.RequestID, "image:") {
		taskID = strings.Split(log.RequestID, ":")[1]
	}
	if taskID == "" {
		return log, nil
	}
	query := "SELECT " + usageLogSelectColumns + " FROM " + userTaskUsageSource("$1") + " WHERE task_key=$2 AND api_key_id=$3"
	logs, err := r.queryUsageLogs(ctx, query, log.UserID, taskID, log.APIKeyID)
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return nil, service.ErrUsageLogNotFound
	}
	if err := r.hydrateUsageTaskErrors(ctx, logs); err != nil {
		return nil, err
	}
	return &logs[0], nil
}

package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 编译期断言：VideoService.recordCompletedTask 通过类型断言发现该能力，
// 断言失败时会静默跳过（这正是 video_result_url 长期为空的原因）。
var _ service.VideoUsageResultUpdater = (*usageLogRepository)(nil)

// UpdateVideoResult 在视频任务完成后把结果与视频归属字段补写回 usage_logs。
//
// 为什么必须在这里补：usage_logs 的共享插入路径（usage_log_repo_insert.go）
// 只写入 video_count / video_resolution / video_duration_seconds 三列，
// video_task_id、video_reference_duration_seconds、video_billable_seconds、
// video_result_url 从未落库——任务创建时结果地址还不存在，而任务完成时
// VideoService 已经拿到完整 task，是最合适的补齐时机。
//
// 用 (request_id, api_key_id) 定位，与 usage_logs 的唯一约束一致；
// 记录不存在（例如记账被跳过）时静默返回，不视为错误。
func (r *usageLogRepository) UpdateVideoResult(
	ctx context.Context,
	requestID string,
	apiKeyID int64,
	update service.VideoUsageResultUpdate,
) error {
	if r == nil || r.sql == nil {
		return nil
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || apiKeyID <= 0 {
		return nil
	}

	const query = `
		UPDATE usage_logs
		SET video_result_url = NULLIF($3, ''),
		    video_task_id = NULLIF($4, ''),
		    video_resolution = COALESCE(NULLIF($5, ''), video_resolution),
		    video_duration_seconds = CASE WHEN $6 > 0 THEN $6 ELSE video_duration_seconds END,
		    video_reference_duration_seconds = CASE WHEN $7 > 0 THEN $7 ELSE video_reference_duration_seconds END,
		    video_billable_seconds = CASE WHEN $8 > 0 THEN $8 ELSE video_billable_seconds END,
		    duration_ms = COALESCE($9, duration_ms),
		    inbound_endpoint = COALESCE(NULLIF($10, ''), inbound_endpoint),
		    upstream_endpoint = COALESCE(NULLIF($11, ''), upstream_endpoint),
		    account_id = COALESCE($12, account_id),
		    upstream_model = COALESCE(NULLIF($13, ''), upstream_model)
		WHERE request_id = $1 AND api_key_id = $2
	`

	var durationMs sql.NullInt64
	if update.DurationMs != nil {
		durationMs = sql.NullInt64{Int64: int64(*update.DurationMs), Valid: true}
	}
	var accountID sql.NullInt64
	if update.AccountID != nil {
		accountID = sql.NullInt64{Int64: *update.AccountID, Valid: true}
	}

	_, err := r.sql.ExecContext(
		ctx,
		query,
		requestID,
		apiKeyID,
		update.ResultURL,
		update.VideoTaskID,
		update.VideoResolution,
		update.VideoDurationSeconds,
		update.ReferenceDurationSeconds,
		update.BillableSeconds,
		durationMs,
		update.InboundEndpoint,
		update.UpstreamEndpoint,
		accountID,
		update.UpstreamModel,
	)
	return err
}

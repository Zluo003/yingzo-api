//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 视频完成后必须把结果地址与视频归属字段补写回 usage_logs：共享插入路径不写
// 这些列，测试桩之外的实现曾长期缺失，导致管理端视频列整片空白。
func TestUpdateVideoResultWritesVideoAttributionColumns(t *testing.T) {
	var captured []string
	db, mock := newSQLCapturingMock(t, &captured)
	mock.ExpectExec("UPDATE usage_logs").WillReturnResult(sqlmock.NewResult(0, 1))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	durationMs := 125000
	err := repo.UpdateVideoResult(context.Background(), "video:video_abc", 7, service.VideoUsageResultUpdate{
		ResultURL:                "http://local/media/x/asset.mp4",
		DurationMs:               &durationMs,
		InboundEndpoint:          "/v1/videos",
		UpstreamEndpoint:         "/v1/videos",
		VideoTaskID:              "video_abc",
		VideoResolution:          "720p",
		VideoDurationSeconds:     5,
		ReferenceDurationSeconds: 0,
		BillableSeconds:          5,
	})
	require.NoError(t, err)
	require.Len(t, captured, 1)

	sql := captured[0]
	for _, column := range []string{
		"video_result_url",
		"video_task_id",
		"video_resolution",
		"video_duration_seconds",
		"video_reference_duration_seconds",
		"video_billable_seconds",
	} {
		require.Contains(t, sql, column, "补齐语句必须覆盖 %s", column)
	}
	// 按 usage_logs 的唯一约束定位，避免误更新同 request_id 的其它 key。
	require.Contains(t, sql, "request_id = $1 AND api_key_id = $2")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 缺少定位信息时不应下发 SQL（记账被跳过时记录本就不存在）。
func TestUpdateVideoResultSkipsWithoutRequestIdentity(t *testing.T) {
	var captured []string
	db, _ := newSQLCapturingMock(t, &captured)
	repo := newUsageLogRepositoryWithSQL(nil, db)

	require.NoError(t, repo.UpdateVideoResult(context.Background(), "  ", 7, service.VideoUsageResultUpdate{}))
	require.NoError(t, repo.UpdateVideoResult(context.Background(), "video:x", 0, service.VideoUsageResultUpdate{}))
	require.Empty(t, captured)
}

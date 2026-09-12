//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 普通分组依旧保持"一个分组只归一个渠道"：聚合分组是唯一的例外。
func TestCheckGroupConflictsExemptsOnlyAgentGroups(t *testing.T) {
	conflicts := []int64{11}
	repo := &mockChannelRepository{
		getGroupsInOtherChannelsFn: func(_ context.Context, _ int64, groupIDs []int64) ([]int64, error) {
			// 只有非聚合分组会走到这里：断言聚合分组已被过滤掉。
			require.Equal(t, []int64{11}, groupIDs)
			return conflicts, nil
		},
		listAgentGroupIDsFn: func(_ context.Context, groupIDs []int64) ([]int64, error) {
			return []int64{2}, nil
		},
	}
	svc := NewChannelService(repo, nil, nil, nil, nil)

	// 聚合分组 + 普通分组一起提交：普通分组的冲突照旧报错。
	err := svc.checkGroupConflicts(context.Background(), 0, []int64{2, 11})
	require.ErrorIs(t, err, ErrGroupAlreadyInChannel)

	// 只提交聚合分组：允许关联多个渠道。
	repo.getGroupsInOtherChannelsFn = func(context.Context, int64, []int64) ([]int64, error) {
		t.Fatal("聚合分组不应参与冲突校验")
		return nil, nil
	}
	require.NoError(t, svc.checkGroupConflicts(context.Background(), 0, []int64{2}))
}

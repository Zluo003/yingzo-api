//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// groupPlatformRepoStub 只实现 UpdateGroup 走到的两个方法，其余靠内嵌接口占位。
type groupPlatformRepoStub struct {
	GroupRepository
	group   *Group
	updated *Group
}

func (r *groupPlatformRepoStub) GetByID(_ context.Context, _ int64) (*Group, error) {
	cloned := *r.group
	return &cloned, nil
}

func (r *groupPlatformRepoStub) Update(_ context.Context, group *Group) error {
	r.updated = group
	return nil
}

// authCacheInvalidatorSpy 记录按分组失效的调用次数（正是改平台时必须触发的那一个）。
type authCacheInvalidatorSpy struct {
	calls      int
	groupCalls []int64
	keyCalls   int
	userCalls  int
}

func (s *authCacheInvalidatorSpy) InvalidateAuthCacheByKey(context.Context, string) { s.keyCalls++ }

func (s *authCacheInvalidatorSpy) InvalidateAuthCacheByUserID(context.Context, int64) { s.userCalls++ }

func (s *authCacheInvalidatorSpy) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	s.calls++
	s.groupCalls = append(s.groupCalls, groupID)
}

// 渠道缓存持有 groupID → platform，而渠道定价/模型映射/模型白名单都按平台严格隔离。
// 只要分组被更新就必须失效该分组的鉴权缓存：漏失效会让后续最多 10 分钟的请求仍按旧
// 平台匹配（静默走错价）。当前实现选择"无条件失效"——比按平台差异判断更不容易漏，
// 代价只是一次缓存重建，因此这里断言的是无条件失效、且失效的正是被改的分组。
func TestUpdateGroupInvalidatesChannelCacheOnPlatformChange(t *testing.T) {
	tests := []struct {
		name          string
		fromPlatform  string
		inputPlatform string
		wantCalls     int
	}{
		{
			name:          "platform changed invalidates",
			fromPlatform:  PlatformAnthropic,
			inputPlatform: PlatformOpenAI,
			wantCalls:     1,
		},
		{
			// 平台没变也失效：宁可多重建一次缓存，也不冒漏失效的风险
			name:          "same platform still invalidates",
			fromPlatform:  PlatformAnthropic,
			inputPlatform: PlatformAnthropic,
			wantCalls:     1,
		},
		{
			name:          "platform omitted still invalidates",
			fromPlatform:  PlatformAnthropic,
			inputPlatform: "",
			wantCalls:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupPlatformRepoStub{group: &Group{ID: 7, Name: "g", Platform: tt.fromPlatform}}
			spy := &authCacheInvalidatorSpy{}
			svc := &adminServiceImpl{groupRepo: repo, authCacheInvalidator: spy}

			got, err := svc.UpdateGroup(context.Background(), 7, &UpdateGroupInput{Platform: tt.inputPlatform})
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tt.wantCalls, spy.calls)
			if tt.wantCalls > 0 {
				require.Equal(t, []int64{7}, spy.groupCalls, "失效的必须是本次修改的分组")
			}
		})
	}
}

// 依赖可以不注入（例如测试或裁剪构建），此时不应 panic——缓存靠 TTL 自然重建。
func TestUpdateGroupWithoutChannelCacheInvalidator(t *testing.T) {
	repo := &groupPlatformRepoStub{group: &Group{ID: 7, Name: "g", Platform: PlatformAnthropic}}
	svc := &adminServiceImpl{groupRepo: repo}

	got, err := svc.UpdateGroup(context.Background(), 7, &UpdateGroupInput{Platform: PlatformOpenAI})
	require.NoError(t, err)
	require.Equal(t, PlatformOpenAI, got.Platform)
}

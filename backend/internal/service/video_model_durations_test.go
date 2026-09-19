package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// video_model_durations 是账号级时长白名单：与 video_model_resolutions 同一套
// 语义（未配置 = 不限制、空条目删除、取值必须是模型规格内的整数秒）。
func TestNormalizeVideoModelDurationsAcceptsSpecRanges(t *testing.T) {
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelDurationsExtraKey: map[string]any{
			VideoModelSeedance20Fast: []any{10, 5, 5.0, float64(10)},
			// 时长在规格范围内的其它模型同样合法。
			VideoModelGrokImagineVideo15: []any{float64(1), 15},
		},
	})
	require.NoError(t, err)

	byModel, ok := normalized[VideoModelDurationsExtraKey].(map[string]any)
	require.True(t, ok, "白名单应保持为 map")
	// 去重 + 升序回填。
	require.Equal(t, []any{5, 10}, byModel[VideoModelSeedance20Fast])
	require.Equal(t, []any{1, 15}, byModel[VideoModelGrokImagineVideo15])
}

func TestNormalizeVideoModelDurationsRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown model": {
			VideoModelDurationsExtraKey: map[string]any{"seedance-9.9": []any{5}},
		},
		"duration above spec range": {
			VideoModelDurationsExtraKey: map[string]any{VideoModelSeedance20: []any{16}},
		},
		"duration below spec range": {
			// grok 的规格下限是 1，0 秒非法。
			VideoModelDurationsExtraKey: map[string]any{VideoModelGrokImagineVideo15: []any{0}},
		},
		"non-integer duration": {
			VideoModelDurationsExtraKey: map[string]any{VideoModelSeedance20: []any{4.5}},
		},
		"non-numeric duration": {
			VideoModelDurationsExtraKey: map[string]any{VideoModelSeedance20: []any{"8"}},
		},
		"not an array": {
			VideoModelDurationsExtraKey: map[string]any{VideoModelSeedance20: 8},
		},
		"not an object": {
			VideoModelDurationsExtraKey: []any{8},
		},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeVideoProviderExtra(PlatformVideo, extra)
			require.Error(t, err)
			require.Equal(t, "invalid_video_model_durations", infraerrors.Reason(err))
		})
	}
}

func TestNormalizeVideoModelDurationsDropsEmptyEntries(t *testing.T) {
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelDurationsExtraKey: map[string]any{
			VideoModelSeedance20: []any{},
		},
	})
	require.NoError(t, err)
	// 空列表等价于不限制，不应留下一个需要读取方额外判断的状态。
	require.NotContains(t, normalized, VideoModelDurationsExtraKey)
}

func TestVideoAccountSupportsDuration(t *testing.T) {
	extra := map[string]any{VideoProviderExtraKey: videoProviderAigod}
	restricted := &Account{
		Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: extra,
	}
	restricted.Extra[VideoModelDurationsExtraKey] = map[string]any{
		VideoModelSeedance20Fast: []any{float64(5), float64(10)},
	}

	require.True(t, videoAccountSupportsDuration(restricted, VideoModelSeedance20Fast, 5))
	require.True(t, videoAccountSupportsDuration(restricted, VideoModelSeedance20Fast, 10))
	require.False(t, videoAccountSupportsDuration(restricted, VideoModelSeedance20Fast, 8),
		"未列入的时长必须被拒绝")
	require.True(t, videoAccountSupportsDuration(restricted, VideoModelSeedance20, 8),
		"未配置的模型不受该白名单约束（模型维度由 model_mapping 负责）")

	// 配置损坏时放行：宁可多调度，也不要因一条脏数据让账号彻底不可用。
	corrupted := &Account{
		Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra: map[string]any{
			VideoProviderExtraKey:       videoProviderAigod,
			VideoModelDurationsExtraKey: "not-a-map",
		},
	}
	require.True(t, videoAccountSupportsDuration(corrupted, VideoModelSeedance20Fast, 8))
}

// 选择路径的闸门：时长白名单必须真正参与 selectAccountForRequest 的账号筛选。
func TestVideoAccountSelectionHonoursDurationWhitelist(t *testing.T) {
	const groupID int64 = 17

	restricted := Account{
		ID: 21, Name: "fast-only-5-10", Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{VideoModelSeedance20Fast: VideoModelSeedance20Fast},
		},
		Extra: map[string]any{
			VideoProviderExtraKey:       videoProviderAigod,
			VideoModelDurationsExtraKey: map[string]any{VideoModelSeedance20Fast: []any{5, 10}},
		},
	}
	unrestricted := Account{
		ID: 22, Name: "any-duration", Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Priority: 2,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"model_mapping": map[string]any{VideoModelSeedance20Fast: VideoModelSeedance20Fast},
		},
		Extra: map[string]any{VideoProviderExtraKey: videoProviderAigod},
	}

	videoService := NewVideoService(
		&videoAccountRepoStub{accounts: []Account{restricted, unrestricted}},
		newVideoTaskMemoryRepo(),
		&videoPricingMemoryRepo{rule: &VideoGroupPricingRule{
			GroupID: groupID, ModelCode: VideoModelSeedance20Fast, Resolution: VideoResolution720P,
			CreditsPerSecond: 0.2, Enabled: true,
		}},
		&videoUsageLogRepoStub{}, &videoUsageBillingRepoStub{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	selectFor := func(seconds int) (*Account, error) {
		return videoService.selectAccountForRequest(context.Background(), groupID, &normalizedVideoRequest{
			Model:            VideoModelSeedance20Fast,
			Resolution:       VideoResolution720P,
			GeneratedSeconds: seconds,
		}, false)
	}

	// 8 秒只有不受限账号能服务：受限账号优先级更高(1) 但必须被跳过。
	chosen, err := selectFor(8)
	require.NoError(t, err)
	require.Equal(t, int64(22), chosen.ID, "时长白名单外的秒数不得调度到受限账号")

	// 5 秒两者都行，优先级更高的受限账号应胜出。
	chosen, err = selectFor(5)
	require.NoError(t, err)
	require.Equal(t, int64(21), chosen.ID)
}

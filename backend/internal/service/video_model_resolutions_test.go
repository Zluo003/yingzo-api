package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// videoAccountWithResolutions 构造一个只带分辨率白名单的账号，用于选择路径断言。
func videoAccountWithResolutions(t *testing.T, provider string, resolutions any) *Account {
	t.Helper()
	extra := map[string]any{VideoProviderExtraKey: provider}
	if resolutions != nil {
		extra[VideoModelResolutionsExtraKey] = resolutions
	}
	return &Account{
		Platform:    PlatformVideo,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       extra,
	}
}

func TestNormalizeVideoModelResolutionsAcceptsOfficialResolutions(t *testing.T) {
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelResolutionsExtraKey: map[string]any{
			VideoModelSeedance20: []any{"1080p", "720p", "720p"},
		},
	})
	require.NoError(t, err)

	byModel, ok := normalized[VideoModelResolutionsExtraKey].(map[string]any)
	require.True(t, ok, "白名单应保持为 map")
	// 去重 + 按官方档位顺序回填（480p,720p,1080p,4K 中只保留 720p/1080p）
	require.Equal(t, []any{"720p", "1080p"}, byModel[VideoModelSeedance20])
}

func TestNormalizeVideoModelResolutionsMatchesResolutionCaseInsensitively(t *testing.T) {
	// 4K 的官方写法是大写 K，直接 ToLower 会把合法值判成非法。
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelResolutionsExtraKey: map[string]any{
			VideoModelSeedance20: []any{"4k"},
		},
	})
	require.NoError(t, err)
	byModel, ok := normalized[VideoModelResolutionsExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{VideoResolution4K}, byModel[VideoModelSeedance20])
}

func TestNormalizeVideoModelResolutionsRejectsUnknownModelOrResolution(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown model": {
			VideoModelResolutionsExtraKey: map[string]any{"seedance-9.9": []any{"720p"}},
		},
		"resolution not supported by that model": {
			VideoModelResolutionsExtraKey: map[string]any{VideoModelSeedance20Fast: []any{"1080p"}},
		},
		"not an object": {
			VideoModelResolutionsExtraKey: []any{"720p"},
		},
		"not an array of strings": {
			VideoModelResolutionsExtraKey: map[string]any{VideoModelSeedance20: []any{720}},
		},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeVideoProviderExtra(PlatformVideo, extra)
			require.Error(t, err)
			require.Equal(t, "invalid_video_model_resolutions", infraerrors.Reason(err))
		})
	}
}

func TestNormalizeVideoModelResolutionsDropsEmptyEntries(t *testing.T) {
	normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{
		VideoModelResolutionsExtraKey: map[string]any{
			VideoModelSeedance20: []any{},
		},
	})
	require.NoError(t, err)
	// 空列表等价于不限制，不应留下一个需要读取方额外判断的状态。
	require.NotContains(t, normalized, VideoModelResolutionsExtraKey)
}

func TestNormalizeVideoModelResolutionsUntouchedForOtherPlatforms(t *testing.T) {
	extra := map[string]any{VideoModelResolutionsExtraKey: "garbage"}
	out, err := NormalizeVideoProviderExtra(PlatformOpenAI, extra)
	require.NoError(t, err)
	require.Equal(t, "garbage", out[VideoModelResolutionsExtraKey])
}

func TestVideoAccountSupportsResolution(t *testing.T) {
	restricted := videoAccountWithResolutions(t, videoProviderAigod, map[string]any{
		VideoModelSeedance20: []any{"720p", "1080p"},
	})

	require.True(t, videoAccountSupportsResolution(restricted, VideoModelSeedance20, "720p"))
	require.True(t, videoAccountSupportsResolution(restricted, VideoModelSeedance20, "1080P"),
		"大小写与空白应被归一化后匹配")
	require.False(t, videoAccountSupportsResolution(restricted, VideoModelSeedance20, "480p"),
		"未列入的档位必须被拒绝")
	require.True(t, videoAccountSupportsResolution(restricted, VideoModelSeedance25, "1080p"),
		"未配置的模型不受该白名单约束（模型维度由 model_mapping 负责）")

	unrestricted := videoAccountWithResolutions(t, videoProviderAigod, nil)
	require.True(t, videoAccountSupportsResolution(unrestricted, VideoModelSeedance20, "480p"),
		"未配置白名单时必须保持旧行为：不限制")

	corrupted := videoAccountWithResolutions(t, videoProviderAigod, "not-a-map")
	require.True(t, videoAccountSupportsResolution(corrupted, VideoModelSeedance20, "480p"),
		"配置损坏时应放行，避免一条脏数据让账号整体不可用")
}

// 选择路径的闸门：这是本功能的实际生效点。必须真正跑 selectAccountForRequest，
// 而不是只断言辅助函数——否则闸门被漏接在调度之外也测不出来。
func TestVideoAccountSelectionHonoursResolutionWhitelist(t *testing.T) {
	const groupID int64 = 7

	restricted := *videoAccountWithResolutions(t, videoProviderAigod, map[string]any{
		VideoModelSeedance20: []any{VideoResolution720P},
	})
	restricted.ID = 11
	restricted.Name = "restricted-720p"
	restricted.Schedulable = true
	restricted.Priority = 1
	restricted.Credentials["model_mapping"] = map[string]any{VideoModelSeedance20: VideoModelSeedance20}

	unrestricted := *videoAccountWithResolutions(t, videoProviderAigod, nil)
	unrestricted.ID = 12
	unrestricted.Name = "unrestricted"
	unrestricted.Schedulable = true
	unrestricted.Priority = 2
	unrestricted.Credentials["model_mapping"] = map[string]any{VideoModelSeedance20: VideoModelSeedance20}

	videoService := NewVideoService(
		&videoAccountRepoStub{accounts: []Account{restricted, unrestricted}},
		newVideoTaskMemoryRepo(),
		&videoPricingMemoryRepo{rule: &VideoGroupPricingRule{
			GroupID: groupID, ModelCode: VideoModelSeedance20, Resolution: VideoResolution720P,
			CreditsPerSecond: 0.2, Enabled: true,
		}},
		&videoUsageLogRepoStub{},
		&videoUsageBillingRepoStub{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	selectFor := func(resolution string) (*Account, error) {
		return videoService.selectAccountForRequest(context.Background(), groupID, &normalizedVideoRequest{
			Model:            VideoModelSeedance20,
			Resolution:       resolution,
			GeneratedSeconds: 5,
		}, false)
	}

	// 1080p 只有未受限账号能服务：受限账号优先级更高(1) 但必须被跳过。
	chosen, err := selectFor(VideoResolution1080P)
	require.NoError(t, err)
	require.Equal(t, int64(12), chosen.ID, "受限于 720p 的账号不得被 1080p 请求选中")

	// 720p 两者都行，优先级更高的受限账号应胜出——证明闸门是"按分辨率"生效，
	// 而不是把受限账号整个排除掉。
	chosen, err = selectFor(VideoResolution720P)
	require.NoError(t, err)
	require.Equal(t, int64(11), chosen.ID, "720p 仍应选中优先级更高的受限账号")
}

// 上游「实际能服务哪些 (模型, 分辨率)」这份矩阵同时存在于两处：后端适配器
// （aigodVideoProviderAdapter.Compatible / videoNewtokenUpstreamModel）和前端
// frontend/src/views/admin/videoModelResolutions.ts 的 VIDEO_PROVIDER_RESOLUTIONS。
// 前端用它决定哪些档位可勾选，后端用它决定能不能真正调度；两边一旦漂移，运营就会
// 配出一个"界面上能选、实际调度不到"的账号。
//
// 本用例把后端侧的矩阵显式固化：改适配器时会失败，失败信息提醒同步前端常量。
func TestVideoProviderServableResolutionMatrix(t *testing.T) {
	expected := map[string]map[string][]string{
		videoProviderAigod: {
			// 目录含 seedance-2.0-4k：4K 仅 2.0 有，fast/2.5 的官方档位本就不含 4K。
			VideoModelSeedance20:     {VideoResolution480P, VideoResolution720P, VideoResolution1080P, VideoResolution4K},
			VideoModelSeedance20Fast: {VideoResolution480P, VideoResolution720P},
			VideoModelSeedance25:     {VideoResolution480P, VideoResolution720P, VideoResolution1080P},
		},
		videoProviderNewtoken: {
			VideoModelSeedance20:     {VideoResolution720P, VideoResolution1080P},
			VideoModelSeedance20Fast: {VideoResolution720P},
			// newtoken 目录含 sd2.5-1080p-official，故 2.5 的 1080p 同样可服务。
			VideoModelSeedance25: {VideoResolution720P, VideoResolution1080P},
		},
	}

	for provider, byModel := range expected {
		adapter := videoProviderAdapterByName(provider)
		for model, resolutions := range byModel {
			var servable []string
			for _, resolution := range SupportedVideoResolutions(model) {
				if adapter.Compatible(model, resolution) {
					servable = append(servable, resolution)
				}
			}
			require.Equal(t, resolutions, servable,
				"上游 %s 对 %s 的可服务分辨率发生变化；请同步更新 "+
					"frontend/src/views/admin/videoModelResolutions.ts 的 VIDEO_PROVIDER_RESOLUTIONS",
				provider, model)
		}
	}
}

// 用户视角的核心保证（两组断言必须同时成立）：
//  1. 某账号不支持的分辨率，绝不会被调度到它；
//  2. 两个账号都支持的分辨率，会在同优先级账号之间轮转，而不是永远压在同一个上。
//
// 第 2 条依赖 selectAccountForRequest 内部写入 last_used_at——这条写入一旦被移除，
// 排序会一路落到 id 升序，本用例即失败。
func TestVideoAccountSelectionRotatesEqualPriorityAccounts(t *testing.T) {
	const groupID int64 = 99

	mk := func(id int64, resolutions map[string]any) Account {
		acc := Account{
			ID: id, Name: "video-account", Platform: PlatformVideo, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Priority: 5,
			Credentials: map[string]any{
				"api_key":       "sk-x",
				"model_mapping": map[string]any{VideoModelSeedance20: VideoModelSeedance20},
			},
			Extra: map[string]any{VideoProviderExtraKey: videoProviderAigod},
		}
		if resolutions != nil {
			acc.Extra[VideoModelResolutionsExtraKey] = resolutions
		}
		return acc
	}

	// A(id=1) 只支持 720p；B(id=2) 不受限；两者同优先级。
	restricted := mk(1, map[string]any{VideoModelSeedance20: []any{VideoResolution720P}})
	unrestricted := mk(2, nil)

	svc := NewVideoService(
		&videoAccountRepoStub{accounts: []Account{restricted, unrestricted}},
		newVideoTaskMemoryRepo(),
		&videoPricingMemoryRepo{rule: &VideoGroupPricingRule{
			GroupID: groupID, ModelCode: VideoModelSeedance20, Resolution: VideoResolution720P,
			CreditsPerSecond: 0.2, Enabled: true,
		}},
		&videoUsageLogRepoStub{}, &videoUsageBillingRepoStub{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	pick := func(resolution string) int64 {
		acc, err := svc.selectAccountForRequest(context.Background(), groupID, &normalizedVideoRequest{
			Model: VideoModelSeedance20, Resolution: resolution, GeneratedSeconds: 5,
		}, false)
		require.NoError(t, err)
		return acc.ID
	}

	// 1) 受限档位只落到能服务的账号：连续多次都必须避开 A。
	for i := 0; i < 3; i++ {
		require.Equal(t, int64(2), pick(VideoResolution1080P),
			"1080p 不应被调度到只支持 720p 的账号")
	}

	// 2) 共同支持的档位在两者之间轮转。
	require.Equal(t, int64(1), pick(VideoResolution720P), "首次应选 id 更小的账号")
	require.Equal(t, int64(2), pick(VideoResolution720P),
		"第二次应轮转到另一个账号；若始终为 1，说明 last_used_at 没有被记录")
	require.Equal(t, int64(1), pick(VideoResolution720P), "第三次应轮转回来")
}

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// aigod 上游公开的模型名清单（来自上游接入文档）。我们按 {model}-{resolution}
// 生成，必须与之逐字一致；多出一个不存在的名字会在请求时才失败，少一个则白白
// 放弃一个可用档位。
func TestAigodUpstreamModelNamesMatchUpstreamCatalog(t *testing.T) {
	expected := map[string]string{
		VideoModelSeedance20:     "seedance-2.0-720p",
		VideoModelSeedance20Fast: "seedance-2.0-fast-720p",
		VideoModelSeedance25:     "seedance-2.5-720p",
	}
	// 完整清单（含各分辨率），逐个核对。
	catalog := []struct{ model, resolution, upstream string }{
		{VideoModelSeedance20, VideoResolution1080P, "seedance-2.0-1080p"},
		{VideoModelSeedance20, VideoResolution720P, "seedance-2.0-720p"},
		{VideoModelSeedance20, VideoResolution480P, "seedance-2.0-480p"},
		{VideoModelSeedance20Fast, VideoResolution720P, "seedance-2.0-fast-720p"},
		{VideoModelSeedance20Fast, VideoResolution480P, "seedance-2.0-fast-480p"},
		{VideoModelSeedance25, VideoResolution480P, "seedance-2.5-480p"},
		{VideoModelSeedance25, VideoResolution720P, "seedance-2.5-720p"},
		{VideoModelSeedance25, VideoResolution1080P, "seedance-2.5-1080p"},
		// 4K：目录里写作小写 k，而下游/计费侧的规范写法是大写 4K。
		{VideoModelSeedance20, VideoResolution4K, "seedance-2.0-4k"},
	}
	adapter := videoProviderAdapterByName(videoProviderAigod)
	for _, tc := range catalog {
		require.Equal(t, tc.upstream, adapter.UpstreamModel(nil, &normalizedVideoRequest{
			Model: tc.model, Resolution: tc.resolution,
		}), "%s @%s 的上游模型名与 aigod 文档不一致", tc.model, tc.resolution)
	}

	// 4K 只有 2.0 提供；fast / 2.5 的官方档位不含 4K，必须不可服务。
	require.True(t, adapter.Compatible(VideoModelSeedance20, VideoResolution4K))
	require.False(t, adapter.Compatible(VideoModelSeedance20Fast, VideoResolution4K))
	require.False(t, adapter.Compatible(VideoModelSeedance25, VideoResolution4K))

	for model, upstream := range expected {
		require.Equal(t, upstream, SeedanceUpstreamModel(model, VideoResolution720P))
	}
}

// newtoken 把分辨率编进模型名，且只提供 720p / 1080p 两档 official 模型
// （上游文档：模型、参考视频与计费方式表）。这里逐条锁住映射。
func TestNewtokenUpstreamModelNamesMatchUpstreamCatalog(t *testing.T) {
	catalog := []struct {
		model      string
		resolution string
		upstream   string
	}{
		{VideoModelSeedance20, VideoResolution720P, "sd2.0-720p-official"},
		{VideoModelSeedance20, VideoResolution1080P, "sd2.0-1080p-official"},
		{VideoModelSeedance20Fast, VideoResolution720P, "sd2.0-720p-fast-official"},
		{VideoModelSeedance25, VideoResolution720P, "sd2.5-720p-official"},
		{VideoModelSeedance25, VideoResolution1080P, "sd2.5-1080p-official"},
	}
	adapter := videoProviderAdapterByName(videoProviderNewtoken)
	for _, tc := range catalog {
		require.True(t, adapter.Compatible(tc.model, tc.resolution),
			"%s @%s 应可服务", tc.model, tc.resolution)
		require.Equal(t, tc.upstream, adapter.UpstreamModel(nil, &normalizedVideoRequest{
			Model: tc.model, Resolution: tc.resolution,
		}), "%s @%s 的上游模型名与 newtoken 文档不一致", tc.model, tc.resolution)
	}

	// newtoken 不提供 480p，也不提供 2.0-fast 的 1080p。
	require.Empty(t, videoNewtokenUpstreamModel(VideoModelSeedance20, VideoResolution480P))
	require.Empty(t, videoNewtokenUpstreamModel(VideoModelSeedance25, VideoResolution480P))
	require.Empty(t, videoNewtokenUpstreamModel(VideoModelSeedance20Fast, VideoResolution1080P))
}

// 结果地址的读取位置：上游文档把 output[0].url 与 video_url/url 并列列出。
func TestVideoResultURLFromPayloadReadsDocumentedLocations(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"top-level video_url", map[string]any{"video_url": "https://cdn/a.mp4"}, "https://cdn/a.mp4"},
		{"top-level url", map[string]any{"url": "https://cdn/b.mp4"}, "https://cdn/b.mp4"},
		{"output array", map[string]any{
			"output": []any{map[string]any{"type": "video", "url": "https://cdn/c.mp4"}},
		}, "https://cdn/c.mp4"},
		{"result envelope", map[string]any{
			"result": map[string]any{"video_url": "https://cdn/d.mp4"},
		}, "https://cdn/d.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, videoResultURLFromPayload(tc.payload))
		})
	}
}

// aigod 要求参考图与参考视频统一声明 subject_type=person：该上游把参考素材
// 一律按真人链路处理，因此不能依赖下游是否传了 subject_type（更不能沿用下游
// 可能传入的其他取值）。audio_url 不属于参考图/视频，不设置该字段。
func TestAigodUpstreamBodyForcesPersonSubjectType(t *testing.T) {
	normalized := &normalizedVideoRequest{
		Model:      VideoModelSeedance20,
		Prompt:     "让照片里的人走向镜头",
		Resolution: VideoResolution720P,
		Content: []VideoContent{
			{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://cdn/ref.png"}},
			{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/first.png"},
				SubjectType: "object"},
			{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn/ref.mp4"}},
			{Type: "audio_url", AudioURL: &VideoContentURL{URL: "https://cdn/ref.mp3"}},
		},
	}

	body := videoProviderAdapterByName(videoProviderAigod).BuildCreateBody(normalized, "seedance-2.0-720p")
	content, ok := body["content"].([]map[string]any)
	require.True(t, ok, "content 应为对象数组")

	byType := map[string][]map[string]any{}
	for _, entry := range content {
		kind, _ := entry["type"].(string)
		byType[kind] = append(byType[kind], entry)
	}

	require.Len(t, byType["image_url"], 2, "两张参考图都应保留")
	for _, entry := range byType["image_url"] {
		require.Equal(t, "person", entry["subject_type"],
			"参考图必须统一为 person，包括下游传了其它取值的那张")
	}
	require.Len(t, byType["video_url"], 1)
	require.Equal(t, "person", byType["video_url"][0]["subject_type"], "参考视频同样统一为 person")

	require.Len(t, byType["audio_url"], 1)
	require.NotContains(t, byType["audio_url"][0], "subject_type",
		"参考音频不属于参考图/视频，不应带上 subject_type")
}

// 时长是"按模型"的约束，不是按渠道：seedance-2.0 系列 4-15 秒，2.5 为 4-30 秒，
// 两个上游一致。它由共享规格表在归一化阶段校验，provider 适配器不应再收紧——
// 曾经在这里按渠道统一卡 4-15，结果错误地砍掉了 2.5 的 16-30 秒。
func TestSeedanceDurationBoundsArePerModelNotPerProvider(t *testing.T) {
	spec20, _ := videoSpecForModel(VideoModelSeedance20)
	spec20Fast, _ := videoSpecForModel(VideoModelSeedance20Fast)
	spec25, _ := videoSpecForModel(VideoModelSeedance25)

	require.Equal(t, 4, spec20.MinSeconds)
	require.Equal(t, 15, spec20.MaxSeconds)
	require.Equal(t, 4, spec20Fast.MinSeconds)
	require.Equal(t, 15, spec20Fast.MaxSeconds)
	require.Equal(t, 4, spec25.MinSeconds)
	require.Equal(t, 30, spec25.MaxSeconds)

	// 2.5 的 16-30 秒对两个上游都必须可服务。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		adapter := videoProviderAdapterByName(provider)
		require.True(t, adapter.CompatibleRequest(&normalizedVideoRequest{
			Model: VideoModelSeedance25, Resolution: VideoResolution720P, GeneratedSeconds: 30,
		}), "%s 应接受 2.5 的 30 秒", provider)
	}

	// 越界时长由归一化阶段统一以 400 拒绝（两条路径都经过它）。
	_, err := normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelSeedance20, Prompt: "x", Duration: 16, Resolution: VideoResolution720P,
	})
	require.Error(t, err, "2.0 的 16 秒应被拒绝")

	_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelSeedance25, Prompt: "x", Duration: 31, Resolution: VideoResolution720P,
	})
	require.Error(t, err, "2.5 的 31 秒应被拒绝")

	_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelSeedance25, Prompt: "x", Duration: 30, Resolution: VideoResolution720P,
	})
	require.NoError(t, err, "2.5 的 30 秒应通过")
}

// 时长/画幅/分辨率都是渠道能力，必须在渠道闸门（provider 适配器）里判定：
// 同一模型在不同渠道可能支持不同范围，能服务就参与调度，不能就换渠道。
// 这里锁住"当前两个 Seedance 渠道的取值范围一致"这一事实。
func TestChannelGateEnforcesDurationAndRatio(t *testing.T) {
	base := func(model string, seconds int, ratio string) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: model, Resolution: VideoResolution720P,
			GeneratedSeconds: seconds, Ratio: ratio, RatioProvided: true,
		}
	}

	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		adapter := videoProviderAdapterByName(provider)
		t.Run(provider, func(t *testing.T) {
			// 时长按模型区分：2.0 系列上限 15 秒，2.5 上限 30 秒。
			require.True(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 5, "16:9")))
			require.True(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 15, "16:9")))
			require.False(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 16, "16:9")), "2.0 的 16 秒应被渠道拒绝")
			require.True(t, adapter.CompatibleRequest(base(VideoModelSeedance25, 30, "16:9")), "2.5 的 30 秒应被渠道接受")
			require.False(t, adapter.CompatibleRequest(base(VideoModelSeedance25, 31, "16:9")), "2.5 的 31 秒应被渠道拒绝")
			require.False(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 3, "16:9")), "低于 4 秒应被渠道拒绝")

			// 六个画幅通用；auto 仅 2.5。
			for _, ok := range []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"} {
				require.True(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 5, ok)), "ratio=%s 应放行", ok)
			}
			for _, bad := range []string{"5:4", "16:10", "1:2"} {
				require.False(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 5, bad)), "ratio=%s 应拒绝", bad)
			}
			require.True(t, adapter.CompatibleRequest(base(VideoModelSeedance25, 5, "auto")))
			require.False(t, adapter.CompatibleRequest(base(VideoModelSeedance20, 5, "auto")), "auto 仅 2.5")
		})
	}
}

// aigod 与 newtoken 的轮询超时统一为 15 分钟（覆盖 aigod 真人过白 ≤10 分钟 + 生成）。
func TestSeedancePollTimeoutIsFifteenMinutesForEveryProvider(t *testing.T) {
	require.Equal(t, 15*time.Minute, videoDefaultPollTimeout)

	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		t.Run(provider, func(t *testing.T) {
			account := &Account{
				Platform: PlatformVideo,
				Type:     AccountTypeAPIKey,
				Extra:    map[string]any{VideoProviderExtraKey: provider},
			}
			require.Equal(t, 15*time.Minute, videoAccountDefaultDuration(account, "poll_timeout_ms"),
				"%s 的轮询超时应为 15 分钟", provider)
		})
	}

	// 账号级 poll_timeout_ms 仍然可以覆盖该默认值。
	override := &Account{
		Platform: PlatformVideo,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderAigod,
			"poll_timeout_ms":     1200000,
		},
	}
	require.Equal(t, 20*time.Minute, videoAccountDuration(override, "poll_timeout_ms", videoAccountDefaultDuration(override, "poll_timeout_ms")))
}

// aigod 的状态值使用 in_process（文档第 9 节），必须显式归一化。
func TestVideoUpstreamStatusRecognisesAigodInProcess(t *testing.T) {
	require.Equal(t, VideoTaskStatusProcessing, normalizeVideoUpstreamStatus("in_process"))
	require.Equal(t, VideoTaskStatusQueued, normalizeVideoUpstreamStatus("queued"))
	require.Equal(t, VideoTaskStatusCompleted, normalizeVideoUpstreamStatus("completed"))
	require.Equal(t, VideoTaskStatusFailed, normalizeVideoUpstreamStatus("failed"))
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mikuapi 的 Seedance 上游模型名是固定三档且**不带分辨率**（清晰度走 resolution
// 字段）。逐条锁住映射，并确认没有接入 seedance-2-mini。
func TestMikuapiUpstreamModelNamesMatchUpstreamCatalog(t *testing.T) {
	catalog := []struct {
		model    string
		upstream string
	}{
		{VideoModelSeedance20, "seedance-2-pro"},
		{VideoModelSeedance20Fast, "seedance-2-fast"},
		{VideoModelSeedance25, "seedance-2.5-pro"},
	}
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	for _, tc := range catalog {
		// 分辨率不进模型名：每个可服务档位都应得到同一个上游模型名。
		for _, resolution := range []string{VideoResolution480P, VideoResolution720P} {
			require.Equal(t, tc.upstream, adapter.UpstreamModel(nil, &normalizedVideoRequest{
				Model: tc.model, Resolution: resolution,
			}), "%s @%s 的上游模型名与 mikuapi 文档不一致", tc.model, resolution)
		}
	}

	// 未接入的模型必须没有上游名（不能用兜底拼出一个名字）。
	require.Empty(t, videoMikuapiUpstreamModel("seedance-2-mini"))
	require.Empty(t, videoMikuapiUpstreamModel("wan-3"))
	require.False(t, adapter.Compatible("seedance-2-mini", VideoResolution720P))
}

// 分辨率档位按共享规格表：2.0 含 4K，fast 仅 480p/720p，2.5 无 4K。
func TestMikuapiResolutionSupportMatchesSeedanceSpecs(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)

	require.True(t, adapter.Compatible(VideoModelSeedance20, VideoResolution4K))
	require.True(t, adapter.Compatible(VideoModelSeedance20, VideoResolution1080P))
	require.True(t, adapter.Compatible(VideoModelSeedance20Fast, VideoResolution480P))
	require.False(t, adapter.Compatible(VideoModelSeedance20Fast, VideoResolution1080P))
	require.False(t, adapter.Compatible(VideoModelSeedance20Fast, VideoResolution4K))
	require.True(t, adapter.Compatible(VideoModelSeedance25, VideoResolution1080P))
	require.False(t, adapter.Compatible(VideoModelSeedance25, VideoResolution4K),
		"mikuapi 的 2.5 没有 4K，传 4K 会被上游降到 1080p")
}

// mikuapi 的 2.0-fast 只开放 5 秒与 10 秒。上游对超范围时长是"夹到允许区间"，
// 静默夹取会按 A 时长计费交付 B 时长的片子，因此必须在渠道闸门里严格拒绝，
// 让请求落到别的上游。
func TestMikuapiFastOnlyServesFiveOrTenSeconds(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	request := func(model string, seconds int) *normalizedVideoRequest {
		return &normalizedVideoRequest{Model: model, Resolution: VideoResolution720P, GeneratedSeconds: seconds}
	}

	for _, seconds := range []int{5, 10} {
		require.True(t, adapter.CompatibleRequest(request(VideoModelSeedance20Fast, seconds)),
			"fast 的 %d 秒可服务", seconds)
	}
	for _, seconds := range []int{4, 6, 8, 11, 15} {
		require.False(t, adapter.CompatibleRequest(request(VideoModelSeedance20Fast, seconds)),
			"fast 的 %d 秒必须被渠道拒绝（上游会静默夹到 5/10 秒）", seconds)
	}
	// 2.0 / 2.5 不受这条限制：4-15 / 4-30。
	require.True(t, adapter.CompatibleRequest(request(VideoModelSeedance20, 8)))
	require.True(t, adapter.CompatibleRequest(request(VideoModelSeedance25, 8)))
	require.True(t, adapter.CompatibleRequest(request(VideoModelSeedance25, 30)))
	require.False(t, adapter.CompatibleRequest(request(VideoModelSeedance25, 31)))
}

// 首尾帧与参考素材不能混用：混传时上游按参考模式处理，首尾帧会被丢掉。
func TestMikuapiRejectsFrameAndReferenceMix(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	base := func(content []VideoContent) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: VideoModelSeedance25, Resolution: VideoResolution720P,
			GeneratedSeconds: 8, Content: content,
		}
	}
	firstFrame := VideoContent{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/first.png"}}
	referenceImage := VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/ref.png"}}

	require.True(t, adapter.CompatibleRequest(base([]VideoContent{firstFrame})))
	require.True(t, adapter.CompatibleRequest(base([]VideoContent{referenceImage})))
	require.False(t, adapter.CompatibleRequest(base([]VideoContent{firstFrame, referenceImage})),
		"首尾帧与参考素材混传必须交给别的上游")
}

// 请求体字段名按 mikuapi 文档：seconds / resolution / aspect_ratio，首帧
// input_reference、尾帧 image_end，参考素材是 reference_* 数组。
func TestMikuapiBuildCreateBodyUsesDocumentedFields(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	normalized := &normalizedVideoRequest{
		Model:            VideoModelSeedance25,
		Prompt:           "让 @Image1 里的花轻轻晃动",
		Resolution:       VideoResolution1080P,
		GeneratedSeconds: 8,
		Ratio:            "16:9",
		RatioProvided:    true,
		Content: []VideoContent{
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"}},
			{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn/b.mp4"}},
			{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/c.mp3"}},
		},
	}
	body := adapter.BuildCreateBody(normalized, videoMikuapiSeedance25Model)

	require.Equal(t, videoMikuapiSeedance25Model, body["model"])
	require.Equal(t, 8, body["seconds"])
	require.Equal(t, "1080p", body["resolution"], "分辨率走 resolution 字段，不拼进模型名")
	require.Equal(t, "16:9", body["aspect_ratio"])
	require.Equal(t, []string{"https://cdn/a.png"}, body["reference_images"])
	require.Equal(t, []string{"https://cdn/b.mp4"}, body["reference_videos"])
	require.Equal(t, []string{"https://cdn/c.mp3"}, body["reference_audios"])
	// 文档明确不要传的字段一个都不能出现。
	for _, forbidden := range []string{"stream", "n", "response_format", "webhook_url", "content", "ratio", "duration"} {
		require.NotContains(t, body, forbidden, "mikuapi 不接受字段 %s", forbidden)
	}

	// 首尾帧请求走标量字段。
	framed := &normalizedVideoRequest{
		Model: VideoModelSeedance25, Resolution: VideoResolution720P, GeneratedSeconds: 5,
		Content: []VideoContent{
			{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/start.jpg"}},
			{Type: "image_url", Role: "last_frame", ImageURL: &VideoContentURL{URL: "https://cdn/end.jpg"}},
		},
	}
	frameBody := adapter.BuildCreateBody(framed, videoMikuapiSeedance25Model)
	require.Equal(t, "https://cdn/start.jpg", frameBody["input_reference"])
	require.Equal(t, "https://cdn/end.jpg", frameBody["image_end"])
	require.NotContains(t, frameBody, "reference_images")
}

// mikuapi 的状态响应只给状态，成片要从 /v1/videos/{id}/content 取，并且回捞
// 必须带上游 key。
func TestMikuapiResultURLFallsBackToContentEndpoint(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	endpoint := "https://mikuapi.org/v1/videos"

	require.Equal(t, endpoint+"/vid_123/content",
		adapter.ResultURL(endpoint, "vid_123", map[string]any{"status": "completed"}))

	// 上游若在状态里给了地址，优先用它。
	require.Equal(t, "https://cdn/out.mp4",
		adapter.ResultURL(endpoint, "vid_123", map[string]any{"video_url": "https://cdn/out.mp4"}))

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku"}}
	require.Equal(t, "Bearer sk-miku", adapter.ResultAuthorization(account, videoMikuapiSeedance25Model))

	// 其它上游保持"地址自带授权"的既有行为。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		other := videoProviderAdapterByName(provider)
		require.Empty(t, other.ResultURL(endpoint, "vid_123", map[string]any{}), "%s 交给通用解析", provider)
		require.Empty(t, other.ResultAuthorization(account, "seedance-2.0-720p"), "%s 不需要额外授权头", provider)
	}
}

// 账号 extra 的 video_provider 必须接受 mikuapi（未知取值依旧拒绝）。
func TestNormalizeVideoProviderExtraAcceptsMikuapi(t *testing.T) {
	for _, provider := range []string{"mikuapi", "MikuAPI", " mikuapi "} {
		normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: provider})
		require.NoError(t, err, "provider=%q 应被接受", provider)
		require.Equal(t, videoProviderMikuapi, normalized[VideoProviderExtraKey])
	}

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Extra: map[string]any{VideoProviderExtraKey: videoProviderMikuapi}}
	require.Equal(t, "https://mikuapi.org", videoDefaultBaseURLForProvider(videoAccountProvider(account)))
	require.Equal(t, videoMikuapiPollInterval, videoAccountDefaultDuration(account, "poll_interval_ms"))
	require.Equal(t, 15*time.Minute, videoAccountDefaultDuration(account, "poll_timeout_ms"))
}

// mikuapi 上游不稳定：轮询阶段连"查不到任务"这类不可重试错误也要先容忍若干次。
func TestMikuapiPollToleranceRetriesTransientFailures(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	require.Equal(t, videoMikuapiPollMaxConsecutiveFailures, adapter.PollMaxConsecutiveFailures())
	require.Greater(t, adapter.PollMaxConsecutiveFailures(), 1)

	// 其余上游保持既有行为（一次不可重试失败即判失败）。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		require.Equal(t, 1, videoProviderAdapterByName(provider).PollMaxConsecutiveFailures(),
			"%s 的轮询容错不应被改动", provider)
	}

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Extra: map[string]any{VideoProviderExtraKey: videoProviderMikuapi}}
	require.Equal(t, videoMikuapiPollMaxConsecutiveFailures, videoPollFailureTolerance(account))
	// 未知/空 provider 退回 aigod 的容错值。
	require.Equal(t, 1, videoPollFailureTolerance(&Account{Platform: PlatformVideo, Type: AccountTypeAPIKey}))
}

// 轮询失败是否判死的判定表：可重试错误一律继续；不可重试错误在容忍次数内继续，
// 到上限才放弃；tolerance<=1 即"一次就失败"（既有行为）。
func TestShouldAbandonVideoPoll(t *testing.T) {
	cases := []struct {
		name        string
		retryable   bool
		failures    int
		tolerance   int
		wantAbandon bool
	}{
		{"可重试错误即使超过容忍次数也继续", true, 99, 12, false},
		{"不可重试错误在容忍期内继续", false, 1, 12, false},
		{"不可重试错误在第 11 次仍继续", false, 11, 12, false},
		{"不可重试错误到第 12 次放弃", false, 12, 12, true},
		{"不可重试错误超过上限放弃", false, 13, 12, true},
		{"容忍值为 1 时一次即失败", false, 1, 1, true},
		{"容忍值为 0 视为一次即失败", false, 1, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantAbandon, shouldAbandonVideoPoll(tc.retryable, tc.failures, tc.tolerance))
		})
	}
}

type mikuapiPollTaskRepoStub struct {
	VideoTaskRepository
	task     *VideoTask
	statuses []string
}

func (r *mikuapiPollTaskRepoStub) UpdateByPublicID(_ context.Context, _ string, update VideoTaskUpdate) (*VideoTask, error) {
	if update.Status != nil {
		r.statuses = append(r.statuses, *update.Status)
		r.task.Status = *update.Status
	}
	if update.UpstreamTaskID != nil {
		r.task.UpstreamTaskID = update.UpstreamTaskID
	}
	if update.ResultVideoURL != nil {
		r.task.ResultVideoURL = update.ResultVideoURL
	}
	if update.CompletedAt != nil {
		r.task.CompletedAt = update.CompletedAt
	}
	if update.ErrorJSON != nil {
		r.task.ErrorJSON = update.ErrorJSON
	}
	return r.task, nil
}

func (r *mikuapiPollTaskRepoStub) GetByPublicID(context.Context, string) (*VideoTask, error) {
	return r.task, nil
}

type mikuapiResultPublisherStub struct {
	urls  []string
	auths []string
}

func (p *mikuapiResultPublisherStub) PublishGeneratedVideo(_ context.Context, _ TemporaryAssetOwner, _, upstreamURL string) (string, error) {
	p.urls = append(p.urls, upstreamURL)
	return "https://local.example/out.mp4", nil
}

func (p *mikuapiResultPublisherStub) PublishGeneratedVideoWithAuth(_ context.Context, _ TemporaryAssetOwner, _, upstreamURL, authorization string) (string, error) {
	p.urls = append(p.urls, upstreamURL)
	p.auths = append(p.auths, authorization)
	return "https://local.example/out.mp4", nil
}

func newMikuapiPollTestService(taskRepo VideoTaskRepository, publisher VideoResultPublisher) *VideoService {
	svc := NewVideoService(nil, taskRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.SetVideoResultPublisher(publisher)
	return svc
}

func mikuapiPollTestInput(account *Account) VideoTaskLifecycleInput {
	groupID := int64(5)
	return VideoTaskLifecycleInput{
		PublicID: "video_miku_poll",
		Account:  account,
		APIKey: &APIKey{
			ID: 9, GroupID: &groupID, Group: &Group{ID: groupID},
			User: &User{ID: 3},
		},
	}
}

// 上游前几次轮询查不到任务（404，属不可重试错误）也不能判失败：mikuapi 的容错
// 次数内继续轮询，直到真正拿到成片；成片地址按 /content 拼，并带上游 key 回捞。
func TestMikuapiPollLifecycleToleratesTransientTaskMisses(t *testing.T) {
	const transientMisses = 3
	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/task-miku-poll":
			require.Equal(t, "Bearer sk-miku-test", r.Header.Get("Authorization"))
			if atomic.AddInt32(&statusCalls, 1) <= transientMisses {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"task-miku-poll","status":"completed"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 42, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderMikuapi,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
			"poll_interval_ms":    1,
		}}

	taskRepo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll", Status: VideoTaskStatusProcessing}}
	publisher := &mikuapiResultPublisherStub{}
	svc := newMikuapiPollTestService(taskRepo, publisher)

	svc.pollLifecycle(mikuapiPollTestInput(account), "task-miku-poll")

	require.Equal(t, VideoTaskStatusCompleted, taskRepo.task.Status,
		"前 %d 次 404 不应判失败", transientMisses)
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&statusCalls)), transientMisses+1,
		"必须真的重试到成功为止")
	require.Equal(t, []string{server.URL + "/v1/videos/task-miku-poll/content"}, publisher.urls,
		"成片地址按 /content 拼")
	require.Equal(t, []string{"Bearer sk-miku-test"}, publisher.auths,
		"回捞 /content 必须带上游 key")
	require.NotNil(t, taskRepo.task.ResultVideoURL)
}

// 一直查不到任务时，重试到容错上限才判失败（而不是一次就失败）。
func TestMikuapiPollLifecycleFailsOnlyAfterToleranceExhausted(t *testing.T) {
	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/videos/task-miku-dead" {
			atomic.AddInt32(&statusCalls, 1)
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 43, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderMikuapi,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
			"poll_interval_ms":    1,
		}}

	taskRepo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll"}}
	svc := newMikuapiPollTestService(taskRepo, &mikuapiResultPublisherStub{})

	svc.pollLifecycle(mikuapiPollTestInput(account), "task-miku-dead")

	require.Equal(t, VideoTaskStatusFailed, taskRepo.task.Status)
	require.Equal(t, int32(videoMikuapiPollMaxConsecutiveFailures), atomic.LoadInt32(&statusCalls),
		"必须在第 %d 次连续失败后才判失败", videoMikuapiPollMaxConsecutiveFailures)
}

// grok-imagine 与可灵的下游模型名与上游一致（分辨率/时长都走请求体字段），
// 逐条锁住映射与档位。
func TestMikuapiGrokAndKlingUpstreamModelsAndResolutions(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)

	require.Equal(t, videoMikuapiGrokImagineVideo15PreviewModel,
		adapter.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelGrokImagineVideo15, Resolution: VideoResolution720P}))
	require.Equal(t, videoMikuapiKlingVideoV3OmniModel,
		adapter.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelKlingV3Omni, Resolution: VideoResolution720P}))

	// grok：480p/720p/1080p，没有 4K。
	require.True(t, adapter.Compatible(VideoModelGrokImagineVideo15, VideoResolution480P))
	require.True(t, adapter.Compatible(VideoModelGrokImagineVideo15, VideoResolution1080P))
	require.False(t, adapter.Compatible(VideoModelGrokImagineVideo15, VideoResolution4K))
	// 可灵 omni：720p/1080p/4K（下游规范写法大写 K，上游 4k 由请求体转小写）。
	require.True(t, adapter.Compatible(VideoModelKlingV3Omni, VideoResolution4K))
	require.True(t, adapter.Compatible(VideoModelKlingV3Omni, VideoResolution1080P))
	require.False(t, adapter.Compatible(VideoModelKlingV3Omni, VideoResolution480P))

	// 其它渠道不得认领这两个模型。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken, videoProviderJingyu} {
		require.False(t, videoProviderAdapterByName(provider).Compatible(VideoModelGrokImagineVideo15, VideoResolution720P),
			"%s 不能服务 grok-imagine", provider)
		require.False(t, videoProviderAdapterByName(provider).Compatible(VideoModelKlingV3Omni, VideoResolution720P),
			"%s 不能服务可灵", provider)
	}
}

// grok-imagine 的渠道闸门：时长 1-15；参考素材只有图片且首帧/参考图互斥；
// 画幅是 grok 的 7 档（21:9 会 422）；首帧模式不发画幅，因此不校验画幅值。
func TestMikuapiGrokChannelGate(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	request := func(content []VideoContent, ratio string, ratioProvided bool, seconds int) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: VideoModelGrokImagineVideo15, Resolution: VideoResolution720P,
			GeneratedSeconds: seconds, Content: content, Ratio: ratio, RatioProvided: ratioProvided,
		}
	}
	firstFrame := VideoContent{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/first.png"}}
	referenceImage := VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/ref.png"}}

	// 时长：1 秒可服务（seedance 的 4 秒下限不适用），0/16 拒绝。
	require.True(t, adapter.CompatibleRequest(request(nil, "", false, 1)))
	require.True(t, adapter.CompatibleRequest(request(nil, "", false, 15)))
	require.False(t, adapter.CompatibleRequest(request(nil, "", false, 0)))
	require.False(t, adapter.CompatibleRequest(request(nil, "", false, 16)))

	// 参考素材：图片可以，视频/音频一律不行；首帧与参考图混传拒绝。
	require.True(t, adapter.CompatibleRequest(request([]VideoContent{firstFrame}, "", false, 5)))
	require.True(t, adapter.CompatibleRequest(request([]VideoContent{referenceImage}, "", false, 5)))
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{firstFrame, referenceImage}, "", false, 5)),
		"两种传图模式互斥")
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{
		{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn/a.mp4"}},
	}, "", false, 5)))
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{
		{Type: "audio_url", AudioURL: &VideoContentURL{URL: "https://cdn/a.mp3"}},
	}, "", false, 5)))
	// grok 没有尾帧语义（start-end 由渠道闸门拒绝，归一化层不做特例）。
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{
		{Type: "image_url", Role: "last_frame", ImageURL: &VideoContentURL{URL: "https://cdn/end.png"}},
	}, "", false, 5)), "grok 不接受尾帧输入")

	// 画幅：文生模式必须是 7 档之一（含 grok 独有的 3:2/2:3，不含 21:9）。
	require.True(t, adapter.CompatibleRequest(request(nil, "3:2", true, 5)))
	require.True(t, adapter.CompatibleRequest(request(nil, "2:3", true, 5)))
	require.False(t, adapter.CompatibleRequest(request(nil, "21:9", true, 5)))
	// 首帧模式：画幅由适配器丢弃而不是让上游拉伸，值不再参与判定。
	require.True(t, adapter.CompatibleRequest(request([]VideoContent{firstFrame}, "21:9", true, 5)),
		"首帧模式画幅被丢弃，不应把请求排挤出该渠道")
}

// 可灵 omni 的渠道闸门：时长 3-15；画幅只有 16:9/9:16/1:1；参考素材只有图片
// 且至多 7 张；没有尾帧。
func TestMikuapiKlingChannelGate(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	request := func(content []VideoContent, ratio string, seconds int) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: VideoModelKlingV3Omni, Resolution: VideoResolution720P,
			GeneratedSeconds: seconds, Content: content, Ratio: ratio, RatioProvided: ratio != "",
		}
	}
	image := func(url string) VideoContent {
		return VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: url}}
	}

	// 时长：3 秒起（seedance 的 4 秒下限不适用），16 拒绝。
	require.True(t, adapter.CompatibleRequest(request(nil, "", 3)))
	require.True(t, adapter.CompatibleRequest(request(nil, "", 15)))
	require.False(t, adapter.CompatibleRequest(request(nil, "", 2)))
	require.False(t, adapter.CompatibleRequest(request(nil, "", 16)))

	// 画幅：三档白名单。
	require.True(t, adapter.CompatibleRequest(request(nil, "16:9", 5)))
	require.True(t, adapter.CompatibleRequest(request(nil, "9:16", 5)))
	require.True(t, adapter.CompatibleRequest(request(nil, "1:1", 5)))
	require.False(t, adapter.CompatibleRequest(request(nil, "4:3", 5)))
	require.False(t, adapter.CompatibleRequest(request(nil, "21:9", 5)))

	// 参考图：至多 7 张；视频/音频/尾帧不行。
	require.True(t, adapter.CompatibleRequest(request([]VideoContent{image("https://cdn/1.png")}, "", 5)))
	seven := make([]VideoContent, 0, 7)
	for i := 0; i < 7; i++ {
		seven = append(seven, image(fmt.Sprintf("https://cdn/%d.png", i)))
	}
	eight := append(append([]VideoContent{}, seven...), image("https://cdn/8.png"))
	require.True(t, adapter.CompatibleRequest(request(seven, "", 5)))
	require.False(t, adapter.CompatibleRequest(request(eight, "", 5)), "omni 至多 7 张参考图")
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{
		{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn/a.mp4"}},
	}, "", 5)))
	require.False(t, adapter.CompatibleRequest(request([]VideoContent{
		{Type: "image_url", Role: "last_frame", ImageURL: &VideoContentURL{URL: "https://cdn/end.png"}},
	}, "", 5)), "omni 没有尾帧语义")
}

// grok 请求体字段名与形态：首帧是 input_reference 对象、参考图是 reference_images
// 对象数组；首帧模式绝不携带 aspect_ratio（上游会非等比拉伸首帧）。
func TestMikuapiGrokBuildCreateBody(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	firstFrame := VideoContent{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/first.png"}}
	referenceImages := []VideoContent{
		{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"}},
		{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/b.png"}},
	}

	// 文生视频：显式画幅随请求下发。
	textBody := adapter.BuildCreateBody(&normalizedVideoRequest{
		Model: VideoModelGrokImagineVideo15, Prompt: "a cat running in the rain",
		Resolution: VideoResolution720P, GeneratedSeconds: 4, Ratio: "16:9", RatioProvided: true,
	}, videoMikuapiGrokImagineVideo15PreviewModel)
	require.Equal(t, videoMikuapiGrokImagineVideo15PreviewModel, textBody["model"])
	require.Equal(t, 4, textBody["seconds"])
	require.Equal(t, "720p", textBody["resolution"])
	require.Equal(t, "16:9", textBody["aspect_ratio"])
	require.NotContains(t, textBody, "input_reference")
	require.NotContains(t, textBody, "reference_images")

	// 首帧模式：input_reference 必须是对象；即使下游给了画幅也不发——上游会把
	// 首帧非等比拉伸（实测 3.2 倍），省略后输出跟随输入图比例。
	frameBody := adapter.BuildCreateBody(&normalizedVideoRequest{
		Model: VideoModelGrokImagineVideo15, Prompt: "gentle camera push in",
		Resolution: VideoResolution720P, GeneratedSeconds: 5, Ratio: "16:9", RatioProvided: true,
		Content: []VideoContent{firstFrame},
	}, videoMikuapiGrokImagineVideo15PreviewModel)
	require.Equal(t, map[string]any{"image_url": "https://cdn/first.png"}, frameBody["input_reference"],
		"input_reference 必须是对象，字符串会被 422 拒绝")
	require.NotContains(t, frameBody, "aspect_ratio", "首帧模式不能带画幅")
	require.NotContains(t, frameBody, "reference_images")

	// 参考图模式：对象数组 + 键名 image_url，画幅可以安全指定。
	referenceBody := adapter.BuildCreateBody(&normalizedVideoRequest{
		Model: VideoModelGrokImagineVideo15, Prompt: "morph through references",
		Resolution: VideoResolution480P, GeneratedSeconds: 15, Ratio: "16:9", RatioProvided: true,
		Content: referenceImages,
	}, videoMikuapiGrokImagineVideo15PreviewModel)
	require.Equal(t, []map[string]any{
		{"image_url": "https://cdn/a.png"},
		{"image_url": "https://cdn/b.png"},
	}, referenceBody["reference_images"])
	require.Equal(t, "16:9", referenceBody["aspect_ratio"])
	require.NotContains(t, referenceBody, "input_reference")

	// 顶层 image_url 会被上游静默忽略（计费但图不生效），任何模式下都不能出现。
	for _, body := range []map[string]any{textBody, frameBody, referenceBody} {
		require.NotContains(t, body, "image_url")
	}
}

// 可灵请求体：分辨率转小写（4K→4k）、秒数为整数、参考图是 {url} 对象数组、
// 首帧角色同样按参考图下发。
func TestMikuapiKlingBuildCreateBody(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	body := adapter.BuildCreateBody(&normalizedVideoRequest{
		Model: VideoModelKlingV3Omni, Prompt: "a red wooden boat drifting",
		Resolution: VideoResolution4K, GeneratedSeconds: 5, Ratio: "16:9", RatioProvided: true,
		Content: []VideoContent{
			{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/ref.png"}},
		},
	}, videoMikuapiKlingVideoV3OmniModel)
	require.Equal(t, videoMikuapiKlingVideoV3OmniModel, body["model"])
	require.Equal(t, 5, body["seconds"])
	require.Equal(t, "4k", body["resolution"], "可灵只认小写 4k")
	require.Equal(t, "16:9", body["aspect_ratio"])
	require.Equal(t, []map[string]any{{"url": "https://cdn/ref.png"}}, body["reference_images"])
	require.NotContains(t, body, "input_reference", "omni 没有首帧字段")
	require.NotContains(t, body, "image_end")

	// 未显式提供画幅时不发（上游默认 16:9，与网关默认一致）。
	noRatio := adapter.BuildCreateBody(&normalizedVideoRequest{
		Model: VideoModelKlingV3Omni, Prompt: "drift",
		Resolution: VideoResolution720P, GeneratedSeconds: 3,
	}, videoMikuapiKlingVideoV3OmniModel)
	require.NotContains(t, noRatio, "aspect_ratio")
}

// grok-imagine 的创建端点是 /v1/videos/generations；可灵与 Seedance 仍走
// /v1/videos。端点已带 /generations 时不重复追加（防止 api_path 被改写后拼出
// generations/generations）。
func TestMikuapiCreateEndpointPerModelFamily(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	base := "https://mikuapi.org/v1/videos"

	require.Equal(t, base+"/generations",
		adapter.CreateEndpoint(base, videoMikuapiGrokImagineVideo15PreviewModel))
	require.Equal(t, base, adapter.CreateEndpoint(base, videoMikuapiKlingVideoV3OmniModel))
	require.Equal(t, base, adapter.CreateEndpoint(base, videoMikuapiSeedance25Model))
	require.Equal(t, base+"/generations", adapter.CreateEndpoint(base+"/generations", videoMikuapiGrokImagineVideo15PreviewModel))

	// 其余渠道不做端点调整。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken, videoProviderJingyu} {
		require.Equal(t, base, videoProviderAdapterByName(provider).CreateEndpoint(base, videoMikuapiSeedance25Model))
	}
}

// 成片回捞的授权按模型族区分：可灵成片是可灵 CDN 公开直链，带 key 等于把
// 上游密钥发给第三方；Seedance / grok 走受保护的 /content，必须带 key。
func TestMikuapiResultAuthorizationPerModelFamily(t *testing.T) {
	adapter := videoProviderAdapterByName(videoProviderMikuapi)
	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku"}}

	require.Equal(t, "Bearer sk-miku", adapter.ResultAuthorization(account, videoMikuapiSeedance25Model))
	require.Equal(t, "Bearer sk-miku", adapter.ResultAuthorization(account, videoMikuapiGrokImagineVideo15PreviewModel))
	require.Empty(t, adapter.ResultAuthorization(account, videoMikuapiKlingVideoV3OmniModel),
		"可灵 CDN 直链必须裸取")
}

// mikuapi grok 的状态值是 pending/done/failed；可灵沿用 queued/completed。
func TestMikuapiGrokStatusNormalization(t *testing.T) {
	require.Equal(t, VideoTaskStatusCompleted, normalizeVideoUpstreamStatus("done"))
	require.Equal(t, VideoTaskStatusProcessing, normalizeVideoUpstreamStatus("pending"))
	require.Equal(t, VideoTaskStatusQueued, normalizeVideoUpstreamStatus("queued"))
	require.Equal(t, VideoTaskStatusCompleted, normalizeVideoUpstreamStatus("completed"))
}

// grok 创建任务只返回 request_id，可灵返回 id/task_id，都必须能取到任务 id。
func TestVideoTaskIDFromPayloadReadsRequestID(t *testing.T) {
	require.Equal(t, "e29c84ce", videoTaskIDFromPayload(map[string]any{"request_id": "e29c84ce"}))
	// 既有优先级不变：同时携带 id 与 request_id 时仍取 id。
	require.Equal(t, "primary", videoTaskIDFromPayload(map[string]any{
		"id": "primary", "request_id": "secondary",
	}))
}

// mikuapi grok 的轮询全链路：pending 继续等，done 后从 /content 带 key 回捞
// （状态里的 video.url 是相对路径，只作参考，地址仍按任务 id 拼）。
func TestMikuapiGrokPollLifecycleCompletesFromContentEndpoint(t *testing.T) {
	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/videos/grok-req-1" {
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "Bearer sk-miku-test", r.Header.Get("Authorization"))
		if atomic.AddInt32(&statusCalls, 1) == 1 {
			_, _ = w.Write([]byte(`{"status":"pending","progress":37}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"done","progress":100,"model":"grok-imagine-video-1.5","video":{"url":"/v1/videos/grok-req-1/content"}}`))
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 44, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderMikuapi,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
			"poll_interval_ms":    1,
		}}
	taskRepo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_grok", Status: VideoTaskStatusProcessing}}
	publisher := &mikuapiResultPublisherStub{}
	svc := newMikuapiPollTestService(taskRepo, publisher)

	input := mikuapiPollTestInput(account)
	input.UpstreamBody = map[string]any{"model": videoMikuapiGrokImagineVideo15PreviewModel}
	svc.pollLifecycle(input, "grok-req-1")

	require.Equal(t, VideoTaskStatusCompleted, taskRepo.task.Status)
	require.Equal(t, []string{server.URL + "/v1/videos/grok-req-1/content"}, publisher.urls,
		"grok 成片从 /content 回捞")
	require.Equal(t, []string{"Bearer sk-miku-test"}, publisher.auths,
		"/content 受保护，必须带上游 key")
}

// mikuapi 可灵的轮询全链路：queued 原地等待，completed 后成片是可灵 CDN 直链，
// 直接发布且不带上游 key。
func TestMikuapiKlingPollLifecyclePublishesCDNURL(t *testing.T) {
	const cdnURL = "https://v15-kling.klingai.com/bs2/upload-ylab-stunt-sgp/abc-output.mp4?x-kcdn-pid=112372"
	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/videos/kling-task-1" {
			http.NotFound(w, r)
			return
		}
		if atomic.AddInt32(&statusCalls, 1) == 1 {
			_, _ = w.Write([]byte(`{"id":"kling-task-1","status":"queued","progress":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"kling-task-1","status":"completed","progress":100,"download_url":"` + cdnURL + `","video_url":"` + cdnURL + `"}`))
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 45, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderMikuapi,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
			"poll_interval_ms":    1,
		}}
	taskRepo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_kling", Status: VideoTaskStatusProcessing}}
	publisher := &mikuapiResultPublisherStub{}
	svc := newMikuapiPollTestService(taskRepo, publisher)

	input := mikuapiPollTestInput(account)
	input.UpstreamBody = map[string]any{"model": videoMikuapiKlingVideoV3OmniModel}
	svc.pollLifecycle(input, "kling-task-1")

	require.Equal(t, VideoTaskStatusCompleted, taskRepo.task.Status)
	require.Equal(t, []string{cdnURL}, publisher.urls, "可灵成片按状态响应里的 CDN 直链发布")
	require.Empty(t, publisher.auths, "可灵 CDN 直链公开可读，不能把上游 key 发给第三方")
}

// 归一化阶段的按模型约束：时长边界（grok 1-15、可灵 3-15）与参考图数量上限。
// 这两项来自共享规格表（与 Seedance 同一机制）；上游特有约束一律不在归一化
// 层出现，由 mikuapi 适配器的渠道闸门与上游自身兜底。
func TestMikuapiNewModelsRequestValidation(t *testing.T) {
	// 时长：grok 的 1 秒可服务，可灵 2 秒越界、3 秒可服务。
	_, err := normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelGrokImagineVideo15, Prompt: "x", Duration: 1,
	})
	require.NoError(t, err, "grok 的 1 秒应通过")
	_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelKlingV3Omni, Prompt: "x", Duration: 2,
	})
	require.Error(t, err, "可灵的 2 秒应被拒绝")
	_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelKlingV3Omni, Prompt: "x", Duration: 3,
	})
	require.NoError(t, err, "可灵的 3 秒应通过")

	// 参考图数量：两模型都是 7 张（grok 实测上限，可灵 omni 文档上限）。
	refs := make([]VideoContent, 0, 8)
	for i := 0; i < 8; i++ {
		refs = append(refs, VideoContent{
			Type: "image_url", Role: "reference_image",
			ImageURL: &VideoContentURL{URL: fmt.Sprintf("https://cdn/%d.png", i)},
		})
	}
	for _, model := range []string{VideoModelGrokImagineVideo15, VideoModelKlingV3Omni} {
		_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
			Model: model, Prompt: "x", Duration: 5, Content: refs[:7],
		})
		require.NoError(t, err, "%s 的 7 张参考图应通过", model)
		_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
			Model: model, Prompt: "x", Duration: 5, Content: refs,
		})
		require.Error(t, err, "%s 的第 8 张参考图应被拒绝", model)
	}

	// 归一化层对两个新模型不再有其它特例：超长提示词等上游约束不在这里校验，
	// 与 Seedance 的行为保持一致。
	_, err = normalizeVideoCreateRequest(&VideoCreateRequest{
		Model: VideoModelGrokImagineVideo15, Prompt: strings.Repeat("a", 5000), Duration: 5,
	})
	require.NoError(t, err, "提示词长度交由上游判定，归一化层不做特例")
}

// 端到端锁死创建端点的路由：kling 必须POST 到 /v1/videos（可灵端点），
// grok-imagine 才追加 /generations。真实走一遍 createUpstreamTask，
// 捕获实际发出的 URL，防止"发到原生 grok 端点"这类回归。
func TestMikuapiCreateUpstreamTaskPostsToCorrectPath(t *testing.T) {
	var gotPath string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"probe-1"}`))
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 46, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-miku-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderMikuapi,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
		}}
	svc := newMikuapiPollTestService(nil, nil)

	// kling：上游模型名是 kling-video-v3-omni，不匹配 grok 前缀。
	_, err := svc.createUpstreamTask(context.Background(), account, map[string]any{
		"model": videoMikuapiKlingVideoV3OmniModel,
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/videos", gotPath, "可灵必须 POST 到 /v1/videos")
	require.Equal(t, "kling-video-v3-omni", gotModel)

	// grok：上游模型名带 grok-imagine 前缀，追加 /generations。
	_, err = svc.createUpstreamTask(context.Background(), account, map[string]any{
		"model": videoMikuapiGrokImagineVideo15PreviewModel,
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/videos/generations", gotPath, "grok 走 /v1/videos/generations")
	require.Equal(t, "grok-imagine-video-1.5-preview", gotModel)
}

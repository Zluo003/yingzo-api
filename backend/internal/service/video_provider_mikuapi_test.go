package service

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	require.Equal(t, "Bearer sk-miku", adapter.ResultAuthorization(account))

	// 其它上游保持"地址自带授权"的既有行为。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		other := videoProviderAdapterByName(provider)
		require.Empty(t, other.ResultURL(endpoint, "vid_123", map[string]any{}), "%s 交给通用解析", provider)
		require.Empty(t, other.ResultAuthorization(account), "%s 不需要额外授权头", provider)
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

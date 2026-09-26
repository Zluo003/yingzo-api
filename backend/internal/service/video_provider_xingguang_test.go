package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// xingguang 的上游模型名是文档固定的两档且不带分辨率后缀（清晰度走 resolution
// 字段）。逐条锁住映射；seedance-2.0-fast 上游没有对应模型，不接。
func TestXingguangProviderModelAndEndpoint(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	require.Equal(t, videoProviderXingguang, a.Provider())
	require.Equal(t, "https://xingapi.top", a.DefaultBaseURL())
	require.Equal(t, "/v1/videos", a.DefaultAPIPath())

	require.Equal(t, videoXingguangSeedance20Model,
		a.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelSeedance20, Resolution: VideoResolution720P}))
	require.Equal(t, videoXingguangSeedance25Model,
		a.UpstreamModel(nil, &normalizedVideoRequest{Model: VideoModelSeedance25, Resolution: VideoResolution480P}))

	// 未接入的模型必须没有上游名（不能用兜底拼出一个名字），fast 与未知模型不可服务。
	require.Empty(t, videoXingguangUpstreamModel(VideoModelSeedance20Fast))
	require.Empty(t, videoXingguangUpstreamModel("seedance-2-mini"))
	require.False(t, a.Compatible(VideoModelSeedance20Fast, VideoResolution720P))
	require.False(t, a.Compatible("seedance-2-mini", VideoResolution720P))
}

// 账号级 model_mapping 优先于目录映射：运营把 seedance-2.0 显式指到
// seedance2.0-933-2（第二条上游）时必须原样生效。
func TestXingguangAccountModelMappingWins(t *testing.T) {
	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":       "sk-xing",
			"model_mapping": map[string]any{VideoModelSeedance20: "seedance2.0-933-2"},
		}}
	a := videoProviderAdapterByName(videoProviderXingguang)
	require.Equal(t, "seedance2.0-933-2",
		a.UpstreamModel(account, &normalizedVideoRequest{Model: VideoModelSeedance20, Resolution: VideoResolution720P}))
	// 未映射的模型仍走目录默认值。
	require.Equal(t, videoXingguangSeedance25Model,
		a.UpstreamModel(account, &normalizedVideoRequest{Model: VideoModelSeedance25, Resolution: VideoResolution720P}))
}

// 分辨率与画幅按文档只有 480p/720p 与 16:9/9:16：共享规格表里的 1080p/4K
// xingguang 出不了片，必须在渠道闸门拒绝，让请求落到别的上游。
func TestXingguangResolutionAndRatioMatchDocumentation(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)

	require.True(t, a.Compatible(VideoModelSeedance20, VideoResolution480P))
	require.True(t, a.Compatible(VideoModelSeedance20, VideoResolution720P))
	require.False(t, a.Compatible(VideoModelSeedance20, VideoResolution1080P),
		"xingguang 文档只开放 480p/720p，1080p 必须交给别的上游")
	require.False(t, a.Compatible(VideoModelSeedance20, VideoResolution4K))
	require.True(t, a.Compatible(VideoModelSeedance25, VideoResolution720P))
	require.False(t, a.Compatible(VideoModelSeedance25, VideoResolution1080P))

	request := func(model string, ratio string, provided bool) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: model, Resolution: VideoResolution720P, GeneratedSeconds: 8,
			AbilityCode: videoAbilityReferenceToVideo,
			Content: []VideoContent{
				{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"}},
			},
			Ratio: ratio, RatioProvided: provided,
		}
	}
	require.True(t, a.CompatibleRequest(request(VideoModelSeedance20, "16:9", true)))
	require.True(t, a.CompatibleRequest(request(VideoModelSeedance25, "9:16", true)))
	require.False(t, a.CompatibleRequest(request(VideoModelSeedance25, "21:9", true)),
		"画幅 21:9 会被上游 400，必须交给别的上游")
	// 未提供画幅时不校验（上游按默认 16:9 处理）。
	require.True(t, a.CompatibleRequest(request(VideoModelSeedance20, "", false)))
}

// xingguang 当前只开放参考生视频：文生 / 图生（首帧）/ 首尾帧一律不接；参考素材
// 只有图片与音频（图至多 10 张、音频至多 3 条），参考视频未开放，音频必须伴随
// 参考图。
func TestXingguangServesReferenceToVideoOnly(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	normalized := func(ability string, content []VideoContent) *normalizedVideoRequest {
		return &normalizedVideoRequest{
			Model: VideoModelSeedance20, Resolution: VideoResolution720P,
			GeneratedSeconds: 8, AbilityCode: ability, Content: content,
		}
	}
	referenceImage := VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"}}
	secondImage := VideoContent{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/b.png"}}
	referenceAudio := VideoContent{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/a.mp3"}}
	referenceVideo := VideoContent{Type: "video_url", Role: "reference_video", VideoURL: &VideoContentURL{URL: "https://cdn/a.mp4"}}
	firstFrame := VideoContent{Type: "image_url", Role: "first_frame", ImageURL: &VideoContentURL{URL: "https://cdn/first.png"}}

	// 参考生视频：图、图+音频均可服务。
	require.True(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, []VideoContent{referenceImage})))
	require.True(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, []VideoContent{referenceImage, referenceAudio})))

	// 其余能力交给别的上游。
	require.False(t, a.CompatibleRequest(normalized(videoAbilityTextToVideo, nil)), "文生视频不支持")
	require.False(t, a.CompatibleRequest(normalized(videoAbilityImageToVideo, []VideoContent{firstFrame})), "图生视频（首帧）不支持")
	require.False(t, a.CompatibleRequest(normalized(videoAbilityStartEndToVideo, []VideoContent{
		firstFrame,
		{Type: "image_url", Role: "last_frame", ImageURL: &VideoContentURL{URL: "https://cdn/last.png"}},
	})), "首尾帧不支持")
	require.False(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, []VideoContent{referenceVideo})),
		"参考视频上游未开放")

	// 参考素材约束：音频必须伴随参考图；图至多 10 张、音频至多 3 条。
	require.False(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, []VideoContent{referenceAudio})),
		"音频必须伴随参考图")
	maxImages := make([]VideoContent, 0, xingguangMaxReferenceImages+1)
	for i := 0; i <= xingguangMaxReferenceImages; i++ {
		maxImages = append(maxImages, VideoContent{
			Type: "image_url", Role: "reference_image",
			ImageURL: &VideoContentURL{URL: "https://cdn/img" + string(rune('a'+i)) + ".png"},
		})
	}
	require.True(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, maxImages[:xingguangMaxReferenceImages])))
	require.False(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, maxImages)),
		"第 11 张参考图必须被渠道拒绝")
	maxAudios := []VideoContent{referenceImage, referenceAudio,
		{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/b.mp3"}},
		{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/c.mp3"}},
		{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/d.mp3"}},
	}
	require.True(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, maxAudios[:4])))
	require.False(t, a.CompatibleRequest(normalized(videoAbilityReferenceToVideo, maxAudios)),
		"第 4 条参考音频必须被渠道拒绝")

	// 时长按模型区分：2.0 系列 4-15 秒、2.5 为 4-30 秒。
	duration := func(model string, seconds int) *normalizedVideoRequest {
		request := normalized(videoAbilityReferenceToVideo, []VideoContent{referenceImage, secondImage})
		request.Model = model
		request.GeneratedSeconds = seconds
		return request
	}
	require.True(t, a.CompatibleRequest(duration(VideoModelSeedance20, 15)))
	require.False(t, a.CompatibleRequest(duration(VideoModelSeedance20, 16)))
	require.True(t, a.CompatibleRequest(duration(VideoModelSeedance25, 30)))
	require.False(t, a.CompatibleRequest(duration(VideoModelSeedance25, 31)))
	require.False(t, a.CompatibleRequest(duration(VideoModelSeedance25, 3)))
}

// 请求体字段名按 xingguang 文档：duration / ratio / resolution，参考素材是
// images / audios 字符串数组；参考视频上游未开放，绝不出现 videos 字段。
func TestXingguangBuildCreateBodyUsesDocumentedFields(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	normalized := &normalizedVideoRequest{
		Model:            VideoModelSeedance25,
		Prompt:           "以 @Image1 为人物参考，向镜头自然走来",
		Resolution:       "720P",
		GeneratedSeconds: 8,
		Ratio:            "9:16",
		RatioProvided:    true,
		AbilityCode:      videoAbilityReferenceToVideo,
		Content: []VideoContent{
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"}},
			{Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/b.png"}},
			{Type: "audio_url", Role: "reference_audio", AudioURL: &VideoContentURL{URL: "https://cdn/a.mp3"}},
		},
	}
	body := a.BuildCreateBody(normalized, videoXingguangSeedance25Model)

	require.Equal(t, videoXingguangSeedance25Model, body["model"])
	require.Equal(t, "以 @Image1 为人物参考，向镜头自然走来", body["prompt"])
	require.Equal(t, 8, body["duration"])
	require.Equal(t, "9:16", body["ratio"])
	require.Equal(t, "720p", body["resolution"], "分辨率走 resolution 字段，统一小写")
	require.Equal(t, []string{"https://cdn/a.png", "https://cdn/b.png"}, body["images"])
	require.Equal(t, []string{"https://cdn/a.mp3"}, body["audios"])
	// 文档之外的字段一个都不能出现。
	for _, forbidden := range []string{"seconds", "aspect_ratio", "images_url", "videos", "reference_images", "stream", "n", "response_format", "webhook_url"} {
		require.NotContains(t, body, forbidden, "xingguang 不接受字段 %s", forbidden)
	}

	// 未提供画幅时不发 ratio；没有参考素材的字段不发键。
	plain := &normalizedVideoRequest{
		Model: VideoModelSeedance20, Prompt: "x", Resolution: VideoResolution720P,
		GeneratedSeconds: 5, AbilityCode: videoAbilityReferenceToVideo,
		Content: []VideoContent{{
			Type: "image_url", Role: "reference_image", ImageURL: &VideoContentURL{URL: "https://cdn/a.png"},
		}},
	}
	plainBody := a.BuildCreateBody(plain, videoXingguangSeedance20Model)
	require.NotContains(t, plainBody, "ratio")
	require.NotContains(t, plainBody, "audios")
}

// 成片只能从受保护的 /v1/videos/{id}/content 下载：状态响应里即使出现地址字段
// 也未经文档保证可公开访问（实测无鉴权直链 401），一律忽略、按任务 id 拼端点，
// 回捞必须带上游 key。
func TestXingguangResultURLAlwaysUsesProtectedContentEndpoint(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	endpoint := "https://xingapi.top/v1/videos"

	require.Equal(t, endpoint+"/task-1"+xingguangContentPathSuffix,
		a.ResultURL(endpoint, "task-1", map[string]any{"status": "completed"}))
	// 响应内地址不可信：即使带 video_url 也按受保护端点回捞。
	require.Equal(t, endpoint+"/task-1"+xingguangContentPathSuffix,
		a.ResultURL(endpoint, "task-1", map[string]any{"video_url": "https://cdn/out.mp4"}))

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-xing"}}
	require.Equal(t, "Bearer sk-xing", a.ResultAuthorization(account, videoXingguangSeedance20Model))
	require.Empty(t, a.ResultAuthorization(&Account{Platform: PlatformVideo, Type: AccountTypeAPIKey}, videoXingguangSeedance20Model),
		"没有 key 时返回空，交由调用方裸取失败")

	// 其它上游保持既有行为：地址交给通用解析、不带额外授权头。
	for _, provider := range []string{videoProviderAigod, videoProviderNewtoken} {
		other := videoProviderAdapterByName(provider)
		require.Empty(t, other.ResultURL(endpoint, "task-1", map[string]any{}), "%s 交给通用解析", provider)
		require.Empty(t, other.ResultAuthorization(account, "sd2.0-720p"), "%s 不需要额外授权头", provider)
	}
}

// 创建端点必须 POST 到 /v1/videos/generations（对 /v1/videos 创建会 404），
// 且追加是幂等的。
func TestXingguangCreateEndpointAppendsGenerations(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	require.Equal(t, "https://xingapi.top/v1/videos/generations",
		a.CreateEndpoint("https://xingapi.top/v1/videos", videoXingguangSeedance20Model))
	require.Equal(t, "https://xingapi.top/v1/videos/generations",
		a.CreateEndpoint("https://xingapi.top/v1/videos/generations", videoXingguangSeedance20Model),
		"已带后缀的端点原样返回")
}

// 轮询容错与 mikuapi 同窗口：上游偶发查不到任务（404）或短暂 5xx，一次判失败
// 会让已扣费的任务拿不到成片。
func TestXingguangPollToleranceRetriesTransientFailures(t *testing.T) {
	a := videoProviderAdapterByName(videoProviderXingguang)
	require.Equal(t, videoXingguangPollMaxConsecutiveFailures, a.PollMaxConsecutiveFailures())
	require.Greater(t, a.PollMaxConsecutiveFailures(), 1)

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Extra: map[string]any{VideoProviderExtraKey: videoProviderXingguang}}
	require.Equal(t, videoXingguangPollMaxConsecutiveFailures, videoPollFailureTolerance(account))
	require.Equal(t, videoXingguangPollInterval, videoAccountDefaultDuration(account, "poll_interval_ms"))
	require.Equal(t, 15*time.Minute, videoAccountDefaultDuration(account, "poll_timeout_ms"))
}

// 账号 extra 的 video_provider 必须接受 xingguang（大小写/空白归一化，未知取值
// 依旧拒绝），默认 base_url 与轮询间隔来自适配器声明。
func TestNormalizeVideoProviderExtraAcceptsXingguang(t *testing.T) {
	for _, provider := range []string{"xingguang", "Xingguang", " xingguang "} {
		normalized, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: provider})
		require.NoError(t, err, "provider=%q 应被接受", provider)
		require.Equal(t, videoProviderXingguang, normalized[VideoProviderExtraKey])
	}
	_, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoProviderExtraKey: "xingguang "})
	require.NoError(t, err)

	require.True(t, videoProviderNeedsRequestCompatibility(videoProviderXingguang),
		"xingguang 收紧了画幅/参考素材/能力，agent 分组调度必须执行请求能力闸门")

	account := &Account{Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Extra: map[string]any{VideoProviderExtraKey: videoProviderXingguang}}
	require.Equal(t, "https://xingapi.top", videoDefaultBaseURLForProvider(videoAccountProvider(account)))
	require.Equal(t, "/v1/videos", videoDefaultAPIPathForProvider(videoAccountProvider(account)))
}

type xingguangPollTaskRepoStub struct {
	VideoTaskRepository
	task     *VideoTask
	statuses []string
}

func (r *xingguangPollTaskRepoStub) UpdateByPublicID(_ context.Context, _ string, update VideoTaskUpdate) (*VideoTask, error) {
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

func (r *xingguangPollTaskRepoStub) GetByPublicID(context.Context, string) (*VideoTask, error) {
	return r.task, nil
}

type xingguangResultPublisherStub struct {
	urls  []string
	auths []string
}

func (p *xingguangResultPublisherStub) PublishGeneratedVideo(_ context.Context, _ TemporaryAssetOwner, _, upstreamURL string) (string, error) {
	p.urls = append(p.urls, upstreamURL)
	return "https://local.example/out.mp4", nil
}

func (p *xingguangResultPublisherStub) PublishGeneratedVideoWithAuth(_ context.Context, _ TemporaryAssetOwner, _, upstreamURL, authorization string) (string, error) {
	p.urls = append(p.urls, upstreamURL)
	p.auths = append(p.auths, authorization)
	return "https://local.example/out.mp4", nil
}

func newXingguangPollTestService(taskRepo VideoTaskRepository, publisher VideoResultPublisher) *VideoService {
	svc := NewVideoService(nil, taskRepo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	svc.SetVideoResultPublisher(publisher)
	return svc
}

func xingguangPollTestInput(account *Account) VideoTaskLifecycleInput {
	groupID := int64(5)
	return VideoTaskLifecycleInput{
		PublicID: "video_xing_poll",
		Account:  account,
		APIKey: &APIKey{
			ID: 9, GroupID: &groupID, Group: &Group{ID: groupID},
			User: &User{ID: 3},
		},
	}
}

// 轮询全链路：状态响应里即使带 video_url（未验证可公开访问的地址）也必须从
// 受保护的 /content 回捞，并带上游 key——防止"直接用响应地址裸取"的回归。
func TestXingguangPollLifecycleCompletesFromContentEndpoint(t *testing.T) {
	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/videos/xg-task-1" {
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "Bearer sk-xing-test", r.Header.Get("Authorization"))
		if atomic.AddInt32(&statusCalls, 1) == 1 {
			_, _ = w.Write([]byte(`{"id":"xg-task-1","status":"queued","progress":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"xg-task-1","status":"completed","progress":100,"video_url":"https://cdn/unauthed.mp4"}`))
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 47, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-xing-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderXingguang,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
			"poll_interval_ms":    1,
		}}
	taskRepo := &xingguangPollTaskRepoStub{task: &VideoTask{PublicID: "video_xing_poll", Status: VideoTaskStatusProcessing}}
	publisher := &xingguangResultPublisherStub{}
	svc := newXingguangPollTestService(taskRepo, publisher)

	input := xingguangPollTestInput(account)
	input.UpstreamBody = map[string]any{"model": videoXingguangSeedance20Model}
	svc.pollLifecycle(input, "xg-task-1")

	require.Equal(t, VideoTaskStatusCompleted, taskRepo.task.Status)
	require.Equal(t, []string{server.URL + "/v1/videos/xg-task-1/content"}, publisher.urls,
		"成片固定从受保护 /content 回捞，忽略响应内的地址")
	require.Equal(t, []string{"Bearer sk-xing-test"}, publisher.auths,
		"/content 受保护，必须带上游 key")
}

// 端到端锁死创建端点的路由：真实走一遍 createUpstreamTask，捕获实际发出的
// URL，防止"发到 /v1/videos"这类回归。
func TestXingguangCreateUpstreamTaskPostsToGenerationsPath(t *testing.T) {
	var gotPath string
	var gotModel string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"xg-task-2","task_id":"xg-task-2","status":"queued"}`))
	}))
	t.Cleanup(server.Close)

	account := &Account{ID: 48, Platform: PlatformVideo, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-xing-test"},
		Extra: map[string]any{
			VideoProviderExtraKey: videoProviderXingguang,
			"base_url":            server.URL,
			"api_path":            videoDefaultAPIPath,
		}}
	svc := newXingguangPollTestService(nil, nil)

	created, err := svc.createUpstreamTask(context.Background(), account, map[string]any{
		"model": videoXingguangSeedance20Model,
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/videos/generations", gotPath, "创建必须 POST 到 /v1/videos/generations")
	require.Equal(t, videoXingguangSeedance20Model, gotModel)
	require.Equal(t, "Bearer sk-xing-test", gotAuth)
	require.Equal(t, "xg-task-2", created.ID)
}

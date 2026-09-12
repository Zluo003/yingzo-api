package service

import "strings"

const (
	// mikuapi（mikuapi.org）的 Seedance 上游模型名是固定三档，**不带分辨率后缀**：
	// 清晰度走请求体里的 resolution 字段。这与 aigod（拼后缀）和 newtoken（把分辨率
	// 编进模型名）都不同，所以映射必须写在这里，而不是复用 SeedanceUpstreamModel。
	videoMikuapiSeedance20Model     = "seedance-2-pro"   // seedance 2.0
	videoMikuapiSeedance20FastModel = "seedance-2-fast"  // seedance 2.0 fast
	videoMikuapiSeedance25Model     = "seedance-2.5-pro" // seedance 2.5
	// mikuapi 的 2.0-fast 只开放 5 秒与 10 秒两档。上游对超范围时长是"夹到允许区间"
	// 而不是报错，静默夹取会让我们按 A 时长计费、交付 B 时长的片子，因此这里必须
	// 严格判定；判定不通过时该账号在调度阶段就被排除，请求会落到别的上游。
	videoMikuapiFastSeconds5  = 5
	videoMikuapiFastSeconds10 = 10
)

// mikuapiVideoProviderAdapter 适配 mikuapi.org 的 Seedance 视频接口。
//
// 与 aigod / newtoken 的差异（只在上游侧，不影响下游请求方式）：
//   - 模型名不带分辨率，分辨率放请求体的 resolution；
//   - 参考素材字段名为 reference_images / reference_videos / reference_audios，
//     首帧 input_reference、尾帧 image_end（不与参考素材混用）；
//   - 状态查询只给状态，成片要另外取 GET /v1/videos/{id}/content。
type mikuapiVideoProviderAdapter struct{}

func (m mikuapiVideoProviderAdapter) Provider() string {
	return videoProviderMikuapi
}

func (m mikuapiVideoProviderAdapter) DefaultBaseURL() string {
	return videoDefaultMikuapiBaseURL
}

func (m mikuapiVideoProviderAdapter) DefaultAPIPath() string {
	return videoDefaultAPIPath
}

// Compatible 只按模型与分辨率判断：mikuapi 的 Seedance 三档与共享规格表一致
// （2.0 含 4K，fast 仅 480p/720p，2.5 无 4K），因此直接复用规格表。
func (m mikuapiVideoProviderAdapter) Compatible(model, resolution string) bool {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20, VideoModelSeedance20Fast, VideoModelSeedance25:
	default:
		return false
	}
	return IsSupportedVideoResolution(model, resolution)
}

func (m mikuapiVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !m.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	// 时长：2.0 系列 4-15 秒、2.5 为 4-30 秒（共享规格表），fast 另有 5/10 的限制。
	if normalized.GeneratedSeconds < videoMinDurationSeconds {
		return false
	}
	maxSeconds := videoMaxDurationSeconds
	switch normalized.Model {
	case VideoModelSeedance25:
		maxSeconds = videoSeedance25MaxDuration
	case VideoModelSeedance20Fast:
		if normalized.GeneratedSeconds != videoMikuapiFastSeconds5 && normalized.GeneratedSeconds != videoMikuapiFastSeconds10 {
			return false
		}
	}
	if normalized.GeneratedSeconds > maxSeconds {
		return false
	}
	// 画幅是渠道能力，与 aigod / newtoken 共用同一份 Seedance 白名单。
	if normalized.RatioProvided && !videoRequestRatioAllowed(normalized.Model, normalized.Ratio) {
		return false
	}
	// 首尾帧与参考素材不能混用：混着传上游按参考模式处理，首尾帧会被忽略，
	// 交付的片子与下游请求不一致。这种请求交给别的上游去服务。
	stats := inspectVideoContent(normalized.Content)
	if stats.HasReference && (stats.FirstFrameCount > 0 || stats.LastFrameCount > 0) {
		return false
	}
	return true
}

func (m mikuapiVideoProviderAdapter) UpstreamModel(account *Account, normalized *normalizedVideoRequest) string {
	if normalized == nil {
		return ""
	}
	// 账号级模型映射优先（运营显式指定什么就发什么）。
	if mappedModel := resolvedMappedVideoModel(account, normalized.Model); mappedModel != "" {
		return mappedModel
	}
	if !m.Compatible(normalized.Model, normalized.Resolution) {
		return ""
	}
	return videoMikuapiUpstreamModel(normalized.Model)
}

// videoMikuapiUpstreamModel 是 mikuapi 模型名的单一来源：分辨率不进模型名，
// 所以只按下游模型映射；未接入的模型（例如 seedance-2-mini）返回空串。
func videoMikuapiUpstreamModel(model string) string {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20:
		return videoMikuapiSeedance20Model
	case VideoModelSeedance20Fast:
		return videoMikuapiSeedance20FastModel
	case VideoModelSeedance25:
		return videoMikuapiSeedance25Model
	default:
		return ""
	}
}

func (m mikuapiVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	// 只发文档列出的字段：stream / n / response_format / webhook_url 一律不带。
	body := map[string]any{
		"model":      upstreamModel,
		"prompt":     normalized.Prompt,
		"seconds":    normalized.GeneratedSeconds,
		"resolution": normalized.Resolution,
	}
	if normalized.RatioProvided {
		body["aspect_ratio"] = normalized.Ratio
	}
	for field, urls := range mikuapiMediaReferences(normalized.Content) {
		switch field {
		case mikuapiFieldFirstFrame, mikuapiFieldLastFrame:
			body[field] = urls[0]
		default:
			body[field] = urls
		}
	}
	return body
}

// ResultURL：mikuapi 的状态响应只给状态，成片固定从 GET {endpoint}/{id}/content 取；
// 若上游将来在状态里带上地址，则优先用它。
func (m mikuapiVideoProviderAdapter) ResultURL(endpoint, taskID string, payload map[string]any) string {
	if payloadURL := videoResultURLFromPayload(payload); payloadURL != "" {
		return payloadURL
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return strings.TrimRight(endpoint, "/") + "/" + taskID + mikuapiContentPathSuffix
}

// ResultAuthorization：/content 是受保护端点，回捞成片必须带上游 key。
func (m mikuapiVideoProviderAdapter) ResultAuthorization(account *Account) string {
	if account == nil {
		return ""
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return ""
	}
	return "Bearer " + apiKey
}

// videoMikuapiPollMaxConsecutiveFailures 是 mikuapi 轮询阶段可容忍的连续失败次数。
// 该上游偶发查不到任务（404）或短暂 5xx，一次就判失败会让已扣费的任务拿不到成片；
// 按 5 秒轮询间隔，12 次约等于 1 分钟的上游抖动窗口。
const videoMikuapiPollMaxConsecutiveFailures = 12

// PollMaxConsecutiveFailures：mikuapi 上游不稳定，连"查不到任务"这类不可重试错误
// 也先容忍若干次，只有连续失败到上限才判任务失败。
func (m mikuapiVideoProviderAdapter) PollMaxConsecutiveFailures() int {
	return videoMikuapiPollMaxConsecutiveFailures
}

const (
	mikuapiFieldFirstFrame   = "input_reference"
	mikuapiFieldLastFrame    = "image_end"
	mikuapiContentPathSuffix = "/content"
)

// mikuapiMediaReferences 把下游的规范内容分组到 mikuapi 的参考素材字段：
// 首/尾帧各是标量字段，其余按类型汇总成 reference_* 数组（上游也接受单个字符串，
// 数组是文档里的主形态）。
func mikuapiMediaReferences(content []VideoContent) map[string][]string {
	fields := make(map[string][]string, 5)
	appendURL := func(field, rawURL string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		if (field == mikuapiFieldFirstFrame || field == mikuapiFieldLastFrame) && len(fields[field]) > 0 {
			return
		}
		fields[field] = append(fields[field], rawURL)
	}
	for _, item := range content {
		switch item.Type {
		case "image_url":
			if item.ImageURL == nil {
				continue
			}
			field := "reference_images"
			switch item.Role {
			case "first_frame":
				field = mikuapiFieldFirstFrame
			case "last_frame":
				field = mikuapiFieldLastFrame
			}
			appendURL(field, item.ImageURL.URL)
		case "video_url":
			if item.VideoURL == nil {
				continue
			}
			appendURL("reference_videos", item.VideoURL.URL)
		case "audio_url":
			if item.AudioURL == nil {
				continue
			}
			appendURL("reference_audios", item.AudioURL.URL)
		}
	}
	return fields
}

package service

import "strings"

const (
	// mikuapi（mikuapi.org）的 Seedance 上游模型名是固定三档，**不带分辨率后缀**：
	// 清晰度走请求体里的 resolution 字段。这与 aigod（拼后缀）和 newtoken（把分辨率
	// 编进模型名）都不同，所以映射必须写在这里，而不是复用 SeedanceUpstreamModel。
	videoMikuapiSeedance20Model     = "seedance-2-pro"   // seedance 2.0
	videoMikuapiSeedance20FastModel = "seedance-2-fast"  // seedance 2.0 fast
	videoMikuapiSeedance25Model     = "seedance-2.5-pro" // seedance 2.5
	// Grok Imagine 与可灵的下游模型名与上游一致（清晰度/时长同样走请求体字段）。
	videoMikuapiGrokImagineVideo15PreviewModel = VideoModelGrokImagineVideo15Preview
	videoMikuapiKlingVideoV3OmniModel          = VideoModelKlingVideoV3Omni
	// mikuapi 的 2.0-fast 只开放 5 秒与 10 秒两档。上游对超范围时长是"夹到允许区间"
	// 而不是报错，静默夹取会让我们按 A 时长计费、交付 B 时长的片子，因此这里必须
	// 严格判定；判定不通过时该账号在调度阶段就被排除，请求会落到别的上游。
	videoMikuapiFastSeconds5  = 5
	videoMikuapiFastSeconds10 = 10
	// grok-imagine 的创建端点是 /v1/videos/generations：POST /v1/videos 是可灵的
	// 端点，对 grok 返回 text/plain 的 404 page not found。轮询与成片地址对三个
	// 模型族统一按 GET /v1/videos/{id}（+ 可选 /content）解析，因此 mikuapi 账号
	// 的 api_path 必须保持默认 /v1/videos。
	mikuapiGrokCreatePathSuffix = "/generations"
	// kling 上游模型名前缀：可灵与 Grok/Seedance 挂在不同账号体系上，成片机制
	// 也不同（可灵给可灵 CDN 公开直链，其余走 mikuapi 受保护的 /content）。
	mikuapiKlingModelPrefix   = "kling-"
	mikuapiGrokModelPrefix    = "grok-imagine"
	mikuapiMaxReferenceImages = 7
	mikuapiKlingMinSeconds    = 3
	mikuapiKlingMaxSeconds    = 15
	mikuapiGrokMinSeconds     = 1
	mikuapiGrokMaxSeconds     = 15
)

// mikuapiVideoProviderAdapter 适配 mikuapi.org 的视频接口。同一渠道上有三套
// 完全独立的模型族（任何映射表都不要复用）：
//
//   - Seedance（本适配器最初接入的三档）：端点 /v1/videos，参考素材字段
//     reference_images / reference_videos / reference_audios，首帧 input_reference、
//     尾帧 image_end；
//   - Grok Imagine（grok-imagine-*）：创建端点是 /v1/videos/generations，参考图
//     reference_images（对象数组）与首帧 input_reference（对象）互斥，首帧模式
//     不能带 aspect_ratio（上游会把首帧非等比拉伸），状态值为 pending/done/failed，
//     成片走受保护的 GET /v1/videos/{id}/content；
//   - 可灵 Kling（kling-*）：端点 /v1/videos，画幅只有 16:9/9:16/1:1，分辨率
//     720p/1080p/4k（小写 k），状态值为 queued/completed，成片是可灵 CDN 的
//     公开直链（video_url / download_url），回捞不能带上游 key。
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

// Compatible 只按模型与分辨率判断：三档 Seedance 与共享规格表一致（2.0 含 4K，
// fast 仅 480p/720p，2.5 无 4K），Grok 与可灵的档位见 videoModelSpecs。
func (m mikuapiVideoProviderAdapter) Compatible(model, resolution string) bool {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20, VideoModelSeedance20Fast, VideoModelSeedance25,
		VideoModelGrokImagineVideo15Preview, VideoModelKlingVideoV3Omni:
	default:
		return false
	}
	return IsSupportedVideoResolution(model, resolution)
}

func (m mikuapiVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !m.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	switch strings.TrimSpace(normalized.Model) {
	case VideoModelGrokImagineVideo15Preview:
		return mikuapiGrokRequestCompatible(normalized)
	case VideoModelKlingVideoV3Omni:
		return mikuapiKlingRequestCompatible(normalized)
	default:
		return mikuapiSeedanceRequestCompatible(normalized)
	}
}

// mikuapiSeedanceRequestCompatible 是 Seedance 三档的渠道闸门（原适配器逻辑）。
func mikuapiSeedanceRequestCompatible(normalized *normalizedVideoRequest) bool {
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

// mikuapiGrokRequestCompatible 是 grok-imagine 的渠道闸门：
//   - 时长 1-15 秒（1.5-preview 在首帧与参考图两种模式下上限都是 15）；
//   - 参考素材只有图片：首帧（1 张）与参考图互斥，不接受视频/音频引用；
//   - 画幅取值必须是 grok 的 7 档之一。首帧模式不发 aspect_ratio（见
//     mikuapiGrokCreateBody），因此首帧请求的画幅值不再校验——省略后输出跟随
//     输入图比例，比让上游非等比拉伸更接近下游意图。
func mikuapiGrokRequestCompatible(normalized *normalizedVideoRequest) bool {
	if normalized.GeneratedSeconds < mikuapiGrokMinSeconds || normalized.GeneratedSeconds > mikuapiGrokMaxSeconds {
		return false
	}
	stats := inspectVideoContent(normalized.Content)
	if stats.VideoCount > 0 || stats.AudioCount > 0 || stats.LastFrameCount > 0 {
		return false
	}
	if stats.FirstFrameCount > 0 && stats.HasReference {
		return false
	}
	if stats.FirstFrameCount > 1 {
		return false
	}
	if normalized.RatioProvided && stats.FirstFrameCount == 0 && !isGrokImagineAspectRatio(normalized.Ratio) {
		return false
	}
	return true
}

// mikuapiKlingRequestCompatible 是 kling-video-v3-omni 的渠道闸门：时长 3-15 秒，
// 画幅只有 16:9/9:16/1:1，参考素材只有图片（omni 至多 7 张参考图，没有尾帧）。
func mikuapiKlingRequestCompatible(normalized *normalizedVideoRequest) bool {
	if normalized.GeneratedSeconds < mikuapiKlingMinSeconds || normalized.GeneratedSeconds > mikuapiKlingMaxSeconds {
		return false
	}
	if normalized.RatioProvided && !isKlingAspectRatio(normalized.Ratio) {
		return false
	}
	stats := inspectVideoContent(normalized.Content)
	if stats.VideoCount > 0 || stats.AudioCount > 0 || stats.LastFrameCount > 0 {
		return false
	}
	if stats.ImageCount > mikuapiMaxReferenceImages {
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

// videoMikuapiUpstreamModel 是 mikuapi 模型名的单一来源：Seedance 不带分辨率、
// Grok 与可灵与下游同名；未接入的模型（例如 seedance-2-mini）返回空串。
func videoMikuapiUpstreamModel(model string) string {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20:
		return videoMikuapiSeedance20Model
	case VideoModelSeedance20Fast:
		return videoMikuapiSeedance20FastModel
	case VideoModelSeedance25:
		return videoMikuapiSeedance25Model
	case VideoModelGrokImagineVideo15Preview:
		return videoMikuapiGrokImagineVideo15PreviewModel
	case VideoModelKlingVideoV3Omni:
		return videoMikuapiKlingVideoV3OmniModel
	default:
		return ""
	}
}

// CreateEndpoint 返回创建任务要 POST 的地址。endpoint 是账号解析出的基础端点
// （默认 .../v1/videos）：Seedance 与可灵直接用；grok-imagine 必须发到
// /v1/videos/generations。按上游模型名前缀判定，账号级映射改写后只要保留
// grok-imagine 前缀就仍走正确端点。
func (m mikuapiVideoProviderAdapter) CreateEndpoint(endpoint, upstreamModel string) string {
	if strings.HasPrefix(strings.TrimSpace(upstreamModel), mikuapiGrokModelPrefix) &&
		!strings.HasSuffix(strings.TrimRight(endpoint, "/"), mikuapiGrokCreatePathSuffix) {
		return strings.TrimRight(endpoint, "/") + mikuapiGrokCreatePathSuffix
	}
	return endpoint
}

func (m mikuapiVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	switch strings.TrimSpace(normalized.Model) {
	case VideoModelGrokImagineVideo15Preview:
		return mikuapiGrokCreateBody(normalized, upstreamModel)
	case VideoModelKlingVideoV3Omni:
		return mikuapiKlingCreateBody(normalized, upstreamModel)
	default:
		return mikuapiSeedanceCreateBody(normalized, upstreamModel)
	}
}

// mikuapiSeedanceCreateBody 组装 Seedance 请求体：只发文档列出的字段，
// stream / n / response_format / webhook_url 一律不带。
func mikuapiSeedanceCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
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

// mikuapiGrokCreateBody 组装 grok-imagine 请求体：
//   - 首帧模式用 input_reference 对象（{"image_url": ...}）；参考图模式用
//     reference_images 对象数组。两者互斥，混传上游会拒绝；顶层 image_url 会被
//     上游静默忽略（任务照常计费但图不生效），绝不能发；
//   - aspect_ratio 只在文生/参考图模式随请求发送：首帧模式一旦携带，上游会把
//     首帧非等比拉伸填满目标画幅（实测 3.2 倍），因此一律省略，让输出跟随输入图。
func mikuapiGrokCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	body := map[string]any{
		"model":      upstreamModel,
		"prompt":     normalized.Prompt,
		"seconds":    normalized.GeneratedSeconds,
		"resolution": normalized.Resolution,
	}
	stats := inspectVideoContent(normalized.Content)
	if stats.FirstFrameCount > 0 && !stats.HasReference {
		if firstFrame := firstVideoImageURL(normalized.Content, "first_frame"); firstFrame != "" {
			body["input_reference"] = map[string]any{"image_url": firstFrame}
		}
	} else if refs := mikuapiImageReferences(normalized.Content); len(refs) > 0 {
		body["reference_images"] = refs
	}
	if normalized.RatioProvided && stats.FirstFrameCount == 0 && isGrokImagineAspectRatio(normalized.Ratio) {
		body["aspect_ratio"] = normalized.Ratio
	}
	return body
}

// mikuapiKlingCreateBody 组装可灵请求体：分辨率只认 720p/1080p/4k（下游规范
// 写法是大写 4K，必须转小写），参考图是 reference_images 对象数组（omni 至多
// 7 张，首帧角色同样按参考图下发）。秒数与画幅在可服务范围内始终显式下发，
// 不依赖上游默认值（4K/16:9 是最贵的一档，不能靠默认值兜底）。
func mikuapiKlingCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	body := map[string]any{
		"model":      upstreamModel,
		"prompt":     normalized.Prompt,
		"seconds":    normalized.GeneratedSeconds,
		"resolution": strings.ToLower(normalized.Resolution),
	}
	if normalized.RatioProvided && isKlingAspectRatio(normalized.Ratio) {
		body["aspect_ratio"] = normalized.Ratio
	}
	if refs := mikuapiKlingImageReferences(normalized.Content); len(refs) > 0 {
		body["reference_images"] = refs
	}
	return body
}

// ResultURL：Seedance 与 Grok 的状态响应只给状态（Grok 把地址放在 video.url 里
// 且是相对路径），成片固定从 GET {endpoint}/{id}/content 取；可灵在状态里给出
// 可灵 CDN 直链，通用解析会先取到它。若上游将来在状态里带上地址，则优先用。
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

// ResultAuthorization：Seedance / Grok 的成片走 mikuapi 受保护的 /content 端点，
// 回捞必须带上游 key；可灵的成片是可灵 CDN 公开直链，带 key 等于把上游密钥
// 发给第三方，必须裸取。
func (m mikuapiVideoProviderAdapter) ResultAuthorization(account *Account, upstreamModel string) string {
	if strings.HasPrefix(strings.TrimSpace(upstreamModel), mikuapiKlingModelPrefix) {
		return ""
	}
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

// mikuapiMediaReferences 把下游的规范内容分组到 mikuapi Seedance 的参考素材字段：
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

// mikuapiImageReferences 收集参考图（对象数组，键名 image_url）：Grok 参考图
// 模式的入参形态，首/尾帧角色不属于参考图。
func mikuapiImageReferences(content []VideoContent) []map[string]any {
	var refs []map[string]any
	for _, item := range content {
		if item.Type != "image_url" || item.ImageURL == nil {
			continue
		}
		if item.Role == "first_frame" || item.Role == "last_frame" {
			continue
		}
		rawURL := strings.TrimSpace(item.ImageURL.URL)
		if rawURL == "" {
			continue
		}
		refs = append(refs, map[string]any{"image_url": rawURL})
	}
	return refs
}

// mikuapiKlingImageReferences 收集可灵的参考图（对象数组，键名 url；文档写明
// url / image_url 两种键名都可受理）。omni 没有首尾帧语义，所有图片一律按
// 参考图下发。
func mikuapiKlingImageReferences(content []VideoContent) []map[string]any {
	var refs []map[string]any
	for _, item := range content {
		if item.Type != "image_url" || item.ImageURL == nil {
			continue
		}
		rawURL := strings.TrimSpace(item.ImageURL.URL)
		if rawURL == "" {
			continue
		}
		refs = append(refs, map[string]any{"url": rawURL})
	}
	return refs
}

// firstVideoImageURL 返回第一个指定角色的图片地址；没有时返回空串。
func firstVideoImageURL(content []VideoContent, role string) string {
	for _, item := range content {
		if item.Type != "image_url" || item.ImageURL == nil || item.Role != role {
			continue
		}
		if rawURL := strings.TrimSpace(item.ImageURL.URL); rawURL != "" {
			return rawURL
		}
	}
	return ""
}

// isGrokImagineAspectRatio 报告 ratio 是否在 grok-imagine 的 7 档画幅白名单内
// （21:9 不在其中，上游会 422；3:2 / 2:3 是 grok 独有，Seedance 渠道不支持）。
func isGrokImagineAspectRatio(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3":
		return true
	default:
		return false
	}
}

// isKlingAspectRatio 报告 ratio 是否在可灵的 3 档画幅白名单内。
func isKlingAspectRatio(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "16:9", "9:16", "1:1":
		return true
	default:
		return false
	}
}

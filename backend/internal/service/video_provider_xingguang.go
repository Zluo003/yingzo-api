package service

import (
	"strings"
	"time"
)

// xingguang（xingapi.top）视频适配器：全部常量与逻辑自包含于本文件，共享文件
// 只保留一行式接线（videoProviderAdapterForAccount / videoProviderAdapterByName、
// supportedVideoProviders、videoAccountProvider、videoAccountDefaultDuration、
// videoProviderNeedsRequestCompatibility）。
//
// 上游文档（XingAPI 视频模型对接文档）只开放两个模型，上游模型名不带分辨率
// 后缀（清晰度走请求体 resolution 字段，与 mikuapi 同形态）：
//   - Seedance 2.0 → seedance2.0-933（另有 seedance2.0-933-2，由账号 model_mapping
//     显式指定，运营可用它给 2.0 建第二条上游）；
//   - Seedance 2.5 → seedance2.5。
// seedance-2.0-fast 上游没有对应模型，不接（Compatible 返回 false）。
//
// 上游当前只开放参考生视频：参考素材只有公网图片（至多 10 张，数组顺序对应
// @Image1、@Image2）与参考音频（至多 3 条，且必须伴随参考图），参考视频未开放；
// 分辨率文档只给出 480p/720p；画幅只有 16:9/9:16。
const (
	videoProviderXingguang = "xingguang"

	// 账号 base_url 留空时使用的文档基础地址。
	videoDefaultXingguangBaseURL = "https://xingapi.top"

	videoXingguangSeedance20Model = "seedance2.0-933"
	videoXingguangSeedance25Model = "seedance2.5"

	// 创建端点是 /v1/videos/generations（文档公共路径族是 /v1/videos：轮询
	// GET /v1/videos/{id}、成片 GET /v1/videos/{id}/content），因此账号 api_path
	// 必须保持默认 /v1/videos，创建路径在这里追加后缀。
	xingguangCreatePathSuffix  = "/generations"
	xingguangContentPathSuffix = "/content"

	xingguangMaxReferenceImages = 10
	xingguangMaxReferenceAudios = 3

	// 轮询容错与 mikuapi 同窗口：上游异步任务偶发查不到（404）或短暂 5xx，一次
	// 判失败会让已扣费的任务拿不到成片；按 5 秒一轮，12 次约 1 分钟抖动窗口。
	videoXingguangPollInterval               = 5 * time.Second
	videoXingguangPollMaxConsecutiveFailures = 12
)

type xingguangVideoProviderAdapter struct{}

func (x xingguangVideoProviderAdapter) Provider() string {
	return videoProviderXingguang
}

func (x xingguangVideoProviderAdapter) DefaultBaseURL() string {
	return videoDefaultXingguangBaseURL
}

func (x xingguangVideoProviderAdapter) DefaultAPIPath() string {
	return videoDefaultAPIPath
}

// Compatible 只接 seedance-2.0 与 seedance-2.5，分辨率按文档只开放 480p/720p：
// 共享规格表里的 1080p/4K xingguang 出不了片，必须在渠道闸门拒绝，让请求落到
// 别的上游，而不是到上游才失败。
func (x xingguangVideoProviderAdapter) Compatible(model, resolution string) bool {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20, VideoModelSeedance25:
	default:
		return false
	}
	resolution = strings.TrimSpace(resolution)
	return strings.EqualFold(resolution, VideoResolution480P) ||
		strings.EqualFold(resolution, VideoResolution720P)
}

// CompatibleRequest 是 xingguang 的渠道闸门：
//   - 仅参考生视频：文生 / 图生（首帧）/ 首尾帧能力一律不接，交给别的上游；
//   - 时长按模型区分：2.0 系列 4-15 秒、2.5 为 4-30 秒（与共享规格表一致，上游
//     对超范围时长是夹取而不是报错，静默夹取会按 A 时长计费交付 B 时长）；
//   - 画幅是渠道能力：文档只开放 16:9 与 9:16（2.5 的 auto 哨兵同样不在其中）；
//   - 参考素材只有图片与音频，参考视频未开放；音频必须伴随参考图（文档示例中
//     参考音频始终与参考图同传）。
func (x xingguangVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !x.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	maxSeconds := videoMaxDurationSeconds
	if normalized.Model == VideoModelSeedance25 {
		maxSeconds = videoSeedance25MaxDuration
	}
	if normalized.GeneratedSeconds < videoMinDurationSeconds || normalized.GeneratedSeconds > maxSeconds {
		return false
	}
	if normalized.RatioProvided && !isXingguangAspectRatio(normalized.Ratio) {
		return false
	}
	if normalized.AbilityCode != videoAbilityReferenceToVideo {
		return false
	}
	stats := inspectVideoContent(normalized.Content)
	if stats.VideoCount > 0 {
		return false
	}
	if stats.ImageCount > xingguangMaxReferenceImages || stats.AudioCount > xingguangMaxReferenceAudios {
		return false
	}
	// 至少 1 个参考素材，且参考音频必须伴随参考图（视频已拒绝，等价于必须有图）。
	if stats.ImageCount+stats.VideoCount == 0 {
		return false
	}
	return true
}

func (x xingguangVideoProviderAdapter) UpstreamModel(account *Account, normalized *normalizedVideoRequest) string {
	if normalized == nil {
		return ""
	}
	// 账号级模型映射优先（运营显式指定什么就发什么，例如把 seedance-2.0 指到
	// seedance2.0-933-2）。
	if mappedModel := resolvedMappedVideoModel(account, normalized.Model); mappedModel != "" {
		return mappedModel
	}
	if !x.Compatible(normalized.Model, normalized.Resolution) {
		return ""
	}
	return videoXingguangUpstreamModel(normalized.Model)
}

// videoXingguangUpstreamModel 是 xingguang 上游模型名的单一来源：不带分辨率
// 后缀，未接入的模型（seedance-2.0-fast 等）返回空串。
func videoXingguangUpstreamModel(model string) string {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20:
		return videoXingguangSeedance20Model
	case VideoModelSeedance25:
		return videoXingguangSeedance25Model
	default:
		return ""
	}
}

// BuildCreateBody 组装 xingguang 请求体：只发文档列出的公共字段
// （model / prompt / duration / ratio / resolution / images / audios）。
// 参考视频上游未开放，CompatibleRequest 已挡掉，请求体不会出现 videos 字段。
func (x xingguangVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	body := map[string]any{
		"model":      upstreamModel,
		"prompt":     normalized.Prompt,
		"duration":   normalized.GeneratedSeconds,
		"resolution": strings.ToLower(normalized.Resolution),
	}
	if normalized.RatioProvided {
		body["ratio"] = normalized.Ratio
	}
	if images := xingguangReferenceImages(normalized.Content); len(images) > 0 {
		body["images"] = images
	}
	if audios := xingguangReferenceAudios(normalized.Content); len(audios) > 0 {
		body["audios"] = audios
	}
	return body
}

// xingguangReferenceImages 收集参考图公网直链（数组顺序对应上游的 @Image1、
// @Image2）。首尾帧角色不属于参考生视频（渠道闸门已拒绝），能走到这里的图片
// 一律按参考图下发。
func xingguangReferenceImages(content []VideoContent) []string {
	var urls []string
	for _, item := range content {
		if item.Type != "image_url" || item.ImageURL == nil {
			continue
		}
		if rawURL := strings.TrimSpace(item.ImageURL.URL); rawURL != "" {
			urls = append(urls, rawURL)
		}
	}
	return urls
}

// xingguangReferenceAudios 收集参考音频公网直链。
func xingguangReferenceAudios(content []VideoContent) []string {
	var urls []string
	for _, item := range content {
		if item.Type != "audio_url" || item.AudioURL == nil {
			continue
		}
		if rawURL := strings.TrimSpace(item.AudioURL.URL); rawURL != "" {
			urls = append(urls, rawURL)
		}
	}
	return urls
}

// ResultURL：文档写明成片只能从受保护的 GET /v1/videos/{id}/content 下载；状态
// 响应里即使出现地址字段也未保证可公开访问（实测无鉴权直链 401），一律按任务
// id 拼受保护端点，忽略响应内的地址。
func (x xingguangVideoProviderAdapter) ResultURL(endpoint, taskID string, _ map[string]any) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return strings.TrimRight(endpoint, "/") + "/" + taskID + xingguangContentPathSuffix
}

// ResultAuthorization：/content 受保护，回捞必须带上游 key（Bearer）。
func (x xingguangVideoProviderAdapter) ResultAuthorization(account *Account, _ string) string {
	if account == nil {
		return ""
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return ""
	}
	return "Bearer " + apiKey
}

// CreateEndpoint：创建必须 POST 到 /v1/videos/generations（对 /v1/videos 创建
// 会 404）。幂等：已带后缀的端点原样返回，账号级映射改写不影响判定。
func (x xingguangVideoProviderAdapter) CreateEndpoint(endpoint, _ string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, xingguangCreatePathSuffix) {
		return endpoint
	}
	return endpoint + xingguangCreatePathSuffix
}

// PollMaxConsecutiveFailures：xingguang 上游不稳定，连"查不到任务"这类不可重试
// 错误也先容忍若干次，只有连续失败到上限才判任务失败。
func (x xingguangVideoProviderAdapter) PollMaxConsecutiveFailures() int {
	return videoXingguangPollMaxConsecutiveFailures
}

// isXingguangAspectRatio 报告 ratio 是否在 xingguang 的两档画幅白名单内
// （文档只开放 16:9 与 9:16；21:9 等其余取值上游会 400）。
func isXingguangAspectRatio(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "16:9", "9:16":
		return true
	default:
		return false
	}
}

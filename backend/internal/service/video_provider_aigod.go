package service

import "strings"

type aigodVideoProviderAdapter struct{}

func (a aigodVideoProviderAdapter) Provider() string {
	return videoProviderAigod
}

func (a aigodVideoProviderAdapter) DefaultBaseURL() string {
	return videoDefaultBaseURL
}

func (a aigodVideoProviderAdapter) DefaultAPIPath() string {
	return videoDefaultAPIPath
}

func (a aigodVideoProviderAdapter) Compatible(model, resolution string) bool {
	model = strings.TrimSpace(model)
	resolution = strings.TrimSpace(resolution)
	// aigod only carries the Seedance family. Without this guard the shared
	// resolution table would make the default adapter claim every video model
	// and pull requests onto accounts that cannot serve them.
	switch model {
	case VideoModelSeedance20, VideoModelSeedance20Fast, VideoModelSeedance25:
	default:
		return false
	}
	// 分辨率上限交给共享规格表：只有 seedance-2.0 的官方档位含 4K，
	// 2.0-fast / 2.5 自然会被 IsSupportedVideoResolution 挡掉。
	// （曾在此额外硬拒 4K，但 aigod 目录里确实有 seedance-2.0-4k。）
	return IsSupportedVideoResolution(model, resolution)
}

// CompatibleRequest 校验 aigod 侧的请求约束。
//
// 时长不在这里判断：它是按模型定义的（seedance-2.0 系列 4-15 秒，2.5 为 4-30 秒），
// 由共享的 videoModelSpecs 在请求归一化阶段统一校验（两条路径都会经过
// normalizeVideoCreateRequestWithDynamicModel），因此按 provider 再收紧一次只会
// 错误地砍掉 2.5 的 16-30 秒。
//
// 画幅白名单则是渠道约束、规格表里没有，必须在这里拦：非法取值会被上游直接 400，
// 白白消耗一次调度与任务创建。
func (a aigodVideoProviderAdapter) CompatibleRequest(normalized *normalizedVideoRequest) bool {
	if normalized == nil || !a.Compatible(normalized.Model, normalized.Resolution) {
		return false
	}
	// 时长与画幅都是渠道能力，在这里按渠道声明。当前 aigod 与 newtoken 的取值范围
	// 相同，但将来出现"同模型、不同范围"的渠道时，就在这里各写各的。
	//
	// 时长按模型区分：seedance-2.0 系列 4-15 秒，2.5 为 4-30 秒。
	maxSeconds := videoMaxDurationSeconds
	if normalized.Model == VideoModelSeedance25 {
		maxSeconds = videoSeedance25MaxDuration
	}
	if normalized.GeneratedSeconds < videoMinDurationSeconds || normalized.GeneratedSeconds > maxSeconds {
		return false
	}
	// 只在调用方显式给出画幅时才校验：未提供时上游按自己的默认值处理，
	// 归一化阶段也已补上默认 16:9。
	if normalized.RatioProvided && !videoRequestRatioAllowed(normalized.Model, normalized.Ratio) {
		return false
	}
	return true
}

func (a aigodVideoProviderAdapter) UpstreamModel(account *Account, normalized *normalizedVideoRequest) string {
	if normalized == nil {
		return ""
	}
	// 账号级模型映射优先：运营显式指定的上游模型名照旧生效，即使它超出了
	// 下面的目录判定。
	if mappedModel := resolvedMappedVideoModel(account, normalized.Model); mappedModel != "" {
		return mappedModel
	}
	// 与 Compatible 保持一致：目录外的组合（如 4K）不能凭空拼出一个上游模型名。
	// newtoken 的适配器同样以空串表示"此组合不可服务"，两个适配器行为要统一，
	// 否则调用方无法用同一种方式判断。
	if !a.Compatible(normalized.Model, normalized.Resolution) {
		return ""
	}
	return SeedanceUpstreamModel(normalized.Model, strings.ToLower(normalized.Resolution))
}

func (a aigodVideoProviderAdapter) BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any {
	if normalized == nil {
		return nil
	}
	return normalized.UpstreamBody(upstreamModel)
}

// ResultURL：aigod 的状态响应自带成片地址，交给通用解析。
func (a aigodVideoProviderAdapter) ResultURL(string, string, map[string]any) string {
	return ""
}

// ResultAuthorization：aigod 的成片地址是公开/预签名地址，不需要额外授权头。
func (a aigodVideoProviderAdapter) ResultAuthorization(*Account, string) string {
	return ""
}

// CreateEndpoint：aigod 的创建端点不区分模型族。
func (a aigodVideoProviderAdapter) CreateEndpoint(endpoint, _ string) string {
	return endpoint
}

// PollMaxConsecutiveFailures：保持既有轮询容错（可重试错误继续重试，
// 其余错误一次即判失败）。
func (a aigodVideoProviderAdapter) PollMaxConsecutiveFailures() int {
	return 1
}

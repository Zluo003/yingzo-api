package service

import "strings"

type videoProviderAdapter interface {
	Provider() string
	DefaultBaseURL() string
	DefaultAPIPath() string
	Compatible(model, resolution string) bool
	CompatibleRequest(normalized *normalizedVideoRequest) bool
	UpstreamModel(account *Account, normalized *normalizedVideoRequest) string
	BuildCreateBody(normalized *normalizedVideoRequest, upstreamModel string) map[string]any
	// ResultURL 决定成片地址：返回空串表示"地址在上游状态响应里"，交给通用解析
	// （videoResultURLFromPayload）；mikuapi 的状态响应不带地址，需要按任务 id 拼
	// /v1/videos/{id}/content。
	ResultURL(endpoint, taskID string, payload map[string]any) string
	// ResultAuthorization 返回回捞成片时要带的 Authorization 头；空串表示成片地址
	// 自带授权（预签名）或公开可读。
	ResultAuthorization(account *Account) string
	// PollMaxConsecutiveFailures 是轮询阶段可容忍的连续失败次数：<=1 表示沿用既有
	// 行为（可重试错误继续重试，其余错误一次即判失败）；>1 表示连不可重试的错误
	// （例如上游暂时查不到任务返回 404）也先重试到该次数再放弃。
	PollMaxConsecutiveFailures() int
}

func videoProviderAdapterForAccount(account *Account) videoProviderAdapter {
	switch videoAccountProvider(account) {
	case videoProviderNewtoken:
		return newtokenVideoProviderAdapter{}
	case videoProviderMikuapi:
		return mikuapiVideoProviderAdapter{}
	default:
		return aigodVideoProviderAdapter{}
	}
}

func videoProviderAdapterByName(provider string) videoProviderAdapter {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case videoProviderNewtoken:
		return newtokenVideoProviderAdapter{}
	case videoProviderMikuapi:
		return mikuapiVideoProviderAdapter{}
	default:
		return aigodVideoProviderAdapter{}
	}
}

// videoPollFailureTolerance 返回该账号在轮询阶段可容忍的连续失败次数（至少 1）。
func videoPollFailureTolerance(account *Account) int {
	tolerance := videoProviderAdapterForAccount(account).PollMaxConsecutiveFailures()
	if tolerance <= 0 {
		return 1
	}
	return tolerance
}

// videoResultURLForAccount 让适配器先决定成片地址；适配器不关心时回落到通用解析。
func videoResultURLForAccount(account *Account, endpoint, taskID string, payload map[string]any) string {
	if resultURL := videoProviderAdapterForAccount(account).ResultURL(endpoint, taskID, payload); resultURL != "" {
		return resultURL
	}
	return videoResultURLFromPayload(payload)
}

func resolvedMappedVideoModel(account *Account, downstreamModel string) string {
	if account == nil {
		return ""
	}
	mappedModel, matched := account.ResolveMappedModel(strings.TrimSpace(downstreamModel))
	if !matched {
		return ""
	}
	mappedModel = strings.TrimSpace(mappedModel)
	if mappedModel == "" || mappedModel == strings.TrimSpace(downstreamModel) {
		return ""
	}
	return mappedModel
}

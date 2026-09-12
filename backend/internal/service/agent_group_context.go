package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// isAgentGroupContext 判断请求上下文里挂的是不是系统内置聚合分组（Yingzo Agent）。
//
// 为什么单独抽出来：聚合分组会把多个 provider 的账号放进同一个分组，分组的
// platform 字段（openai）并不代表本次请求真正要用的 provider——provider 由入口
// 协议强制指定（/v1/messages→anthropic、/v1beta→gemini、/v1/responses→openai、
// /v1/videos→video）。因此这类分组必须绕开全局调度快照的"按分组分桶"结果，
// 直接按 provider 查账号。
//
// 这里刻意不复用 IsGroupContextValid：那条闸门要求分组来自可信仓库加载
// （Hydrated）且 id 匹配，而本判断只关心"ctx 里这个分组是不是 agent 分组"。
func isAgentGroupContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	return ok && group.IsAgent()
}

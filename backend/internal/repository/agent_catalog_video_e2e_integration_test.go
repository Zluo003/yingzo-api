//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 端到端盯住"绑定视频账号 → 同步目录 → 配好每秒单价 → 解析出价格"这条真实链路：
// 单元测试用的是内存仓库，覆盖不到 agent_group_models 的平台 CHECK、价格表唯一键
// 以及仓库 SQL 的组合效果。
func TestAgentCatalogDiscoversVideoModelsAndResolvesConfiguredPrice(t *testing.T) {
	ctx := context.Background()
	groupRepo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	modelRepo := NewAgentModelRepository(integrationDB)
	catalog := service.NewAgentModelCatalogService(accountRepo, groupRepo, modelRepo)

	// 迁移 245 之后全库只允许一条存活 agent 分组（唯一索引兜底），测试直接复用
	// 迁移种入的系统分组；本用例创建的模型/价格在结束时清空，保证用例间互不影响。
	groupID := seededSystemAgentGroupID(t)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM agent_group_models WHERE group_id = $1`, groupID)
	})

	var accountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ('agent video account', 'video', 'apikey', 'active', TRUE,
			'{"model_mapping":{"seedance-2.5":"seedance-2.5","seedance-2.0-fast":"seedance-2.0-fast"}}'::jsonb,
			'{}'::jsonb)
		RETURNING id
	`).Scan(&accountID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, accountID)
	})

	// 未绑定前同步不出任何模型：目录只认"分组内可调度账号"。
	config, err := catalog.Sync(ctx, groupID)
	require.NoError(t, err)
	require.Empty(t, config.Models)

	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 1, NOW())
	`, accountID, groupID)
	require.NoError(t, err)

	config, err = catalog.Sync(ctx, groupID)
	require.NoError(t, err)
	require.Len(t, config.Models, 2)
	byCode := map[string]service.AgentGroupModel{}
	for _, model := range config.Models {
		require.Equal(t, service.PlatformVideo, model.Platform)
		require.Equal(t, service.AgentMediaTypeVideo, model.MediaType)
		byCode[model.ModelCode] = model
	}
	model, ok := byCode["seedance-2.5"]
	require.True(t, ok, "the account's model_mapping must be discovered as-is")

	// 视频模型必须按分辨率给每秒单价；媒体模型不允许挂文本倍率。
	rate := 1.5
	_, err = catalog.UpdateModel(ctx, groupID, model.ID, service.AgentModelConfigInput{
		MediaType: service.AgentMediaTypeVideo, Enabled: true, RateMultiplier: &rate,
	})
	require.Error(t, err)

	_, err = catalog.UpdateModel(ctx, groupID, model.ID, service.AgentModelConfigInput{
		MediaType: service.AgentMediaTypeVideo, Enabled: true,
		Prices: []service.AgentModelPrice{
			{Resolution: "720p", UnitPrice: 0.2},
			{Resolution: "1080p", UnitPrice: 0.35},
		},
	})
	require.NoError(t, err)

	unitPrice, modelCode, err := catalog.ResolveMediaUnitPrice(
		ctx, groupID, service.PlatformVideo, service.AgentMediaTypeVideo, "1080p", "seedance-2.5",
	)
	require.NoError(t, err)
	require.InDelta(t, 0.35, unitPrice, 1e-9)
	require.Equal(t, "seedance-2.5", modelCode)

	// 没配价格的档位必须失败，避免静默按 0 计费。
	_, _, err = catalog.ResolveMediaUnitPrice(
		ctx, groupID, service.PlatformVideo, service.AgentMediaTypeVideo, "480p", "seedance-2.5",
	)
	require.ErrorIs(t, err, service.ErrVideoPricingRuleNotFound)
}

// 国产供应商（deepseek/glm/kimi/minimax）账号也要能被聚合：目录发现、平台解析、
// 按模型倍率解析都要走通真实数据库写入路径。
func TestAgentCatalogDiscoversCNProviderModelsAndResolvesTextRate(t *testing.T) {
	ctx := context.Background()
	groupRepo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	modelRepo := NewAgentModelRepository(integrationDB)
	catalog := service.NewAgentModelCatalogService(accountRepo, groupRepo, modelRepo)

	// 迁移 245 之后全库只允许一条存活 agent 分组（唯一索引兜底），测试直接复用
	// 迁移种入的系统分组；本用例创建的模型/价格在结束时清空，保证用例间互不影响。
	groupID := seededSystemAgentGroupID(t)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM agent_group_models WHERE group_id = $1`, groupID)
	})

	var accountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ('agent deepseek account', 'deepseek', 'apikey', 'active', TRUE,
			'{"model_mapping":{"deepseek-v4-pro":"deepseek-v4-pro","deepseek-flash":"deepseek-flash"}}'::jsonb,
			'{}'::jsonb)
		RETURNING id
	`).Scan(&accountID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 1, NOW())
	`, accountID, groupID)
	require.NoError(t, err)

	config, err := catalog.Sync(ctx, groupID)
	require.NoError(t, err)
	require.Len(t, config.Models, 2)
	byCode := map[string]service.AgentGroupModel{}
	for _, model := range config.Models {
		require.Equal(t, service.PlatformDeepseek, model.Platform)
		require.Equal(t, service.AgentMediaTypeText, model.MediaType)
		byCode[model.ModelCode] = model
	}

	// 入口要能按模型解析出 deepseek，否则请求会被派到 openai 账号池。
	platform, found, err := catalog.ResolveModelPlatform(ctx, groupID, "deepseek-v4-pro")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, service.PlatformDeepseek, platform)

	model := byCode["deepseek-v4-pro"]
	rate := 1.8
	_, err = catalog.UpdateModel(ctx, groupID, model.ID, service.AgentModelConfigInput{
		MediaType: service.AgentMediaTypeText, Enabled: true, RateMultiplier: &rate,
	})
	require.NoError(t, err)

	resolved, modelCode, err := catalog.ResolveTextModelRate(ctx, groupID, service.PlatformDeepseek, "deepseek-v4-pro")
	require.NoError(t, err)
	require.InDelta(t, 1.8, resolved, 1e-9)
	require.Equal(t, "deepseek-v4-pro", modelCode)
}

// seededSystemAgentGroupID 取迁移种入的系统内置聚合分组：245 的唯一索引保证全库
// 只有一条存活 agent 分组，测试不能再自己造第二条。
func seededSystemAgentGroupID(t *testing.T) int64 {
	t.Helper()
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		SELECT id FROM groups
		WHERE kind = 'agent' AND system_code = 'yingzo' AND deleted_at IS NULL
	`).Scan(&groupID))
	return groupID
}

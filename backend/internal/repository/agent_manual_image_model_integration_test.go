//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 手工声明图片模型的真实链路：写库（含 manual 列与价格）→ 同步（账号映射里没有它）
// → 仍能取到价格。单元测试的内存仓库镜像了这些语义，这里盯的是迁移 248 的列、
// 唯一键冲突和仓库 SQL 的组合效果。
func TestAgentManualImageModelSurvivesSyncAndResolvesPrice(t *testing.T) {
	ctx := context.Background()
	groupRepo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	modelRepo := NewAgentModelRepository(integrationDB)
	catalog := service.NewAgentModelCatalogService(accountRepo, groupRepo, modelRepo)

	groupID := seededSystemAgentGroupID(t)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM agent_group_models WHERE group_id = $1`, groupID)
	})

	// 分组里有一个 Gemini 账号，但它的 model_mapping 里没有这个图片模型——
	// 这正是管理员需要手工声明的原因。
	var accountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ('agent manual image account', 'gemini', 'apikey', 'active', TRUE,
			'{"model_mapping":{"gemini-3-pro":"gemini-3-pro"}}'::jsonb, '{}'::jsonb)
		RETURNING id
	`).Scan(&accountID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 1, NOW())
	`, accountID, groupID)
	require.NoError(t, err)

	config, err := catalog.CreateManualImageModel(ctx, groupID, service.ManualImageModelInput{
		Platform: service.PlatformGemini, ModelCode: "gemini-3-pro-image", Enabled: true,
		Prices: []service.AgentModelPrice{
			{Resolution: "1K", UnitPrice: 0.3},
			{Resolution: "2K", UnitPrice: 0.5},
		},
	})
	require.NoError(t, err)

	var created *service.AgentGroupModel
	for i := range config.Models {
		if config.Models[i].ModelCode == "gemini-3-pro-image" {
			created = &config.Models[i]
		}
	}
	require.NotNil(t, created)
	require.True(t, created.Manual, "迁移 248 的 manual 列必须被读出来")
	require.Equal(t, service.AgentMediaTypeImage, created.MediaType)

	// 同步只看得到账号映射里的 gemini-3-pro：手工行不能被置为不可用。
	_, err = catalog.Sync(ctx, groupID)
	require.NoError(t, err)

	var available bool
	var manual bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT available, manual FROM agent_group_models
		WHERE group_id = $1 AND platform = 'gemini' AND model_code = 'gemini-3-pro-image'
	`, groupID).Scan(&available, &manual))
	require.True(t, available)
	require.True(t, manual)

	price, _, err := catalog.ResolveMediaUnitPrice(
		ctx, groupID, service.PlatformGemini, service.AgentMediaTypeImage, "2K", "gemini-3-pro-image")
	require.NoError(t, err)
	require.Equal(t, 0.5, price)

	// 重复声明报冲突（唯一键 group_id + platform + model_code）。
	_, err = catalog.CreateManualImageModel(ctx, groupID, service.ManualImageModelInput{
		Platform: service.PlatformGemini, ModelCode: "gemini-3-pro-image", Enabled: false,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")

	// Gemini 入口的请求前校验：图片模型只需要存在任一档位价格，不要求文本倍率。
	isImage, err := catalog.EnsureAgentImageModelPriced(ctx, groupID, service.PlatformGemini, "gemini-3-pro-image")
	require.NoError(t, err)
	require.True(t, isImage)
}

func TestAgentManualImageModelRejectsPlatformWithoutImageEndpoint(t *testing.T) {
	ctx := context.Background()
	groupRepo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	accountRepo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	catalog := service.NewAgentModelCatalogService(accountRepo, groupRepo, NewAgentModelRepository(integrationDB))

	groupID := seededSystemAgentGroupID(t)
	_, err := catalog.CreateManualImageModel(ctx, groupID, service.ManualImageModelInput{
		Platform: service.PlatformVideo, ModelCode: "seedance-2-pro", Enabled: true,
		Prices: []service.AgentModelPrice{{Resolution: "1K", UnitPrice: 0.3}},
	})
	require.ErrorContains(t, err, "cannot serve image models")
}

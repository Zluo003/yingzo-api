package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentGatewayMigrationsAreForwardOnly(t *testing.T) {
	for _, name := range []string{
		"161_yingzo_agent_foundations.sql",
		"162_yingzo_agent_generation_quotes.sql",
		"163_yingzo_temporary_asset_metadata.sql",
		"164_yingzo_agent_group_visibility.sql",
		"165_yingzo_agent_group_private.sql",
		"167_yingzo_agent_model_pricing.sql",
		"169_require_agent_image_generation.sql",
	} {
		content, err := FS.ReadFile(name)
		require.NoError(t, err)
		sql := strings.ToLower(string(content))
		require.NotContains(t, sql, "+goose down", "%s must use the repository's forward-only migration format", name)
		require.NotContains(t, sql, "drop table", "%s must not contain rollback DDL", name)
	}
}

func TestYingzoDistributionRemovalKeepsModelGatewayTables(t *testing.T) {
	content, err := FS.ReadFile("175_remove_yingzo_distribution.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(content))

	for _, table := range []string{
		"yingzo_release_upload_sessions",
		"yingzo_release_artifacts",
		"yingzo_releases",
		"agent_device_authorizations",
		"agent_installations",
	} {
		require.Contains(t, sql, "drop table if exists "+table)
	}
	require.Contains(t, sql, "update api_keys")
	require.Contains(t, sql, "system_code = 'yingzo'")
	require.Contains(t, sql, "set is_exclusive = false")
	require.NotContains(t, sql, "drop table if exists agent_model_pricing")
	require.NotContains(t, sql, "drop table if exists temporary_assets")
	require.NotContains(t, sql, "delete from groups")
}

func TestYingzoAgentPricingSimplificationRemovesOnlyGeneratedPricingState(t *testing.T) {
	content, err := FS.ReadFile("176_simplify_yingzo_agent_pricing.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(content))

	require.Contains(t, sql, "drop table if exists agent_model_pricing")
	require.Contains(t, sql, "system agent pricing #[0-9]+")
	require.Contains(t, sql, "system-managed agent model pricing")
	require.Contains(t, sql, "yingzo agent pricing #[0-9]+")
	require.Contains(t, sql, "system-managed yingzo agent model pricing")
	require.Contains(t, sql, "image_rate_multiplier = 1")
	require.NotContains(t, sql, "delete from groups")
	require.NotContains(t, sql, "drop table if exists video_group_pricing_rules")
}

func TestAgentModelCatalogMigrationKeepsPricingExplicit(t *testing.T) {
	content, err := FS.ReadFile("177_agent_model_catalog_and_pricing.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(content))

	for _, table := range []string{"agent_platform_rates", "agent_group_models", "agent_model_prices"} {
		require.Contains(t, sql, "create table if not exists "+table)
	}
	require.Contains(t, sql, "platform in ('openai', 'anthropic', 'gemini')")
	require.Contains(t, sql, "media_type in ('text', 'image', 'video')")
	require.Contains(t, sql, "billing_unit in ('image', 'second')")
	require.NotContains(t, sql, "insert into agent_platform_rates")
	require.NotContains(t, sql, "insert into agent_model_prices")
	require.NotContains(t, sql, "drop table")
}

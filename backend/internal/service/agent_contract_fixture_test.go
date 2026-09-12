package service

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type yingzoAgentContractFixture struct {
	AgentAPIContractVersion string `json:"agent_api_contract_version"`
	ImageResponseFormatURL  string `json:"image_response_format_url"`
	ModelCatalog            struct {
		SchemaVersion    int      `json:"schema_version"`
		Source           string   `json:"source"`
		CapabilityFields []string `json:"capability_fields"`
		CacheHeaders     []string `json:"cache_headers"`
	} `json:"model_catalog"`
	Endpoints                   []string `json:"endpoints"`
	VideoResponseRequiredFields []string `json:"video_response_required_fields"`
	VideoRefundStatusValues     []string `json:"video_refund_status_values"`
}

func TestYingzoAgentContractFixtureMatchesService(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/yingzo-agent-api-contract.json")
	require.NoError(t, err)
	var fixture yingzoAgentContractFixture
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, AgentAPIContractVersion, fixture.AgentAPIContractVersion)

	require.Equal(t, AgentModelCatalogSchemaVersion, fixture.ModelCatalog.SchemaVersion)
	require.Equal(t, AgentModelCatalogSource, fixture.ModelCatalog.Source)
	require.Equal(t, []string{
		"media_types", "platforms", "interfaces", "input_modalities", "output_modalities",
		"operations", "streaming", "asynchronous", "max_input_images",
		"supported_aspect_ratios", "supported_image_sizes", "supported_video_resolutions",
		"supported_video_durations_sec", "supports_video_audio", "cancellation",
	}, fixture.ModelCatalog.CapabilityFields)
	require.Equal(t, []string{
		"ETag", "X-Model-Catalog-Schema-Version", "X-Gateway-Contract-Version",
	}, fixture.ModelCatalog.CacheHeaders)
	require.Equal(t, "temporary_http_url_no_data_url_or_b64_json", fixture.ImageResponseFormatURL)
	require.Contains(t, fixture.Endpoints, "GET /v1/models")
	for _, endpoint := range []string{
		"POST /v1/responses",
		"POST /v1/chat/completions",
		"POST /v1/embeddings",
		"POST /v1/messages",
		"POST /v1/messages/count_tokens",
		"GET /v1beta/models",
		"GET /v1beta/models/:model",
		"POST /v1beta/models/:model:generateContent",
		"POST /v1beta/models/:model:streamGenerateContent",
		"HEAD /media/:id/:filename",
		"GET /media/:id/:filename",
		"POST /v1/images/generations",
		"POST /v1/images/edits",
		"POST /v1/videos",
		"GET /v1/videos/:id",
	} {
		require.Contains(t, fixture.Endpoints, endpoint)
	}
	require.NotContains(t, fixture.Endpoints, "GET /api/v1/agent/models/:id/capabilities")
	require.NotContains(t, fixture.Endpoints, "POST /api/v1/agent/media/preflight")
	require.NotContains(t, fixture.Endpoints, "DELETE /api/v1/agent/installation")
	require.Contains(t, fixture.Endpoints, "POST /api/v1/agent/assets")
	require.NotContains(t, fixture.Endpoints, "GET /api/v1/agent/assets/:id")

	encodedVideo, err := json.Marshal(videoResponseFromTask(&VideoTask{
		PublicID:  "video_contract",
		Model:     VideoModelSeedance20,
		Status:    VideoTaskStatusQueued,
		CreatedAt: time.Unix(1782700000, 0),
	}))
	require.NoError(t, err)
	var videoFields map[string]any
	require.NoError(t, json.Unmarshal(encodedVideo, &videoFields))
	for _, field := range fixture.VideoResponseRequiredFields {
		require.Contains(t, videoFields, field)
	}
	require.Equal(t, []string{
		VideoRefundStatusNotApplicable,
		VideoRefundStatusPending,
		VideoRefundStatusRefunded,
	}, fixture.VideoRefundStatusValues)
}

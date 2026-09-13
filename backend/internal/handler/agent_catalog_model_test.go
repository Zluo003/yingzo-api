package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAgentCatalogModelPublishesRoutingCapabilities(t *testing.T) {
	model := agentCatalogModel(service.AgentModelCatalogEntry{
		ID:         "seedance-custom",
		MediaTypes: []string{service.AgentMediaTypeVideo},
		Platforms:  []string{service.PlatformVideo},
		Interfaces: []string{service.AgentInterfaceSeedanceVideos},
	}, &service.AgentModelCatalogConfig{Models: []service.AgentGroupModel{{
		ModelCode: "seedance-custom",
		MediaType: service.AgentMediaTypeVideo,
		Enabled:   true,
		Available: true,
		Prices: []service.AgentModelPrice{
			{Resolution: "720p"},
			{Resolution: "1080p"},
		},
	}}})

	require.Equal(t, "gateway", model["source"])
	require.Equal(t, "advertised", model["availability"])
	require.Equal(t, "gateway", model["capability_source"])
	capabilities := model["capabilities"].(gin.H)
	require.Equal(t, []string{"seedance"}, model["platforms"])
	require.Equal(t, []string{"seedance"}, capabilities["platforms"])
	require.Equal(t, []string{"seedance.videos"}, model["interfaces"])
	require.Equal(t, []string{"video"}, capabilities["output_modalities"])
	require.Equal(t, []string{"video.generate"}, capabilities["operations"])
	require.Equal(t, []string{"720p", "1080p"}, capabilities["supported_video_resolutions"])
	require.Equal(t, true, capabilities["asynchronous"])
}

func TestAgentCatalogModelUnionsTextAndImageRoutes(t *testing.T) {
	model := agentCatalogModel(service.AgentModelCatalogEntry{
		ID:         "gpt-image-alias",
		MediaTypes: []string{service.AgentMediaTypeText, service.AgentMediaTypeImage},
		Platforms:  []string{service.PlatformOpenAI},
		Interfaces: []string{service.AgentInterfaceOpenAIResponses, service.AgentInterfaceOpenAIImages},
	}, nil)

	capabilities := model["capabilities"].(gin.H)
	require.Equal(t, []string{"text", "image"}, capabilities["input_modalities"])
	require.Equal(t, []string{"text", "image"}, capabilities["output_modalities"])
	require.Equal(t, []string{"text.generate", "image.generate", "image.edit"}, capabilities["operations"])
	require.Equal(t, false, capabilities["streaming"])
}

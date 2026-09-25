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
			{Resolution: "1080p", Enabled: boolPointer(false)},
			{Resolution: "4K", Enabled: boolPointer(true)},
		},
	}}})

	require.Equal(t, "gateway", model["source"])
	require.Equal(t, "advertised", model["availability"])
	require.Equal(t, "gateway", model["capability_source"])
	capabilities := capabilitiesOf(t, model)
	require.Equal(t, []string{"video"}, capabilities["output_modalities"])
	require.Equal(t, []string{"video.generate"}, capabilities["operations"])
	require.Equal(t, []string{"720p", "4K"}, capabilities["supported_video_resolutions"])
	require.Equal(t, true, capabilities["asynchronous"])
}

func boolPointer(value bool) *bool { return &value }

func TestAgentCatalogModelPublishesPerModelVideoDurations(t *testing.T) {
	cases := []struct {
		model      string
		minSeconds int
		maxSeconds int
	}{
		{service.VideoModelSeedance20, 4, 15},
		{service.VideoModelSeedance20Fast, 4, 15},
		{service.VideoModelSeedance25, 4, 30},
		// 账号 model_mapping 里的自定义视频模型走 legacy 兜底窗口（4-15），
		// 但无论如何都不再是固定的 4/8/15 三档。
		{"seedance-custom", 4, 15},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			model := agentCatalogModel(service.AgentModelCatalogEntry{
				ID:         tc.model,
				MediaTypes: []string{service.AgentMediaTypeVideo},
				Platforms:  []string{service.PlatformVideo},
				Interfaces: []string{service.AgentInterfaceSeedanceVideos},
			}, nil)

			capabilities := capabilitiesOf(t, model)
			durations, ok := capabilities["supported_video_durations_sec"].([]int)
			require.True(t, ok)
			require.Len(t, durations, tc.maxSeconds-tc.minSeconds+1)
			require.Equal(t, tc.minSeconds, durations[0])
			require.Equal(t, tc.maxSeconds, durations[len(durations)-1])
			require.NotEqual(t, []int{4, 8, 15}, durations)
		})
	}
}

func TestAgentCatalogModelPublishesAudioOnlyReferenceCapability(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{service.VideoModelSeedance20, false},
		{service.VideoModelSeedance20Fast, false},
		// 2.5 与 2.0 系列同规则：纯参考音频不允许，字段必须显式声明（而不是省略）。
		{service.VideoModelSeedance25, false},
		{"seedance-custom", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			model := agentCatalogModel(service.AgentModelCatalogEntry{
				ID:         tc.model,
				MediaTypes: []string{service.AgentMediaTypeVideo},
				Platforms:  []string{service.PlatformVideo},
				Interfaces: []string{service.AgentInterfaceSeedanceVideos},
			}, nil)

			capabilities := capabilitiesOf(t, model)
			declared, present := capabilities["supports_audio_only_reference"]
			require.True(t, present, "每个视频模型都要声明该能力")
			require.Equal(t, tc.want, declared)
		})
	}
}

func TestAgentCatalogModelUnionsTextAndImageRoutes(t *testing.T) {
	model := agentCatalogModel(service.AgentModelCatalogEntry{
		ID:         "gpt-image-alias",
		MediaTypes: []string{service.AgentMediaTypeText, service.AgentMediaTypeImage},
		Platforms:  []string{service.PlatformOpenAI},
		Interfaces: []string{service.AgentInterfaceOpenAIResponses, service.AgentInterfaceOpenAIImages},
	}, nil)

	capabilities := capabilitiesOf(t, model)
	require.Equal(t, []string{"text", "image"}, capabilities["input_modalities"])
	require.Equal(t, []string{"text", "image"}, capabilities["output_modalities"])
	require.Equal(t, []string{"text.generate", "image.generate", "image.edit"}, capabilities["operations"])
	require.Equal(t, false, capabilities["streaming"])
}

// capabilitiesOf 取目录条目的能力表。用 comma-ok 断言，errcheck 的
// check-type-assertions 不接受 `model["capabilities"].(gin.H)` 这种写法。
func capabilitiesOf(t *testing.T, model gin.H) gin.H {
	t.Helper()
	capabilities, ok := model["capabilities"].(gin.H)
	require.True(t, ok, "model catalog entry must carry a capabilities object")
	return capabilities
}

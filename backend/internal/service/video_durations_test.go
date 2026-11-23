package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 时长是按模型的上游约束，不是固定三档：2.0 系列 4-15 秒，2.5 为 4-30 秒；
// grok-imagine 为 1-15 秒，可灵 omni 为 3-15 秒。
func TestSupportedVideoDurationsArePerModelRanges(t *testing.T) {
	require.Equal(t, secondsRange(4, 15), SupportedVideoDurations(VideoModelSeedance20))
	require.Equal(t, secondsRange(4, 15), SupportedVideoDurations(VideoModelSeedance20Fast))
	require.Equal(t, secondsRange(4, 30), SupportedVideoDurations(VideoModelSeedance25))
	require.Equal(t, secondsRange(1, 15), SupportedVideoDurations(VideoModelGrokImagineVideo15))
	require.Equal(t, secondsRange(3, 15), SupportedVideoDurations(VideoModelKlingV3Omni))
	// 账号 model_mapping 里自定义的视频模型走 legacy 兜底窗口。
	require.Equal(t, secondsRange(4, 15), SupportedVideoDurations("seedance-custom"))
}

// 广告出去的时长清单必须与创建请求的校验边界完全一致，否则客户端会选到
// 网关随后拒掉的秒数（或看不到本可用的秒数）。
func TestSupportedVideoDurationsMatchRequestValidationBounds(t *testing.T) {
	for model, spec := range videoModelSpecs {
		t.Run(model, func(t *testing.T) {
			durations := SupportedVideoDurations(model)
			require.Equal(t, secondsRange(spec.MinSeconds, spec.MaxSeconds), durations)
			require.Equal(t, spec.MinSeconds, durations[0])
			require.Equal(t, spec.MaxSeconds, durations[len(durations)-1])
		})
	}
}

func secondsRange(minSeconds, maxSeconds int) []int {
	durations := make([]int, 0, maxSeconds-minSeconds+1)
	for seconds := minSeconds; seconds <= maxSeconds; seconds++ {
		durations = append(durations, seconds)
	}
	return durations
}

func TestSupportedVideoAspectRatiosCoverAllBuiltInAndCustomModels(t *testing.T) {
	require.Equal(t, []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"}, SupportedVideoAspectRatios(VideoModelSeedance25Flash))
	require.Equal(t, []string{"1:1", "9:16", "16:9", "3:4", "4:3"}, SupportedVideoAspectRatios(VideoModelViduQ4Preview))
	require.Equal(t, []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}, SupportedVideoAspectRatios(VideoModelGrokImagineVideo15))
	require.Equal(t, []string{"16:9", "9:16", "1:1"}, SupportedVideoAspectRatios(VideoModelKlingV3Omni))
	require.Equal(t, []string{"16:9", "9:16", "1:1", "21:9"}, SupportedVideoAspectRatios(VideoModelKlingV4))
	require.Equal(t, []string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"}, SupportedVideoAspectRatios("seedance-custom"))

	for _, model := range SupportedVideoModels() {
		ratios := SupportedVideoAspectRatios(model)
		require.NotEmpty(t, ratios, "model %s must declare supported aspect ratios", model)
		ratios[0] = "mutated"
		require.NotEqual(t, "mutated", SupportedVideoAspectRatios(model)[0], "caller mutation must not affect shared spec")
	}
}

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 参考音频不能单独成篇：2.0 / 2.0-fast / 2.5 一致，都必须搭配图或视频。
func TestSupportsAudioOnlyReferenceIsPerModel(t *testing.T) {
	require.False(t, SupportsAudioOnlyReference(VideoModelSeedance20))
	require.False(t, SupportsAudioOnlyReference(VideoModelSeedance20Fast))
	require.False(t, SupportsAudioOnlyReference(VideoModelSeedance25))
	// 账号 model_mapping 里的自定义视频模型走 legacy 兜底，同样不允许。
	require.False(t, SupportsAudioOnlyReference("seedance-custom"))
}

// 声明必须与校验结果逐个模型对齐，否则客户端会按目录放行一个必然 400 的请求
// （或反过来藏掉本可用的纯音频生成）。
func TestSupportsAudioOnlyReferenceMatchesValidation(t *testing.T) {
	for model, spec := range videoModelSpecs {
		t.Run(model, func(t *testing.T) {
			require.Equal(t, !spec.AudioNeedsVisual, SupportsAudioOnlyReference(model))

			_, err := normalizeVideoCreateRequest(&VideoCreateRequest{
				Model:       model,
				Prompt:      "follow the music rhythm",
				Duration:    8,
				Resolution:  VideoResolution720P,
				AbilityCode: videoAbilityReferenceToVideo,
				Content: []VideoContent{{
					Type:     "audio_url",
					Role:     "reference_audio",
					AudioURL: &VideoContentURL{URL: "https://cdn.example.com/music.mp3"},
				}},
			})
			require.Equal(t, SupportsAudioOnlyReference(model), err == nil,
				"声明与校验不一致：supports_audio_only_reference=%v, err=%v",
				SupportsAudioOnlyReference(model), err)
		})
	}
}

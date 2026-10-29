//go:build unit

package captcha

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateProducesDecodablePNGWithAnswerFromAlphabet(t *testing.T) {
	challenge, err := Generate(Options{})
	require.NoError(t, err)
	require.Len(t, challenge.Answer, DefaultLength)
	for _, ch := range challenge.Answer {
		require.True(t, strings.ContainsRune(Alphabet, ch), "unexpected character %q", ch)
	}

	img, err := png.Decode(bytes.NewReader(challenge.PNG))
	require.NoError(t, err)
	require.Equal(t, DefaultWidth, img.Bounds().Dx())
	require.Equal(t, DefaultHeight, img.Bounds().Dy())
}

func TestGenerateDrawsDarkGlyphPixels(t *testing.T) {
	challenge, err := generateWithSeed(Options{}, [32]byte{7})
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(challenge.PNG))
	require.NoError(t, err)

	dark := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			luma := (299*r + 587*g + 114*bl) / 1000 >> 8
			if luma < 110 {
				dark++
			}
		}
	}
	// 背景是高明度渐变，字符与干扰线必须留下足量深色像素，否则说明字形没有画上。
	require.Greater(t, dark, b.Dx()*b.Dy()/25)
}

func TestGenerateWithSeedIsDeterministic(t *testing.T) {
	seed := [32]byte{1, 2, 3}
	first, err := generateWithSeed(Options{}, seed)
	require.NoError(t, err)
	second, err := generateWithSeed(Options{}, seed)
	require.NoError(t, err)
	require.Equal(t, first.Answer, second.Answer)
	require.Equal(t, first.PNG, second.PNG)

	other, err := generateWithSeed(Options{}, [32]byte{9, 9, 9})
	require.NoError(t, err)
	require.NotEqual(t, first.PNG, other.PNG)
}

func TestGenerateAnswersVaryAcrossCalls(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		challenge, err := Generate(Options{})
		require.NoError(t, err)
		seen[challenge.Answer] = struct{}{}
	}
	require.Greater(t, len(seen), 15)
}

func TestNormalizeOptionsClampsToSaneBounds(t *testing.T) {
	require.Equal(t, Options{Length: DefaultLength, Width: DefaultWidth, Height: DefaultHeight}, normalizeOptions(Options{}))
	require.Equal(t, Options{Length: maxLength, Width: maxWidth, Height: maxHeight}, normalizeOptions(Options{Length: 99, Width: 9999, Height: 9999}))
	require.Equal(t, Options{Length: minLength, Width: minWidth, Height: minHeight}, normalizeOptions(Options{Length: 1, Width: 10, Height: 10}))

	challenge, err := Generate(Options{Length: 6, Width: 300, Height: 90})
	require.NoError(t, err)
	require.Len(t, challenge.Answer, 6)
	img, err := png.Decode(bytes.NewReader(challenge.PNG))
	require.NoError(t, err)
	require.Equal(t, 300, img.Bounds().Dx())
	require.Equal(t, 90, img.Bounds().Dy())
}

func TestHSLColorPrimaries(t *testing.T) {
	red := hslColor(0, 1, 0.5)
	require.Equal(t, uint8(255), red.R)
	require.Equal(t, uint8(0), red.G)
	require.Equal(t, uint8(0), red.B)

	blue := hslColor(240, 1, 0.5)
	require.Equal(t, uint8(0), blue.R)
	require.Equal(t, uint8(0), blue.G)
	require.Equal(t, uint8(255), blue.B)
}

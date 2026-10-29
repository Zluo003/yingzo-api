// Package captcha 渲染自研图形验证码。
//
// 出图流程：随机字符逐个缩放、旋转、错切后贴到透明字符层，对字符层做正弦扭曲，
// 合成到浅色渐变背景上，再叠加与字符同色系的干扰曲线和噪点，最后编码为 PNG。
// 干扰曲线与字符同色、同粗细，无法靠颜色或线宽把它们从字符里分离出来。
//
// 本包只负责出图，答案的存储、一次性消费与校验在 service 层完成。
package captcha

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	mrand "math/rand/v2"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

const (
	// Alphabet 去掉了 0/O、1/I/L、2/Z、5/S、8/B、G/6，以及旋转后像「+」的 X、
	// 扭曲后与 U 难以分辨的 V；只渲染大写，校验时大小写不敏感。
	Alphabet = "ACDEFHJKMNPRTUWY34679"

	DefaultLength = 4
	// 默认按 2 倍像素出图，前端以 120×40 显示，高分屏下依然清晰。
	DefaultWidth  = 240
	DefaultHeight = 80

	minLength = 4
	maxLength = 8
	minWidth  = 120
	maxWidth  = 480
	minHeight = 40
	maxHeight = 160
)

// Options 控制验证码尺寸与字符数；零值使用默认值。
type Options struct {
	Length int
	Width  int
	Height int
}

// Challenge 是一张生成好的验证码。
type Challenge struct {
	Answer string
	PNG    []byte
}

var (
	fontOnce   sync.Once
	parsedFont *opentype.Font
	fontErr    error
)

func loadFont() (*opentype.Font, error) {
	fontOnce.Do(func() {
		parsedFont, fontErr = opentype.Parse(gobold.TTF)
	})
	return parsedFont, fontErr
}

// Generate 生成一张验证码。答案与所有视觉扰动都取自同一个以 crypto/rand 播种的
// ChaCha8 流，答案不可预测。
func Generate(opts Options) (*Challenge, error) {
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("captcha seed: %w", err)
	}
	return generateWithSeed(opts, seed)
}

func generateWithSeed(opts Options, seed [32]byte) (*Challenge, error) {
	opts = normalizeOptions(opts)
	face, err := loadFont()
	if err != nil {
		return nil, fmt.Errorf("captcha font: %w", err)
	}
	rng := mrand.New(mrand.NewChaCha8(seed))

	answer := make([]byte, opts.Length)
	for i := range answer {
		answer[i] = Alphabet[rng.IntN(len(Alphabet))]
	}

	r := &renderer{rng: rng, font: face, width: opts.Width, height: opts.Height}
	img, err := r.render(string(answer))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("captcha encode: %w", err)
	}
	return &Challenge{Answer: string(answer), PNG: buf.Bytes()}, nil
}

func normalizeOptions(opts Options) Options {
	if opts.Length <= 0 {
		opts.Length = DefaultLength
	}
	if opts.Width <= 0 {
		opts.Width = DefaultWidth
	}
	if opts.Height <= 0 {
		opts.Height = DefaultHeight
	}
	opts.Length = clampInt(opts.Length, minLength, maxLength)
	opts.Width = clampInt(opts.Width, minWidth, maxWidth)
	opts.Height = clampInt(opts.Height, minHeight, maxHeight)
	return opts
}

type renderer struct {
	rng    *mrand.Rand
	font   *opentype.Font
	width  int
	height int
	inks   []color.RGBA
}

func (r *renderer) render(answer string) (*image.RGBA, error) {
	bounds := image.Rect(0, 0, r.width, r.height)
	canvas := image.NewRGBA(bounds)
	hue := r.rng.Float64() * 360
	r.paintBackground(canvas, hue)
	r.inks = r.pickInks(hue, len(answer))

	// 背景层的浅色细线，增加二值化难度。
	for i := 0; i < 3; i++ {
		r.strokeCurve(canvas, hslColor(r.rng.Float64()*360, 0.35, 0.72), 1+r.rng.Float64(), 0.7)
	}

	glyphs := image.NewRGBA(bounds)
	if err := r.drawGlyphs(glyphs, answer); err != nil {
		return nil, err
	}
	warped := r.warp(glyphs)
	draw.Draw(canvas, bounds, warped, image.Point{}, draw.Over)

	// 与字符同色同粗的干扰曲线，穿过字符区域。
	for i := 0; i < 2; i++ {
		ink := r.inks[r.rng.IntN(len(r.inks))]
		r.strokeCurve(canvas, ink, float64(r.height)*0.03+r.rng.Float64(), 0.95)
	}
	r.sprinkleNoise(canvas)
	return canvas, nil
}

func (r *renderer) paintBackground(img *image.RGBA, hue float64) {
	from := hslColor(hue, 0.45, 0.93)
	to := hslColor(math.Mod(hue+40+r.rng.Float64()*60, 360), 0.45, 0.88)
	w := float64(r.width - 1)
	for x := 0; x < r.width; x++ {
		t := float64(x) / w
		c := color.RGBA{
			R: lerpByte(from.R, to.R, t),
			G: lerpByte(from.G, to.G, t),
			B: lerpByte(from.B, to.B, t),
			A: 255,
		}
		for y := 0; y < r.height; y++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// pickInks 为每个字符挑一种深色：色相各不相同，但明度接近，
// 既保证可读，又让颜色无法作为分割字符与干扰线的依据。
func (r *renderer) pickInks(baseHue float64, n int) []color.RGBA {
	inks := make([]color.RGBA, n)
	for i := range inks {
		hue := math.Mod(baseHue+180+float64(i)*67+r.rng.Float64()*40, 360)
		inks[i] = hslColor(hue, 0.55+r.rng.Float64()*0.3, 0.22+r.rng.Float64()*0.16)
	}
	return inks
}

func (r *renderer) drawGlyphs(dst *image.RGBA, answer string) error {
	n := len(answer)
	slot := float64(r.width) * 0.86 / float64(n)
	left := float64(r.width) * 0.07
	for i, ch := range answer {
		size := float64(r.height) * (0.62 + r.rng.Float64()*0.14)
		tile, err := r.glyphTile(string(ch), size, r.inks[i])
		if err != nil {
			return err
		}
		tb := tile.Bounds()
		cx, cy := float64(tb.Dx())/2, float64(tb.Dy())/2

		angle := (r.rng.Float64()*2 - 1) * 22 * math.Pi / 180
		scaleX := 0.88 + r.rng.Float64()*0.28
		scaleY := 0.88 + r.rng.Float64()*0.28
		shear := (r.rng.Float64()*2 - 1) * 0.2
		// 字符之间允许轻微重叠，增加切分难度。
		dx := left + slot*(float64(i)+0.5) + (r.rng.Float64()*2-1)*slot*0.12
		dy := float64(r.height)/2 + (r.rng.Float64()*2-1)*float64(r.height)*0.1

		cos, sin := math.Cos(angle), math.Sin(angle)
		a := scaleX * cos
		b := scaleY * (shear*cos - sin)
		d := scaleX * sin
		e := scaleY * (shear*sin + cos)
		m := f64.Aff3{
			a, b, dx - (a*cx + b*cy),
			d, e, dy - (d*cx + e*cy),
		}
		draw.BiLinear.Transform(dst, m, tile, tb, draw.Over, nil)
	}
	return nil
}

func (r *renderer) glyphTile(ch string, size float64, ink color.RGBA) (*image.RGBA, error) {
	face, err := opentype.NewFace(r.font, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, fmt.Errorf("captcha face: %w", err)
	}
	defer func() { _ = face.Close() }()

	glyphBounds, _ := font.BoundString(face, ch)
	gw := (glyphBounds.Max.X - glyphBounds.Min.X).Ceil()
	gh := (glyphBounds.Max.Y - glyphBounds.Min.Y).Ceil()
	side := int(math.Ceil(math.Max(float64(gw), float64(gh)) * 1.5))
	if side < 1 {
		side = 1
	}
	tile := image.NewRGBA(image.Rect(0, 0, side, side))
	drawer := font.Drawer{
		Dst:  tile,
		Src:  image.NewUniform(ink),
		Face: face,
	}
	// 把字形外接框居中到瓦片中心。
	originX := fixed.I((side-gw)/2) - glyphBounds.Min.X
	originY := fixed.I((side-gh)/2) - glyphBounds.Min.Y
	drawer.Dot = fixed.Point26_6{X: originX, Y: originY}
	drawer.DrawString(ch)
	return tile, nil
}

// warp 对字符层做二维正弦位移并双线性采样，打断字形的直线笔画。
func (r *renderer) warp(src *image.RGBA) *image.RGBA {
	out := image.NewRGBA(src.Bounds())
	ampX := float64(r.height) * (0.04 + r.rng.Float64()*0.04)
	ampY := float64(r.height) * (0.05 + r.rng.Float64()*0.05)
	periodX := float64(r.height) * (0.9 + r.rng.Float64()*0.6)
	periodY := float64(r.width) * (0.35 + r.rng.Float64()*0.3)
	phaseX := r.rng.Float64() * 2 * math.Pi
	phaseY := r.rng.Float64() * 2 * math.Pi
	for y := 0; y < r.height; y++ {
		shiftX := ampX * math.Sin(2*math.Pi*float64(y)/periodX+phaseX)
		for x := 0; x < r.width; x++ {
			sx := float64(x) + shiftX
			sy := float64(y) + ampY*math.Sin(2*math.Pi*float64(x)/periodY+phaseY)
			out.SetRGBA(x, y, sampleBilinear(src, sx, sy))
		}
	}
	return out
}

// strokeCurve 沿一条横穿画布的三次贝塞尔曲线连续盖章抗锯齿圆点。
func (r *renderer) strokeCurve(img *image.RGBA, ink color.RGBA, thickness, opacity float64) {
	w, h := float64(r.width), float64(r.height)
	p0 := [2]float64{-w * 0.05, h * (0.2 + r.rng.Float64()*0.6)}
	p1 := [2]float64{w * (0.2 + r.rng.Float64()*0.2), h * r.rng.Float64()}
	p2 := [2]float64{w * (0.6 + r.rng.Float64()*0.2), h * r.rng.Float64()}
	p3 := [2]float64{w * 1.05, h * (0.2 + r.rng.Float64()*0.6)}
	steps := r.width * 3
	radius := thickness / 2
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		u := 1 - t
		x := u*u*u*p0[0] + 3*u*u*t*p1[0] + 3*u*t*t*p2[0] + t*t*t*p3[0]
		y := u*u*u*p0[1] + 3*u*u*t*p1[1] + 3*u*t*t*p2[1] + t*t*t*p3[1]
		stampDisc(img, x, y, radius, ink, opacity)
	}
}

func (r *renderer) sprinkleNoise(img *image.RGBA) {
	count := r.width * r.height / 70
	for i := 0; i < count; i++ {
		x := r.rng.Float64() * float64(r.width)
		y := r.rng.Float64() * float64(r.height)
		var ink color.RGBA
		if r.rng.IntN(3) == 0 {
			ink = r.inks[r.rng.IntN(len(r.inks))]
		} else {
			ink = hslColor(r.rng.Float64()*360, 0.4, 0.55+r.rng.Float64()*0.3)
		}
		stampDisc(img, x, y, 0.5+r.rng.Float64()*0.9, ink, 0.55+r.rng.Float64()*0.4)
	}
}

// stampDisc 以覆盖率做抗锯齿，把半径为 radius 的圆点按 opacity 叠加到 img。
func stampDisc(img *image.RGBA, cx, cy, radius float64, ink color.RGBA, opacity float64) {
	b := img.Bounds()
	minX := int(math.Floor(cx - radius - 1))
	maxX := int(math.Ceil(cx + radius + 1))
	minY := int(math.Floor(cy - radius - 1))
	maxY := int(math.Ceil(cy + radius + 1))
	for y := minY; y <= maxY; y++ {
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		for x := minX; x <= maxX; x++ {
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			dist := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			coverage := radius + 0.5 - dist
			if coverage <= 0 {
				continue
			}
			if coverage > 1 {
				coverage = 1
			}
			// 盖章间距远小于半径，单次叠加取较低 alpha，避免曲线中段过黑。
			blendPixel(img, x, y, ink, coverage*opacity*0.35)
		}
	}
}

func blendPixel(img *image.RGBA, x, y int, ink color.RGBA, alpha float64) {
	if alpha <= 0 {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	i := img.PixOffset(x, y)
	pix := img.Pix[i : i+4 : i+4]
	pix[0] = lerpByte(pix[0], ink.R, alpha)
	pix[1] = lerpByte(pix[1], ink.G, alpha)
	pix[2] = lerpByte(pix[2], ink.B, alpha)
	pix[3] = lerpByte(pix[3], 255, alpha)
}

func sampleBilinear(img *image.RGBA, x, y float64) color.RGBA {
	b := img.Bounds()
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	c00 := rgbaAt(img, b, x0, y0)
	c10 := rgbaAt(img, b, x0+1, y0)
	c01 := rgbaAt(img, b, x0, y0+1)
	c11 := rgbaAt(img, b, x0+1, y0+1)
	mix := func(a, b, c, d uint8) uint8 {
		top := float64(a)*(1-fx) + float64(b)*fx
		bottom := float64(c)*(1-fx) + float64(d)*fx
		return uint8(math.Round(top*(1-fy) + bottom*fy))
	}
	return color.RGBA{
		R: mix(c00.R, c10.R, c01.R, c11.R),
		G: mix(c00.G, c10.G, c01.G, c11.G),
		B: mix(c00.B, c10.B, c01.B, c11.B),
		A: mix(c00.A, c10.A, c01.A, c11.A),
	}
}

func rgbaAt(img *image.RGBA, b image.Rectangle, x, y int) color.RGBA {
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return color.RGBA{}
	}
	return img.RGBAAt(x, y)
}

// hslColor 把 HSL（h: 0-360，s/l: 0-1）转换为不透明 RGBA。
func hslColor(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	hp := math.Mod(h, 360) / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r1, g1, b1 float64
	switch {
	case hp < 1:
		r1, g1, b1 = c, x, 0
	case hp < 2:
		r1, g1, b1 = x, c, 0
	case hp < 3:
		r1, g1, b1 = 0, c, x
	case hp < 4:
		r1, g1, b1 = 0, x, c
	case hp < 5:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	m := l - c/2
	return color.RGBA{
		R: uint8(math.Round((r1 + m) * 255)),
		G: uint8(math.Round((g1 + m) * 255)),
		B: uint8(math.Round((b1 + m) * 255)),
		A: 255,
	}
}

func lerpByte(from, to uint8, t float64) uint8 {
	return uint8(math.Round(float64(from) + (float64(to)-float64(from))*t))
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

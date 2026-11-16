package artwork

import (
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"math"
)

// composeNavigation makes a 16:9 card. A missing logo always has a legible
// typographic fallback; genre cards use a strip of actual member posters.
func composeNavigation(req PosterRequest, posters, logos []image.Image) *image.RGBA {
	w := clampWidth(req.Width)
	h := w * 9 / 16
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	accent, ok := parseHex(req.Accent)
	if !ok {
		accent = color.RGBA{40, 75, 100, 255}
	}
	diagonalGradient(dst, shade(accent, 0.7), shade(accent, 0.12))
	if len(posters) > 0 {
		for n, p := range posters {
			coverRect(dst, image.Rect(n*w/len(posters), 0, (n+1)*w/len(posters), h), p)
		}
		verticalFade(dst, 0.15, 0.95, color.RGBA{0, 0, 0, 255}, 0.94)
	}
	if len(logos) > 0 {
		logo := logos[0]
		b := logo.Bounds()
		scale := math.Min(float64(w)*0.7/float64(b.Dx()), float64(h)*0.5/float64(b.Dy()))
		lw, lh := int(float64(b.Dx())*scale), int(float64(b.Dy())*scale)
		x, y := (w-lw)/2, (h-lh)/2-h/12
		draw.ApproxBiLinear.Scale(dst, image.Rect(x, y, x+lw, y+lh), logo, b, draw.Over, nil)
	}
	bold, _, err := fonts()
	if err != nil {
		return dst
	}
	size := float64(w) * 0.09
	if len(logos) > 0 {
		size = float64(w) * 0.055
	}
	var fc font.Face
	for {
		fc = face(bold, size)
		if fc == nil {
			return dst
		}
		if font.MeasureString(fc, req.Title).Ceil() <= w*9/10 || size <= float64(w)*0.025 {
			break
		}
		fc.Close()
		size *= 0.9
	}
	defer fc.Close()
	x := (w - font.MeasureString(fc, req.Title).Ceil()) / 2
	y := h/2 + fc.Metrics().Ascent.Ceil()/2
	if len(posters) > 0 || len(logos) > 0 {
		y = h * 86 / 100
	}
	d := font.Drawer{Dst: dst, Src: image.NewUniform(color.White), Face: fc, Dot: fixed.P(x, y)}
	d.DrawString(req.Title)
	return dst
}

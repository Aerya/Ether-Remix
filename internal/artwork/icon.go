package artwork

import (
	"bytes"
	"context"
	"image"
	"image/png"

	xdraw "golang.org/x/image/draw"
)

// MaxIconPNGBytes is the largest PNG icon RasterizeIcon accepts.
const MaxIconPNGBytes = 512 << 10

// MaxIconPNGDimension is the largest width or height of a PNG icon.
const MaxIconPNGDimension = 1024

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// IsPNG reports whether data starts with the PNG signature.
func IsPNG(data []byte) bool { return bytes.HasPrefix(data, pngSignature) }

// ValidateIconPNG checks a PNG icon's size and dimensions without decoding
// its pixels.
func ValidateIconPNG(data []byte) error {
	if len(data) == 0 || len(data) > MaxIconPNGBytes || !IsPNG(data) {
		return ErrSVG
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxIconPNGDimension || cfg.Height > MaxIconPNGDimension {
		return ErrSVG
	}
	return nil
}

// RasterizeIcon renders a package icon that is either an SVG or a PNG. A PNG
// is decoded within bounds and redrawn centred in the requested box, so the
// bytes served are always Ether's own encoding.
func (r *SVGRasterizer) RasterizeIcon(ctx context.Context, reference string, data []byte, opts RasterOptions) ([]byte, string, error) {
	if !IsPNG(data) {
		return r.Rasterize(ctx, reference, data, opts)
	}
	if err := ValidateIconPNG(data); err != nil {
		return nil, "", err
	}
	data = append([]byte(nil), data...)
	return r.execute(ctx, rasterKey(reference, data, opts), func(context.Context) (image.Image, error) {
		src, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrSVG
		}
		sb := src.Bounds()
		width, height := opts.Width, opts.Height
		if width == 0 && height == 0 {
			width, height = sb.Dx(), sb.Dy()
		}
		if width == 0 {
			width = max(1, height*sb.Dx()/sb.Dy())
		}
		if height == 0 {
			height = max(1, width*sb.Dy()/sb.Dx())
		}
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		// Fit inside the box, keeping the aspect ratio.
		w, h := width, width*sb.Dy()/sb.Dx()
		if h > height {
			w, h = height*sb.Dx()/sb.Dy(), height
		}
		w, h = max(1, w), max(1, h)
		x, y := (width-w)/2, (height-h)/2
		xdraw.CatmullRom.Scale(dst, image.Rect(x, y, x+w, y+h), src, sb, xdraw.Src, nil)
		return dst, nil
	}, opts)
}

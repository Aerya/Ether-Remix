package vela

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/webclient"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	MaxAppIconBytes = 512 << 10
	MaxAppIconSize  = 1024

	// W3C maskable icons guarantee a central 80%-diameter circle safe zone.
	// Using an 18% inset on each side places the source icon in the central
	// 64% box (matching Vela's own 0.63 maskable scale) so square icons stay
	// unclipped under circular and squircle OS masks.
	maskableInsetPercent = 18
)

var (
	ErrAppIconTooLarge = errors.New("app icon exceeds 512 KB")

	appIconURLPattern   = regexp.MustCompile(`^/vela/v0/branding/icon-([0-9a-f]{64})\.(png|svg|webp)$`)
	appIconAssetPattern = regexp.MustCompile(`^icon-([0-9a-f]{64})\.(png|svg|webp)$`)

	velaIconFallbackBG = color.RGBA{R: 0x09, G: 0x0b, B: 0x0e, A: 0xff}
)

func init() {
	frontend.RegisterMigration("vela_app_icons", 1, `
CREATE TABLE vela_app_icons (digest TEXT PRIMARY KEY, ext TEXT NOT NULL, mime TEXT NOT NULL, size INTEGER NOT NULL, data BLOB NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE vela_app_icon_variants (digest TEXT NOT NULL, variant TEXT NOT NULL, mime TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY(digest, variant));`)
}

// AppIconValidationError reports an invalid square app icon image or dimension check.
type AppIconValidationError struct {
	Message string
}

func (e *AppIconValidationError) Error() string { return e.Message }

// AppIcon is a validated, content-addressed square owner PWA icon asset.
type AppIcon struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Digest string `json:"digest"`
}

type appIconSetting struct {
	URL    string
	Width  int
	Height int
	Digest string
	Ext    string
}

type iconVariantSpec struct {
	name     string
	size     int
	maskable bool
	opaque   bool
}

var appIconVariantSpecs = []iconVariantSpec{
	{name: "vela-icon-512.png", size: 512},
	{name: "vela-icon-192.png", size: 192},
	{name: "vela-icon-maskable-512.png", size: 512, maskable: true, opaque: true},
	{name: "vela-icon-maskable-192.png", size: 192, maskable: true, opaque: true},
	{name: "vela-icon-180.png", size: 180, opaque: true},
	{name: "favicon-48.png", size: 48},
	{name: "favicon-32.png", size: 32},
	{name: "favicon-16.png", size: 16},
}

// UploadAppIcon validates a square PNG, SVG, or WebP app icon (<= 512 KB,
// 1x1..1024x1024 px), generates safe PNG sizes for PWA any/maskable (192, 512),
// apple-touch-icon (180), and favicons (16, 32, 48), and stores them by SHA-256 digest.
func UploadAppIcon(ctx context.Context, db *sql.DB, r io.Reader) (AppIcon, error) {
	if db == nil {
		return AppIcon{}, errors.New("app icon storage unavailable")
	}
	if r == nil {
		return AppIcon{}, &AppIconValidationError{Message: "app icon file is empty"}
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxAppIconBytes+1))
	if err != nil {
		return AppIcon{}, err
	}
	if len(data) == 0 {
		return AppIcon{}, &AppIconValidationError{Message: "app icon file is empty"}
	}
	if len(data) > MaxAppIconBytes {
		return AppIcon{}, ErrAppIconTooLarge
	}
	ext, mimeType, size, srcImg, err := decodeAndValidateAppIcon(data)
	if err != nil {
		return AppIcon{}, err
	}
	variants, err := generateAppIconVariants(srcImg)
	if err != nil {
		return AppIcon{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AppIcon{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO vela_app_icons(digest, ext, mime, size, data, created_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(digest) DO NOTHING`,
		digest, ext, mimeType, size, data, time.Now().Unix(),
	); err != nil {
		return AppIcon{}, err
	}
	for variant, pngBytes := range variants {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO vela_app_icon_variants(digest, variant, mime, data) VALUES(?, ?, ?, ?) ON CONFLICT(digest, variant) DO NOTHING`,
			digest, variant, "image/png", pngBytes,
		); err != nil {
			return AppIcon{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AppIcon{}, err
	}
	return AppIcon{
		URL:    "/vela/v0/branding/icon-" + digest + "." + ext,
		Width:  size,
		Height: size,
		Digest: digest,
	}, nil
}

// PruneAppIcons removes unreferenced app icons (and their generated variants)
// older than an hour so an uploaded draft is never deleted mid-edit while old
// assets do not accumulate.
func PruneAppIcons(ctx context.Context, db *sql.DB, settings map[string]any) {
	if db == nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour).Unix()
	keep := referencedAppIconDigest(settings)
	if keep == "" {
		_, _ = db.ExecContext(ctx, `DELETE FROM vela_app_icon_variants WHERE digest IN (SELECT digest FROM vela_app_icons WHERE created_at < ?)`, cutoff)
		_, _ = db.ExecContext(ctx, `DELETE FROM vela_app_icons WHERE created_at < ?`, cutoff)
		return
	}
	_, _ = db.ExecContext(ctx, `DELETE FROM vela_app_icon_variants WHERE digest IN (SELECT digest FROM vela_app_icons WHERE created_at < ? AND digest <> ?)`, cutoff, keep)
	_, _ = db.ExecContext(ctx, `DELETE FROM vela_app_icons WHERE created_at < ? AND digest <> ?`, cutoff, keep)
}

func referencedAppIconDigest(settings map[string]any) string {
	icon, ok := parseAppIconSetting(settings["appIcon"])
	if !ok {
		return ""
	}
	return icon.Digest
}

func parseAppIconSetting(v any) (appIconSetting, bool) {
	if v == nil {
		return appIconSetting{}, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return appIconSetting{}, false
	}
	for k := range m {
		if k != "url" && k != "width" && k != "height" {
			return appIconSetting{}, false
		}
	}
	urlVal, ok := m["url"].(string)
	if !ok {
		return appIconSetting{}, false
	}
	match := appIconURLPattern.FindStringSubmatch(urlVal)
	if match == nil {
		return appIconSetting{}, false
	}
	width, okW := settingInt(m["width"])
	height, okH := settingInt(m["height"])
	if !okW || !okH || width < 1 || width > MaxAppIconSize || height < 1 || height > MaxAppIconSize || width != height {
		return appIconSetting{}, false
	}
	return appIconSetting{
		URL:    urlVal,
		Width:  width,
		Height: height,
		Digest: match[1],
		Ext:    match[2],
	}, true
}

func validateAppIconInDB(db *sql.DB, icon appIconSetting) error {
	if db == nil {
		return nil
	}
	var ext string
	var size int
	err := db.QueryRow(`SELECT ext, size FROM vela_app_icons WHERE digest = ?`, icon.Digest).Scan(&ext, &size)
	if err != nil || ext != icon.Ext || size != icon.Width || size != icon.Height {
		return errors.New("unknown or mismatched app icon asset")
	}
	return nil
}

func decodeAndValidateAppIcon(data []byte) (ext, mimeType string, size int, img image.Image, err error) {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", "", 0, nil, &AppIconValidationError{Message: "invalid PNG image"}
		}
		if err := checkAppIconDimensions(cfg.Width, cfg.Height); err != nil {
			return "", "", 0, nil, err
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
			return "", "", 0, nil, &AppIconValidationError{Message: "corrupt PNG image"}
		}
		return "png", "image/png", cfg.Width, decoded, nil

	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		cfg, err := webp.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", "", 0, nil, &AppIconValidationError{Message: "invalid WebP image"}
		}
		if err := checkAppIconDimensions(cfg.Width, cfg.Height); err != nil {
			return "", "", 0, nil, err
		}
		decoded, err := webp.Decode(bytes.NewReader(data))
		if err != nil || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
			return "", "", 0, nil, &AppIconValidationError{Message: "corrupt WebP image"}
		}
		return "webp", "image/webp", cfg.Width, decoded, nil

	case looksLikeSVG(data):
		w, _, err := validateAppIconSVG(data)
		if err != nil {
			return "", "", 0, nil, err
		}
		raster, err := rasterizeAppIconSVG(data, 512)
		if err != nil {
			return "", "", 0, nil, err
		}
		return "svg", "image/svg+xml", w, raster, nil

	default:
		return "", "", 0, nil, &AppIconValidationError{Message: "app icon must be a square PNG, SVG, or WebP image"}
	}
}

func checkAppIconDimensions(width, height int) error {
	if width < 1 || height < 1 {
		return &AppIconValidationError{Message: "app icon must have positive dimensions"}
	}
	if width > MaxAppIconSize || height > MaxAppIconSize {
		return &AppIconValidationError{Message: fmt.Sprintf("app icon exceeds maximum dimensions (%dx%d px)", MaxAppIconSize, MaxAppIconSize)}
	}
	if width != height {
		return &AppIconValidationError{Message: "app icon must be square (width must equal height)"}
	}
	return nil
}

func validateAppIconSVG(data []byte) (int, int, error) {
	trimmed := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(trimmed) {
		return 0, 0, &AppIconValidationError{Message: "SVG must be valid UTF-8"}
	}
	dec := xml.NewDecoder(bytes.NewReader(trimmed))
	root := false
	depth, elements, styleDepth := 0, 0, 0
	var rootWidthAttr, rootHeightAttr, rootViewBox string

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, &AppIconValidationError{Message: "invalid SVG XML"}
		}
		switch node := tok.(type) {
		case xml.Directive:
			return 0, 0, &AppIconValidationError{Message: "SVG directives and entities are not allowed"}
		case xml.ProcInst:
			if node.Target != "xml" || root {
				return 0, 0, &AppIconValidationError{Message: "SVG processing instructions are not allowed"}
			}
		case xml.StartElement:
			depth++
			elements++
			if depth > 64 || elements > 2048 {
				return 0, 0, &AppIconValidationError{Message: "SVG is too complex"}
			}
			name := strings.ToLower(node.Name.Local)
			if !root {
				if name != "svg" || (node.Name.Space != "" && node.Name.Space != "http://www.w3.org/2000/svg") {
					return 0, 0, &AppIconValidationError{Message: "root element must be <svg>"}
				}
				root = true
				for _, attr := range node.Attr {
					if attr.Name.Space != "" {
						continue
					}
					switch strings.ToLower(attr.Name.Local) {
					case "width":
						rootWidthAttr = attr.Value
					case "height":
						rootHeightAttr = attr.Value
					case "viewbox":
						rootViewBox = attr.Value
					}
				}
			}
			switch name {
			case "script", "foreignobject", "iframe", "frame", "object", "embed", "audio", "video", "canvas", "animate", "set", "animatemotion", "animatetransform", "feimage", "image", "a":
				return 0, 0, &AppIconValidationError{Message: fmt.Sprintf("SVG <%s> element is not allowed", name)}
			case "style":
				styleDepth++
			}
			for _, attr := range node.Attr {
				key := strings.ToLower(attr.Name.Local)
				val := strings.TrimSpace(attr.Value)
				if len(val) > 16384 {
					return 0, 0, &AppIconValidationError{Message: "SVG attribute is too long"}
				}
				if strings.HasPrefix(key, "on") {
					return 0, 0, &AppIconValidationError{Message: "SVG event handlers are not allowed"}
				}
				if key == "href" {
					if !isLocalSVGFragment(val) {
						return 0, 0, &AppIconValidationError{Message: "SVG external references are not allowed"}
					}
					continue
				}
				if key == "style" {
					if !safeSVGCSS(val) {
						return 0, 0, &AppIconValidationError{Message: "SVG style contains disallowed references"}
					}
					continue
				}
				if !safeSVGAttrValue(val) {
					return 0, 0, &AppIconValidationError{Message: "SVG attribute contains disallowed references"}
				}
			}
		case xml.CharData:
			if styleDepth > 0 && !safeSVGCSS(string(node)) {
				return 0, 0, &AppIconValidationError{Message: "SVG stylesheet contains disallowed references"}
			}
		case xml.EndElement:
			if strings.EqualFold(node.Name.Local, "style") && styleDepth > 0 {
				styleDepth--
			}
			depth--
		}
	}
	if !root || depth != 0 {
		return 0, 0, &AppIconValidationError{Message: "incomplete SVG document"}
	}
	w, h, ok := resolveAppIconSVGDimensions(rootWidthAttr, rootHeightAttr, rootViewBox)
	if !ok {
		return 0, 0, &AppIconValidationError{Message: "SVG must specify square pixel dimensions or a square viewBox"}
	}
	if err := checkAppIconDimensions(w, h); err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func resolveAppIconSVGDimensions(widthAttr, heightAttr, viewBoxAttr string) (int, int, bool) {
	wPx, hasW := parseSVGPixels(widthAttr)
	hPx, hasH := parseSVGPixels(heightAttr)
	vbW, vbH, hasVB := parseSVGViewBox(viewBoxAttr)

	var wf, hf float64
	switch {
	case hasW && hasH:
		wf, hf = wPx, hPx
	case hasW && hasVB:
		wf = wPx
		hf = wPx * vbH / vbW
	case hasH && hasVB:
		hf = hPx
		wf = hPx * vbW / vbH
	case hasVB:
		wf, hf = vbW, vbH
	default:
		return 0, 0, false
	}
	if math.IsNaN(wf) || math.IsInf(wf, 0) || math.IsNaN(hf) || math.IsInf(hf, 0) || wf < 1 || hf < 1 {
		return 0, 0, false
	}
	if wf > MaxAppIconSize || hf > MaxAppIconSize {
		return int(math.Ceil(wf)), int(math.Ceil(hf)), true
	}
	return int(math.Round(wf)), int(math.Round(hf)), true
}

func rasterizeAppIconSVG(data []byte, target int) (*image.RGBA, error) {
	trimmed := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	icon, err := oksvg.ReadIconStream(bytes.NewReader(trimmed), oksvg.StrictErrorMode)
	if err != nil || icon == nil || len(icon.SVGPaths) == 0 || len(icon.SVGPaths) > 256 ||
		icon.ViewBox.W < 1e-6 || icon.ViewBox.H < 1e-6 ||
		math.IsNaN(icon.ViewBox.W) || math.IsNaN(icon.ViewBox.H) ||
		math.IsNaN(icon.ViewBox.X) || math.IsNaN(icon.ViewBox.Y) ||
		math.IsInf(icon.ViewBox.W, 0) || math.IsInf(icon.ViewBox.H, 0) ||
		math.IsInf(icon.ViewBox.X, 0) || math.IsInf(icon.ViewBox.Y, 0) ||
		icon.ViewBox.W > 1e6 || icon.ViewBox.H > 1e6 ||
		math.Abs(icon.ViewBox.X) > 1e6 || math.Abs(icon.ViewBox.Y) > 1e6 {
		return nil, &AppIconValidationError{Message: "SVG icon could not be rendered; use vector paths or upload a PNG/WebP"}
	}
	img := image.NewRGBA(image.Rect(0, 0, target, target))
	icon.SetTarget(0, 0, float64(target), float64(target))
	dasher := rasterx.NewDasher(target, target, rasterx.NewScannerGV(target, target, img, img.Bounds()))
	for _, p := range icon.SVGPaths {
		p.DrawTransformed(dasher, 1, icon.Transform)
	}
	return img, nil
}

func generateAppIconVariants(src image.Image) (map[string][]byte, error) {
	bleed := maskableBleedColor(src)
	out := make(map[string][]byte, len(appIconVariantSpecs))
	for _, spec := range appIconVariantSpecs {
		var rendered *image.RGBA
		if spec.maskable {
			rendered = renderMaskableIcon(src, spec.size, bleed)
		} else {
			rendered = renderStandardIcon(src, spec.size, spec.opaque, bleed)
		}
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, rendered); err != nil {
			return nil, fmt.Errorf("encode %s: %w", spec.name, err)
		}
		out[spec.name] = buf.Bytes()
	}
	return out, nil
}

func renderStandardIcon(src image.Image, size int, opaque bool, bg color.RGBA) *image.RGBA {
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, src.Bounds(), draw.Src, nil)
	if !opaque {
		return scaled
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), scaled, image.Point{}, draw.Over)
	return dst
}

func renderMaskableIcon(src image.Image, size int, bg color.RGBA) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	inset := (size*maskableInsetPercent + 50) / 100
	inner := image.Rect(inset, inset, size-inset, size-inset)
	scaled := image.NewRGBA(image.Rect(0, 0, inner.Dx(), inner.Dy()))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, src.Bounds(), draw.Src, nil)
	draw.Draw(dst, inner, scaled, image.Point{}, draw.Over)
	return dst
}

// maskableBleedColor extends an opaque icon's own uniform corner background
// into the maskable safe-zone margin when all four corners are opaque and
// closely matched; otherwise falls back to Vela's dark manifest background.
func maskableBleedColor(src image.Image) color.RGBA {
	b := src.Bounds()
	if b.Empty() {
		return velaIconFallbackBG
	}
	pts := [4]image.Point{
		{X: b.Min.X, Y: b.Min.Y},
		{X: b.Max.X - 1, Y: b.Min.Y},
		{X: b.Min.X, Y: b.Max.Y - 1},
		{X: b.Max.X - 1, Y: b.Max.Y - 1},
	}
	var corners [4]color.RGBA
	for idx, pt := range pts {
		c := color.RGBAModel.Convert(src.At(pt.X, pt.Y)).(color.RGBA)
		if c.A < 250 {
			return velaIconFallbackBG
		}
		corners[idx] = c
	}
	base := corners[0]
	for _, c := range corners[1:] {
		if absDiff(base.R, c.R) > 12 || absDiff(base.G, c.G) > 12 || absDiff(base.B, c.B) > 12 {
			return velaIconFallbackBG
		}
	}
	return color.RGBA{R: base.R, G: base.G, B: base.B, A: 0xff}
}

func absDiff(a, b uint8) uint8 {
	if a >= b {
		return a - b
	}
	return b - a
}

func (i *instance) velaBrand() webclient.VelaBrand {
	app := i.appearanceDocument()
	brand := webclient.VelaBrand{
		AppName:     app.appName,
		IconVersion: app.appIconDigest,
		IconIsSVG:   app.appIconExt == "svg",
	}
	if app.appIconDigest != "" {
		digest, ext := app.appIconDigest, app.appIconExt
		brand.IconAsset = func(name string) (webclient.VelaIconAsset, bool) {
			return i.loadAppIconAsset(digest, ext, name)
		}
	}
	return brand
}

func (i *instance) loadAppIconAsset(digest, ext, name string) (webclient.VelaIconAsset, bool) {
	if i.db == nil || digest == "" {
		return webclient.VelaIconAsset{}, false
	}
	cacheKey := digest + ":" + name
	i.iconMu.Lock()
	cached, ok := i.iconBlobs[cacheKey]
	i.iconMu.Unlock()
	if ok {
		return cached, true
	}

	var mimeType string
	var data []byte
	var err error
	ctx := i.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if name == "vela-icon.svg" {
		if ext == "svg" {
			err = i.db.QueryRowContext(ctx, `SELECT mime, data FROM vela_app_icons WHERE digest = ? AND ext = 'svg'`, digest).Scan(&mimeType, &data)
		} else {
			err = i.db.QueryRowContext(ctx, `SELECT mime, data FROM vela_app_icon_variants WHERE digest = ? AND variant = 'favicon-48.png'`, digest).Scan(&mimeType, &data)
		}
	} else {
		err = i.db.QueryRowContext(ctx, `SELECT mime, data FROM vela_app_icon_variants WHERE digest = ? AND variant = ?`, digest, name).Scan(&mimeType, &data)
	}
	if err != nil || len(data) == 0 {
		return webclient.VelaIconAsset{}, false
	}
	sum := sha256.Sum256(data)
	asset := webclient.VelaIconAsset{
		ContentType: mimeType,
		ETag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
		Data:        data,
		IsSVG:       mimeType == "image/svg+xml",
	}
	i.iconMu.Lock()
	if i.iconBlobs == nil {
		i.iconBlobs = make(map[string]webclient.VelaIconAsset)
	}
	if len(i.iconBlobs) >= 32 {
		clear(i.iconBlobs)
	}
	i.iconBlobs[cacheKey] = asset
	i.iconMu.Unlock()
	return asset, true
}

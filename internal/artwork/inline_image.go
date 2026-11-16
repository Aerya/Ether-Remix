package artwork

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"strings"
)

// InlineImage accepts small raster uploads only, not SVG/HTML or unbounded
// compressed images. The owner console resizes files before storing them.
func InlineImage(raw string) ([]byte, string, error) {
	if len(raw) > 350000 {
		return nil, "", fmt.Errorf("image exceeds 256 KiB")
	}
	head, body, ok := strings.Cut(raw, ",")
	mime := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if !ok || head != "data:"+mime+";base64" || (mime != "image/jpeg" && mime != "image/png" && mime != "image/webp") {
		return nil, "", fmt.Errorf("expected JPEG, PNG or WebP")
	}
	data, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(data) > 256<<10 {
		return nil, "", fmt.Errorf("invalid image")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 2400 || cfg.Height > 2400 || cfg.Width*cfg.Height > 4000000 || mime != "image/"+format {
		return nil, "", fmt.Errorf("invalid image dimensions or format")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", fmt.Errorf("invalid image data")
	}
	return data, mime, nil
}

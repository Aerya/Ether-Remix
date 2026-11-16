package artwork

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestInlineImageBounds(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	raw := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	data, mime, err := InlineImage(raw)
	if err != nil || mime != "image/png" || !bytes.Equal(data, b.Bytes()) {
		t.Fatal(mime, err)
	}
	for _, bad := range []string{strings.Replace(raw, "image/png", "image/jpeg", 1), "data:image/svg+xml;base64,AAAA", "data:image/png;base64," + strings.Repeat("A", 350001), "data:image/png;base64,aGVsbG8="} {
		if _, _, err := InlineImage(bad); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
}

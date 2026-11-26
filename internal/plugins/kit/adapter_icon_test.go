package kit

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestCheckAdapterIcons(t *testing.T) {
	var small, huge bytes.Buffer
	_ = png.Encode(&small, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	_ = png.Encode(&huge, image.NewRGBA(image.Rect(0, 0, 2048, 16)))
	cases := []struct {
		icon  string
		files map[string][]byte
		want  string
	}{
		{"assets/mark.svg", map[string][]byte{"assets/mark.svg": []byte(goodSVG)}, ""},
		{"assets/mark.png", map[string][]byte{"assets/mark.png": small.Bytes()}, ""},
		{"assets/mark.svg", nil, "missing adapter icon file"},
		{"assets/mark.png", map[string][]byte{"assets/mark.png": []byte(goodSVG)}, "do not match"},
		{"assets/mark.png", map[string][]byte{"assets/mark.png": huge.Bytes()}, "cannot be rendered"},
		{"assets/mark.svg", map[string][]byte{"assets/mark.svg": []byte(`<svg><script/></svg>`)}, "cannot be rendered"},
	}
	for _, tc := range cases {
		man := plugins.Manifest{ID: "com.example.adapter", Ether: &plugins.EtherBlock{}}
		man.Ether.Contributes.FrontendAdapters = []plugins.FrontendAdapterMeta{{Key: "feed", Icon: tc.icon}}
		var rep Report
		checkAdapterIcons(&rep, context.Background(), man, tc.files)
		got := ""
		for _, e := range rep.Errors {
			got += e.Message
		}
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Fatalf("%s: errors %q, want %q", tc.icon, got, tc.want)
		}
	}
}

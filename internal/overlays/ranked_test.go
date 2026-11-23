package overlays

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestRankedVariantsDurableCache(t *testing.T) {
	ctx := context.Background()
	img := image.NewRGBA(image.Rect(0, 0, 64, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, color.RGBA{220, uint8(x), uint8(y), 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	opts := Options{CacheDir: t.TempDir(), MemoryCacheBytes: -1}
	s := New(nil, opts)
	seen := map[string]bool{}
	for _, wide := range []bool{false, true} {
		for _, rank := range []int{1, 7, 10} {
			data, ct, err := s.Ranked(ctx, source.Bytes(), rank, wide)
			if err != nil || ct != "image/jpeg" {
				t.Fatalf("render: %s %v", ct, err)
			}
			if seen[string(data)] {
				t.Fatal("rank/aspect cache collision")
			}
			seen[string(data)] = true
			config, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			w, h := 500, 750
			if wide {
				w, h = 960, 540
			}
			if config.Width != w || config.Height != h {
				t.Fatal(config)
			}
			warm, _, err := New(nil, opts).Ranked(ctx, source.Bytes(), rank, wide)
			if err != nil || !bytes.Equal(data, warm) {
				t.Fatal("durable restart differs", err)
			}
		}
	}
	if _, _, err := s.Ranked(ctx, source.Bytes(), 11, false); err == nil {
		t.Fatal("accepted rank 11")
	}
}

func TestRankedBackdrop(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			img.SetRGBA(x, y, color.RGBA{220, 80, 40, 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Options{CacheDir: t.TempDir()})
	data, _, err := s.Ranked(context.Background(), source.Bytes(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != image.Rect(0, 0, 960, 540) {
		t.Fatal(got.Bounds())
	}
	r, g, b, _ := got.At(850, 250).RGBA()
	if r < 40000 || g < 10000 || b < 5000 {
		t.Fatal("backdrop missing from wide card", r, g, b)
	}
	other, _, err := s.Ranked(context.Background(), source.Bytes(), 10, true)
	if err != nil || bytes.Equal(data, other) {
		t.Fatal("rank missing", err)
	}
}

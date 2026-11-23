package plex

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"sync"

	"golang.org/x/image/draw"

	"github.com/braedonsaunders/ether/internal/frontend/kit/lru"
)

// PMS's photo transcoder returns the size a client asks for. Ether serves
// TMDB's fixed widths, rounded up: Plezy in TV mode asks for ~520-640 px
// posters and received w780 originals of 135-450 KB, so a 6-at-a-time grid
// on a tablet's Wi-Fi filled at about one poster per second. A source more
// than 15% wider than the request is scaled to the requested width and
// re-encoded once, then served from a bounded cache.

type photoFitKey struct {
	url   string
	width int
}

type photoFitEntry struct {
	data []byte
}

var photoFit = struct {
	mu    sync.Mutex
	cache *lru.Cache[photoFitKey, photoFitEntry]
}{cache: lru.New(lru.Options[photoFitKey, photoFitEntry]{MaxEntries: 4096, MaxCost: 192 << 20, Cost: func(_ photoFitKey, v photoFitEntry) int64 { return int64(len(v.data)) }})}

type photoFitWidthKey struct{}

func withPhotoFitWidth(ctx context.Context, width int) context.Context {
	if width <= 0 {
		return ctx
	}
	return context.WithValue(ctx, photoFitWidthKey{}, width)
}

// fitPhoto returns data scaled to the request's photo-transcode width, or
// data unchanged when no width was asked, the source is not a JPEG, or it is
// already within 15% of the request.
func fitPhoto(ctx context.Context, u string, data []byte, contentType string) []byte {
	width, _ := ctx.Value(photoFitWidthKey{}).(int)
	if width <= 0 || width > 4096 || contentType != "image/jpeg" {
		return data
	}
	key := photoFitKey{url: u, width: width}
	photoFit.mu.Lock()
	if hit, ok := photoFit.cache.Get(key); ok {
		photoFit.mu.Unlock()
		return hit.data
	}
	photoFit.mu.Unlock()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*100 <= width*115 {
		return data
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	height := max(1, cfg.Height*width/cfg.Width)
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}) != nil || out.Len() >= len(data) {
		return data
	}
	fitted := out.Bytes()
	photoFit.mu.Lock()
	photoFit.cache.Add(key, photoFitEntry{data: fitted})
	photoFit.mu.Unlock()
	return fitted
}

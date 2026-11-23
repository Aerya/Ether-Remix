package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestBackedRemoteCachePersistsTMDBOnDiskWithoutTTLRefetch(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 30))
	img.SetRGBA(0, 0, color.RGBA{R: 180, G: 60, B: 90, A: 255})
	var posterBuf bytes.Buffer
	if err := jpeg.Encode(&posterBuf, img, nil); err != nil {
		t.Fatal(err)
	}
	posterBytes := posterBuf.Bytes()

	var upstreamHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		// Even if TMDB CDN sends a short max-age, TMDB paths are immutable.
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", `"tmdb-1"`)
		_, _ = w.Write(posterBytes)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	imageURL := func(path, size string) string { return srv.URL + "/t/p/" + size + path }

	newStack := func(memBudget int64) (*artwork.Images, *RemoteCache, Source) {
		store := artwork.NewImages(artwork.ImagesOptions{
			Dir:         dir,
			MemoryBytes: memBudget,
			HTTPClient:  srv.Client(),
			ImageURL:    imageURL,
			Now:         func() time.Time { return now },
		})
		rc := NewBackedRemoteCache(memBudget, store)
		key := core.MovieKey(550)
		viewer := &core.User{ID: 1, IsOwner: true}
		src := Source{
			Catalog: storedArtwork{key: {
				Key:        key,
				Type:       core.TypeMovie,
				PosterPath: "/immutable550.jpg",
				Released:   time.Unix(1, 0),
			}},
			Visibility: experience.Visibility{Policy: &artworkPolicy{viewer: viewer}},
			Images:     rc,
			ImageURL:   imageURL,
			Now:        func() time.Time { return now },
			Variants:   func(*core.Title, string) (*tmdb.Images, bool) { return nil, false },
		}
		return store, rc, src
	}

	ctx := context.Background()
	viewer := &core.User{ID: 1, IsOwner: true}
	req := frontend.ArtworkRequest{Viewer: viewer, Item: core.MovieKey(550)}

	// Tiny memory budget forces entries out of the memory tier so disk serves them.
	store1, _, src1 := newStack(64)
	list, _, err := src1.List(ctx, req)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, ct, err := src1.Read(ctx, req, list[0], frontend.ArtworkTransform{Width: 500})
		if err != nil || ct != "image/jpeg" || !bytes.Equal(got, posterBytes) {
			t.Fatalf("read %d: ct=%q err=%v", i, ct, err)
		}
		// Advance past the former 24 h TTL and past the 30-day URL ceiling.
		now = now.Add(48 * time.Hour)
	}
	store1.Close()

	// Simulated server restart with warm memory budget over the same cache/images dir.
	store2, _, src2 := newStack(1 << 20)
	defer store2.Close()
	for i := 0; i < 50; i++ {
		got, ct, err := src2.Read(ctx, req, list[0], frontend.ArtworkTransform{Width: 500})
		if err != nil || ct != "image/jpeg" || !bytes.Equal(got, posterBytes) {
			t.Fatalf("post-restart read %d: ct=%q err=%v", i, ct, err)
		}
	}
	// Direct artwork.Images.Get shares the same disk key as frontend RemoteCache.
	if got, _, err := store2.Get(ctx, "/immutable550.jpg", 500); err != nil || !bytes.Equal(got, posterBytes) {
		t.Fatalf("shared disk key miss: %v", err)
	}
	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("immutable TMDB poster fetched %d times across 100 reads, 100 days, and restart; want 1", hits)
	}
}

package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
)

type rewriteHostTransport struct {
	target *url.URL
	rt     http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.rt.RoundTrip(clone)
}

func TestArtworkSharedFrontendPersistsTMDBInCacheImagesOnce(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 24, 36))
	img.SetRGBA(0, 0, color.RGBA{R: 120, G: 80, B: 200, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	posterBytes := buf.Bytes()

	var tmdbImageHits atomic.Int32
	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/t/p/w500/steward.jpg" {
			tmdbImageHits.Add(1)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(posterBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer tmdbSrv.Close()

	targetURL, err := url.Parse(tmdbSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	prevHTTP := testImagesHTTP
	testImagesHTTP = &http.Client{
		Transport: rewriteHostTransport{target: targetURL, rt: tmdbSrv.Client().Transport},
	}
	defer func() { testImagesHTTP = prevHTTP }()

	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.Plex.Token = ""
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: false}}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	openApp := func() *App {
		a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	a1 := openApp()
	local, err := identity.NewLocal(a1.db)
	if err != nil {
		a1.Close()
		t.Fatal(err)
	}
	ownerID, err := local.CreateOwner(ctx, "steward-owner", "offline-test-password")
	if err != nil {
		a1.Close()
		t.Fatal(err)
	}
	if err := a1.users.RefreshViewers(ctx); err != nil {
		a1.Close()
		t.Fatal(err)
	}
	viewer, err := a1.users.ActiveUser(ctx, ownerID)
	if err != nil {
		a1.Close()
		t.Fatal(err)
	}
	title := core.Title{
		Key:        core.MovieKey(777),
		Type:       core.TypeMovie,
		IDs:        core.IDs{TMDB: 777},
		Title:      "Stewardship",
		PosterPath: "/steward.jpg",
		Released:   time.Unix(1, 0),
	}
	if err := a1.catalog.UpsertTitle(ctx, title, true); err != nil {
		a1.Close()
		t.Fatal(err)
	}

	src1 := *a1.artwork
	src1.Variants = nil
	src1.Metadata = nil
	src1.Palettes = nil
	req := frontend.ArtworkRequest{Viewer: viewer, Item: title.Key}
	list, _, err := src1.List(ctx, req)
	if err != nil || len(list) == 0 {
		a1.Close()
		t.Fatalf("list: %v", err)
	}
	for i := 0; i < 10; i++ {
		got, ct, err := src1.Read(ctx, req, list[0], frontend.ArtworkTransform{Width: 500})
		if err != nil || ct != "image/jpeg" || !bytes.Equal(got, posterBytes) {
			a1.Close()
			t.Fatalf("read %d: ct=%q err=%v", i, ct, err)
		}
	}
	a1.Close()

	entries, err := os.ReadDir(filepath.Join(dir, "cache", "images"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected disk cache entries in cache/images: %v", err)
	}

	// Second App instance on the same DataDir serves the poster from disk without contacting TMDB.
	a2 := openApp()
	defer a2.Close()
	viewer2, err := a2.users.ActiveUser(ctx, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	src2 := *a2.artwork
	src2.Variants = nil
	src2.Metadata = nil
	src2.Palettes = nil
	req2 := frontend.ArtworkRequest{Viewer: viewer2, Item: title.Key}
	got, ct, err := src2.Read(ctx, req2, list[0], frontend.ArtworkTransform{Width: 500})
	if err != nil || ct != "image/jpeg" || !bytes.Equal(got, posterBytes) {
		t.Fatalf("post-restart read: ct=%q err=%v", ct, err)
	}
	if hits := tmdbImageHits.Load(); hits != 1 {
		t.Fatalf("TMDB poster fetched %d times across restart, want 1", hits)
	}
}

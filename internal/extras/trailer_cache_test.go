package extras

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestTrailerCacheBoundsAndEviction(t *testing.T) {
	ctx := context.Background()
	payload := []byte("0000ftypisom00000000000000000000")
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads++; _, _ = w.Write(payload) }))
	defer server.Close()
	cfg := config.Extras{Enabled: true, TrailerCacheMaxBytes: 64}
	now := time.Now()
	opts := Options{CacheDir: t.TempDir(), Config: func() config.Extras { return cfg }, Now: func() time.Time { return now }, LookPath: func(s string) (string, error) { return s, nil }, Runner: func(context.Context, string, []string) ([]byte, error) { return []byte(server.URL), nil }}
	s := New(opts)
	extra := func(key string) Extra {
		return Extra{ParentKey: core.MovieKey(1), Source: SourceCommand, Site: "YouTube", SiteKey: key, Subtype: "trailer"}
	}
	a, err := s.CacheTrailer(ctx, extra("aaaaaaaaaaa"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	b, err := s.CacheTrailer(ctx, extra("bbbbbbbbbbb"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = s.CacheTrailer(ctx, extra("aaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = s.CacheTrailer(ctx, extra("ccccccccccc")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(a.Path); err != nil {
		t.Fatal("recent hit was evicted", err)
	}
	if _, err = os.Stat(b.Path); !os.IsNotExist(err) {
		t.Fatal("oldest file survived", err)
	}
	if downloads != 3 {
		t.Fatal("hit downloaded again", downloads)
	}
	// Restart retains bytes without invoking the resolver.
	restarted := New(opts)
	if _, err = restarted.CacheTrailer(ctx, extra("aaaaaaaaaaa")); err != nil || downloads != 3 {
		t.Fatal("cache did not survive restart", err, downloads)
	}
	cfg.TrailerCacheMaxBytes = 32
	if _, err = restarted.CacheTrailer(ctx, extra("aaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(opts.CacheDir)
	var bytes int64
	for _, entry := range entries {
		info, _ := os.Stat(filepath.Join(opts.CacheDir, entry.Name()))
		bytes += info.Size()
	}
	if bytes > 32 {
		t.Fatal("lowered cap exceeded", bytes)
	}
	cfg.ResolverArgs = []string{"changed"}
	if f, err := s.OpenCachedTrailer(a); err == nil {
		f.Close()
		t.Fatal("stale source generation opened")
	}
	cfg.TrailerCacheMaxBytes = 16
	if _, err = s.CacheTrailer(ctx, extra("ddddddddddd")); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err = os.Stat(filepath.Join(opts.CacheDir, "download.part")); !os.IsNotExist(err) {
		t.Fatal("staging survived")
	}
	s.cacheDownloadMu.Lock()
	if _, err = s.CacheTrailer(ctx, extra("eeeeeeeeeee")); err != ErrCachePreparing {
		t.Fatal("concurrent fill admitted", err)
	}
	s.cacheDownloadMu.Unlock()
}

func TestDownloadExtraSizeCapAndSkippedFallback(t *testing.T) {
	ctx := context.Background()
	mp4 := []byte("0000ftypisom00000000000000000000")

	// Smart default entry limit covers >64 MiB 1080p trailers under the 512 MiB
	// cache cap, scales up for larger caps, and never exceeds a lowered cap.
	if got := trailerEntryLimit(defaultTrailerCacheMaxBytes); got < 128<<20 || got > defaultTrailerCacheMaxBytes {
		t.Fatalf("default entry limit = %d, want >= 128 MiB and <= 512 MiB", got)
	}
	if got := trailerEntryLimit(1 << 30); got != 256<<20 {
		t.Fatalf("1 GiB cap entry limit = %d, want 256 MiB", got)
	}
	if got := trailerEntryLimit(32 << 20); got != 32<<20 {
		t.Fatalf("32 MiB cap entry limit = %d, want 32 MiB", got)
	}

	// Simulate yt-dlp skipping 1080p (exits 0 leaving only partial/audio files
	// and no media.mp4) and succeeding when retried at <=720p.
	var seenFormats []string
	cacheDir := t.TempDir()
	s := New(Options{
		CacheDir:    cacheDir,
		ThemeFFmpeg: func() string { return "/bin/true" },
		Config:      func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath:    func(cmd string) (string, error) { return cmd, nil },
		Runner: func(_ context.Context, _ string, args []string) ([]byte, error) {
			if slices.Contains(args, "--max-filesize") {
				t.Fatalf("downloadExtra must not pass --max-filesize: %v", args)
			}
			var format, outTmpl string
			for i := 0; i+1 < len(args); i++ {
				switch args[i] {
				case "-f":
					format = args[i+1]
				case "-o":
					outTmpl = args[i+1]
				}
			}
			seenFormats = append(seenFormats, format)
			dir := filepath.Dir(outTmpl)
			if strings.Contains(format, "[height<=1080]") {
				// Leave unmerged artifacts like yt-dlp does when a stream is skipped.
				_ = os.WriteFile(filepath.Join(dir, "media.f137.mp4.part"), []byte("part"), 0600)
				_ = os.WriteFile(filepath.Join(dir, "media.f140.m4a"), []byte("audio"), 0600)
				return []byte("[download] File is larger than max-filesize. Aborting.\n"), nil
			}
			if err := os.WriteFile(filepath.Join(dir, "media.mp4"), mp4, 0600); err != nil {
				return nil, err
			}
			return nil, nil
		},
	})
	s.downloadTrailers = true
	t.Cleanup(s.Close)

	clip := Extra{ParentKey: core.MovieKey(7), Source: SourceCommand, Site: "YouTube", SiteKey: "xI4vbUvezLU", Subtype: "trailer"}
	cached, err := s.CacheExtra(ctx, clip)
	if err != nil {
		t.Fatalf("CacheExtra fallback failed: %v", err)
	}
	if _, err := os.Stat(cached.Path); err != nil {
		t.Fatalf("cached MP4 missing: %v", err)
	}
	if len(seenFormats) != 2 || !strings.Contains(seenFormats[0], "[height<=1080][filesize<=?") || !strings.Contains(seenFormats[1], "[height<=720][filesize<=?") {
		t.Fatalf("unexpected format attempts: %v", seenFormats)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "source.part")); !os.IsNotExist(err) {
		t.Fatalf("source.part staging survived: %v", err)
	}

	// If every format is skipped and exits 0 without media.mp4, report a clear
	// size-limit error instead of os.ErrNotExist from os.Stat.
	sAllSkipped := New(Options{
		CacheDir:    t.TempDir(),
		ThemeFFmpeg: func() string { return "/bin/true" },
		Config:      func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath:    func(cmd string) (string, error) { return cmd, nil },
		Runner: func(_ context.Context, _ string, _ []string) ([]byte, error) {
			return nil, nil
		},
	})
	sAllSkipped.downloadTrailers = true
	t.Cleanup(sAllSkipped.Close)

	_, err = sAllSkipped.CacheExtra(ctx, clip)
	if err == nil || errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "cache entry limit") {
		t.Fatalf("all-skipped error = %v, want explicit cache entry limit error", err)
	}
}

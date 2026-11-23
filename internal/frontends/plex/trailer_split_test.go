package plex

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/extras"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type cachedSplitExtras struct {
	*fakeExtrasProvider
	service        *extras.Service
	resolveStarted chan struct{}
}

func (s *cachedSplitExtras) Resolve(ctx context.Context, e frontend.ExtraClip) (frontend.ResolvedExtra, error) {
	if s.resolveStarted != nil {
		select {
		case s.resolveStarted <- struct{}{}:
		default:
		}
	}
	cached, err := s.service.CacheExtra(ctx, extras.Extra{Key: e.Key, ParentKey: e.ParentKey, Source: extras.SourceCommand, Site: e.Site, SiteKey: e.SiteKey, Subtype: e.Subtype})
	return frontend.ResolvedExtra{LocalPath: cached.Path, Container: "mp4"}, err
}

// The invariant is complete audio+video bytes across a shared fill, playback,
// and byte-range seeking. This uses real muxing and entirely offline inputs.
func TestSplitTrailerMuxAndPlexRange(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, input := range []struct {
		name string
		args []string
	}{
		{"video.mp4", []string{"-f", "lavfi", "-i", "color=size=64x64:rate=10", "-t", "0.5", "-an", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p"}},
		{"audio.m4a", []string{"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "0.5", "-vn", "-c:a", "aac", "-threads", "1"}},
	} {
		args := append([]string{"-nostdin", "-v", "error", "-y"}, input.args...)
		out, err := exec.CommandContext(ctx, ff, append(args, filepath.Join(dir, input.name))...).CombinedOutput()
		if err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	upstream := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer upstream.Close()
	var resolves atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	cfg := config.Extras{Enabled: true, ResolverCommand: "fake-resolver"}
	service := extras.New(extras.Options{Config: func() config.Extras { return cfg }, CacheDir: filepath.Join(dir, "cache"), ThemeFFmpeg: func() string { return ff }, LookPath: func(s string) (string, error) { return s, nil }, Runner: func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		if resolves.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []byte(upstream.URL + "/video.mp4\n" + upstream.URL + "/audio.m4a\n"), nil
	}})
	defer service.Close()
	f, ex, _ := extrasFixture(t)
	provider := &cachedSplitExtras{fakeExtrasProvider: ex, service: service, resolveStarted: make(chan struct{}, 1)}
	f.srv.setExtras(provider)
	// A preview starts the fill; Plex joins it instead of getting a 404 or
	// launching a second resolver/muxer.
	type result struct {
		cached extras.CachedTrailer
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		c, e := service.CacheTrailer(ctx, extras.Extra{Key: fightClubTrailer.Key, ParentKey: fightClubTrailer.ParentKey, Source: extras.SourceCommand, Site: fightClubTrailer.Site, SiteKey: fightClubTrailer.SiteKey, Subtype: "trailer"})
		finished <- result{c, e}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	path := "/library/parts/" + fightClubTrailer.Key.String() + "/1/file.mp4"
	served := make(chan *httptest.ResponseRecorder, 1)
	go func() { served <- f.get(t, path) }()
	select {
	case <-provider.resolveStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	first := <-finished
	if first.err != nil {
		t.Fatal(first.err)
	}
	full := <-served
	if full.Code != http.StatusOK {
		t.Fatalf("part: %d %s", full.Code, full.Body.String())
	}
	output := filepath.Join(dir, "delivered.mp4")
	if err := os.WriteFile(output, full.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_name,codec_type", "-of", "json", output).Output()
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			CodecType string `json:"codec_type"`
		}
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Streams) != 2 || info.Streams[0].CodecName != "h264" || info.Streams[1].CodecName != "aac" {
		t.Fatalf("streams: %s", raw)
	}
	for _, rng := range []struct {
		header     string
		start, end int
	}{{"bytes=0-31", 0, 32}, {"bytes=128-191", 128, 192}} {
		part := f.request(t, http.MethodGet, path, map[string]string{"Range": rng.header})
		if part.Code != http.StatusPartialContent || !bytes.Equal(part.Body.Bytes(), full.Body.Bytes()[rng.start:rng.end]) || part.Header().Get("Content-Range") == "" {
			t.Fatalf("range: %d %v", part.Code, part.Header())
		}
	}
	if resolves.Load() != 1 {
		t.Fatal("fill ran again", resolves.Load())
	}
	if bytes.Index(full.Body.Bytes(), []byte("moov")) > bytes.Index(full.Body.Bytes(), []byte("mdat")) {
		t.Fatal("MP4 is not faststart")
	}
}

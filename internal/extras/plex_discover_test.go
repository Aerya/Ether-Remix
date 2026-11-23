package extras

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestPlexDiscoverExtrasSource(t *testing.T) {
	const (
		discoverHex = "5d776827999c64001ec2c7f8"
		plexToken   = "secret-plex-token"
	)
	var (
		failDiscover atomic.Bool
		sigSeq       atomic.Int64
		cdnSawToken  atomic.Bool
		mp4Body      = append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 64)...)
	)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/" + discoverHex + "/extras":
			if failDiscover.Load() {
				http.Error(w, "upstream down", http.StatusBadGateway)
				return
			}
			sig := sigSeq.Add(1)
			payload := map[string]any{
				"MediaContainer": map[string]any{
					"Metadata": []map[string]any{
						{
							"ratingKey":             "disc-trl-1",
							"title":                 "Goldfinger Official Trailer",
							"subtype":               "trailer",
							"extraType":             1,
							"duration":              191000,
							"originallyAvailableAt": "1964-09-17",
							"Media": []map[string]any{
								{
									"container":       "mp4",
									"videoCodec":      "h264",
									"audioCodec":      "aac",
									"videoResolution": "720",
									"bitrate":         2500,
									"duration":        191000,
									"url":             fmt.Sprintf("%s/cdn/trailer.mp4?sig=%d", srv.URL, sig),
									"Part": []map[string]any{
										{"key": "/library/metadata/" + discoverHex + "/extras/disc-trl-1/parts/mp4-2500"},
									},
								},
							},
						},
						{
							"ratingKey": "disc-scene-1",
							"title":     "Laser Scene",
							"subtype":   "sceneOrSample",
							"extraType": 6,
							"duration":  95000,
							"Media": []map[string]any{
								{
									"container":       "mp4",
									"videoCodec":      "h264",
									"audioCodec":      "aac",
									"videoResolution": "720",
									"bitrate":         2200,
									"Part": []map[string]any{
										{"key": "/library/metadata/" + discoverHex + "/extras/disc-scene-1/parts/mp4-2200"},
									},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case "/library/metadata/" + discoverHex + "/extras/disc-scene-1/parts/mp4-2200":
			if r.Header.Get("X-Plex-Token") != plexToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Location", srv.URL+"/cdn/scene.mp4")
			w.WriteHeader(http.StatusFound)
		case "/cdn/trailer.mp4", "/cdn/scene.mp4":
			if r.Header.Get("X-Plex-Token") != "" || strings.Contains(r.URL.RawQuery, plexToken) {
				cdnSawToken.Store(true)
			}
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mp4Body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tokenVal := plexToken
	discover := NewPlexDiscover(PlexDiscoverOptions{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   func() string { return tokenVal },
		GUID:    func(context.Context, core.RatingKey) string { return "plex://movie/" + discoverHex },
	})
	movie := &core.Title{Key: core.MovieKey(658), Type: core.TypeMovie, Title: "Goldfinger", IDs: core.IDs{TMDB: 658}}
	ytVideos := fakeVideos{vs: []tmdb.Video{
		{Site: "YouTube", Key: "dQw4w9WgXcQ", Name: "YouTube Trailer", Type: tmdb.VideoTrailer, Official: true, ISO639: "en", Size: 1080},
	}}

	// 1. Discover enables extras without yt-dlp or any resolver command.
	noCmd := New(Options{
		Discover:  discover,
		Videos:    ytVideos,
		CacheDir:  t.TempDir(),
		CacheHTTP: srv.Client(),
		Config:    func() config.Extras { return config.Extras{Enabled: true} },
		LookPath:  func(string) (string, error) { return "", exec.ErrNotFound },
	})
	t.Cleanup(noCmd.Close)
	if !noCmd.Enabled() || noCmd.CommandAvailable() || !noCmd.DiscoverAvailable() || noCmd.DisabledReason() != "" {
		t.Fatalf("Discover alone should enable extras without yt-dlp: enabled=%v cmd=%v discover=%v reason=%q",
			noCmd.Enabled(), noCmd.CommandAvailable(), noCmd.DiscoverAvailable(), noCmd.DisabledReason())
	}

	// 2. With both Discover and command available, Discover trailer orders first,
	// resolves fresh signed URLs per play, resolves Part.key 302 server-side
	// without exposing the Plex token, and caches MP4 without invoking yt-dlp.
	var runnerCalls atomic.Int32
	svc := New(Options{
		Discover:  discover,
		Videos:    ytVideos,
		CacheDir:  t.TempDir(),
		CacheHTTP: srv.Client(),
		Config:    func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath:  func(s string) (string, error) { return s, nil },
		Runner: func(context.Context, string, []string) ([]byte, error) {
			runnerCalls.Add(1)
			return nil, errors.New("yt-dlp should not run for Plex Discover extras")
		},
	})
	t.Cleanup(svc.Close)

	list, err := svc.List(context.Background(), movie, "en")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 || list[0].Source != SourcePlexDiscover || !list[0].IsTrailer() {
		t.Fatalf("want Plex Discover trailer first, got %+v", list)
	}
	if list[0].Site != "" || list[0].SiteKey != "" {
		t.Fatalf("Discover extra must not set YouTube/Vimeo Site/SiteKey: %+v", list[0])
	}

	// Signed URL is resolved fresh per play and never carries X-Plex-Token.
	r1, err := svc.Resolve(context.Background(), list[0])
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	r2, err := svc.Resolve(context.Background(), list[0])
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if r1.URL == r2.URL || r1.Container != "mp4" || len(r1.Headers) != 0 || strings.Contains(r1.URL, plexToken) {
		t.Fatalf("expected fresh token-free signed MP4 URL per Resolve: r1=%+v r2=%+v", r1, r2)
	}

	// Part.key-only extra resolves 302 server-side with token and returns clean media URL.
	var scene Extra
	for _, item := range list {
		if item.Source == SourcePlexDiscover && item.Subtype == "sceneOrSample" {
			scene = item
			break
		}
	}
	if scene.Key == 0 {
		t.Fatalf("missing Discover sceneOrSample extra in %+v", list)
	}
	rs, err := svc.Resolve(context.Background(), scene)
	if err != nil {
		t.Fatalf("Resolve Part.key fallback: %v", err)
	}
	if rs.URL != srv.URL+"/cdn/scene.mp4" || len(rs.Headers) != 0 {
		t.Fatalf("Part.key redirect = %+v, want clean /cdn/scene.mp4 without headers", rs)
	}

	// Caching Discover trailer downloads MP4 directly without invoking yt-dlp or sending Plex token.
	cached, err := svc.CacheExtra(context.Background(), list[0])
	if err != nil {
		t.Fatalf("CacheExtra: %v", err)
	}
	if _, err := os.Stat(cached.Path); err != nil {
		t.Fatalf("cached MP4 missing: %v", err)
	}
	if runnerCalls.Load() != 0 {
		t.Fatalf("yt-dlp runner called %d times for Discover MP4", runnerCalls.Load())
	}
	if cdnSawToken.Load() {
		t.Fatal("Plex token leaked to media CDN")
	}

	// 3. Fallback: when Discover fails, TMDB + command extras still list cleanly.
	failDiscover.Store(true)
	fallbackSvc := New(Options{
		Discover:  discover,
		Videos:    ytVideos,
		CacheDir:  t.TempDir(),
		CacheHTTP: srv.Client(),
		Now:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Config:    func() config.Extras { return config.Extras{Enabled: true, ResolverCommand: "yt-dlp"} },
		LookPath:  func(s string) (string, error) { return s, nil },
	})
	t.Cleanup(fallbackSvc.Close)
	fb, err := fallbackSvc.List(context.Background(), movie, "en")
	if err != nil {
		t.Fatalf("fallback List: %v", err)
	}
	if len(fb) == 0 || fb[0].Source != SourceCommand || fb[0].SiteKey != "dQw4w9WgXcQ" {
		t.Fatalf("expected clean fallback to TMDB+command, got %+v", fb)
	}
}

func TestPlexDiscoverVariantFallbackBySize(t *testing.T) {
	const (
		discoverHex = "5d7769c6f617c900201648c0"
		plexToken   = "secret-plex-token"
	)
	makeMP4 := func(total int, tag string) []byte {
		b := make([]byte, total)
		copy(b, []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
		copy(b[12:], tag)
		return b
	}
	body5000 := makeMP4(200, "5000")
	body2500 := makeMP4(100, "2500")
	body750 := makeMP4(40, "750!")

	var (
		cdnSawToken atomic.Bool
		servedPaths []string
	)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/" + discoverHex + "/extras":
			// Mirror real Plex Discover: all three variants share Media.url with
			// videokbrate=5000 and differ only by Part.key.
			sharedURL := srv.URL + "/cdn/video.mp4?publishedid=611198&videokbrate=5000"
			payload := map[string]any{
				"MediaContainer": map[string]any{
					"Metadata": []map[string]any{
						{
							"ratingKey": "disc-multi-1",
							"title":     "Official Trailer",
							"subtype":   "trailer",
							"extraType": 1,
							"duration":  152000,
							"Media": []map[string]any{
								{
									"container": "mp4", "videoCodec": "h264", "audioCodec": "aac",
									"videoResolution": "1080", "height": 1080, "bitrate": 5000, "duration": 152000,
									"url":  sharedURL,
									"Part": []map[string]any{{"key": "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-5000"}},
								},
								{
									"container": "mp4", "videoCodec": "h264", "audioCodec": "aac",
									"videoResolution": "720", "height": 720, "bitrate": 2500, "duration": 152000,
									"url":  sharedURL,
									"Part": []map[string]any{{"key": "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-2500"}},
								},
								{
									"container": "mp4", "videoCodec": "h264", "audioCodec": "aac",
									"videoResolution": "sd", "bitrate": 750, "duration": 152000,
									"url":  sharedURL,
									"Part": []map[string]any{{"key": "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-750"}},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-5000":
			w.Header().Set("Location", srv.URL+"/cdn/video.mp4?publishedid=611198&videokbrate=5000")
			w.WriteHeader(http.StatusFound)
		case "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-2500":
			w.Header().Set("Location", srv.URL+"/cdn/video.mp4?publishedid=611198&videokbrate=2500")
			w.WriteHeader(http.StatusFound)
		case "/library/metadata/" + discoverHex + "/extras/disc-multi-1/parts/mp4-750":
			w.Header().Set("Location", srv.URL+"/cdn/video.mp4?publishedid=611198&videokbrate=750")
			w.WriteHeader(http.StatusFound)
		case "/cdn/video.mp4":
			if r.Header.Get("X-Plex-Token") != "" || strings.Contains(r.URL.RawQuery, plexToken) {
				cdnSawToken.Store(true)
			}
			kb := r.URL.Query().Get("videokbrate")
			servedPaths = append(servedPaths, kb)
			w.Header().Set("Content-Type", "video/mp4")
			switch kb {
			case "5000":
				_, _ = w.Write(body5000)
			case "2500":
				_, _ = w.Write(body2500)
			case "750":
				_, _ = w.Write(body750)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	discover := NewPlexDiscover(PlexDiscoverOptions{
		BaseURL: srv.URL,
		HTTP:    srv.Client(),
		Token:   func() string { return plexToken },
		GUID:    func(context.Context, core.RatingKey) string { return "plex://movie/" + discoverHex },
	})
	movie := &core.Title{Key: core.MovieKey(153518), Type: core.TypeMovie, Title: "The Angry Birds Movie"}

	runWithCap := func(capBytes int64) ([]byte, error) {
		servedPaths = nil
		svc := New(Options{
			Discover:  discover,
			CacheDir:  t.TempDir(),
			CacheHTTP: srv.Client(),
			Config:    func() config.Extras { return config.Extras{Enabled: true, TrailerCacheMaxBytes: capBytes} },
			LookPath:  func(string) (string, error) { return "", exec.ErrNotFound },
		})
		t.Cleanup(svc.Close)
		list, err := svc.List(context.Background(), movie, "en")
		if err != nil || len(list) == 0 {
			t.Fatalf("List: err=%v list=%+v", err, list)
		}
		cached, err := svc.CacheExtra(context.Background(), list[0])
		if err != nil {
			return nil, err
		}
		return os.ReadFile(cached.Path)
	}

	// 1. Cap = 128 B: 5000 kbps (200 B) rejected, falls back via Part.key to 2500 kbps (100 B).
	got, err := runWithCap(128)
	if err != nil {
		t.Fatalf("cap=128 should fall back to 720p (2500 kbps): %v", err)
	}
	if string(got[12:16]) != "2500" {
		t.Fatalf("cap=128 cached variant = %q, want 2500 (tried %v)", string(got[12:16]), servedPaths)
	}

	// 2. Cap = 64 B: 5000 kbps (200 B) and 2500 kbps (100 B) rejected, falls back to SD (40 B).
	got, err = runWithCap(64)
	if err != nil {
		t.Fatalf("cap=64 should fall back to sd (750 kbps): %v", err)
	}
	if string(got[12:16]) != "750!" {
		t.Fatalf("cap=64 cached variant = %q, want 750! (tried %v)", string(got[12:16]), servedPaths)
	}

	// 3. Cap = 20 B: all variants exceed limit; error names actual size and limit.
	_, err = runWithCap(20)
	if err == nil || !strings.Contains(err.Error(), "40 bytes") || !strings.Contains(err.Error(), "20 bytes") {
		t.Fatalf("cap=20 error = %v, want actual size (40 bytes) and limit (20 bytes)", err)
	}
	if cdnSawToken.Load() {
		t.Fatal("Plex token leaked to media CDN")
	}
}

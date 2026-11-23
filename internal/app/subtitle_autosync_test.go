package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/subtitles"
)

type listingSessionFinder struct {
	plain   *core.PlaySession
	surface map[string]*core.PlaySession
	all     []*core.PlaySession
}

func (f listingSessionFinder) SessionFor(int64, string, core.RatingKey) (*core.PlaySession, bool) {
	if f.plain == nil {
		return nil, false
	}
	return f.plain, true
}

func (f listingSessionFinder) SessionForSurfaceClient(surface string, _ int64, _ string, _ core.RatingKey) (*core.PlaySession, bool) {
	if s := f.surface[surface]; s != nil {
		return s, true
	}
	return nil, false
}

func (f listingSessionFinder) Sessions() []*core.PlaySession { return f.all }

func TestLookupPlaySessionSkipsPreparedOnlyAndFindsActiveWithoutClientID(t *testing.T) {
	key := core.MovieKey(101)
	prep := &core.PlaySession{ID: "prep", UserID: 7, ClientID: "tv", Key: key, PreparedOnly: true}
	live := &core.PlaySession{ID: "live", UserID: 7, ClientID: "tv", Key: key, PreparedOnly: false}

	fPrep := listingSessionFinder{
		plain:   prep,
		surface: map[string]*core.PlaySession{"jellyfin": prep},
		all:     []*core.PlaySession{prep},
	}
	if got := lookupPlaySession(fPrep, "", 7, "tv", key); got != nil {
		t.Fatalf("PreparedOnly returned for plex client: %+v", got)
	}
	if got := lookupPlaySession(fPrep, "jellyfin", 7, "tv", key); got != nil {
		t.Fatalf("PreparedOnly returned for jellyfin client: %+v", got)
	}
	if got := lookupPlaySession(fPrep, "", 7, "", key); got != nil {
		t.Fatalf("PreparedOnly returned for empty clientID: %+v", got)
	}

	fLive := listingSessionFinder{
		plain: prep,
		all:   []*core.PlaySession{prep, live},
	}
	if got := lookupPlaySession(fLive, "", 7, "", key); got == nil || got.ID != "live" {
		t.Fatalf("empty clientID did not resolve active session: %+v", got)
	}
}

func TestPrePlayDownloadedSubtitleAlignsOnPlaybackOpenWithoutPrefetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	body, err := os.ReadFile("../transcode/testdata/nativeprobe/h264_aac_movtext_faststart.mp4")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true, Settings: map[string]any{"discovery": false}},
		}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.OpenSubtitles = config.OpenSubtitles{Enabled: true, APIKey: "offline-fixture"}
		for i := range c.Quality.Profiles {
			c.Quality.Profiles[i].MinSizeMBPerMin = 0
			c.Quality.Profiles[i].MaxSizeMBPerMin = 0
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	media := &operabilityMedia{tmdb: 1234, relID: "autosync-rel", body: body}
	encoder := &operabilityTranscoder{max: 1}
	a, err := New(ctx, Options{
		DataDir:           dir,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		FixtureMedia:      media,
		FixtureTranscoder: encoder,
		plextvHTTP:        &http.Client{Transport: fakePlexTVTransport{}},
		diskUsage:         func() (int64, int64, error) { return 1 << 40, 1 << 39, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Reference timeline uploaded for the title; downloaded French sidecar is shifted +1.8s
	// and timed for 25 fps instead of 23.976 fps.
	ref := make([]subtitles.Cue, 20)
	cand := make([]subtitles.Cue, 20)
	const fps23976 = 24000.0 / 1001.0
	const fps25 = 25.0
	tCur := 2 * time.Second
	for i := 0; i < 20; i++ {
		ref[i] = subtitles.Cue{Start: tCur, End: tCur + 700*time.Millisecond, Text: "Ref"}
		cStart := time.Duration(math.Round(float64(tCur+1800*time.Millisecond) * (fps25 / fps23976)))
		cand[i] = subtitles.Cue{Start: cStart, End: cStart + 700*time.Millisecond, Text: "Bonjour"}
		tCur += time.Duration(1500+((i*11)%13)*200) * time.Millisecond
	}

	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"attributes": map[string]any{
				"language": "fr", "release": "Other.PAL.DVDRip", "download_count": 200,
				"files": []any{map[string]any{"file_id": 901, "file_name": "fr.srt"}},
			}}}})
		case "/download":
			_ = json.NewEncoder(w).Encode(map[string]any{"link": provider.URL + "/file", "remaining": 99})
		case "/file":
			_, _ = io.WriteString(w, subtitles.RenderSRT(cand))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	a.subtitles = subtitles.New(subtitles.Options{
		Config:   cfg.Read().OpenSubtitles,
		CacheDir: filepath.Join(dir, "subtitles"),
		BaseURL:  provider.URL,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err = a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "fixture-owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerUser, err := a.users.ActiveUser(ctx, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{
		Key: core.MovieKey(1234), Type: core.TypeMovie, IDs: core.IDs{TMDB: 1234},
		Title: "Autosync Movie", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Popularity: 100, VoteCount: 10000,
	}
	if err = a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.subtitles.StoreUpload(&title, "en.srt", "srt", "en", []byte(subtitles.RenderSRT(ref))); err != nil {
		t.Fatal(err)
	}

	// Viewer picks French subtitle on pre-play screen before any playback session exists.
	surf := subtitleSurface{app: a}
	if _, err := a.subtitles.SearchOnDemand(ctx, &title, "fr", ""); err != nil {
		t.Fatal(err)
	}
	picked, err := surf.DownloadSubtitle(ctx, &title, ownerUser, 901)
	if err != nil {
		t.Fatal(err)
	}
	// No background prefetch: media must not have been opened yet.
	if media.opensOf("autosync-rel") != 0 || a.subtitles.HasCorrection(picked.ExternalURL, "autosync-rel") {
		t.Fatalf("prefetch ran before playback: opens=%d hasCorr=%v", media.opensOf("autosync-rel"), a.subtitles.HasCorrection(picked.ExternalURL, "autosync-rel"))
	}

	// Planning reserves a PreparedOnly source; alignment still waits for real playback Open.
	b := a.frontends.broker
	device := core.Device{ID: "autosync-device"}
	caps := core.ClientCaps{ClientID: device.ID, Product: "offline", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, SubtitleCodecs: []string{"srt", "mov_text"}}
	plan, err := b.Plan(ctx, frontend.PlanRequest{
		Viewer: viewer, Device: device, Protocol: "jellyfin", Item: title.Key, Caps: caps,
		Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.subtitles.HasCorrection(picked.ExternalURL, "autosync-rel") {
		t.Fatal("PreparedOnly plan triggered subtitle alignment")
	}

	// Opening the session starts playback and triggers one-shot alignment for attached sidecars.
	if _, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID}); err != nil {
		t.Fatal(err)
	}

	rc, _, err := a.subtitles.OpenFormat(picked.ExternalURL, "srt")
	if err != nil {
		t.Fatal(err)
	}
	alignedRaw, _ := io.ReadAll(rc)
	_ = rc.Close()
	alignedCues, err := subtitles.ParseCues(string(alignedRaw))
	if err != nil || len(alignedCues) != len(ref) {
		t.Fatalf("parse aligned: %v (%d)", err, len(alignedCues))
	}
	lastDiff := alignedCues[len(alignedCues)-1].Start - ref[len(ref)-1].Start
	if lastDiff < 0 {
		lastDiff = -lastDiff
	}
	if lastDiff > 50*time.Millisecond {
		t.Fatalf("last cue = %s, want %s", alignedCues[len(alignedCues)-1].Start, ref[len(ref)-1].Start)
	}
}

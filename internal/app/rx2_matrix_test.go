package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

type rx2Viewer struct {
	ID      int64
	Cap     int
	Blocked bool
}

type rx2Fixture struct {
	Name      string
	Config    json.RawMessage
	Viewers   []rx2Viewer
	Libraries []core.Library
}

type rx2Choice struct {
	Profile         map[string]any
	Bitrate         int
	Stereo, Blocked bool
}

func rx2JSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func rx2Read(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func rx2Write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func rx2Profile(t *testing.T, profile core.QualityProfile, title *core.Title) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(rx2JSON(t, profile), &result); err != nil {
		t.Fatal(err)
	}
	delete(result, "mediaType")
	if title.Type == core.TypeMovie {
		delete(result, "episodeMinSizeMbPerMin")
		delete(result, "episodeMaxSizeMbPerMin")
		if scores, ok := result["customFormatScores"].(map[string]any); ok {
			delete(scores, "ether:series-year-differs")
			if len(scores) == 0 {
				delete(result, "customFormatScores")
			}
		}
	}
	return result
}

func rx2Boot(t *testing.T, fixture rx2Fixture) (*App, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	rx2Write(t, filepath.Join(dir, "config.json"), fixture.Config)
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range fixture.Libraries {
		id := lib.ID
		if id == 0 {
			created, err := svc.Create(ctx, lib.Name, lib.MediaType, lib.Rules, "hide", core.Presentations{})
			if err != nil {
				t.Fatal(err)
			}
			id = created.ID
		} else if len(lib.Rules) > 0 {
			if err := svc.SetRules(ctx, id, lib.Rules, "show"); err != nil {
				t.Fatal(err)
			}
		}
		if lib.QualityProfile != "" {
			if err := svc.SetQualityProfile(ctx, id, lib.QualityProfile); err != nil {
				t.Fatal(err)
			}
		}
	}
	policies, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range fixture.Viewers {
		if v.Cap != 0 || v.Blocked {
			if err := policies.Put(ctx, v.ID, safety.Policy{MaxBitrateKbps: v.Cap, RemoteDisabled: v.Blocked}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, rx2Options(dir))
	if err != nil {
		t.Fatal(err)
	}
	return a, dir
}

func rx2Options(dir string) Options {
	return Options{DataDir: dir, Version: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func rx2Matrix(t *testing.T, a *App, fixture rx2Fixture) map[string]rx2Choice {
	t.Helper()
	ctx := context.Background()
	devices := rx2Devices()
	viewers := fixture.Viewers
	if len(viewers) == 0 {
		viewers = []rx2Viewer{{ID: 1}, {ID: 2}}
	}
	titles := map[string]*core.Title{}
	// Playback derives library membership from the title. The genre samples
	// exercise ordinary catalogue membership, a matching filter library, and
	// an item hidden from Movies with no profiled filter library to catch it.
	for j, genre := range []string{"Drama", "Animation", "Horror"} {
		n := int64(10000 + j)
		movie := &core.Title{Key: core.MovieKey(n), Type: core.TypeMovie, Title: "Movie", Genres: []string{genre}, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
		show := &core.Title{Key: core.ShowKey(n), Type: core.TypeShow, Title: "Show", Genres: []string{genre}, Released: movie.Released}
		for _, title := range []*core.Title{movie, show} {
			if err := a.catalog.UpsertTitle(ctx, *title, true); err != nil {
				t.Fatal(err)
			}
		}
		titles[genre+"/movie"] = movie
		titles[genre+"/episode"] = &core.Title{Key: core.EpisodeKey(n, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key}
	}
	choices := map[string]rx2Choice{}
	for path, title := range titles {
		for i, v := range viewers {
			for _, device := range devices {
				for _, remote := range []bool{false, true} {
					key := fmt.Sprintf("viewer-%d/%s/%s/away=%t", i+1, device.ID, path, remote)
					choices[key] = rx2Resolve(t, a, v, device, title, remote)
				}
			}
		}
	}
	return choices
}

type rx2Device struct {
	ID, Product, Platform, Device string
	Protocol                      core.Protocol
}

func rx2Devices() []rx2Device {
	return []rx2Device{
		{ID: "unknown"},
		{ID: "apple-tv", Product: "Plex for Apple TV", Platform: "tvOS", Device: "Apple TV"},
		{ID: "roku", Product: "Plex for Roku", Platform: "Roku", Device: "Roku"},
		{ID: "android-tv", Product: "Plex for Android (TV)", Platform: "Android", Device: "Android TV"},
		{ID: "shield", Product: "Plex for Android (TV)", Platform: "Android", Device: "SHIELD Android TV"},
		{ID: "fire-tv", Product: "Plex for Android (TV)", Platform: "Android", Device: "AFTKRT"},
		{ID: "ios", Product: "Plex for iOS", Platform: "iOS", Device: "iPhone"},
		{ID: "android-phone", Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 8"},
		{ID: "lg", Product: "Plex for LG", Platform: "webOS"},
		{ID: "samsung", Product: "Plex for Samsung", Platform: "Tizen"},
		{ID: "xbox", Product: "Plex for Xbox", Platform: "Xbox"},
		{ID: "playstation", Product: "Plex for PlayStation", Platform: "PlayStation"},
		{ID: "web-chrome", Product: "Plex Web", Platform: "Chrome"},
		{ID: "web-safari", Product: "Plex Web", Platform: "Safari"},
		{ID: "htpc", Product: "Plex HTPC", Platform: "Windows"},
		{ID: "kodi", Product: "PM4K", Device: "Kodi"},
		{ID: "desktop", Product: "Plex for Windows", Platform: "Windows"},
		{ID: "jellyfin", Product: "Jellyfin", Protocol: "jellyfin"},
		{ID: "infuse", Product: "Infuse-Direct", Platform: "tvOS", Device: "Apple TV", Protocol: "jellyfin"},
		{ID: "emby", Product: "Emby", Protocol: "emby"},
	}
}

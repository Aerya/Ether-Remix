package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPluginPosterBadgesResolutionPrePushDisableAndUninstall(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Nil TMDB client proves ResolveExternalTitles never makes external TMDB calls.
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, log)
	if err != nil {
		t.Fatal(err)
	}

	knownByTMDB := core.Title{
		Key:        core.MovieKey(603),
		Type:       core.TypeMovie,
		Title:      "The Matrix",
		Year:       1999,
		PosterPath: "/matrix.jpg",
		Released:   time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC),
		IDs:        core.IDs{TMDB: 603, IMDB: "tt0133093"},
	}
	knownByIMDB := core.Title{
		Key:        core.ShowKey(1399),
		Type:       core.TypeShow,
		Title:      "Game of Thrones",
		Year:       2011,
		PosterPath: "/got.jpg",
		Released:   time.Date(2011, 4, 17, 0, 0, 0, 0, time.UTC),
		IDs:        core.IDs{TMDB: 1399, IMDB: "tt0944947"},
	}
	for _, item := range []core.Title{knownByTMDB, knownByIMDB} {
		if err := cat.UpsertDetailedTitle(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	a := &App{db: db, catalog: cat}
	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Registry: extend.New(),
		Logger:   log,
		IsLeader: func() bool { return true },
		Backends: plugins.Backends{
			ResolveBadgeTitles: func(ctx context.Context, refs []plugins.BadgeTitleRef) ([]string, error) {
				crefs := make([]catalog.ExternalTitleRef, len(refs))
				for i, r := range refs {
					crefs[i] = catalog.ExternalTitleRef{
						Type: core.MediaType(r.Type),
						TMDB: r.TMDB,
						IMDB: r.IMDB,
					}
				}
				keys, err := cat.ResolveExternalTitles(ctx, crefs)
				if err != nil {
					return nil, err
				}
				out := make([]string, len(keys))
				for i, k := range keys {
					if k != 0 {
						out[i] = k.String()
					}
				}
				return out, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.pluginsRT = &pluginRuntime{mgr: mgr}

	ov := overlays.New(db, overlays.Options{
		PluginBadges: a.overlayPluginBadgeSets,
	})
	if err := ov.SaveSettings(ctx, overlays.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	manifestRaw, _ := json.Marshal(map[string]any{
		"id":      "community.example.dubbadges",
		"version": "1.0.0",
		"name":    "Dub Badges",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"summary":      "Marks dubbed audio and special cuts on library posters",
			"runtime":      map[string]any{"kind": "hosted", "entry": "plugin.js"},
			"capabilities": []string{"poster_badges"},
			"contributes": map[string]any{
				"posterBadges": []map[string]any{
					{"key": "dubs", "label": "Dubbed Audio", "description": "Available dubbed audio"},
				},
			},
		},
	})
	prev, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manifestRaw,
		Files:    map[string][]byte{"plugin.js": []byte("// noop")},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	host := mgr.HostFor(rec.ID)

	// Push badges for:
	// 1) knownByTMDB (TMDB takes priority even if IMDB doesn't match another title)
	// 2) knownByIMDB (resolved by IMDb alone)
	// 3) futureMovie (not yet in catalog_titles -> stored as "tmdb:movie:9090")
	// 4) futureShow (not yet in catalog_titles -> stored as "imdb:show:tt7654321")
	if err := host.BadgesSet(ctx, "dubs", []plugins.BadgeEntry{
		{Title: plugins.BadgeTitleRef{Type: "movie", TMDB: 603, IMDB: "tt0000001"}, Labels: []string{"en dub"}},
		{Title: plugins.BadgeTitleRef{Type: "show", IMDB: "TT0944947"}, Labels: []string{"dual audio"}},
		{Title: plugins.BadgeTitleRef{Type: "movie", TMDB: 9090}, Labels: []string{"ja dub"}},
		{Title: plugins.BadgeTitleRef{Type: "show", IMDB: "tt7654321"}, Labels: []string{"es dub"}},
	}); err != nil {
		t.Fatalf("BadgesSet: %v", err)
	}

	// Now add futureMovie and futureShow to catalog_titles AFTER the plugin pushed badges.
	futureMovie := core.Title{
		Key:        core.MovieKey(9090),
		Type:       core.TypeMovie,
		Title:      "Future Anime Film",
		Year:       2025,
		PosterPath: "/future-movie.jpg",
		Released:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		IDs:        core.IDs{TMDB: 9090, IMDB: "tt9090909"},
	}
	futureShow := core.Title{
		Key:        core.ShowKey(7654),
		Type:       core.TypeShow,
		Title:      "Future Series",
		Year:       2025,
		PosterPath: "/future-show.jpg",
		Released:   time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		IDs:        core.IDs{TMDB: 7654, IMDB: "tt7654321"},
	}
	for _, item := range []core.Title{futureMovie, futureShow} {
		if err := cat.UpsertDetailedTitle(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	page := []core.Title{knownByTMDB, knownByIMDB, futureMovie, futureShow}
	pageCtx, err := ov.WithTitlePage(ctx, nil, page)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"EN DUB"},
		{"DUAL AUDIO"},
		{"JA DUB"},
		{"ES DUB"},
	}
	for i := range page {
		if got := ov.Labels(pageCtx, nil, &page[i]); !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("page[%d] (%s) labels = %v, want %v", i, page[i].Title, got, want[i])
		}
	}

	// Re-pushing futureMovie now that it is in catalog_titles migrates "tmdb:movie:9090" -> canonical key cleanly.
	if err := host.BadgesSet(ctx, "dubs", []plugins.BadgeEntry{
		{Title: plugins.BadgeTitleRef{Type: "movie", TMDB: 9090}, Labels: []string{"ja+en dub"}},
	}); err != nil {
		t.Fatal(err)
	}
	usage, err := host.BadgesUsage(ctx)
	if err != nil || usage.Titles != 4 {
		t.Fatalf("usage after alias migration = %+v, err=%v (want 4 distinct titles)", usage, err)
	}

	// Disabling the plugin hides its badges immediately without deleting rows; re-enabling restores them.
	vEnabled := ov.Version(ctx, nil, &knownByTMDB, "en")
	if err := mgr.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := ov.Labels(ctx, nil, &knownByTMDB); len(got) != 0 {
		t.Fatalf("disabled plugin still rendered labels: %v", got)
	}
	if ov.Version(ctx, nil, &knownByTMDB, "en") == vEnabled {
		t.Fatal("disabling plugin did not change Version")
	}
	if err := mgr.SetEnabled(ctx, rec.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := ov.Labels(ctx, nil, &knownByTMDB); !reflect.DeepEqual(got, []string{"EN DUB"}) {
		t.Fatalf("re-enabled plugin labels = %v, want [EN DUB]", got)
	}

	// Uninstalling the plugin removes all rows from plugin_poster_badges.
	if err := mgr.Remove(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_poster_badges WHERE plugin_id=?`, rec.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining rows after Remove = %d (err=%v), want 0", remaining, err)
	}
}

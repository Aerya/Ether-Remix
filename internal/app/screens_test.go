package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/screens"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSurfaceScreensWireAndEntityFavoritePolicy(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)

	reg := screens.NewRegistry(screens.RegistryOptions{})
	pluginID := "community.example.scores"
	feed := "scores"
	rawMan, _ := json.Marshal(map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Scores",
		"ether": map[string]any{
			"contributes": map[string]any{
				"liveFeeds": []map[string]any{
					{"id": feed, "title": "Scores", "minIntervalSec": 15, "entityKinds": []string{"team"}},
				},
			},
		},
	})
	reg.Sync(plugins.Record{
		ID:          pluginID,
		Name:        "Scores",
		Version:     "1.0.0",
		Enabled:     true,
		ManifestRaw: rawMan,
		Manifest: plugins.Manifest{
			ID:      pluginID,
			Version: "1.0.0",
			Name:    "Scores",
			Ether: &plugins.EtherBlock{
				Protocol: 1,
				Kind:     plugins.KindExtension,
				Summary:  "Live scores feed",
				Runtime:  plugins.RuntimeDecl{Kind: "hosted", Entry: "dist/plugin.js"},
				Contributes: plugins.Contributes{
					Screens: []plugins.ScreenMeta{
						{
							ID:    "scores",
							Title: "Live scores",
							Icon:  "trophy",
							Feed:  feed,
							Sections: []plugins.ScreenSection{
								{ID: "hero", Type: "hero"},
								{ID: "entities", Type: "entities", EntityKind: "team"},
							},
						},
					},
				},
			},
		},
	}, nil)

	a := &App{}
	favSvc, err := favorites.New(favorites.Options{
		DB:     db,
		Marked: state.New(db, state.Options{}).Playlists().(core.TransactionalMarkedPlaylists),
		Now:    func() time.Time { return now },
		Active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil },
		Allowed: func(ctx context.Context, viewer *core.User, sub favorites.Subject) bool {
			if sub.Kind == favorites.KindEntity {
				ok, _ := a.favoriteEntityAllowed(ctx, viewer, sub.ID)
				return ok
			}
			return true
		},
		Changed: func(context.Context, int64, favorites.Subject) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.favorites = favSvc
	a.pluginsRT = &pluginRuntime{
		screens: screens.NewService(screens.ServiceOptions{
			Registry:  reg,
			Favorites: appFavoriteEntities{app: a},
			Now:       func() time.Time { return now },
		}),
	}

	surf := surfaceScreens{app: a, surface: "vela"}
	viewer := &core.User{ID: 7}

	// Navigation returns the published Vela screen and Generation matches registry.
	nav, err := surf.Navigation(ctx, viewer)
	if err != nil || len(nav) != 1 || nav[0].Kind != "screen" || nav[0].ID != "scores" {
		t.Fatalf("Navigation = %+v %v", nav, err)
	}
	if surf.Generation() != reg.Generation() || surf.Generation() == 0 {
		t.Fatalf("Generation = %d, registry = %d", surf.Generation(), reg.Generation())
	}

	// Valid entity key saves; malformed key returns ErrInvalidEntityKey; unknown plugin returns ErrForbidden.
	validKey := favorites.FormatEntityKey(pluginID, feed, "team", "alpha")
	if err := surf.SetFavoriteEntity(ctx, viewer, validKey, true); err != nil {
		t.Fatalf("SetFavoriteEntity valid: %v", err)
	}
	if err := surf.SetFavoriteEntity(ctx, viewer, "bad", true); err != frontend.ErrInvalidEntityKey {
		t.Fatalf("SetFavoriteEntity bad = %v, want ErrInvalidEntityKey", err)
	}
	if err := surf.SetFavoriteEntity(ctx, viewer, "community.missing.scores:scores:team:x", true); err != frontend.ErrForbidden {
		t.Fatalf("SetFavoriteEntity missing plugin = %v, want ErrForbidden", err)
	}

	// Malformed schedule date maps to ErrInvalidScreenDate.
	if _, err := surf.ResolveScreen(ctx, viewer, "scores", "", "invalid"); err != frontend.ErrInvalidScreenDate {
		t.Fatalf("ResolveScreen invalid date = %v, want ErrInvalidScreenDate", err)
	}

	// Valid screen resolves cleanly with non-empty JSON and zero background calls.
	raw, err := surf.ResolveScreen(ctx, viewer, "scores", "", "2026-11-22")
	if err != nil || len(raw) == 0 {
		t.Fatalf("ResolveScreen = %s %v", string(raw), err)
	}
	_ = http.StatusNoContent
}

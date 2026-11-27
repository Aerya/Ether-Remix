package app

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pseSessionThemes struct{ keys []core.RatingKey }

func (p pseSessionThemes) Themes() []frontend.PSETheme {
	return []frontend.PSETheme{{Section: 5101, Title: "Enfants", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "r"}}}}
}
func (p pseSessionThemes) RowKeys(context.Context, string, *core.User) []core.RatingKey {
	return p.keys
}

// Un profil limité à une bibliothèque Remix lit les titres de ses rangées,
// et seulement eux (Stremio, Jellyfin… passent par cette admission).
func TestPSESessionTitleThemeGrant(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := core.WithLiveApp(context.Background(), "stremio")
	day := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	in := core.Title{Key: core.MovieKey(9201), Type: core.TypeMovie, Title: "Dans le thème", Released: day, HomeReleased: day}
	out := core.Title{Key: core.MovieKey(9202), Type: core.TypeMovie, Title: "Hors thème", Released: day, HomeReleased: day}
	for _, title := range []core.Title{in, out} {
		if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	frontend.SetPSEThemes(pseSessionThemes{keys: []core.RatingKey{in.Key}})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	frontend.BumpPSEThemesGeneration()
	vis := experience.Visibility{GetTitle: a.catalog.CachedGet, Policy: a.safety}
	kid := &core.Viewer{ID: 77, AllowedLibraries: []core.LibraryID{5100, 5101}}
	if _, err := a.librarySessionTitle(ctx, kid, in.Key, vis, false, false); err != nil {
		t.Fatalf("titre du thème refusé : %v", err)
	}
	if _, err := a.librarySessionTitle(ctx, kid, out.Key, vis, false, false); err == nil {
		t.Fatal("titre hors thème accepté")
	}
}

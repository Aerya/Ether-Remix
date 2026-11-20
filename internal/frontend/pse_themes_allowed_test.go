package frontend

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestThemeAllowed(t *testing.T) {
	ids := func(v ...int) []core.LibraryID {
		var out []core.LibraryID
		for _, x := range v {
			out = append(out, core.LibraryID(x))
		}
		return out
	}
	cases := []struct {
		name   string
		viewer *core.User
		sec    int
		want   bool
	}{
		{"nil", nil, 5101, true},
		{"propriétaire", &core.User{IsOwner: true, AllowedLibraries: ids(1)}, 5101, true},
		{"liste vide", &core.User{}, 5101, true},
		{"thème présent", &core.User{AllowedLibraries: ids(1, 5101)}, 5101, true},
		{"autre thème coché", &core.User{AllowedLibraries: ids(1, 5102)}, 5101, false},
		{"ancien réglage", &core.User{AllowedLibraries: ids(1, 2)}, 5103, true},
		{"moitié Séries suit le principal", &core.User{AllowedLibraries: ids(2, 5101)}, 5201, true},
		{"moitié Séries refusée", &core.User{AllowedLibraries: ids(2, 5102)}, 5201, false},
		{"marqueur 5100 seul : aucun thème", &core.User{AllowedLibraries: ids(1, 5100)}, 5101, false},
		{"marqueur 5100 seul, moitié Séries", &core.User{AllowedLibraries: ids(2, 5100)}, 5201, false},
		{"5100 + 5103 : 5103 seul", &core.User{AllowedLibraries: ids(1, 5100, 5103)}, 5103, true},
		{"5100 + 5103 : pas 5101", &core.User{AllowedLibraries: ids(1, 5100, 5103)}, 5101, false},
		{"5201 stocké traité comme 5101", &core.User{AllowedLibraries: ids(2, 5201)}, 5101, true},
	}
	for _, c := range cases {
		if got := ThemeAllowed(c.viewer, c.sec); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestNormalizeAllowedLibraries(t *testing.T) {
	got := NormalizeAllowedLibraries([]core.LibraryID{1, 5100, 5201, 5101, 5102, 1, 5100})
	if want := []core.LibraryID{1, 5100, 5101, 5102}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if NormalizeAllowedLibraries(nil) != nil {
		t.Fatal("nil attendu")
	}
}

type grantThemes struct {
	rows  map[string][]core.RatingKey
	calls int
}

func (g *grantThemes) Themes() []PSETheme {
	return []PSETheme{
		{Section: 5101, Title: "A", Type: core.TypeMovie, Rows: []PSEThemeRow{{ID: "a"}}},
		{Section: 5102, Title: "B", Type: core.TypeMovie, Rows: []PSEThemeRow{{ID: "b"}}},
	}
}
func (g *grantThemes) RowKeys(_ context.Context, row string, _ *core.User) []core.RatingKey {
	g.calls++
	return g.rows[row]
}

func TestPSEThemeGrant(t *testing.T) {
	g := &grantThemes{rows: map[string][]core.RatingKey{"a": {core.MovieKey(1), core.ShowKey(2)}, "b": {core.MovieKey(3)}}}
	SetPSEThemes(g)
	t.Cleanup(func() { SetPSEThemes(nil) })
	kid := &core.User{ID: 9, AllowedLibraries: []core.LibraryID{5100, 5101}}
	movie := &core.Title{Key: core.MovieKey(1)}
	episode := &core.Title{Key: core.EpisodeKey(2, 1, 1), ShowKey: core.ShowKey(2)}
	if !PSEThemeGrant(kid, movie) || !PSEThemeGrant(kid, episode) {
		t.Fatal("titre du thème autorisé refusé")
	}
	if PSEThemeGrant(kid, &core.Title{Key: core.MovieKey(3)}) || PSEThemeGrant(kid, &core.Title{Key: core.MovieKey(99)}) {
		t.Fatal("titre hors thème accordé")
	}
	// thème décoché : 5100 seul
	if PSEThemeGrant(&core.User{ID: 10, AllowedLibraries: []core.LibraryID{5100}}, movie) {
		t.Fatal("thème décoché accordé")
	}
	// ancien réglage, propriétaire, liste vide : pas de passe-droit
	for _, u := range []*core.User{{ID: 11, AllowedLibraries: []core.LibraryID{1}}, {ID: 12, IsOwner: true, AllowedLibraries: []core.LibraryID{5100, 5101}}, {ID: 13}} {
		if PSEThemeGrant(u, movie) {
			t.Fatalf("passe-droit inattendu pour %+v", u)
		}
	}
	// Visibility : la base refusée est contournée par le thème, pas pour le reste.
	v := Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, ThemeGrant: PSEThemeGrant}
	movie.Type = core.TypeMovie
	if !v.TitleAllowed(kid, movie) {
		t.Fatal("Visibility.TitleAllowed refuse le titre du thème")
	}
	other := &core.Title{Key: core.MovieKey(99), Type: core.TypeMovie}
	if v.TitleAllowed(kid, other) {
		t.Fatal("Visibility.TitleAllowed accepte un titre hors thème")
	}
	// cache : pas de nouvel appel tant que la génération ne change pas
	before := g.calls
	PSEThemeGrant(kid, movie)
	if g.calls != before {
		t.Fatal("résultat non mis en cache")
	}
	g.rows["a"] = []core.RatingKey{core.MovieKey(77)}
	BumpPSEThemesGeneration()
	if PSEThemeGrant(kid, movie) || !PSEThemeGrant(kid, &core.Title{Key: core.MovieKey(77)}) {
		t.Fatal("cache non invalidé par la génération")
	}
}

// Collections pas encore construites (RowKeys nil) : refus, sans cache.
func TestPSEThemeGrantNotBuilt(t *testing.T) {
	g := &grantThemes{rows: map[string][]core.RatingKey{}}
	SetPSEThemes(g)
	t.Cleanup(func() { SetPSEThemes(nil) })
	kid := &core.User{ID: 21, AllowedLibraries: []core.LibraryID{5100, 5101}}
	movie := &core.Title{Key: core.MovieKey(1)}
	if PSEThemeGrant(kid, movie) {
		t.Fatal("accordé alors que rien n'est construit")
	}
	g.rows["a"] = []core.RatingKey{core.MovieKey(1)}
	if !PSEThemeGrant(kid, movie) {
		t.Fatal("un résultat vide a été mis en cache")
	}
}

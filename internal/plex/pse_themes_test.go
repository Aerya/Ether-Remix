package plex

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pseFakeThemes struct{ rows map[string][]core.RatingKey }

func (f *pseFakeThemes) Themes() []frontend.PSETheme {
	return []frontend.PSETheme{{Section: 5101, Title: "Services", Type: core.TypeMovie,
		Rows: []frontend.PSEThemeRow{{ID: "11", Title: "Netflix"}, {ID: "12", Title: "Canal+"}}},
		// Moitié Séries d'une bibliothèque « Films & séries ».
		{Section: 5201, Title: "Services · Séries", Type: core.TypeShow,
			Rows: []frontend.PSEThemeRow{{ID: "13", Title: "Netflix"}}}}
}

func (f *pseFakeThemes) RowKeys(_ context.Context, row string, _ *core.User) []core.RatingKey {
	return f.rows[row]
}

// Une bibliothèque à thème apparaît dans Plex, se parcourt restreinte à
// ses collections et montre une rangée par collection.
func TestPSEThemeLibrary(t *testing.T) {
	frontend.SetPSEThemes(&pseFakeThemes{rows: map[string][]core.RatingKey{"11": {fightClub.Key}, "12": {alien.Key, fightClub.Key}}})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	f := newFixture()
	defer f.srv.Close()

	mc := decodeJSON(t, f.get(t, "/library/sections", jsonAccept...))
	found := false
	for _, d := range mc["Directory"].([]any) {
		if dd := d.(map[string]any); dd["key"] == "5101" && dd["type"] == "movie" && dd["title"] == "Services" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bibliothèque à thème absente : %+v", mc["Directory"])
	}
	shows := false
	for _, d := range mc["Directory"].([]any) {
		if dd := d.(map[string]any); dd["key"] == "5201" && dd["type"] == "show" {
			shows = true
		}
	}
	if !shows {
		t.Fatalf("partie Séries absente : %+v", mc["Directory"])
	}
	prov := f.get(t, "/media/providers", jsonAccept...).Body.String()
	if !strings.Contains(prov, `"/hubs/sections/5101"`) {
		t.Fatalf("bibliothèque à thème absente de /media/providers : %s", prov)
	}

	f.get(t, "/library/sections/5101/all?type=1", jsonAccept...)
	if !f.catalog.lastQ.Filter.PSEThemed {
		t.Fatal("bibliothèque à thème hors du filtre VF")
	}
	if got := f.catalog.lastQ.Filter.IncludeKeys; !slices.Equal(got, []core.RatingKey{fightClub.Key, alien.Key}) {
		t.Fatalf("titres de la bibliothèque = %v", got)
	}
	f.get(t, "/library/sections/5101/all?type=1&pseRow=11", jsonAccept...)
	if got := f.catalog.lastQ.Filter.IncludeKeys; !slices.Equal(got, []core.RatingKey{fightClub.Key}) {
		t.Fatalf("titres de la rangée = %v", got)
	}
	f.get(t, "/library/sections/1/all?type=1", jsonAccept...)
	if f.catalog.lastQ.Filter.IncludeKeys != nil {
		t.Fatal("la bibliothèque Films est restreinte")
	}

	hubs := decodeJSON(t, f.get(t, "/hubs/sections/5101", jsonAccept...))
	var titles []string
	for _, h := range hubs["Hub"].([]any) {
		hh := h.(map[string]any)
		titles = append(titles, hh["title"].(string))
		if !strings.Contains(hh["key"].(string), "pseRow=") {
			t.Fatalf("rangée sans lien « tout voir » : %+v", hh)
		}
	}
	if !slices.Equal(titles, []string{"Netflix", "Canal+"}) {
		t.Fatalf("rangées = %v", titles)
	}

	frontend.SetPSEThemes(nil)
	if code := f.get(t, "/library/sections/5101/all", jsonAccept...).Code; code != 404 {
		t.Fatalf("bibliothèque retirée : code %d", code)
	}
}

// L'onglet Collections d'une bibliothèque à thème ne garde que ses rangées,
// dans leur ordre.
func TestPSEThemeCollections(t *testing.T) {
	frontend.SetPSEThemes(&pseKeyedThemes{})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	s := &Server{}
	all := []core.Collection{{Key: core.SmartKey(1), Title: "Autre"}, {Key: core.SmartKey(3), Title: "Canal+"}, {Key: core.SmartKey(2), Title: "Netflix"}}
	got := s.pseThemeCollections(Section{ID: 5101}, all)
	if len(got) != 2 || got[0].Title != "Netflix" || got[1].Title != "Canal+" {
		t.Fatalf("collections = %+v", got)
	}
	if got := s.pseThemeCollections(Section{ID: 1}, all); len(got) != 3 {
		t.Fatalf("bibliothèque ordinaire réduite : %+v", got)
	}
}

type pseKeyedThemes struct{}

func (pseKeyedThemes) Themes() []frontend.PSETheme {
	return []frontend.PSETheme{{Section: 5101, Title: "Services", Type: core.TypeMovie,
		Rows: []frontend.PSEThemeRow{{ID: "11", Title: "Netflix", Key: core.SmartKey(2)}, {ID: "12", Title: "Canal+", Key: core.SmartKey(3)}}}}
}

func (pseKeyedThemes) RowKeys(context.Context, string, *core.User) []core.RatingKey { return nil }

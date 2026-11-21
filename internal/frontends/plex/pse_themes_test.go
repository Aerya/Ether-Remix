package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
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

type pseTwoThemes struct{}

func (pseTwoThemes) Themes() []frontend.PSETheme {
	return []frontend.PSETheme{
		{Section: 5101, Title: "Services", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "11", Title: "Netflix"}}},
		{Section: 5102, Title: "Concerts", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "21", Title: "Live"}}},
		{Section: 5201, Title: "Services · Séries", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "13", Title: "Netflix"}}},
	}
}
func (pseTwoThemes) RowKeys(context.Context, string, *core.User) []core.RatingKey { return nil }

// Une bibliothèque à thème suit AllowedLibraries : numéro 5100+ID explicite,
// la moitié Séries suit le principal, et un ancien réglage (aucun numéro de
// thème) garde la visibilité de la section de base.
func TestPSEThemeAllowedLibraries(t *testing.T) {
	frontend.SetPSEThemes(pseTwoThemes{})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	f := newFixture()
	defer f.srv.Close()
	users := f.srv.testAuth().users
	users["explicit"] = core.User{ID: 41, Username: "explicit", AllowedLibraries: []core.LibraryID{1, 2, 5101}}
	users["other"] = core.User{ID: 42, Username: "other", AllowedLibraries: []core.LibraryID{1, 2, 5102}}
	users["legacy"] = core.User{ID: 43, Username: "legacy", AllowedLibraries: []core.LibraryID{1, 2}}
	users["none"] = core.User{ID: 44, Username: "none", AllowedLibraries: []core.LibraryID{1, 2, 5100}}
	// Choix Remix explicite sans section de base : les thèmes listés restent ouverts.
	users["nobase"] = core.User{ID: 45, Username: "nobase", AllowedLibraries: []core.LibraryID{2, 5100, 5101, 5102}}
	// ID de thème orphelin : ignoré, les autres thèmes restent en mode explicite.
	users["orphan"] = core.User{ID: 46, Username: "orphan", AllowedLibraries: []core.LibraryID{1, 2, 5100, 5150, 5102}}
	cases := []struct {
		token string
		want  map[string]bool
	}{
		{"nobase", map[string]bool{"5101": true, "5102": true, "5201": true}},
		{"orphan", map[string]bool{"5101": false, "5102": true, "5201": false}},
		{"none", map[string]bool{"5101": false, "5102": false, "5201": false}},
		{"explicit", map[string]bool{"5101": true, "5102": false, "5201": true}},
		{"other", map[string]bool{"5101": false, "5102": true, "5201": false}},
		{"legacy", map[string]bool{"5101": true, "5102": true, "5201": true}},
	}
	for _, c := range cases {
		hdr := map[string]string{"X-Plex-Token": c.token, "Accept": "application/json"}
		body := f.request(t, http.MethodGet, "/library/sections", hdr).Body.String()
		for key, want := range c.want {
			if got := strings.Contains(body, `"key":"`+key+`"`); got != want {
				t.Errorf("%s : section %s listée = %v, attendu %v (%s)", c.token, key, got, want, body)
			}
			code := f.request(t, http.MethodGet, "/library/sections/"+key+"/all", hdr).Code
			if (code != http.StatusNotFound) != want {
				t.Errorf("%s : ouverture de %s = %d, visible attendu %v", c.token, key, code, want)
			}
		}
	}
}

type pseKidThemes struct{}

func (pseKidThemes) Themes() []frontend.PSETheme {
	return []frontend.PSETheme{
		{Section: 5101, Title: "Cartoons", Type: core.TypeMovie, Rows: []frontend.PSEThemeRow{{ID: "11", Title: "Films"}}},
		{Section: 5201, Title: "Cartoons · Séries", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "13", Title: "Séries"}}},
	}
}
func (pseKidThemes) RowKeys(_ context.Context, row string, _ *core.User) []core.RatingKey {
	switch row {
	case "11":
		return []core.RatingKey{fightClub.Key}
	case "13":
		return []core.RatingKey{breakingBad.Key}
	}
	return nil
}

// Un profil enfant [5100, 5101] sans les sections de base ne voit que le thème
// et les titres de ce thème (fiches, saisons, épisodes, recherche).
func TestPSEThemeWithoutBaseSections(t *testing.T) {
	frontend.SetPSEThemes(pseKidThemes{})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	f := newFixture()
	defer f.srv.Close()
	f.srv.testAuth().users["kid"] = core.User{ID: 50, Username: "kid", AllowedLibraries: []core.LibraryID{5100, 5101}}
	hdr := map[string]string{"X-Plex-Token": "kid", "Accept": "application/json"}
	get := func(target string) *httptest.ResponseRecorder { return f.request(t, http.MethodGet, target, hdr) }

	body := get("/library/sections").Body.String()
	for _, want := range []string{`"key":"5101"`, `"key":"5201"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("thème absent (%s) : %s", want, body)
		}
	}
	for _, base := range []string{`"key":"1"`, `"key":"2"`} {
		if strings.Contains(body, base) {
			t.Fatalf("section de base visible (%s) : %s", base, body)
		}
	}
	if code := get("/library/sections/1/all").Code; code != http.StatusNotFound {
		t.Fatalf("ouverture directe de la base Films : %d", code)
	}
	if code := get("/library/sections/2/all").Code; code != http.StatusNotFound {
		t.Fatalf("ouverture directe de la base Séries : %d", code)
	}
	rec := get("/library/sections/5101/all?type=1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Fjord Harbor") {
		t.Fatalf("navigation dans le thème : %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/hubs/sections/5101"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Fjord Harbor") {
		t.Fatalf("rangées du thème : %d %s", rec.Code, rec.Body.String())
	}
	for _, k := range []core.RatingKey{fightClub.Key, breakingBad.Key, bbS1.Key, bbE1.Key} {
		if code := get("/library/metadata/" + k.String()).Code; code != http.StatusOK {
			t.Errorf("titre du thème %s : %d", k, code)
		}
	}
	if code := get("/library/metadata/" + alien.Key.String()).Code; code != http.StatusNotFound {
		t.Errorf("titre hors thème : %d", code)
	}
	search := get("/hubs/search?query=Fjord").Body.String()
	if !strings.Contains(search, "Fjord Harbor") {
		t.Errorf("recherche du titre du thème : %s", search)
	}
	if s := get("/hubs/search?query=Alien").Body.String(); strings.Contains(s, `"title":"Alien"`) {
		t.Errorf("recherche hors thème : %s", s)
	}
}

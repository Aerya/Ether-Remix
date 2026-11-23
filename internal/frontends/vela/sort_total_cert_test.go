package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAcceptLanguageRegionsParsing(t *testing.T) {
	cases := []struct {
		header string
		want   []string
	}{
		{"", nil},
		{"en", nil},
		{"en-GB", []string{"GB"}},
		{"en-US,en;q=0.9,de-DE;q=0.8", []string{"US", "DE"}},
		{"fr-FR;q=0.5,en-GB;q=0.9,de-DE;q=0.9", []string{"GB", "DE", "FR"}},
		{"en-GB;q=0,fr-CA;q=0.8", []string{"CA"}},
		{"zh-Hant-TW,es-419;q=0.8,en_AU;q=0.7", []string{"TW", "AU"}},
		{"en-x-gb,de-u-co-phonebk", nil},
	}
	for _, tc := range cases {
		if got := acceptLanguageRegions(tc.header); !slices.Equal(got, tc.want) {
			t.Errorf("acceptLanguageRegions(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestVelaLibrarySortTotalAndCertificateCountry(t *testing.T) {
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{Region: "DE"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	rel := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	seed := []core.Title{
		{
			Key: core.MovieKey(1), Type: core.TypeMovie, Title: `"Pimpernel" Smith`, Year: 1941,
			Genres: []string{"Drama"}, Released: rel,
			ContentRating: "PG-13", ContentRatings: map[string]string{"US": "PG-13", "GB": "12A", "DE": "12"},
		},
		{
			Key: core.MovieKey(2), Type: core.TypeMovie, Title: `"Sr."`, Year: 2022,
			Genres: []string{"Documentary"}, Released: rel,
			ContentRating: "de/6",
		},
		{
			Key: core.MovieKey(3), Type: core.TypeMovie, Title: "#1 Cheerleader Camp", Year: 2010,
			Genres: []string{"Comedy"}, Released: rel,
			ContentRating: "R", ContentRatings: map[string]string{"US": "R", "GB": "18"},
		},
		{
			Key: core.MovieKey(4), Type: core.TypeMovie, Title: "#Alive", Year: 2020,
			Genres: []string{"Drama"}, Released: rel,
			ContentRating: "TV-MA",
		},
		{
			Key: core.MovieKey(5), Type: core.TypeMovie, Title: "10 Cloverfield Lane", Year: 2016,
			Genres: []string{"Drama"}, Released: rel,
			ContentRating: "PG-13", ContentRatings: map[string]string{"US": "PG-13", "DE": "12"},
		},
		{
			Key: core.MovieKey(6), Type: core.TypeMovie, Title: "2 Fast 2 Furious", Year: 2003,
			Genres: []string{"Action"}, Released: rel,
			ContentRating: "CustomBoard",
		},
	}
	for _, m := range seed {
		if err := cat.UpsertTitle(ctx, m, true); err != nil {
			t.Fatal(err)
		}
	}

	exp := detailColdService(t, experience.ServiceDeps{
		Browse:       cat.Browse,
		CountWatched: cat.CountWatched,
		Get: func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
			return cat.CachedGet(ctx, k)
		},
		CachedGet: cat.CachedGet,
		Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			return nil, nil
		},
		Libraries: []experience.Library{{ID: 1, Name: "Movies", Type: core.TypeMovie}},
		PageExclusions: func(context.Context, *core.User, core.MediaType) experience.LibraryPageExclusions {
			return experience.LibraryPageExclusions{AvailabilityInSQL: true}
		},
	})
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	serverRegion := "DE"
	inst := &instance{
		seal: seal,
		host: frontend.Host{
			Identity:      identityFake{},
			Experience:    exp,
			CatalogRegion: func() string { return serverRegion },
		},
	}

	getJSON := func(target, acceptLang string) (*httptest.ResponseRecorder, object) {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.SetPathValue("id", "1")
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "device")
		if acceptLang != "" {
			r.Header.Set("Accept-Language", acceptLang)
		}
		w := httptest.NewRecorder()
		inst.libraryItems(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", target, w.Code, w.Body.String())
		}
		var out object
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return w, out
	}

	// 1. Title sort order + cheap exact total across pages (page 1 & page 2).
	w1, p1 := getJSON("/vela/v0/libraries/1/items?sort=title&limit=4", "en-GB,en;q=0.9")
	if !strings.Contains(w1.Header().Get("Vary"), "Accept-Language") {
		t.Fatalf("expected Vary to include Accept-Language, got %q", w1.Header().Get("Vary"))
	}
	if p1["total"] != float64(6) {
		t.Fatalf("page 1 total = %v, want 6", p1["total"])
	}
	if p1["continuation"] != "more" {
		t.Fatalf("page 1 continuation = %v, want more", p1["continuation"])
	}
	cursor := p1["nextCursor"].(string)
	_, p2 := getJSON("/vela/v0/libraries/1/items?sort=title&limit=4&cursor="+url.QueryEscape(cursor), "en-GB,en;q=0.9")
	if p2["total"] != float64(6) {
		t.Fatalf("page 2 total = %v, want 6", p2["total"])
	}
	if p2["continuation"] != "exhausted" {
		t.Fatalf("page 2 continuation = %v, want exhausted", p2["continuation"])
	}

	var titles []string
	byName := map[string]map[string]any{}
	for _, page := range []object{p1, p2} {
		for _, raw := range page["items"].([]any) {
			title := raw.(map[string]any)["title"].(map[string]any)
			name := title["name"].(string)
			titles = append(titles, name)
			byName[name] = title
		}
	}
	wantOrder := []string{
		"#1 Cheerleader Camp",
		"2 Fast 2 Furious",
		"10 Cloverfield Lane",
		"#Alive",
		`"Pimpernel" Smith`,
		`"Sr."`,
	}
	if !slices.Equal(titles, wantOrder) {
		t.Fatalf("Vela title sort = %q, want %q", titles, wantOrder)
	}

	// 2. Certificate country preference: Accept-Language (GB) > server region (DE) > US.
	if got := byName[`"Pimpernel" Smith`]; got["rating"] != "12A" || got["ratingCountry"] != "GB" {
		t.Fatalf("GB viewer Pimpernel = rating:%v country:%v, want 12A / GB", got["rating"], got["ratingCountry"])
	}
	if got := byName["10 Cloverfield Lane"]; got["rating"] != "12" || got["ratingCountry"] != "DE" {
		t.Fatalf("GB viewer (no GB cert -> server DE) Cloverfield = rating:%v country:%v, want 12 / DE", got["rating"], got["ratingCountry"])
	}
	if got := byName[`"Sr."`]; got["rating"] != "6" || got["ratingCountry"] != "DE" {
		t.Fatalf("stored de/6 bare rating = rating:%v country:%v, want 6 / DE", got["rating"], got["ratingCountry"])
	}
	if got := byName["2 Fast 2 Furious"]; got["rating"] != "CustomBoard" || got["ratingCountry"] != nil {
		t.Fatalf("unknown bare rating = rating:%v country:%v, want CustomBoard / nil", got["rating"], got["ratingCountry"])
	}

	// Without Accept-Language, prefers server region (DE); when server region absent, falls back to US.
	_, pNoLang := getJSON("/vela/v0/libraries/1/items?sort=title&limit=10", "")
	noLangByName := map[string]map[string]any{}
	for _, raw := range pNoLang["items"].([]any) {
		title := raw.(map[string]any)["title"].(map[string]any)
		noLangByName[title["name"].(string)] = title
	}
	if got := noLangByName[`"Pimpernel" Smith`]; got["rating"] != "12" || got["ratingCountry"] != "DE" {
		t.Fatalf("server DE Pimpernel = rating:%v country:%v, want 12 / DE", got["rating"], got["ratingCountry"])
	}
	if got := noLangByName["#1 Cheerleader Camp"]; got["rating"] != "R" || got["ratingCountry"] != "US" {
		t.Fatalf("fallback US Cheerleader = rating:%v country:%v, want R / US", got["rating"], got["ratingCountry"])
	}

	// 3. Un-tallied multi-facet filter (genre + year) must omit total completely (never send estimate).
	_, untallied := getJSON("/vela/v0/libraries/1/items?sort=title&genre=Drama&year=2020", "en-US")
	if _, hasTotal := untallied["total"]; hasTotal {
		t.Fatalf("expected un-tallied genre+year query to omit total, got %v", untallied["total"])
	}
}

type cachedBootstrapExperience struct {
	frontendtest.Experience
}

func (cachedBootstrapExperience) HomeRevision(context.Context, *frontend.Viewer) (frontend.HomeRevision, bool, error) {
	return frontend.HomeRevision{Viewer: "v1"}, true, nil
}

func TestVelaBootstrapIsolatesAcceptLanguage(t *testing.T) {
	movie := frontend.Title{
		Key:            core.MovieKey(9),
		Type:           frontend.TypeMovie,
		Title:          "Localized Title",
		ContentRating:  "PG-13",
		ContentRatings: map[string]string{"US": "PG-13", "GB": "12A"},
	}
	exp := cachedBootstrapExperience{
		Experience: frontendtest.Experience{
			ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
				return []frontend.View{{ID: "1", Name: "Movies", Type: frontend.TypeMovie}}, nil
			},
			RowsFunc: func(context.Context, frontend.RowRequest) ([]frontend.Row, error) {
				return []frontend.Row{{ID: "r1", Title: "Featured", Kind: "editorial", Items: []frontend.Title{movie}}}, nil
			},
		},
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		seal: seal,
		host: frontend.Host{
			Identity:      identityFake{},
			Experience:    exp,
			CatalogRegion: func() string { return "US" },
		},
	}

	fetch := func(acceptLang string) (string, string) {
		r := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", "device")
		r.Header.Set("Accept-Language", acceptLang)
		w := httptest.NewRecorder()
		inst.bootstrap(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("bootstrap(%q) = %d: %s", acceptLang, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Vary"), "Accept-Language") {
			t.Fatalf("bootstrap Vary = %q, want Accept-Language", w.Header().Get("Vary"))
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		rows := body["rows"].([]any)
		row := rows[0].(map[string]any)
		entry := row["items"].([]any)[0].(map[string]any)
		title := entry["title"].(map[string]any)
		return title["rating"].(string), title["ratingCountry"].(string)
	}

	rGB, cGB := fetch("en-GB")
	if rGB != "12A" || cGB != "GB" {
		t.Fatalf("GB bootstrap = (%q, %q), want (12A, GB)", rGB, cGB)
	}
	rUS, cUS := fetch("en-US")
	if rUS != "PG-13" || cUS != "US" {
		t.Fatalf("US bootstrap = (%q, %q), want (PG-13, US)", rUS, cUS)
	}
	// Cached GB response remains isolated after US build.
	rGB2, cGB2 := fetch("en-GB")
	if rGB2 != "12A" || cGB2 != "GB" {
		t.Fatalf("cached GB bootstrap = (%q, %q), want (12A, GB)", rGB2, cGB2)
	}
}

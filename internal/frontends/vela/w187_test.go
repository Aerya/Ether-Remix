package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type levelPresExperience struct {
	*pluginPresExperience
	lastBucketLevel string
	lastColLevel    string
	lastPeopleLevel string
	lastRowLang     string
}

func (e *levelPresExperience) LibraryFacetBucketsAtLevel(_ context.Context, _ *core.Viewer, _ core.Device, viewID, level, field string) ([]frontend.FacetBucket, error) {
	e.lastBucketLevel = level
	if viewID != "77" {
		return nil, core.ErrNotFound
	}
	return append([]frontend.FacetBucket(nil), e.buckets[level+":"+field]...), nil
}

func (e *levelPresExperience) LibraryFacetCollections(ctx context.Context, v *core.Viewer, d core.Device, viewID, field, order string, page frontend.PageQuery) (frontend.FacetCollectionPage, error) {
	e.lastColLevel = page.Level
	return e.pluginPresExperience.LibraryFacetCollections(ctx, v, d, viewID, field, order, page)
}

func (e *levelPresExperience) LibraryFacetPeople(ctx context.Context, v *core.Viewer, d core.Device, viewID, field, order string, page frontend.PageQuery) (frontend.FacetPersonPage, error) {
	e.lastPeopleLevel = page.Level
	return e.pluginPresExperience.LibraryFacetPeople(ctx, v, d, viewID, field, order, page)
}

func (e *levelPresExperience) PluginLibraryRows(_ context.Context, _ *core.Viewer, dev core.Device, viewID, _ string) ([]frontend.Row, error) {
	e.lastRowLang = dev.Language
	return e.pluginRows[viewID], nil
}

func TestVelaMultiLevelTabsFacetsAndLanguagePropagation(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	base := &pluginPresExperience{
		buckets: map[string][]frontend.FacetBucket{
			"album:genre": {{ID: "Modal Jazz", Label: "Modal Jazz", Count: 4}},
		},
		colPages: map[string]frontend.FacetCollectionPage{
			"genre": {Items: []frontend.FacetCollection{{ID: "Modal Jazz", Name: "Modal Jazz", ItemCount: 4}}},
		},
		peoplePages: map[string]frontend.FacetPersonPage{
			"composer": {Items: []frontend.FacetPerson{{Person: frontend.PersonDetail{ID: "p-1", Name: "Miles Davis"}, ItemCount: 2}}},
		},
		pluginRows: map[string][]frontend.Row{
			"77": {{ID: "plugin:77:shelf:recent", Title: "Jump back in", Purpose: frontend.RowRecommendation, Kind: "shelf", Style: "shelf"}},
		},
	}
	base.ViewsFunc = func(context.Context, *core.Viewer) ([]frontend.View, error) {
		return []frontend.View{{
			ID:        "77",
			LibraryID: 77,
			Name:      "Music",
			Type:      core.TypeShow,
			PluginID:  "community.example.music",
			KindKey:   "music",
			Card:      "square",
			Sorts:     []string{"title", "released", "lastViewed"},
			Facets: []frontend.ViewFacet{
				{ID: "genre", Label: "Genre", Type: "strings", Multi: true},
				{ID: "composer", Label: "Composer", Type: "people", Multi: true},
			},
			Tabs: []frontend.ViewTab{
				{ID: "recommended", Label: "Recommended", Kind: "recommended"},
				{ID: "artists", Label: "Artists", Kind: "library", Level: "artist", Search: true},
				{ID: "albums", Label: "Albums", Kind: "library", Level: "album", Search: true},
				{ID: "album-genres", Label: "Album Genres", Kind: "collections", Level: "album", Facet: "genre", Order: "count", Search: true},
				{ID: "composers", Label: "Composers", Kind: "people", Level: "track", Facet: "composer", Order: "name", Search: true},
			},
		}}, nil
	}
	base.LibraryItemsFunc = func(_ context.Context, _ *core.Viewer, _ core.Device, viewID string, q frontend.ItemQuery) (core.Page, error) {
		base.lastLibID = viewID
		base.lastQuery = q
		return core.Page{Items: []core.Title{{Key: core.PluginKey(1), Type: core.TypeSeason, Title: "Kind of Blue"}}, Total: 1}, nil
	}

	exp := &levelPresExperience{pluginPresExperience: base}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.15"},
		},
	}
	inst.routes()

	doGet := func(path string, extraHeaders map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Vela-Device-Id", "tv")
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		inst.serve(rr, req)
		return rr
	}

	// 1. GET /vela/v0/libraries emits tab.level.
	res := doGet("/vela/v0/libraries", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /libraries = %d %s", res.Code, res.Body.String())
	}
	var libsResp struct {
		Items []struct {
			ID   string `json:"id"`
			Tabs []struct {
				ID    string `json:"id"`
				Level string `json:"level"`
			} `json:"tabs"`
		} `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &libsResp); err != nil {
		t.Fatal(err)
	}
	if len(libsResp.Items) != 1 || len(libsResp.Items[0].Tabs) != 5 {
		t.Fatalf("unexpected libraries response: %+v", libsResp)
	}
	if libsResp.Items[0].Tabs[2].Level != "album" || libsResp.Items[0].Tabs[4].Level != "track" {
		t.Fatalf("tab levels not emitted on wire: %+v", libsResp.Items[0].Tabs)
	}

	// 2. GET /items?tab=albums propagates Level="album" into ItemQuery.
	res = doGet("/vela/v0/libraries/77/items?tab=albums", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /items?tab=albums = %d %s", res.Code, res.Body.String())
	}
	if base.lastQuery.Level != "album" {
		t.Fatalf("tab=albums ItemQuery.Level = %q, want album", base.lastQuery.Level)
	}

	// Conflicting ?tab=albums&level=track returns 400.
	res = doGet("/vela/v0/libraries/77/items?tab=albums&level=track", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("conflicting tab+level status = %d, want 400", res.Code)
	}

	// 3. GET /facets/genre?level=album and ?tab=albums call LibraryFacetBucketsAtLevel.
	res = doGet("/vela/v0/libraries/77/facets/genre?level=album", nil)
	if res.Code != http.StatusOK || exp.lastBucketLevel != "album" {
		t.Fatalf("facets?level=album: code=%d level=%q body=%s", res.Code, exp.lastBucketLevel, res.Body.String())
	}
	exp.lastBucketLevel = ""
	res = doGet("/vela/v0/libraries/77/facets/genre?tab=albums", nil)
	if res.Code != http.StatusOK || exp.lastBucketLevel != "album" {
		t.Fatalf("facets?tab=albums: code=%d level=%q", res.Code, exp.lastBucketLevel)
	}

	// 4. GET /collections?tab=album-genres and ?tab=composers pass tab.Level in PageQuery.
	res = doGet("/vela/v0/libraries/77/collections?tab=album-genres", nil)
	if res.Code != http.StatusOK || exp.lastColLevel != "album" {
		t.Fatalf("collections?tab=album-genres: code=%d level=%q", res.Code, exp.lastColLevel)
	}
	res = doGet("/vela/v0/libraries/77/collections?tab=composers", nil)
	if res.Code != http.StatusOK || exp.lastPeopleLevel != "track" {
		t.Fatalf("collections?tab=composers: code=%d level=%q", res.Code, exp.lastPeopleLevel)
	}

	// 5. GET /rows propagates Accept-Language into Device.Language.
	res = doGet("/vela/v0/libraries/77/rows", map[string]string{"Accept-Language": "fr-FR,fr;q=0.9"})
	if res.Code != http.StatusOK || exp.lastRowLang != "fr-FR,fr;q=0.9" {
		t.Fatalf("rows Accept-Language: code=%d lang=%q", res.Code, exp.lastRowLang)
	}
}

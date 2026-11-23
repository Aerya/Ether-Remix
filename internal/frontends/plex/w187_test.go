package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type plexMultiLevelExperience struct {
	*kindExperience
	lastQuery      frontend.ItemQuery
	lastFacetLevel string
	lastFacetName  string
}

func (e *plexMultiLevelExperience) LibraryItems(_ context.Context, _ *core.Viewer, _ core.Device, _ string, q frontend.ItemQuery) (core.Page, error) {
	e.lastQuery = q
	if len(q.Types) == 1 && string(q.Types[0]) == plexBaseAlbum {
		return core.Page{Items: []core.Title{kindMusicAlbum()}, Total: 1}, nil
	}
	if len(q.Types) == 1 && string(q.Types[0]) == plexBaseTrack {
		return core.Page{Items: []core.Title{kindMusicTrack()}, Total: 1}, nil
	}
	return core.Page{Items: []core.Title{kindMusicArtist()}, Total: 1}, nil
}

func (e *plexMultiLevelExperience) FacetsAtLevel(_ context.Context, _ *core.Viewer, _ core.Device, _, level, name string) ([]core.FacetValue, error) {
	e.lastFacetLevel = level
	e.lastFacetName = name
	return []core.FacetValue{{ID: "g1", Key: "Jazz", Title: "Jazz", Count: 2}}, nil
}

func TestPlexPluginMusicTypeParamsAndLevelFacets(t *testing.T) {
	exp := &plexMultiLevelExperience{kindExperience: newKindExperience()}
	srv := newTestServer(testServerDeps{Experience: exp})
	sec, ok := srv.sectionByID(7)
	if !ok {
		t.Fatal("expected music plugin section 7")
	}

	// 1. ?type=9 (albums) on plugin music section queries album type recursively.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/library/sections/7/all?type=9", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: &ownerUser, Format: model.JSON}))
	srv.sectionKindList(rec, req, sec, "all")
	if rec.Code != http.StatusOK {
		t.Fatalf("type=9 status = %d", rec.Code)
	}
	if !exp.lastQuery.Recursive || len(exp.lastQuery.Types) != 1 || string(exp.lastQuery.Types[0]) != plexBaseAlbum {
		t.Fatalf("type=9 query = %+v, want Recursive=true Types=[album]", exp.lastQuery)
	}
	var out struct {
		MediaContainer struct {
			ViewGroup string           `json:"viewGroup"`
			Title2    string           `json:"title2"`
			Metadata  []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.MediaContainer.ViewGroup != "album" || out.MediaContainer.Title2 != "All Albums" || len(out.MediaContainer.Metadata) != 1 || out.MediaContainer.Metadata[0]["type"] != "album" {
		t.Fatalf("type=9 response = %+v", out.MediaContainer)
	}

	// 2. ?type=10 (tracks) and /allLeaves query track type recursively.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/library/sections/7/allLeaves", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: &ownerUser, Format: model.JSON}))
	srv.sectionKindList(rec, req, sec, "allLeaves")
	if rec.Code != http.StatusOK {
		t.Fatalf("allLeaves status = %d", rec.Code)
	}
	if !exp.lastQuery.Recursive || len(exp.lastQuery.Types) != 1 || string(exp.lastQuery.Types[0]) != plexBaseTrack {
		t.Fatalf("allLeaves query = %+v, want Recursive=true Types=[track]", exp.lastQuery)
	}

	// 3. /genre?type=9 calls FacetsAtLevel with level="album".
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/library/sections/7/genre?type=9", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: &ownerUser, Format: model.JSON}))
	srv.sectionFacet(rec, req, sec, "genre")
	if rec.Code != http.StatusOK {
		t.Fatalf("genre?type=9 status = %d", rec.Code)
	}
	if exp.lastFacetLevel != "album" || exp.lastFacetName != "genre" {
		t.Fatalf("sectionFacet(genre?type=9) got level=%q name=%q, want album/genre", exp.lastFacetLevel, exp.lastFacetName)
	}
}

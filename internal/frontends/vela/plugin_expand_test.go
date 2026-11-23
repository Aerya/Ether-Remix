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
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type pluginExpandVelaExperience struct {
	frontendtest.Experience
	expanding bool
	items     []core.Title
	person    frontend.PersonDetail
	lastQuery frontend.ItemQuery
}

func (e *pluginExpandVelaExperience) Person(_ context.Context, _ *frontend.Viewer, _ frontend.Device, id string, _ frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	if id == e.person.ID {
		return e.person, nil
	}
	return frontend.PersonDetail{}, core.ErrNotFound
}

func (e *pluginExpandVelaExperience) People(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	return []frontend.PersonDetail{e.person}, nil
}

func (e *pluginExpandVelaExperience) PersonCredit(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ frontend.RatingKey, _ int64) (frontend.PersonDetail, error) {
	return frontend.PersonDetail{}, core.ErrNotFound
}

func TestVelaPluginCollectionAndPersonExpandingHintAndETag(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	pluginPersonID := core.PluginPersonID("Charles Martinet")
	personFacetID, err := ids.GUID(ids.Person, pluginPersonID)
	if err != nil {
		t.Fatal(err)
	}

	views := []frontend.View{
		{
			ID:        "77",
			LibraryID: 77,
			Name:      "Games",
			Type:      frontend.TypeMovie,
			Virtual:   true,
			PluginID:  "community.example.games",
			KindKey:   "games",
			Revision:  "r1",
			Sorts:     []string{"title", "released"},
			Facets: []frontend.ViewFacet{
				{ID: "developers", Label: "Developers", Type: "strings", Multi: true},
				{ID: "voiceCast", Label: "Voice cast", Type: "people", Multi: true},
			},
		},
	}

	exp := &pluginExpandVelaExperience{
		expanding: true,
		items: []core.Title{
			{Key: core.PluginKey(1), Type: core.TypeMovie, LibraryID: 77, Title: "Super Mario 64", Year: 1996},
		},
		person: frontend.PersonDetail{
			ID:   personFacetID,
			Name: "Charles Martinet",
			TMDB: pluginPersonID,
		},
	}
	exp.ViewsFunc = func(_ context.Context, _ *frontend.Viewer) ([]frontend.View, error) {
		return views, nil
	}
	exp.LibraryItemsFunc = func(_ context.Context, _ *frontend.Viewer, _ core.Device, viewID string, q frontend.ItemQuery) (frontend.Page, error) {
		if viewID != "77" {
			return frontend.Page{}, core.ErrNotFound
		}
		exp.lastQuery = q
		if q.Offset >= len(exp.items) {
			return frontend.Page{Items: []core.Title{}, Total: len(exp.items), Expanding: exp.expanding}, nil
		}
		end := len(exp.items)
		if q.Limit > 0 && q.Offset+q.Limit < end {
			end = q.Offset + q.Limit
		}
		return frontend.Page{
			Items:     append([]core.Title(nil), exp.items[q.Offset:end]...),
			Total:     len(exp.items),
			Expanding: exp.expanding,
		}, nil
	}

	imgService, err := identity.NewArtworkImageGrantService(db, "vela", grants, func() frontend.Experience { return exp }, castArtwork{}, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{seal: seal, mux: http.NewServeMux(), host: frontend.Host{Experience: exp, Identity: identityFake{}, ImageGrants: imgService}}
	inst.routes()

	getJSON := func(path string) (int, string, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("X-Vela-Device-Id", "tv")
		rec := httptest.NewRecorder()
		inst.serve(rec, req)
		var out map[string]any
		if rec.Body.Len() > 0 {
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
		}
		return rec.Code, rec.Header().Get("ETag"), out
	}

	// 1. collectionId=facet:developers:Nintendo maps to Facets["developers"]=["Nintendo"] and emits expanding:true.
	code, etagRunning, bodyRunning := getJSON("/vela/v0/libraries/77/items?collectionId=facet:developers:Nintendo")
	if code != 200 {
		t.Fatalf("collectionId facet status = %d, body = %v", code, bodyRunning)
	}
	if got := exp.lastQuery.Filter.Facets["developers"]; len(got) != 1 || got[0] != "Nintendo" {
		t.Fatalf("mapped facets = %v, want [Nintendo]", got)
	}
	if bodyRunning["expanding"] != true {
		t.Fatalf("expected expanding=true while running, got %v", bodyRunning["expanding"])
	}

	// Undeclared collectionId facet is rejected with 400.
	if badCode, _, _ := getJSON("/vela/v0/libraries/77/items?collectionId=facet:undeclared:Val"); badCode != 400 {
		t.Fatalf("undeclared collectionId facet status = %d, want 400", badCode)
	}

	// 2. Person titles emits expanding:true while running.
	pCode, pEtagRunning, pBodyRunning := getJSON("/vela/v0/people/" + personFacetID + "/titles")
	if pCode != 200 || pBodyRunning["expanding"] != true {
		t.Fatalf("person titles running = %d %v, want 200 with expanding=true", pCode, pBodyRunning)
	}

	// 3. When expansion finishes, expanding is omitted and ETag bumps on both routes.
	exp.expanding = false
	exp.items = append(exp.items, core.Title{
		Key: core.PluginKey(2), Type: core.TypeMovie, LibraryID: 77, Title: "Luigi's Mansion", Year: 2001,
	})
	views[0].Revision = "r2"

	codeDone, etagDone, bodyDone := getJSON("/vela/v0/libraries/77/items?facet.developers=Nintendo")
	if codeDone != 200 {
		t.Fatalf("done status = %d, body = %v", codeDone, bodyDone)
	}
	if _, present := bodyDone["expanding"]; present {
		t.Fatalf("expected expanding omitted when done, got %v", bodyDone["expanding"])
	}
	if etagDone == "" || etagDone == etagRunning {
		t.Fatalf("expected ETag bump after expansion finished: running=%q done=%q", etagRunning, etagDone)
	}

	pCodeDone, pEtagDone, pBodyDone := getJSON("/vela/v0/people/" + personFacetID + "/titles")
	if pCodeDone != 200 {
		t.Fatalf("person titles done status = %d, body = %v", pCodeDone, pBodyDone)
	}
	if _, present := pBodyDone["expanding"]; present {
		t.Fatalf("expected person titles expanding omitted when done, got %v", pBodyDone["expanding"])
	}
	if pEtagDone == "" || pEtagDone == pEtagRunning {
		t.Fatalf("expected person titles ETag bump after expansion finished: running=%q done=%q", pEtagRunning, pEtagDone)
	}
}

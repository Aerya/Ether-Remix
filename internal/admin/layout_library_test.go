package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
)

type libraryAwareEngine struct {
	*fakeEngine
	rows []core.RowDescriptor
}

func (e *libraryAwareEngine) AvailableRows(context.Context) ([]core.RowDescriptor, error) {
	return e.rows, nil
}

func (e *libraryAwareEngine) PreviewCandidateRows(_ context.Context, u *core.User, hc core.HubContext, section string, _ int, d core.HomeLayout, rows []core.HomeRow) (*core.HomePreview, error) {
	e.draft = d
	e.who = u.ID
	e.hc = hc
	e.section = section
	out := make([]core.HomeRow, 0, len(rows))
	for i, r := range rows {
		r.Slot = i + 1
		out = append(out, r)
	}
	return &core.HomePreview{
		Context:     hc,
		GeneratedAt: time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC),
		Rows:        out,
	}, nil
}

type libraryLayoutResp struct {
	Library       admin.LayoutLibrary       `json:"library"`
	Customized    bool                      `json:"customized"`
	EffectivePage core.LayoutPage           `json:"effectivePage"`
	ViewerPages   []admin.ViewerLibraryPage `json:"viewerPages"`
	Defaults      struct {
		MaxRows int `json:"maxRows"`
	} `json:"defaults"`
	AvailableRows []core.RowDescriptor `json:"availableRows"`
}

func TestPerLibraryLayoutEndpointsAndPreviewOverlay(t *testing.T) {
	base := &fakeEngine{
		doc: core.HomeLayout{
			Global: []core.LayoutPage{
				{
					Section: "home",
					MaxRows: 8,
					Rows:    []core.RowPolicy{{Row: "movies.top10", Status: "always", Slot: 1}},
				},
				{
					Section: "library:1",
					MaxRows: 6,
					Rows:    []core.RowPolicy{{Row: "movies.top10", Status: "always", Slot: 1}},
				},
			},
			Viewers: []core.ViewerLayout{
				{
					UserID: 2,
					Pages: []core.LayoutPage{
						{Section: "library:1", Rows: []core.RowPolicy{{Row: "movies.gems", Status: "always", Slot: 2}}},
					},
				},
			},
		},
	}
	eng := &libraryAwareEngine{
		fakeEngine: base,
		rows: []core.RowDescriptor{
			{HubID: "home.only", Title: "Home Only", Kind: "home-only", Sections: []string{"home"}},
			{HubID: "movies.top10", Title: "Top 10 Movies", Kind: "top-10", Sections: []string{"home", "movies"}},
			{HubID: "movies.gems", Title: "Hidden Gems", Kind: "gems", Sections: []string{"movies", "shows"}},
			{HubID: "shows.only", Title: "Shows Only", Kind: "shows-only", Sections: []string{"shows"}},
		},
	}
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{
		Config:          cfg,
		IsOwnerToken:    storedOwner(cfg),
		Personalization: eng,
		Users:           &fakeUsers{users: []core.User{{ID: 1, IsOwner: true}, {ID: 2}}},
		LayoutLibraries: func(context.Context) ([]admin.LayoutLibrary, error) {
			return []admin.LayoutLibrary{
				{ID: 1, Name: "Movies", Slug: "movies", Type: "movie"},
				{ID: 2, Name: "TV Shows", Slug: "shows", Type: "show"},
				{ID: 3, Name: "Kids Movies", Slug: "kids-movies", Type: "movie"},
				{ID: 4, Name: "Creator Clips", Slug: "creator-clips", Type: "plugin"},
			}, nil
		},
		PluginLibraryRows: func(_ context.Context, _ *core.User, libraryID int) ([]core.RowDescriptor, []core.HomeRow, error) {
			if libraryID != 4 {
				return nil, nil, core.ErrNotFound
			}
			descs := []core.RowDescriptor{
				{HubID: "plugin:4:added", Title: "Recently Added", Kind: "recentlyAdded", Sections: []string{"library:4"}},
				{HubID: "plugin:4:rating", Title: "Top Rated", Kind: "topRated", Sections: []string{"library:4"}},
			}
			rows := []core.HomeRow{
				{HubID: "plugin:4:added", Title: "Recently Added", Kind: "recentlyAdded", Items: []core.Title{poster}},
				{HubID: "plugin:4:rating", Title: "Top Rated", Kind: "topRated", Items: []core.Title{poster}},
			}
			return descs, rows, nil
		},
	})

	rec := serve(h, req(http.MethodGet, "/ether/api/layout", ""))
	if rec.Code != 200 {
		t.Fatalf("GET /api/layout: %d %s", rec.Code, rec.Body)
	}
	var rootResp struct {
		Libraries []admin.LayoutLibrary `json:"libraries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rootResp); err != nil {
		t.Fatal(err)
	}
	if len(rootResp.Libraries) != 4 || !rootResp.Libraries[0].Customized || rootResp.Libraries[2].Section != "library:3" || rootResp.Libraries[2].Customized {
		t.Fatalf("unexpected libraries in GET /api/layout: %+v", rootResp.Libraries)
	}

	// Uncustomised library:3 starts clean (zero inheritance from home or library:1).
	rec = serve(h, req(http.MethodGet, "/ether/api/layout/libraries/3", ""))
	if rec.Code != 200 {
		t.Fatalf("GET /api/layout/libraries/3: %d %s", rec.Code, rec.Body)
	}
	var libGet libraryLayoutResp
	if err := json.Unmarshal(rec.Body.Bytes(), &libGet); err != nil {
		t.Fatal(err)
	}
	if libGet.Customized || libGet.EffectivePage.Section != "library:3" || libGet.EffectivePage.MaxRows != 0 || len(libGet.EffectivePage.Rows) != 0 {
		t.Fatalf("expected clean independent library:3 page, got %+v", libGet)
	}
	if len(libGet.ViewerPages) != 0 {
		t.Fatalf("expected zero inherited viewer overrides on library:3, got %+v", libGet.ViewerPages)
	}
	if len(libGet.AvailableRows) != 2 || libGet.AvailableRows[0].HubID != "movies.top10" || libGet.AvailableRows[1].HubID != "movies.gems" {
		t.Fatalf("availableRows not filtered to movie candidates: %+v", libGet.AvailableRows)
	}
	if libGet.Defaults.MaxRows != 10 {
		t.Fatalf("defaults.maxRows = %d, want 10", libGet.Defaults.MaxRows)
	}

	// POST /api/layout/libraries/{id} (fork from standard rows) no longer exists.
	if rPost := serve(h, req(http.MethodPost, "/ether/api/layout/libraries/3", "")); rPost.Code == 200 {
		t.Fatalf("expected POST /api/layout/libraries/3 to be removed, got 200")
	}

	putBody := `{
		"page": {"maxRows": 5, "rows": [{"row": "movies.gems", "status": "always", "slot": 1}]},
		"viewerPages": [{"userId": 2, "page": {"maxRows": 4, "rows": [{"row": "movies.top10", "status": "always", "slot": 2}]}}]
	}`
	rec = serve(h, req(http.MethodPut, "/ether/api/layout/libraries/3", putBody))
	if rec.Code != 200 {
		t.Fatalf("PUT /api/layout/libraries/3: %d %s", rec.Code, rec.Body)
	}
	var afterPut libraryLayoutResp
	if err := json.Unmarshal(rec.Body.Bytes(), &afterPut); err != nil {
		t.Fatal(err)
	}
	if !afterPut.Customized || afterPut.EffectivePage.MaxRows != 5 || len(afterPut.EffectivePage.Rows) != 1 || afterPut.EffectivePage.Rows[0].Row != "movies.gems" {
		t.Fatalf("after PUT: effectivePage = %+v", afterPut.EffectivePage)
	}
	if base.doc.Global[0].Section != "home" || base.doc.Global[0].MaxRows != 8 || base.doc.Global[1].Section != "library:1" || base.doc.Global[1].MaxRows != 6 {
		t.Fatalf("home or library:1 clobbered: %+v", base.doc.Global)
	}

	writesBefore := base.writes
	prevBody := `{"viewerId": 2, "section": "library:3", "page": {"maxRows": 3, "rows": [{"row": "movies.top10", "status": "always", "slot": 1}]}}`
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", prevBody))
	if rec.Code != 200 {
		t.Fatalf("POST /api/layout/preview: %d %s", rec.Code, rec.Body)
	}
	if base.writes != writesBefore || base.section != "library:3" {
		t.Fatalf("preview mutated state or lost section: writes=%d section=%q", base.writes, base.section)
	}

	// Plugin library layout + preview works end-to-end.
	rec = serve(h, req(http.MethodGet, "/ether/api/layout/libraries/4", ""))
	if rec.Code != 200 {
		t.Fatalf("GET /api/layout/libraries/4: %d %s", rec.Code, rec.Body)
	}
	var pluginGet libraryLayoutResp
	if err := json.Unmarshal(rec.Body.Bytes(), &pluginGet); err != nil {
		t.Fatal(err)
	}
	if pluginGet.Defaults.MaxRows != 8 || len(pluginGet.AvailableRows) != 2 || pluginGet.AvailableRows[0].HubID != "plugin:4:added" {
		t.Fatalf("unexpected plugin library layout: %+v", pluginGet)
	}
	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", `{"section": "library:4"}`))
	if rec.Code != 200 || base.section != "library:4" {
		t.Fatalf("POST /api/layout/preview(library:4): %d %s (section=%q)", rec.Code, rec.Body, base.section)
	}

	rec = serve(h, req(http.MethodDelete, "/ether/api/layout/libraries/3", ""))
	if rec.Code != 200 {
		t.Fatalf("DELETE /api/layout/libraries/3: %d %s", rec.Code, rec.Body)
	}
	var afterReset libraryLayoutResp
	if err := json.Unmarshal(rec.Body.Bytes(), &afterReset); err != nil {
		t.Fatal(err)
	}
	if afterReset.Customized || afterReset.EffectivePage.MaxRows != 0 || len(afterReset.EffectivePage.Rows) != 0 {
		t.Fatalf("after DELETE reset: %+v", afterReset)
	}

	if r404 := serve(h, req(http.MethodGet, "/ether/api/layout/libraries/99", "")); r404.Code != 404 {
		t.Fatalf("unknown library = %d, want 404", r404.Code)
	}
	if r400 := serve(h, req(http.MethodGet, "/ether/api/layout/libraries/not-an-id", "")); r400.Code != 400 {
		t.Fatalf("bad library id = %d, want 400", r400.Code)
	}
}

func TestPluginLibraryDeclaredShelvesDefaultMaxRows(t *testing.T) {
	base := &fakeEngine{
		doc: core.HomeLayout{
			Global: []core.LayoutPage{{Section: "home", MaxRows: 8}},
		},
	}
	var gotPreviewDefault int
	eng := &declaredShelfPreviewEngine{
		libraryAwareEngine: &libraryAwareEngine{fakeEngine: base},
		onPreview:          func(def int) { gotPreviewDefault = def },
	}
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{
		Config:          cfg,
		IsOwnerToken:    storedOwner(cfg),
		Personalization: eng,
		Users:           &fakeUsers{users: []core.User{{ID: 1, IsOwner: true}}},
		LayoutLibraries: func(context.Context) ([]admin.LayoutLibrary, error) {
			return []admin.LayoutLibrary{
				{ID: 7, Name: "Podcasts", Slug: "podcasts", Type: "plugin"},
			}, nil
		},
		PluginLibraryRows: func(_ context.Context, _ *core.User, libraryID int) ([]core.RowDescriptor, []core.HomeRow, error) {
			if libraryID != 7 {
				return nil, nil, core.ErrNotFound
			}
			descs := make([]core.RowDescriptor, 0, 15)
			rows := make([]core.HomeRow, 0, 15)
			for i := 1; i <= 15; i++ {
				id := "plugin:7:shelf:s" + string(rune('a'+i-1))
				descs = append(descs, core.RowDescriptor{HubID: id, Title: "Shelf", Kind: "shelf", Sections: []string{"library:7"}})
				rows = append(rows, core.HomeRow{HubID: id, Title: "Shelf", Kind: "shelf", Items: []core.Title{{Key: core.PluginKey(int64(i)), Title: "Ep"}}})
			}
			return descs, rows, nil
		},
	})

	rec := serve(h, req(http.MethodGet, "/ether/api/layout/libraries/7", ""))
	if rec.Code != 200 {
		t.Fatalf("GET /ether/api/layout/libraries/7: %d %s", rec.Code, rec.Body)
	}
	var resp libraryLayoutResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Defaults.MaxRows != 15 {
		t.Fatalf("declared shelves defaults.maxRows = %d, want 15", resp.Defaults.MaxRows)
	}

	rec = serve(h, req(http.MethodPost, "/ether/api/layout/preview", `{"section": "library:7"}`))
	if rec.Code != 200 {
		t.Fatalf("POST /ether/api/layout/preview: %d %s", rec.Code, rec.Body)
	}
	if gotPreviewDefault != 15 {
		t.Fatalf("PreviewCandidateRows defaultMaxRows = %d, want 15", gotPreviewDefault)
	}
}

type declaredShelfPreviewEngine struct {
	*libraryAwareEngine
	onPreview func(int)
}

func (e *declaredShelfPreviewEngine) PreviewCandidateRows(ctx context.Context, u *core.User, hc core.HubContext, section string, def int, d core.HomeLayout, rows []core.HomeRow) (*core.HomePreview, error) {
	if e.onPreview != nil {
		e.onPreview(def)
	}
	return e.libraryAwareEngine.PreviewCandidateRows(ctx, u, hc, section, def, d, rows)
}

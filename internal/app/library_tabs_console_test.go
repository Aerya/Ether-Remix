package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type captureNeutralLayout struct {
	sit     core.HomeSituation
	section string
}

func (c *captureNeutralLayout) FinalizeNeutralHome(_ context.Context, _ *core.User, sit core.HomeSituation, section string, rows []core.NeutralHomeRow) ([]core.NeutralHomeRow, error) {
	c.sit = sit
	c.section = section
	return rows, nil
}

func TestLibraryTabsConsoleEndpointsAndPreview(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()

	rel2001 := time.Date(2001, 5, 1, 0, 0, 0, 0, time.UTC)
	rel2022 := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, title := range []core.Title{
		{
			Key: core.MovieKey(501), Type: core.TypeMovie, Title: "Solaris", Year: 2001,
			Released: rel2001, HomeReleased: rel2001, Rating: 7.4,
			Genres: []string{"Science Fiction", "Drama"}, Studios: []string{"20th Century Fox"},
			Directors: []core.Person{{TMDB: 1884, Name: "Steven Soderbergh"}},
		},
		{
			Key: core.MovieKey(502), Type: core.TypeMovie, Title: "Aftersun", Year: 2022,
			Released: rel2022, HomeReleased: rel2022, Rating: 8.1,
			Genres: []string{"Drama"}, Studios: []string{"A24"},
			Directors: []core.Person{{TMDB: 2109999, Name: "Charlotte Wells"}},
		},
	} {
		if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}

	c := &librariesConsole{app: a}
	h := c.handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := call("GET", "/1/tabs", "")
	if w.Code != 200 {
		t.Fatalf("GET /1/tabs = %d %s", w.Code, w.Body)
	}
	var dto libraryTabsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Customized || len(dto.EffectiveTabs) != 3 || dto.ScopeNote != libraryTabsScopeNote {
		t.Fatalf("unexpected initial libraryTabsDTO: %+v", dto)
	}
	if len(dto.AvailableFacets.Collections) == 0 || len(dto.AvailableFacets.People) == 0 || len(dto.AvailableSorts) == 0 {
		t.Fatalf("missing availableFacets or availableSorts: %+v", dto)
	}

	putBody := `{
		"tabs": [
			{"key": "recommended", "label": "Recommended", "kind": "recommended"},
			{"key": "library", "label": "All Movies", "kind": "library", "sort": "added", "direction": "desc"},
			{
				"key": "scifi",
				"label": "Sci-Fi",
				"kind": "view",
				"sort": "rating",
				"direction": "desc",
				"search": true,
				"searchPlaceholder": "Search sci-fi",
				"filters": {
					"match": "all",
					"rules": [{"field": "genre", "op": "in", "value": ["Science Fiction"]}]
				}
			},
			{"key": "genres", "label": "Genres", "kind": "collections", "facet": "genre", "order": "count", "search": true},
			{"key": "directors", "label": "Directors", "kind": "people", "facet": "director", "order": "name", "search": true}
		]
	}`
	w = call("PUT", "/1/tabs", putBody)
	if w.Code != 200 {
		t.Fatalf("PUT /1/tabs = %d %s", w.Code, w.Body)
	}
	var afterPut libraryTabsDTO
	if err := json.Unmarshal(w.Body.Bytes(), &afterPut); err != nil {
		t.Fatal(err)
	}
	if !afterPut.Customized || len(afterPut.EffectiveTabs) != 5 || afterPut.EffectiveTabs[2].Key != "scifi" {
		t.Fatalf("after PUT /1/tabs: %+v", afterPut)
	}

	wLib := call("GET", "/1", "")
	if wLib.Code != 200 || !strings.Contains(wLib.Body.String(), `"effectiveTabs"`) || !strings.Contains(wLib.Body.String(), `"scifi"`) {
		t.Fatalf("GET /1 missing effectiveTabs: %d %s", wLib.Code, wLib.Body)
	}

	wPrev := call("POST", "/1/tabs/preview", `{
		"tab": {
			"key": "scifi",
			"label": "Sci-Fi",
			"kind": "view",
			"sort": "year",
			"direction": "desc",
			"filters": {
				"match": "all",
				"rules": [{"field": "genre", "op": "in", "value": ["Science Fiction"]}]
			}
		},
		"limit": 10
	}`)
	if wPrev.Code != 200 {
		t.Fatalf("POST /1/tabs/preview(view) = %d %s", wPrev.Code, wPrev.Body)
	}
	var viewPrev struct {
		Total int          `json:"total"`
		Items []core.Title `json:"items"`
	}
	if err := json.Unmarshal(wPrev.Body.Bytes(), &viewPrev); err != nil {
		t.Fatal(err)
	}
	if viewPrev.Total != 1 || len(viewPrev.Items) != 1 || viewPrev.Items[0].Title != "Solaris" {
		t.Fatalf("view preview = %+v, want Solaris only", viewPrev)
	}

	wColPrev := call("POST", "/1/tabs/preview", `{"tab": {"key": "genres", "label": "Genres", "kind": "collections", "facet": "genre", "order": "count"}}`)
	if wColPrev.Code != 200 || !strings.Contains(wColPrev.Body.String(), `"Drama"`) {
		t.Fatalf("collections preview = %d %s", wColPrev.Code, wColPrev.Body)
	}
	wPeoplePrev := call("POST", "/1/tabs/preview", `{"tab": {"key": "directors", "label": "Directors", "kind": "people", "facet": "director"}, "query": "wells"}`)
	if wPeoplePrev.Code != 200 || !strings.Contains(wPeoplePrev.Body.String(), `"Charlotte Wells"`) || strings.Contains(wPeoplePrev.Body.String(), `"Steven Soderbergh"`) {
		t.Fatalf("people preview = %d %s", wPeoplePrev.Code, wPeoplePrev.Body)
	}

	wDel := call("DELETE", "/1/tabs", "")
	if wDel.Code != 200 {
		t.Fatalf("DELETE /1/tabs = %d %s", wDel.Code, wDel.Body)
	}
	var afterDel libraryTabsDTO
	if err := json.Unmarshal(wDel.Body.Bytes(), &afterDel); err != nil {
		t.Fatal(err)
	}
	if afterDel.Customized || len(afterDel.EffectiveTabs) != 3 {
		t.Fatalf("after DELETE /1/tabs: %+v", afterDel)
	}
}

func TestExperienceLayoutRowsPassesLibraryIDToNeutralHome(t *testing.T) {
	capLayout := &captureNeutralLayout{}
	libs := []experience.Library{
		{ID: 1, Name: "Movies", Type: core.TypeMovie, Virtual: true},
		{ID: 5, Name: "Anime Movies", Type: core.TypeMovie, Virtual: true},
	}
	req := frontend.RowRequest{
		Viewer:    &core.Viewer{ID: 1, IsOwner: true},
		Placement: frontend.PlacementLibrary,
		ViewID:    "5",
		Limit:     10,
	}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie}
	in := []frontend.Row{
		{
			ID:      "movies.top10",
			Title:   "Top 10",
			Kind:    "top-10",
			Purpose: frontend.RowRecommendation,
			Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &title}},
		},
	}
	_, err := experience.LayoutRows(context.Background(), capLayout, libs, req, core.HomeSituation{}, in)
	if err != nil {
		t.Fatalf("LayoutRows: %v", err)
	}
	if capLayout.sit.LibraryID != 5 || capLayout.section != "library:5" {
		t.Fatalf("LayoutRows passed LibraryID=%d section=%q, want LibraryID=5 section=library:5", capLayout.sit.LibraryID, capLayout.section)
	}
}

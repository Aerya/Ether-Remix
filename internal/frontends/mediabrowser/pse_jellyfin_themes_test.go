package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type pseJellyfinTestExperience struct {
	frontendtest.Experience
	allowed bool
	theme   frontend.View
	row     frontend.View
}

func (e *pseJellyfinTestExperience) PSEThemeRowViews(_ context.Context, _ *frontend.Viewer, view string) ([]frontend.View, error) {
	if !e.allowed || view != e.theme.ID {
		return nil, frontend.ErrNotFound
	}
	return []frontend.View{e.row}, nil
}

func (e *pseJellyfinTestExperience) PSEThemeRowView(_ context.Context, _ *frontend.Viewer, view string) (frontend.View, error) {
	if !e.allowed || view != e.row.ID {
		return frontend.View{}, frontend.ErrNotFound
	}
	return e.row, nil
}

func TestPSEJellyfinThemeRouting(t *testing.T) {
	const themeNumber = 5101
	const rowNumber = frontend.PSEThemeJellyfinRowBase + 7
	themeID, _ := ids.GUID(ids.View, themeNumber)
	unknownID, _ := ids.GUID(ids.View, themeNumber+1)
	rowID, _ := ids.GUID(ids.View, rowNumber)
	viewer := &frontend.Viewer{ID: 12, AllowedLibraries: []frontend.LibraryID{5100, themeNumber}}
	thumb := frontend.Title{Key: core.MovieKey(91), Type: "movie", Title: "Dessins"}
	about := frontend.Title{Key: core.EpisodeKey(71, 1, 1), Type: "episode", Title: "Pilote"}
	e := &pseJellyfinTestExperience{
		allowed: true,
		theme:   frontend.View{ID: ids.Decimal(themeNumber), LibraryID: themeNumber, Name: "Enfants", Type: "mixed"},
		row:     frontend.View{ID: ids.Decimal(rowNumber), LibraryID: themeNumber, Name: "Animation", Type: "movie"},
	}
	getTitle := func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, view string, q frontend.ItemQuery) (frontend.Page, error) {
		if !e.allowed || view != e.theme.ID && view != e.row.ID {
			return frontend.Page{}, frontend.ErrNotFound
		}
		title := thumb
		if q.Filter.Query == "pilote" {
			if !q.Recursive || !slices.Contains(q.Types, "episode") {
				t.Fatalf("la recherche d'épisode doit rester dans le thème : %+v", q)
			}
			title = about
		}
		if q.Filter.IncludeKeys != nil && !slices.Contains(q.Filter.IncludeKeys, title.Key) {
			return frontend.Page{}, nil
		}
		if q.Offset > 0 {
			return frontend.Page{Total: 1}, nil
		}
		return frontend.Page{Items: []frontend.Title{title}, Total: 1}, nil
	}
	e.Experience = frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			if e.allowed {
				return []frontend.View{e.theme}, nil
			}
			return nil, nil
		},
		LibraryCountFunc: func(_ context.Context, _ *frontend.Viewer, view string) (int, error) {
			t.Fatalf("chargement du compteur pour la vue Remix %s", view)
			return 0, nil
		},
		LibraryItemsFunc: getTitle,
		SearchFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.SearchPage, error) {
			t.Fatal("recherche générale appelée pour le thème")
			return frontend.SearchPage{}, nil
		},
	}
	identity := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "themes"}).Mount(t.Context(), frontend.Host{
		Identity: identity, Experience: e,
		PageFreshness: func() frontend.PageFreshness { return frontend.PageFreshness{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := inst.Handler()
	get := func(path, etag string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Emby-Token", "test-token")
		r.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s : code %d au lieu de %d, %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	object := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	entry := func(w *httptest.ResponseRecorder, field string) map[string]any {
		t.Helper()
		entries, _ := object(w)[field].([]any)
		if len(entries) != 1 {
			t.Fatal(w.Body.String())
		}
		return entries[0].(map[string]any)
	}
	view := entry(get("/UserViews", "", http.StatusOK), "Items")
	if kind, ok := view["CollectionType"]; view["Id"] != themeID || !ok || kind != nil || view["ChildCount"] != nil {
		t.Fatalf("vue mixte inattendue : %+v", view)
	}
	if view := object(get("/Items/"+themeID, "", http.StatusOK)); view["Id"] != themeID || view["ChildCount"] != nil {
		t.Fatalf("détail de vue inattendu : %+v", view)
	}
	if row := entry(get("/Items?ParentId="+themeID+"&IncludeItemTypes=BoxSet", "", http.StatusOK), "Items"); row["Id"] != rowID || row["Type"] != "BoxSet" || row["ParentId"] != themeID {
		t.Fatalf("rangée inattendue : %+v", row)
	}
	for _, test := range []struct {
		query string
		kind  string
		id    string
		total int
	}{
		{"IncludeItemTypes=Folder,Movie,Series&Limit=1", "Folder", rowID, 2},
		{"IncludeItemTypes=Folder,Movie,Series&Limit=1&StartIndex=1", "Movie", ids.RatingKeyGUID(thumb.Key), 2},
		{"IncludeItemTypes=Folder", "Folder", rowID, 1},
		{"IncludeItemTypes=Folder,Movie,Series&ExcludeItemTypes=Folder", "Movie", ids.RatingKeyGUID(thumb.Key), 1},
		{"IncludeItemTypes=Folder,Movie,Series&SearchTerm=des", "Movie", ids.RatingKeyGUID(thumb.Key), 1},
	} {
		page := object(get("/Items?ParentId="+themeID+"&"+test.query, "", http.StatusOK))
		items, _ := page["Items"].([]any)
		if len(items) != 1 || page["TotalRecordCount"] != float64(test.total) {
			t.Fatalf("%s : page inattendue %+v", test.query, page)
		}
		item := items[0].(map[string]any)
		if item["Type"] != test.kind || item["Id"] != test.id {
			t.Fatalf("%s : titre ou rangée inattendu %+v", test.query, item)
		}
	}
	detail := get("/Items/"+rowID, "", http.StatusOK)
	if row := object(detail); row["Id"] != rowID || row["Name"] != e.row.Name {
		t.Fatalf("détail inattendu : %+v", row)
	}
	if movie := entry(get("/Items?ParentId="+rowID, "", http.StatusOK), "Items"); movie["Id"] != ids.RatingKeyGUID(thumb.Key) || movie["ParentId"] != rowID {
		t.Fatalf("titre inattendu : %+v", movie)
	}
	if hints := entry(get("/Search/Hints?ParentId="+themeID+"&SearchTerm=pilote&IncludeItemTypes=Episode", "", http.StatusOK), "SearchHints"); hints["Id"] != ids.RatingKeyGUID(about.Key) {
		t.Fatalf("recherche inattendue : %+v", hints)
	}
	if reply := object(get("/Items?ParentId="+rowID+"&Ids="+ids.RatingKeyGUID(core.MovieKey(100)), "", http.StatusOK)); reply["TotalRecordCount"] != float64(0) {
		t.Fatalf("titre étranger à la rangée : %+v", reply)
	}
	get("/Items?ParentId="+unknownID+"&IncludeItemTypes=BoxSet", "", http.StatusNotFound)
	get("/Search/Hints?ParentId="+unknownID+"&SearchTerm=pilote", "", http.StatusNotFound)
	get("/Search/Hints?ParentId="+themeID+"&ParentId="+unknownID+"&SearchTerm=pilote", "", http.StatusBadRequest)
	get("/Items/"+rowID, detail.Header().Get("ETag"), http.StatusNotModified)
	e.allowed = false
	frontend.BumpPSEThemesGeneration()
	get("/Items/"+rowID, detail.Header().Get("ETag"), http.StatusNotFound)
}

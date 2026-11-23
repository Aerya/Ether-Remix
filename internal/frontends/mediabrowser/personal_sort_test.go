package mediabrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// A personal row opened as a folder keeps its own order for the default
// SortName-ascending open, and follows any other choice from the sort menu.
func TestPersonalRowFolderHonoursSortMenu(t *testing.T) {
	rows := func() []frontend.Title {
		return []frontend.Title{
			{Title: "Beta", Rating: 7.1, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Title: "Alpha", Rating: 9.4, Released: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Title: "Gamma", Rating: 8.2, Released: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		}
	}
	names := func(titles []frontend.Title) string {
		out := ""
		for _, title := range titles {
			out += title.Title[:1]
		}
		return out
	}
	for _, tc := range []struct {
		query string
		asked bool
		want  string
	}{
		{"SortBy=SortName&SortOrder=Ascending", false, "BAG"},
		{"", false, "BAG"},
		{"sortBy=CommunityRating&sortBy=SortName&sortOrder=Descending", true, "AGB"},
		{"SortBy=CommunityRating&SortOrder=Ascending", true, "BGA"},
		{"SortBy=PremiereDate&SortOrder=Descending", true, "GBA"},
		{"SortBy=SortName&SortOrder=Descending", true, "GBA"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/Items?"+tc.query, nil)
		q := itemQuery(r)
		titles := rows()
		asked := personalSortAsked(r, q)
		if asked {
			sortPersonalTitles(titles, q)
		}
		if asked != tc.asked || names(titles) != tc.want {
			t.Fatalf("%q: asked %v order %s, want %v %s", tc.query, asked, names(titles), tc.asked, tc.want)
		}
	}
}

func TestRandomBrowseBoundedAndUncached(t *testing.T) {
	if cacheableBrowse(httptest.NewRequest(http.MethodGet, "/Items?SortBy=Random,SortName&Limit=20", nil)) {
		t.Fatal("SortBy=Random must not be frozen in browseCache")
	}
	if !cacheableBrowse(httptest.NewRequest(http.MethodGet, "/Items?SortBy=SortName&Limit=20", nil)) {
		t.Fatal("ordinary /Items browse should remain cacheable")
	}

	var maxOffset int
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{{ID: "1", LibraryID: 1, Name: "Movies", Type: "movie", Native: "movies"}}, nil
		},
		LibraryItemsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, q frontend.ItemQuery) (frontend.Page, error) {
			if q.Offset > maxOffset {
				maxOffset = q.Offset
			}
			return frontend.Page{
				Total: 100000,
				Items: []frontend.Title{{Key: frontend.MovieKey(1), Title: "A", Type: "movie"}},
			}, nil
		},
	}
	inst := mountJellyfinViewer(t, exp, frontend.Viewer{ID: 1, IsOwner: true})
	viewGUID, err := ids.GUID(ids.View, 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/Items?ParentId="+viewGUID+"&SortBy=Random&Limit=20", nil)
		req.Header.Set("X-Emby-Token", "grant")
		inst.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if maxOffset > maxRandomBrowseWindow-20 {
		t.Fatalf("random browse offset %d exceeded bound %d", maxOffset, maxRandomBrowseWindow-20)
	}
}

func TestPluginViewSearchHintsStayScoped(t *testing.T) {
	globalSearched := false
	pluginQueried := ""
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{
				{ID: "1", LibraryID: 1, Name: "Movies", Type: "movie", Native: "movies"},
				{ID: "10", LibraryID: 10, Name: "Music", Type: "movie", KindKey: "album", PluginID: "field", Native: "music"},
			}, nil
		},
		LibraryItemsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, libID string, q frontend.ItemQuery) (frontend.Page, error) {
			if libID == "10" {
				pluginQueried = q.Filter.Query
			}
			return frontend.Page{Total: 0}, nil
		},
		SearchFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.SearchPage, error) {
			globalSearched = true
			return frontend.SearchPage{}, nil
		},
	}
	inst := mountJellyfinViewer(t, exp, frontend.Viewer{ID: 1, IsOwner: true})
	musicGUID, err := ids.GUID(ids.View, 10)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/Search/Hints?parentId="+musicGUID+"&searchTerm=bach", nil)
	req.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if globalSearched {
		t.Fatal("plugin library /Search/Hints fell through to global Search")
	}
	if pluginQueried != "bach" {
		t.Fatalf("plugin LibraryItems query = %q, want bach", pluginQueried)
	}
}

package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func TestRecentlyAddedExplicitScopes(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	past := core.Title{Key: core.MovieKey(9101), Type: core.TypeMovie, Title: "Already released", Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	future := core.Title{Key: core.MovieKey(9102), Type: core.TypeMovie, Title: "Not yet released", Released: fixedNow.Add(48 * time.Hour)}
	f.catalog.mu.Lock()
	f.catalog.titles[past.Key] = past
	f.catalog.titles[future.Key] = future
	f.catalog.mu.Unlock()
	// Every filter the parity corpus sends. An unknown or combined type, and
	// contentDirectoryID or pinnedContentDirectoryID, stay unfiltered. A real
	// type still selects that library. sectionID selects one library.
	for _, tc := range []struct {
		query       string
		movie, show bool
	}{
		{"", true, true},
		{"?type=1", true, false},
		{"?type=2", false, true},
		{"?type=1&sectionID=1", true, false},
		{"?type=2&sectionID=1", false, false},
		{"?type=1&contentDirectoryID=1,2", true, false},
		{"?type=1&contentDirectoryID=2", true, false},
		{"?type=2&contentDirectoryID=1", false, true},
		{"?type=1&pinnedContentDirectoryID=1", true, false},
		{"?type=1&pinnedContentDirectoryID=2", true, false},
		{"?type=1&contentDirectoryID=1&pinnedContentDirectoryID=2", true, false},
		{"?contentDirectoryID=1", true, true},
		{"?contentDirectoryID=2", true, true},
		{"?pinnedContentDirectoryID=1", true, true},
		{"?pinnedContentDirectoryID=2", true, true},
		{"?sectionID=1", true, false},
		{"?type=3", false, false},
		{"?type=4", false, false},
		{"?type=15", true, true},
		{"?type=18", false, false},
		{"?type=999", true, true},
		{"?type=1,4", true, true},
		{"?type=1%2C4", true, true},
		{"?type=2,4&contentDirectoryID=2", true, true},
		{"?type=2%2C4&contentDirectoryID=2", true, true},
		{"?type=2&pinnedContentDirectoryID=1", false, true},
		{"?type=1&contentDirectoryID=1", true, false},
		{"?type=2&contentDirectoryID=2", false, true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rec := f.request(t, http.MethodGet, "/hubs/home/recentlyAdded"+tc.query, map[string]string{"Accept": "application/json"})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var body struct{ MediaContainer model.MediaContainer }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			var movie, show, pastSeen, futureSeen bool
			for _, item := range body.MediaContainer.Metadata {
				switch item.RatingKey {
				case past.Key.String():
					pastSeen = true
				case future.Key.String():
					futureSeen = true
				}
				switch item.Type {
				case "movie":
					movie = true
				case "show":
					show = true
				default:
					t.Fatalf("unexpected item type %q", item.Type)
				}
			}
			if movie != tc.movie || show != tc.show {
				t.Fatalf("movie=%v show=%v; want %v %v", movie, show, tc.movie, tc.show)
			}
			if futureSeen {
				t.Fatal("a release after the frozen clock was listed")
			}
			if tc.movie && !pastSeen {
				t.Fatal("2020-01-01 release was treated as future")
			}
		})
	}
	// The similarly named library endpoint retains its existing behavior.
	rec := f.get(t, "/library/recentlyAdded?type=15", "Accept", "application/json")
	var body struct{ MediaContainer model.MediaContainer }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.MediaContainer.Metadata) == 0 {
		t.Fatalf("library endpoint changed: %s (%v)", rec.Body.String(), err)
	}
}

type scopedRecentExperience struct {
	frontend.Experience
	item core.Title
}

func (e *scopedRecentExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "1", LibraryID: 1, Name: "Movies", Type: core.TypeMovie, Native: plexBaseMovie},
		{ID: "9", LibraryID: 9, Name: "Curated Movies", Type: core.TypeMovie, Virtual: true, Native: plexBaseMovie},
	}, nil
}

func (e *scopedRecentExperience) LibraryItems(_ context.Context, _ *frontend.Viewer, _ frontend.Device, viewID string, q frontend.ItemQuery) (core.Page, error) {
	if viewID != "9" {
		return core.Page{}, nil
	}
	if q.Sort != frontend.SortAdded || !q.Desc || !q.Filter.KnownAddedDate {
		return core.Page{}, nil
	}
	return core.Page{Items: []core.Title{e.item}, Total: 1, TotalExact: true}, nil
}

func TestRecentlyAddedScopedCustomSection(t *testing.T) {
	scopedMovie := core.Title{Key: core.MovieKey(9201), Type: core.TypeMovie, Title: "In Collection Only", Released: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)}
	f := newFixture(func(d *testServerDeps) {
		directExp := experience.NewDirectService(experience.ServiceDeps{
			Catalog:   d.Catalog,
			State:     d.State,
			Playlists: d.Playlists,
		})
		d.Experience = &scopedRecentExperience{Experience: directExp, item: scopedMovie}
	})
	defer f.srv.Close()
	f.catalog.mu.Lock()
	f.catalog.titles[scopedMovie.Key] = scopedMovie
	f.catalog.mu.Unlock()

	for _, path := range []string{"/library/recentlyAdded?sectionID=9", "/hubs/home/recentlyAdded?sectionID=9"} {
		rec := f.get(t, path, "Accept", "application/json")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status %d: %s", path, rec.Code, rec.Body.String())
		}
		var body struct{ MediaContainer model.MediaContainer }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		if len(body.MediaContainer.Metadata) != 1 || body.MediaContainer.Metadata[0].RatingKey != scopedMovie.Key.String() {
			t.Fatalf("%s metadata = %+v, want only %s", path, body.MediaContainer.Metadata, scopedMovie.Key)
		}
	}
}

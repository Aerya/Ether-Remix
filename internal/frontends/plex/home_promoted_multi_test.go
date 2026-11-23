package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type multiLibraryViewsExperience struct {
	frontend.Experience
}

func (multiLibraryViewsExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "1", LibraryID: core.LibraryMovies, Name: "Movies", Type: core.TypeMovie, Native: plexBaseMovie},
		{ID: "2", LibraryID: core.LibraryShows, Name: "TV Shows", Type: core.TypeShow, Native: plexBaseShow},
		{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: core.TypeMovie, Native: plexBaseMovie, Subset: true},
		{ID: "4", LibraryID: 4, Name: "Kids Shows", Type: core.TypeShow, Native: plexBaseShow, Subset: true},
	}, nil
}

type multiLibraryRecommender struct {
	bySection map[int][]core.Hub
	budget    int
}

func (r *multiLibraryRecommender) HomeHubs(context.Context, *core.User, int) ([]core.Hub, error) {
	return nil, nil
}

func (r *multiLibraryRecommender) SectionHubs(context.Context, *core.User, core.MediaType, int) ([]core.Hub, error) {
	return nil, nil
}

func (r *multiLibraryRecommender) Related(context.Context, *core.User, core.RatingKey, int) ([]core.Hub, error) {
	return nil, nil
}

func (r *multiLibraryRecommender) HomeHubsFor(context.Context, *core.User, core.HubContext, int) ([]core.Hub, error) {
	// Remix projects the configured Home rows into built-in libraries.
	return append(append([]core.Hub{}, r.bySection[1]...), r.bySection[2]...), nil
}

func (r *multiLibraryRecommender) SectionHubsFor(_ context.Context, _ *core.User, _ core.MediaType, hc core.HubContext, _ int) ([]core.Hub, error) {
	return r.bySection[hc.SectionID], nil
}

func (r *multiLibraryRecommender) HomeRowBudget(context.Context, *core.User, core.HubContext) int {
	return r.budget
}

// Regression for #760: multi-library /hubs/promoted?contentDirectoryID=1,3,4,2
// distinguishes non-first libraries in row titles, drops rule-subset rows that
// repeat their parent library (even when the subset precedes its parent in the
// query, as 4 precedes 2), keeps genuinely distinct subset rows, bounds total
// hubs to the Home row budget, respects count as items per hub, and pages hubs.
func TestMultiLibraryPromotedHubsDedupesSubsetsAndCapsTotal(t *testing.T) {
	const kidToken = "kid-only-token"
	f := newFixture(func(d *testServerDeps) {
		setDepsAuth(d, &fakeAuth{users: map[string]core.User{
			ownerToken: ownerUser,
			kidToken:   {ID: 77, Username: "kid", AllowedLibraries: []core.LibraryID{3}},
		}})
	})
	defer f.srv.Close()

	makeMovie := func(id int64, name string, addedAt int64) core.Title {
		tt := core.Title{Key: core.MovieKey(id), Type: core.TypeMovie, Title: name, Year: 2024, AddedAt: fixedNow.Add(time.Duration(addedAt) * time.Hour)}
		f.catalog.titles[tt.Key] = tt
		return tt
	}
	makeShow := func(id int64, name string, addedAt int64) core.Title {
		tt := core.Title{Key: core.ShowKey(id), Type: core.TypeShow, Title: name, Year: 2024, AddedAt: fixedNow.Add(time.Duration(addedAt) * time.Hour)}
		f.catalog.titles[tt.Key] = tt
		return tt
	}

	var adultMovies, kidsMovies, adultShows, kidsShows []core.Title
	for i := int64(1); i <= 8; i++ {
		adultMovies = append(adultMovies, makeMovie(9000+i, fmt.Sprintf("Adult Movie %d", i), 100+i))
		kidsMovies = append(kidsMovies, makeMovie(9100+i, fmt.Sprintf("Kids Movie %d", i), i))
		adultShows = append(adultShows, makeShow(9200+i, fmt.Sprintf("Adult Show %d", i), 100+i))
		kidsShows = append(kidsShows, makeShow(9300+i, fmt.Sprintf("Kids Show %d", i), i))
	}

	// Build 12 rows for Movies (1) and 12 rows for TV Shows (2), plus overlapping
	// and distinct rows for Kids Movies (3) and Kids Shows (4).
	movieRows := []core.Hub{
		{ID: "movie.top10", Title: "Top 10 for You", Type: core.TypeMovie, Promoted: true, Items: adultMovies},
		{ID: "movie.hiddengems", Title: "Hidden Gems for You", Type: core.TypeMovie, Promoted: true, Items: adultMovies[:6]},
	}
	for i := 1; i <= 10; i++ {
		movieRows = append(movieRows, core.Hub{
			ID: fmt.Sprintf("movie.extra%d", i), Title: fmt.Sprintf("Curated Movies %d", i),
			Type: core.TypeMovie, Promoted: true, Items: adultMovies[:4],
		})
	}
	showRows := []core.Hub{
		{ID: "tv.top10", Title: "Top 10 for You", Type: core.TypeShow, Promoted: true, Items: adultShows},
		{ID: "tv.hiddengems", Title: "Hidden Gems for You", Type: core.TypeShow, Promoted: true, Items: adultShows[:6]},
	}
	for i := 1; i <= 10; i++ {
		showRows = append(showRows, core.Hub{
			ID: fmt.Sprintf("tv.extra%d", i), Title: fmt.Sprintf("Curated Shows %d", i),
			Type: core.TypeShow, Promoted: true, Items: adultShows[:4],
		})
	}

	// Kids Movies (3): Top 10 overlaps 3/4 with Movies (must drop), Hidden Gems is
	// 100% distinct kids movies (must keep and label " · Kids Movies").
	kidsMovieRows := []core.Hub{
		{ID: "movie.top10", Title: "Top 10 for You", Type: core.TypeMovie, Promoted: true, Items: []core.Title{adultMovies[0], adultMovies[1], adultMovies[2], kidsMovies[0]}},
		{ID: "movie.hiddengems", Title: "Hidden Gems for You", Type: core.TypeMovie, Promoted: true, Items: kidsMovies[:6]},
	}
	// Kids Shows (4) precedes its parent TV Shows (2) in contentDirectoryID=1,3,4,2:
	// Hidden Gems repeats TV Shows (must drop), Top 10 is distinct kids shows (must keep).
	kidsShowRows := []core.Hub{
		{ID: "tv.hiddengems", Title: "Hidden Gems for You", Type: core.TypeShow, Promoted: true, Items: adultShows[:4]},
		{ID: "tv.top10", Title: "Top 10 for You", Type: core.TypeShow, Promoted: true, Items: kidsShows[:6]},
	}

	f.srv.setRecommender(&multiLibraryRecommender{
		budget: 14,
		bySection: map[int][]core.Hub{
			1: movieRows,
			2: showRows,
			3: kidsMovieRows,
			4: kidsShowRows,
		},
	})
	f.srv.bindHost(frontend.Host{Experience: multiLibraryViewsExperience{Experience: f.srv.exp()}})

	type hubWire struct {
		HubIdentifier string `json:"hubIdentifier"`
		Title         string `json:"title"`
		Size          int    `json:"size"`
		Metadata      []struct {
			LibrarySectionID int `json:"librarySectionID"`
		} `json:"Metadata"`
	}
	decode := func(path string, hdrs ...string) (int, *int, *int, []hubWire) {
		t.Helper()
		rec := f.get(t, path, append(hdrs, jsonAccept...)...)
		if rec.Code != 200 {
			t.Fatalf("%s -> %d %s", path, rec.Code, rec.Body.String())
		}
		var out struct {
			MediaContainer struct {
				Size      int       `json:"size"`
				TotalSize *int      `json:"totalSize"`
				Offset    *int      `json:"offset"`
				Hub       []hubWire `json:"Hub"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.MediaContainer.Size, out.MediaContainer.TotalSize, out.MediaContainer.Offset, out.MediaContainer.Hub
	}

	_, _, _, hubs := decode("/hubs/promoted?contentDirectoryID=1,3,4,2&count=4&excludeContinueWatching=1")
	if len(hubs) > 14 {
		t.Fatalf("total hubs = %d, want <= 14 (budget): %+v", len(hubs), hubs)
	}

	byID := map[string]hubWire{}
	titles := map[string]string{}
	for _, h := range hubs {
		if len(h.Metadata) > 4 || h.Size > 4 {
			t.Fatalf("hub %s returned %d items, want <= count=4", h.HubIdentifier, len(h.Metadata))
		}
		if prev, dup := titles[h.Title]; dup {
			t.Fatalf("duplicate row title %q on %s and %s", h.Title, prev, h.HubIdentifier)
		}
		titles[h.Title] = h.HubIdentifier
		byID[h.HubIdentifier] = h
	}

	// Overlapping subset rows must be suppressed (including 4 before its parent 2).
	if _, ok := byID["movie.top10.3"]; ok {
		t.Fatalf("overlapping Kids Movies Top 10 should have been suppressed: %+v", byID["movie.top10.3"])
	}
	if _, ok := byID["tv.hiddengems.4"]; ok {
		t.Fatalf("overlapping Kids Shows Hidden Gems should have been suppressed even when 4 precedes 2: %+v", byID["tv.hiddengems.4"])
	}

	// Distinct subset rows must be kept and carry their library name.
	if h, ok := byID["movie.hiddengems.3"]; !ok || h.Title != "Hidden Gems for You · Kids Movies" {
		t.Fatalf("distinct Kids Movies row = %+v, want title %q", h, "Hidden Gems for You · Kids Movies")
	}
	if h, ok := byID["tv.top10.4"]; !ok || h.Title != "Top 10 for You · Kids Shows" {
		t.Fatalf("distinct Kids Shows row = %+v, want title %q", h, "Top 10 for You · Kids Shows")
	}

	// First library (Movies) keeps clean titles; TV Shows (non-first) qualifies generic
	// titles without double-suffixing "Recently Added in TV Shows".
	if h, ok := byID["movie.top10"]; !ok || h.Title != "Top 10 for You" {
		t.Fatalf("first library Top 10 = %+v, want unqualified title", h)
	}
	if h, ok := byID["tv.top10"]; !ok || h.Title != "Top 10 for You · TV Shows" {
		t.Fatalf("TV Shows Top 10 = %+v, want %q", h, "Top 10 for You · TV Shows")
	}
	if h, ok := byID["tv.recentlyadded.2"]; ok && strings.Contains(h.Title, "· TV Shows") {
		t.Fatalf("Recently Added in TV Shows should not double-suffix: %q", h.Title)
	}

	// Container paging slices hubs and reports totalSize/offset matching PMS.
	size, totalSize, offset, page := decode("/hubs/promoted?contentDirectoryID=1,3,4,2&count=4&excludeContinueWatching=1", "X-Plex-Container-Start", "2", "X-Plex-Container-Size", "5")
	if size != 5 || len(page) != 5 || totalSize == nil || *totalSize != len(hubs) || offset == nil || *offset != 2 {
		t.Fatalf("paged hubs size=%d len=%d total=%v offset=%v want 5/%d/2", size, len(page), totalSize, offset, len(hubs))
	}

	// Per-directory fan-out for a subset library whose parent is pinned also
	// suppresses overlapping rows and qualifies remaining rows.
	_, _, _, perDirKids := decode("/hubs/promoted?contentDirectoryID=3&pinnedContentDirectoryID=1,3,4,2&count=4&excludeContinueWatching=1")
	if len(perDirKids) != 1 || perDirKids[0].HubIdentifier != "movie.hiddengems.3" || perDirKids[0].Title != "Hidden Gems for You · Kids Movies" {
		t.Fatalf("per-directory Kids Movies hubs = %+v, want only distinct Hidden Gems · Kids Movies", perDirKids)
	}

	// Restricted viewer allowed only Kids Movies (3) has no parent present, so its
	// rows are neither suppressed nor suffixed.
	_, _, _, kidHubs := decode("/hubs/promoted?contentDirectoryID=1,3,4,2&count=4&excludeContinueWatching=1", "X-Plex-Token", kidToken)
	if len(kidHubs) != 2 {
		t.Fatalf("kid-only viewer got %d hubs, want 2: %+v", len(kidHubs), kidHubs)
	}
	if kidHubs[0].Title != "Top 10 for You" || kidHubs[1].Title != "Hidden Gems for You" {
		t.Fatalf("kid-only viewer titles should stay unqualified: %+v", kidHubs)
	}
}

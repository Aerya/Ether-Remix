package personalize

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type studioCollectionSource struct {
	rows  []core.CollectionRow
	specs []core.CollectionSpec
}

func (s *studioCollectionSource) HomeRowsFor(context.Context, *core.User, core.HubContext) ([]core.CollectionRow, error) {
	out := make([]core.CollectionRow, 0, len(s.rows))
	for _, r := range s.rows {
		if r.HomeRow || r.SectionRow {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *studioCollectionSource) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	return append([]core.CollectionSpec(nil), s.specs...), nil
}

func (s *studioCollectionSource) LayoutRow(_ context.Context, _ *core.User, _ core.HubContext, id string) (*core.CollectionRow, error) {
	for _, r := range s.rows {
		if r.CollectionID == id {
			cp := r
			cp.HomeRow, cp.SectionRow = true, true
			return &cp, nil
		}
	}
	return nil, core.ErrNotFound
}

// A collection whose Studio targets only enable Home (HomeRow=true,
// SectionRow=false) or only enable Library Recommended (HomeRow=false,
// SectionRow=true) must still appear when explicitly placed in the other
// page's layout, rather than being silently dropped and misreported as empty.
func TestLayoutPlacedCollectionSurvivesOppositeTargetFlag(t *testing.T) {
	src := &studioCollectionSource{}
	h := newHarness(t, func(o *Options) {
		cat := o.Catalog.(*fakeCatalog)
		var movies []core.Title
		for i := 0; i < 6; i++ {
			movies = append(movies, cat.titles[movieKey(0, i)])
		}
		src.rows = []core.CollectionRow{
			{
				CollectionID: "home-only",
				HomeRow:      true,
				SectionRow:   false,
				Hub:          core.Hub{ID: "collection.home-only", Title: "Latest Movies", Type: core.TypeMovie, Promoted: true, Items: movies},
			},
			{
				CollectionID: "section-only",
				HomeRow:      false,
				SectionRow:   true,
				Hub:          core.Hub{ID: "collection.section-only", Title: "Library Picks", Type: core.TypeMovie, Items: movies},
			},
		}
		src.specs = []core.CollectionSpec{
			{ID: "home-only", Name: "Latest Movies", MediaType: "movie", Enabled: true, Targets: core.CollectionTargets{HomeRow: true, Users: core.AllUsers()}},
			{ID: "section-only", Name: "Library Picks", MediaType: "movie", Enabled: true, Targets: core.CollectionTargets{SectionRow: true, Users: core.AllUsers()}},
		}
		o.Collections = src
		o.OwnerRowsWait = time.Second
	})
	ctx := context.Background()
	u := core.User{ID: 1, IsOwner: true}
	h.rebuild(u)
	hc := hubContext(testNow, core.DeviceTV)

	libLayout := core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "library:1",
			Rows:    []core.RowPolicy{{Row: "collection:home-only", Status: "always", Slot: 1}},
		}},
	}
	libPrev, err := h.e.PreviewLayout(ctx, &u, hc, "library:1", 12, libLayout)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByKind(libPrev.Rows, "collection:home-only"); r == nil || r.Hidden || len(r.Items) == 0 {
		t.Fatalf("home-only collection placed on library:1 dropped or hidden: %+v", r)
	}

	homeLayout := core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "home",
			Rows:    []core.RowPolicy{{Row: "collection:section-only", Status: "always", Slot: 2}},
		}},
	}
	homePrev, err := h.e.PreviewLayout(ctx, &u, hc, "home", 12, homeLayout)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByKind(homePrev.Rows, "collection:section-only"); r == nil || r.Hidden || len(r.Items) == 0 {
		t.Fatalf("section-only collection placed on home dropped or hidden: %+v", r)
	}
}

// Scoped Home requests (Plex iOS / Apple TV contentDirectoryID=1 or 2) filter
// strictly to the requested section's media type while applying the "home"
// layout, HomeRow target flag, and skipping DedupeHome.
func TestHomeScopedSectionCandidatesUseHomeTargetAndMediaType(t *testing.T) {
	src := &studioCollectionSource{}
	h := newHarness(t, func(o *Options) {
		cat := o.Catalog.(*fakeCatalog)
		var movies, shows []core.Title
		for i := 0; i < 6; i++ {
			movies = append(movies, cat.titles[movieKey(0, i)])
			shows = append(shows, cat.titles[showKey(1, i)])
		}
		src.rows = []core.CollectionRow{
			{
				CollectionID: "latest-movies",
				HomeRow:      true,
				SectionRow:   false,
				Hub:          core.Hub{ID: "collection.latest-movies", Title: "Latest Movies", Type: core.TypeMovie, Promoted: true, Items: movies},
			},
			{
				CollectionID: "latest-series",
				HomeRow:      true,
				SectionRow:   false,
				Hub:          core.Hub{ID: "collection.latest-series", Title: "Latest Series", Type: core.TypeShow, Promoted: true, Items: shows},
			},
		}
		o.Collections = src
		o.OwnerRowsWait = time.Second
	})
	ctx := context.Background()
	u := core.User{ID: 1, IsOwner: true}
	h.rebuild(u)

	// Normal library Recommended (HomeScoped=false) must NOT include HomeRow-only
	// collections that are not placed on library:1.
	libHC := hubContext(testNow, core.DeviceTV)
	libHC.SectionID = 1
	libHubs, err := h.e.SectionCandidatesFor(ctx, &u, core.TypeMovie, libHC, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, hub := range libHubs {
		if hub.ID == "collection.latest-movies" || hub.ID == "movie.collection.latest-movies" {
			t.Fatalf("HomeRow-only collection leaked into non-scoped library Recommended: %+v", hub.ID)
		}
	}

	// HomeScoped=true on Movies (section 1) returns Latest Movies and never Latest Series.
	movieHC := libHC
	movieHC.HomeScoped = true
	movieHubs, err := h.e.SectionCandidatesFor(ctx, &u, core.TypeMovie, movieHC, 12)
	if err != nil {
		t.Fatal(err)
	}
	foundMovie := false
	for _, hub := range movieHubs {
		if hub.ID == "collection.latest-movies" {
			foundMovie = true
		}
		if hub.ID == "collection.latest-series" || hub.ID == "tv.collection.latest-series" || hub.ID == "movie.collection.latest-series" {
			t.Fatalf("show collection leaked into scoped movie Home: %+v", hub.ID)
		}
	}
	if !foundMovie {
		t.Fatalf("Latest Movies missing from HomeScoped Movies candidates: %+v", hubIDsOf(movieHubs))
	}

	// HomeScoped=true on TV Shows (section 2) returns Latest Series and never Latest Movies.
	showHC := hubContext(testNow, core.DeviceTV)
	showHC.SectionID = 2
	showHC.HomeScoped = true
	showHubs, err := h.e.SectionCandidatesFor(ctx, &u, core.TypeShow, showHC, 12)
	if err != nil {
		t.Fatal(err)
	}
	foundShow := false
	for _, hub := range showHubs {
		if hub.ID == "collection.latest-series" {
			foundShow = true
		}
		if hub.ID == "collection.latest-movies" || hub.ID == "movie.collection.latest-movies" || hub.ID == "tv.collection.latest-movies" {
			t.Fatalf("movie collection leaked into scoped show Home: %+v", hub.ID)
		}
	}
	if !foundShow {
		t.Fatalf("Latest Series missing from HomeScoped Shows candidates: %+v", hubIDsOf(showHubs))
	}
}

func TestInvalidateOwnerRowsBumpsLayoutVersion(t *testing.T) {
	h := newHarness(t, nil)
	before := h.e.LayoutVersion()
	h.e.InvalidateOwnerRows()
	if after := h.e.LayoutVersion(); after <= before {
		t.Fatalf("LayoutVersion did not advance on InvalidateOwnerRows: before=%d after=%d", before, after)
	}
}

// Absent "always" collections in PreviewHome must explain the real reason
// (viewer restrictions, custom library membership, unbuilt remote source,
// unshared target) rather than falling through to collection_empty.
func TestAbsentCollectionReasonsAreTruthful(t *testing.T) {
	src := &studioCollectionSource{}
	var rRated []core.Title
	h := newHarness(t, func(o *Options) {
		cat := o.Catalog.(*fakeCatalog)
		for i := 0; i < 4; i++ {
			title := cat.titles[movieKey(0, i)]
			title.ContentRating = "R"
			rRated = append(rRated, title)
		}
		src.rows = []core.CollectionRow{
			{
				CollectionID: "r-only",
				HomeRow:      true,
				SectionRow:   true,
				Hub:          core.Hub{ID: "collection.r-only", Title: "R Only", Type: core.TypeMovie, Items: rRated},
			},
		}
		src.specs = []core.CollectionSpec{
			{ID: "r-only", Name: "R Only", MediaType: "movie", Enabled: true, ItemCount: len(rRated), Targets: core.CollectionTargets{HomeRow: true, Users: core.AllUsers()}},
			{ID: "remote-building", Name: "Latest Movies", MediaType: "movie", Enabled: true, Sources: []core.CollectionSource{{Kind: "tmdbList", List: "trending_week"}}, Targets: core.CollectionTargets{HomeRow: true, Users: core.AllUsers()}},
			{ID: "unshared-empty", Name: "Nobody", MediaType: "movie", Enabled: true, Targets: core.CollectionTargets{HomeRow: true, Users: core.UserTargets{IDs: []int64{}}}},
			{ID: "private-other", Name: "Private", MediaType: "movie", Enabled: true, OwnerUserID: 99, Targets: core.CollectionTargets{HomeRow: true, Users: core.AllUsers()}},
		}
		o.Collections = src
		o.OwnerRowsWait = time.Second
		o.LibraryMembers = func(_ context.Context, id core.LibraryID, _ []core.RatingKey) (map[core.RatingKey]bool, int64, bool, error) {
			if id == 3 {
				return map[core.RatingKey]bool{}, 1, true, nil
			}
			return nil, 0, false, nil
		}
		o.LibrarySections = func(context.Context) map[int]core.MediaType {
			return map[int]core.MediaType{1: core.TypeMovie, 2: core.TypeShow, 3: core.TypeMovie}
		}
	})
	ctx := context.Background()
	kid := core.User{ID: 2, MaxContentRating: "PG"}
	h.rebuild(kid)
	hc := hubContext(testNow, core.DeviceTV)

	homeLayout := core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "home",
			Rows: []core.RowPolicy{
				{Row: "collection:r-only", Status: "always", Slot: 2},
				{Row: "collection:remote-building", Status: "always", Slot: 3},
				{Row: "collection:unshared-empty", Status: "always", Slot: 4},
				{Row: "collection:private-other", Status: "always", Slot: 5},
			},
		}},
	}
	prev, err := h.e.PreviewLayout(ctx, &kid, hc, "home", 12, homeLayout)
	if err != nil {
		t.Fatal(err)
	}
	wantCodes := map[string]string{
		"collection:r-only":          "filtered_by_restrictions",
		"collection:remote-building": "collection_building",
		"collection:unshared-empty":  "collection_not_shared",
		"collection:private-other":   "collection_not_shared",
	}
	for kind, want := range wantCodes {
		r := rowByKind(prev.Rows, kind)
		if r == nil || !r.Hidden || r.HiddenCode != want {
			t.Fatalf("%s: got %+v, want hidden with code %q", kind, r, want)
		}
	}

	// On custom library:3 where LibraryMembers contains none of r-only's titles,
	// even the owner sees no_matching_titles rather than collection_empty.
	owner := core.User{ID: 1, IsOwner: true}
	h.rebuild(owner)
	libLayout := core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "library:3",
			Rows:    []core.RowPolicy{{Row: "collection:r-only", Status: "always", Slot: 1}},
		}},
	}
	libPrev, err := h.e.PreviewLayout(ctx, &owner, hc, "library:3", 12, libLayout)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowByKind(libPrev.Rows, "collection:r-only"); r == nil || !r.Hidden || r.HiddenCode != "no_matching_titles" {
		t.Fatalf("library:3 absent collection: got %+v, want hidden with code no_matching_titles", r)
	}
}

package plex

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Two show libraries over one source, split by a rule (#788): the built-in
// Shows hides one country's titles and a filter library shows only them.
type splitShowsExperience struct {
	frontend.Experience
	split   core.RatingKey // the only show the filter library lists
	queries *atomic.Int32
}

func (splitShowsExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "1", LibraryID: core.LibraryMovies, Name: "Movies", Type: core.TypeMovie, Native: plexBaseMovie},
		{ID: "2", LibraryID: core.LibraryShows, Name: "Shows", Type: core.TypeShow, Native: plexBaseShow},
		{ID: "11", LibraryID: 11, Name: "Russian shows", Type: core.TypeShow, Native: plexBaseShow, Subset: true},
	}, nil
}

func (splitShowsExperience) LibraryBrowseFilter(_ context.Context, _ *core.User, _ string, f core.Filter) (core.Filter, bool, error) {
	return f, false, nil
}

func (e splitShowsExperience) LibraryMembers(_ context.Context, id core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]bool, bool, error) {
	e.queries.Add(1)
	members := map[core.RatingKey]bool{}
	for _, key := range keys {
		if (key == e.split) == (id == 11) {
			members[key] = true
		}
	}
	return members, true, nil
}

func TestSectionAttributionFollowsLibraryRules(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	split := breakingBad
	split.Key, split.Title = core.ShowKey(238463), "Kill Rita"
	splitEp := bbE1
	splitEp.Key, splitEp.ShowKey = core.EpisodeKey(238463, 1, 1), split.Key
	f.catalog.titles[split.Key], f.catalog.titles[splitEp.Key] = split, splitEp
	var queries atomic.Int32
	f.srv.bindHost(frontend.Host{Experience: splitShowsExperience{Experience: f.srv.exp(), split: split.Key, queries: &queries}})

	read := func(t *testing.T, path string) model.MediaContainer {
		t.Helper()
		rec := f.get(t, path, "Accept", "application/json")
		var body struct{ MediaContainer model.MediaContainer }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != 200 || err != nil {
			t.Fatalf("%s: HTTP %d, decode error %v", path, rec.Code, err)
		}
		return body.MediaContainer
	}

	// One title: the container and the item name the library that lists it.
	mc := read(t, "/library/metadata/"+split.Key.String())
	if len(mc.Metadata) != 1 || mc.Metadata[0].LibrarySectionID != 11 || mc.LibrarySectionID == nil || *mc.LibrarySectionID != 11 {
		t.Fatalf("hidden-from-Shows title attributed to %+v / container %v, want section 11", mc.Metadata, mc.LibrarySectionID)
	}
	if mc.Metadata[0].LibrarySectionTitle != "Russian shows" {
		t.Errorf("section title %q, want Russian shows", mc.Metadata[0].LibrarySectionTitle)
	}

	// A page: each title, an episode included, is attributed by its own
	// library, with a read per candidate library, not per title.
	queries.Store(0)
	keys := breakingBad.Key.String() + "," + split.Key.String() + "," + splitEp.Key.String() + "," + bbE1.Key.String()
	want := map[string]int{breakingBad.Key.String(): 2, split.Key.String(): 11, splitEp.Key.String(): 11, bbE1.Key.String(): 2}
	mc = read(t, "/library/metadata/"+keys)
	if len(mc.Metadata) != len(want) {
		t.Fatalf("got %d items, want %d", len(mc.Metadata), len(want))
	}
	for _, md := range mc.Metadata {
		if md.LibrarySectionID != want[md.RatingKey] {
			t.Errorf("%s attributed to section %d, want %d", md.RatingKey, md.LibrarySectionID, want[md.RatingKey])
		}
	}
	if n := queries.Load(); n > 2 {
		t.Errorf("membership read %d times for one page, want at most one per show library", n)
	}

	// The related hubs container names the same section.
	mc = read(t, "/library/metadata/"+split.Key.String()+"/related")
	if mc.LibrarySectionID == nil || *mc.LibrarySectionID != 11 {
		t.Errorf("related container section %v, want 11", mc.LibrarySectionID)
	}
}

package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSearchMergesPersonTitlesAndPluginPeopleHook(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Exact title match ("Brad Pitt" bio-doc) must stay ahead of person-credited titles,
	//    while Brad Pitt's movies/shows rank immediately after at [0.946..0.956).
	seed := []core.Title{
		{
			Key: core.MovieKey(1), IDs: core.IDs{TMDB: 1}, Type: core.TypeMovie,
			Title: "Brad Pitt", Year: 2021, Released: now.AddDate(-2, 0, 0), Popularity: 15, VoteCount: 100,
		},
		{
			Key: core.MovieKey(550), IDs: core.IDs{TMDB: 550}, Type: core.TypeMovie,
			Title: "Fight Club", Year: 1999, Released: now.AddDate(-10, 0, 0), Popularity: 120, VoteCount: 25000,
			Cast: []core.Person{{TMDB: 287, Name: "Brad Pitt", Role: "Tyler Durden"}},
		},
		{
			Key: core.MovieKey(16869), IDs: core.IDs{TMDB: 16869}, Type: core.TypeMovie,
			Title: "Inglourious Basterds", Year: 2009, Released: now.AddDate(-8, 0, 0), Popularity: 95, VoteCount: 20000,
			Cast: []core.Person{{TMDB: 287, Name: "Brad Pitt", Role: "Aldo Raine"}},
		},
	}
	for _, it := range seed {
		if err := cat.UpsertTitle(ctx, it, true); err != nil {
			t.Fatal(err)
		}
	}

	// Mock plugin PeopleTitles source (for W61 plugin items coordination).
	pluginKey := core.MovieKey(999001)
	pluginClip := core.Title{
		Key: pluginKey, Type: core.TypeShow, Title: "Brad Pitt Actors Roundtable",
		Year: 2024, Released: now.AddDate(-1, 0, 0), Popularity: 80, SearchScore: 0.950,
	}
	combined := CombinePeopleTitles(
		cat.PeopleTitles,
		func(_ context.Context, _ *core.User, q string, _ int) ([]core.Title, error) {
			if q == "Brad Pitt" || q == "brad pitt" {
				return []core.Title{pluginClip}, nil
			}
			return nil, nil
		},
	)

	vis := Visibility{
		SectionFor: func(mt core.MediaType) (int, bool) {
			if mt == core.TypeShow {
				return 7, true
			}
			return 1, true
		},
	}
	srch := SearchFor(frontend.SearchSources{
		Catalog:      cat.Search,
		PeopleTitles: combined,
		Details:      cat.SearchDetails,
	}, vis, func(u *core.User) bool { return u == nil || u.IsOwner })

	owner := &core.User{ID: 1, IsOwner: true}
	res, err := srch.Resolve(ctx, owner, "Brad Pitt", []core.MediaType{core.TypeMovie, core.TypeShow}, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 4 {
		t.Fatalf("got %d items (%+v), want 4", len(res.Items), res.Items)
	}
	if res.Items[0].Title != "Brad Pitt" {
		t.Fatalf("exact title match did not rank first: %+v", res.Items)
	}

	// Viewer restricted to library 1 sees catalogue titles, not library 7's plugin item.
	moviesOnly := &core.User{ID: 2, AllowedLibraries: []core.LibraryID{1}}
	restricted, err := srch.Keyword(ctx, moviesOnly, "Brad Pitt", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(restricted) != 3 {
		t.Fatalf("restricted viewer got %d items (%+v), want 3", len(restricted), restricted)
	}
	for _, it := range restricted {
		if it.Key == pluginKey {
			t.Fatalf("plugin item from disallowed library 7 leaked to restricted viewer: %+v", restricted)
		}
	}
}

func TestPersonSearchDoesNotTriggerSlowDescriptiveAIWait(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(550), IDs: core.IDs{TMDB: 550}, Type: core.TypeMovie,
		Title: "Fight Club", Year: 1999, Released: now.AddDate(-10, 0, 0), Popularity: 120, VoteCount: 25000,
		Cast: []core.Person{{TMDB: 287, Name: "Brad Pitt", Role: "Tyler Durden"}},
	}, true); err != nil {
		t.Fatal(err)
	}
	aiItem := core.Title{
		Key: core.MovieKey(808), IDs: core.IDs{TMDB: 808}, Type: core.TypeMovie,
		Title: "Knives Out", Year: 2019, Released: now.AddDate(-4, 0, 0), Popularity: 90,
	}
	if err := cat.UpsertTitle(ctx, aiItem, true); err != nil {
		t.Fatal(err)
	}

	describeCalls := 0
	descSource := &mockDescriptiveVibeSource{
		onDescribe: func(q string) core.DescriptiveResult {
			describeCalls++
			if q == "cozy british mysteries" {
				return core.DescriptiveResult{State: "ready", Items: []core.Title{aiItem}}
			}
			return core.DescriptiveResult{State: "ready"}
		},
	}
	srch := SearchFor(frontend.SearchSources{
		Catalog:      cat.Search,
		PeopleTitles: cat.PeopleTitles,
		Details:      cat.SearchDetails,
		Vibe:         descSource,
	}, Visibility{}, func(u *core.User) bool { return u != nil && u.IsOwner })
	srch.SettleDelay = -1

	types := []core.MediaType{core.TypeMovie, core.TypeShow}
	// 1. Two-word person name ("brad pitt") resolves via PeopleTitles (score >= 0.935):
	//    IsDescriptiveQuery MUST be false so Resolve returns immediately without starting DescribeSearch!
	personRes, err := srch.Resolve(ctx, &core.User{ID: 1, IsOwner: true}, "brad pitt", types, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if describeCalls != 0 {
		t.Fatalf("2-word person search triggered AI DescribeSearch (calls=%d)", describeCalls)
	}
	if len(personRes.Items) != 1 || personRes.Items[0].Title != "Fight Club" {
		t.Fatalf("unexpected person search results: %+v", personRes.Items)
	}

	// 2. Genuine descriptive query ("cozy british mysteries") still waits for AI and leads with AIItems.
	descRes, err := srch.Resolve(ctx, &core.User{ID: 1, IsOwner: true}, "cozy british mysteries", types, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	if describeCalls == 0 || descRes.AIItems != 1 || len(descRes.Items) == 0 || descRes.Items[0].Title != "Knives Out" {
		t.Fatalf("descriptive AI search broken: calls=%d res=%+v", describeCalls, descRes)
	}
}

func TestPartialPersonMatchesNeverPushDownTitleResults(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// 3 movies with "Star" in their title + 1 movie with no "Star" in title but an actor "Ringo Starr".
	for i, title := range []string{"Star Wars", "Star Trek", "A Star Is Born"} {
		id := int64(10 + i)
		if err := cat.UpsertTitle(ctx, core.Title{
			Key: core.MovieKey(id), IDs: core.IDs{TMDB: id}, Type: core.TypeMovie,
			Title: title, Year: 2018, Released: now.AddDate(-3, 0, 0), Popularity: 80 - float64(i)*5, VoteCount: 5000,
		}, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: core.MovieKey(99), IDs: core.IDs{TMDB: 99}, Type: core.TypeMovie,
		Title: "Caveman", Year: 1981, Released: now.AddDate(-10, 0, 0), Popularity: 200, VoteCount: 9000,
		Cast: []core.Person{{TMDB: 999, Name: "Ringo Starr", Role: "Atouk"}},
	}, true); err != nil {
		t.Fatal(err)
	}

	srch := SearchFor(frontend.SearchSources{
		Catalog:      cat.Search,
		PeopleTitles: cat.PeopleTitles,
		Details:      cat.SearchDetails,
	}, Visibility{}, func(*core.User) bool { return true })

	// With limit=10, all 3 "Star" title matches rank ahead of Ringo Starr's partial person hit ("Caveman").
	items, err := srch.Keyword(ctx, &core.User{ID: 1, IsOwner: true}, "star", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[3].Title != "Caveman" {
		t.Fatalf("partial person hit pushed ahead of title matches: %+v", items)
	}
}

type mockDescriptiveVibeSource struct {
	onDescribe func(string) core.DescriptiveResult
}

func (m *mockDescriptiveVibeSource) VibeCollection(context.Context, *core.User, string) (*core.Collection, []core.Title, bool, error) {
	return nil, nil, false, nil
}
func (m *mockDescriptiveVibeSource) DescriptiveSearchAvailable() bool        { return true }
func (m *mockDescriptiveVibeSource) DescribeCached(*core.User, string) bool  { return true }
func (m *mockDescriptiveVibeSource) CancelDescribeSearch(*core.User, string) {}
func (m *mockDescriptiveVibeSource) DescribeSearch(_ context.Context, _ *core.User, q string, _ bool) (core.DescriptiveResult, error) {
	return m.onDescribe(q), nil
}

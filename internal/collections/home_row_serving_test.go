package collections_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type toggleTMDB struct {
	base    collections.TMDB
	failing atomic.Bool
	calls   atomic.Int32
}

func (t *toggleTMDB) GetJSON(ctx context.Context, path string, q url.Values, out any) error {
	t.calls.Add(1)
	if t.failing.Load() {
		return errors.New("upstream unavailable")
	}
	return t.base.GetJSON(ctx, path, q, out)
}

func newToggleTMDB(t *testing.T, baseURL string) *toggleTMDB {
	t.Helper()
	client := tmdb.New("test-key", nil, tmdb.WithBaseURL(baseURL), tmdb.WithRateLimit(100000, 100000), tmdb.WithMaxRetries(0))
	t.Cleanup(client.Close)
	return &toggleTMDB{base: client}
}

// A built RefreshLive collection whose in-memory cache is cold (e.g. after a
// restart or invalidation) must fall back to its persisted collections_members
// rows when a live refresh fails, instead of disappearing from Home rows.
func TestLiveCollectionColdRestartServesPersistedMembersOnRefreshError(t *testing.T) {
	base := newEnv(t)
	sqlDB := base.db.(*sql.DB)
	tmdbProxy := newToggleTMDB(t, base.tmdb.srv.URL)
	svc, err := collections.New(sqlDB, base.cat, nil,
		collections.WithTMDB(tmdbProxy),
		collections.WithIndexTTL(0),
		collections.WithLiveTTL(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	sp := spec("Live Trending", "movie", []core.CollectionSource{{Kind: "tmdbList", List: "trending_week", MediaType: "movie"}})
	sp.Refresh = collections.RefreshLive
	sp.BrowseInApps = true
	created, err := svc.CreateSpec(base.ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if created.ItemCount == 0 {
		t.Fatal("expected initial build to populate members")
	}

	// Simulate a cold restart where s.members is empty and the remote source errors.
	tmdbProxy.failing.Store(true)
	cold, err := collections.New(sqlDB, base.cat, nil,
		collections.WithTMDB(tmdbProxy),
		collections.WithIndexTTL(0),
		collections.WithLiveTTL(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := collections.PageRowsForTest(base.ctx, cold, owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.CollectionID == created.ID && len(r.Hub.Items) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("cold RefreshLive collection did not fall back to persisted members; got %d rows", len(rows))
	}
}

// Presentation and publication edits (such as enabling Targets.HomeRow) must
// preserve build_hash and existing built members without forcing a rebuild.
func TestUpdateSpecNonMembershipEditPreservesBuild(t *testing.T) {
	base := newEnv(t)
	sqlDB := base.db.(*sql.DB)
	tmdbProxy := newToggleTMDB(t, base.tmdb.srv.URL)
	svc, err := collections.New(sqlDB, base.cat, nil,
		collections.WithTMDB(tmdbProxy),
		collections.WithIndexTTL(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	sp := spec("Trending Toggle", "movie", []core.CollectionSource{{Kind: "tmdbList", List: "trending_week", MediaType: "movie"}})
	sp.Refresh = collections.RefreshDaily
	sp.BrowseInApps = false
	created, err := svc.CreateSpec(base.ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if created.ItemCount == 0 || created.LastBuiltAt == nil {
		t.Fatalf("expected completed initial build, got %+v", created)
	}
	callsBefore := tmdbProxy.calls.Load()

	// Make TMDB fail so any unnecessary rebuild would fail or alter state.
	tmdbProxy.failing.Store(true)
	updatedSpec := *created
	updatedSpec.BrowseInApps = true
	updated, err := svc.UpdateSpec(base.ctx, created.ID, updatedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ItemCount != created.ItemCount || updated.LastBuiltAt == nil {
		t.Fatalf("non-membership UpdateSpec lost build metadata: before=%+v after=%+v", created, updated)
	}
	if tmdbProxy.calls.Load() != callsBefore {
		t.Fatalf("non-membership UpdateSpec triggered unexpected remote fetch")
	}

	rows, err := collections.PageRowsForTest(base.ctx, svc, owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.CollectionID == created.ID && len(r.Hub.Items) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("HomeRowsFor missing collection after enabling HomeRow")
	}
}

// A non-owner viewer restricted to a custom movie library (ID > 2) must still
// see movie collection items when WithLibrarySections maps that ID to TypeMovie,
// while remaining blocked from show items.
func TestCustomAllowedLibraryViewerSeesMatchingCollectionItems(t *testing.T) {
	e := newEnv(t, withOpts(collections.WithLibrarySections(func(context.Context) map[int]core.MediaType {
		return map[int]core.MediaType{1: core.TypeMovie, 2: core.TypeShow, 3: core.TypeMovie}
	})))
	sp := spec("Mixed Picks", "mixed", []core.CollectionSource{{Kind: "catalog"}})
	sp.LocalOnly = true
	sp.BrowseInApps = true
	created, err := e.svc.CreateSpec(e.ctx, sp)
	if err != nil {
		t.Fatal(err)
	}

	viewer := &core.User{ID: 7, Username: "custom-movies", AllowedLibraries: []core.LibraryID{3}}
	rows, err := collections.PageRowsForTest(e.ctx, e.svc, viewer)
	if err != nil {
		t.Fatal(err)
	}
	var got *core.CollectionRow
	for i := range rows {
		if rows[i].CollectionID == created.ID {
			got = &rows[i]
			break
		}
	}
	if got == nil || len(got.Hub.Items) == 0 {
		t.Fatalf("viewer allowed custom movie library 3 saw no collection items")
	}
	for _, item := range got.Hub.Items {
		if item.Type != core.TypeMovie {
			t.Fatalf("viewer restricted to custom movie library 3 saw non-movie item %+v", item.Type)
		}
	}
}

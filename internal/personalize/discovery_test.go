package personalize

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"reflect"
	"testing"
	"time"
)

func TestDiscoveryStickyShowDismissalAndViewerIsolation(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	show := core.ShowKey(99)
	a := core.Title{Key: core.EpisodeKey(99, 1, 1), Type: core.TypeEpisode, ShowKey: show, ContentRating: "PG"}
	b := a
	b.Key = core.EpisodeKey(99, 1, 2)
	cat.set(a)
	cat.set(b)
	d := NewDiscovery(db, cat, nil, nil)
	if e = d.RemoveContinue(ctx, 1, a.Key); e != nil {
		t.Fatal(e)
	}
	if d.ContinueAllowed(ctx, 1, &b) {
		t.Fatal("new episode bypassed sticky show dismissal")
	}
	if !d.ContinueAllowed(ctx, 2, &b) {
		t.Fatal("dismissal leaked across viewers")
	}
	if e = d.RestoreContinue(ctx, 1, a.Key); e != nil {
		t.Fatal(e)
	}
	filtered := d.FilterContinue(ctx, &core.User{ID: 1}, []core.Title{a, b})
	if len(filtered) != 1 {
		t.Fatalf("show duplicated %v", filtered)
	}
}
func TestDiscoveryReviewRespectsYearCompletionAndSafety(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	st := state.New(db, state.Options{Now: func() time.Time { return time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) }})
	key := movieKey(0, 1)
	if e = st.Scrobble(ctx, 1, key); e != nil {
		t.Fatal(e)
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return testNow }
	h, e := d.YearInReview(ctx, &core.User{ID: 1}, 2025, 20)
	if e != nil || len(h.Items) != 1 {
		t.Fatalf("review %v %v", h, e)
	}
	other, e := d.YearInReview(ctx, &core.User{ID: 2}, 2025, 20)
	if e != nil || len(other.Items) != 0 {
		t.Fatal("review leaked viewer history")
	}
	current, e := d.YearInReview(ctx, &core.User{ID: 1}, 2026, 20)
	if e != nil || len(current.Items) != 0 {
		t.Fatal("review included another year")
	}
	cat.set(core.Title{Key: key, Type: core.TypeMovie, Adult: true})
	h, e = d.YearInReview(ctx, &core.User{ID: 1}, 2025, 20)
	if e != nil || len(h.Items) != 0 {
		t.Fatal("adult title leaked into review")
	}
}

func TestDiscoveryVirtualReviewCollectionIsViewerScoped(t *testing.T) {
	ctx := context.Background()
	db, e := store.OpenMemory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	st := state.New(db, state.Options{Now: func() time.Time { return time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) }})
	if e = st.Scrobble(ctx, 1, movieKey(0, 1)); e != nil {
		t.Fatal(e)
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return testNow }
	collections := d.ReviewCollections(nil)
	list, e := collections.List(ctx, &core.User{ID: 1}, core.TypeMovie)
	if e != nil || len(list) != 1 || list[0].ChildCount != 1 || list[0].OwnerUserID != 1 {
		t.Fatalf("collections %v %v", list, e)
	}
	key := list[0].Key
	year, typ, ok := reviewCollectionParts(key)
	if !ok || year != 2025 || typ != core.TypeMovie {
		t.Fatal("unstable review identity")
	}
	page, e := collections.Items(ctx, &core.User{ID: 2}, key, 0, 20)
	if e != nil || page.Total != 0 {
		t.Fatalf("collection leaked viewer history %v %v", page, e)
	}
	if _, e = collections.Get(ctx, nil, key); e != core.ErrUnauthorized {
		t.Fatal("anonymous review allowed")
	}
	if e = collections.DeleteSmart(ctx, key); e != core.ErrNotFound {
		t.Fatal("virtual collection deletion accepted")
	}
}

func TestYearInReviewSkipsAlreadyPresentedShowBeforeLookup(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	show := core.ShowKey(991)
	cat.set(core.Title{Key: show, Type: core.TypeShow, Title: "Long running show"})
	at := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	st := state.New(db, state.Options{Now: func() time.Time { return at }})
	for i := 1; i <= 100; i++ {
		key := core.EpisodeKey(991, 1, i)
		cat.set(core.Title{Key: key, Type: core.TypeEpisode, ShowKey: show})
		if err := st.Scrobble(ctx, 1, key); err != nil {
			t.Fatal(err)
		}
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return testNow }
	before := cat.callCount("get")
	row, err := d.YearInReview(ctx, &core.User{ID: 1}, 2025, 25)
	if err != nil || len(row.Items) != 1 || row.Items[0].Key != show {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if calls := cat.callCount("get") - before; calls != 2 {
		t.Fatalf("got %d title loads for one show, want first episode + show only", calls)
	}
}

type batchedHistoryCatalog struct {
	*fakeCatalog
	batches int
	keys    int
}

func (c *batchedHistoryCatalog) CachedTitles(_ context.Context, keys []core.RatingKey) ([]core.Title, error) {
	c.batches++
	c.keys = len(keys)
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	return c.resolve(keys), nil
}
func TestHistoryTitleBatchAvoidsPerTitleReads(t *testing.T) {
	ctx := context.Background()
	cat := &batchedHistoryCatalog{fakeCatalog: newFakeCatalog(testNow)}
	a, b := movieKey(0, 1), movieKey(0, 2)
	cache := mirroredTitles(ctx, cat, []core.RatingKey{a, b, a})
	for _, key := range []core.RatingKey{a, b} {
		title, err := historyTitle(ctx, cat, cache, key)
		if err != nil || title.Key != key {
			t.Fatalf("title=%+v err=%v", title, err)
		}
	}
	if cat.batches != 1 || cat.keys != 2 || cat.callCount("get") != 0 {
		t.Fatalf("batch=%d keys=%d gets=%d", cat.batches, cat.keys, cat.callCount("get"))
	}
	missing := core.MovieKey(999999)
	if _, err := historyTitle(ctx, cat, cache, missing); err != core.ErrNotFound || cat.callCount("get") != 1 {
		t.Fatal("missing mirror entry did not use ordinary lookup")
	}
}

// rediscoverState fixes the history order so batching must preserve selection,
// show deduplication, exclusions, and the early page limit.
type rediscoverState struct {
	core.State
	entries []core.WatchStatus
}

func (s rediscoverState) InProgress(context.Context, int64, int) ([]core.WatchStatus, error) {
	return s.entries, nil
}

func TestRediscoverBatchesMirroredTitlesWithoutChangingSelection(t *testing.T) {
	ctx := context.Background()
	for _, limit := range []int{3, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cat := newFakeCatalog(testNow)
			st := rediscoverState{}
			old := testNow.Add(-40 * 24 * time.Hour)
			for n := 1; n <= 80; n++ {
				key := core.MovieKey(int64(10000 + n))
				title := core.Title{Key: key, Type: core.TypeMovie, ContentRating: "PG"}
				status := core.WatchStatus{Key: key, LastViewedAt: old}
				switch n {
				case 1, 2:
					title.Key, title.Type, title.ShowKey = core.EpisodeKey(9001, 1, n), core.TypeEpisode, core.ShowKey(9001)
					status.Key = title.Key
					cat.set(core.Title{Key: title.ShowKey, Type: core.TypeShow, ContentRating: "PG"})
				case 3:
					title.Adult = true
				case 4:
					status.LastViewedAt = testNow
				case 5:
					status.ViewCount = 1
				case 6:
					title.ContentRating = "R"
				case 7:
					status.LastViewedAt = time.Time{}
				}
				cat.set(title)
				st.entries = append(st.entries, status)
			}
			viewer := &core.User{ID: 1, MaxContentRating: "PG"}
			makeDiscovery := func(c core.Catalog) *Discovery {
				d := NewDiscovery(nil, c, st, nil)
				d.now = func() time.Time { return testNow }
				d.NotInterested = func(context.Context, *core.User) ([]NotInterestedSignal, error) {
					return []NotInterestedSignal{{Key: core.MovieKey(10008)}}, nil
				}
				return d
			}
			want, err := makeDiscovery(cat).Rediscover(ctx, viewer, limit)
			if err != nil {
				t.Fatal(err)
			}
			before := cat.callCount("get")
			batch := &batchedHistoryCatalog{fakeCatalog: cat}
			got, err := makeDiscovery(batch).Rediscover(ctx, viewer, limit)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("batch changed selection: got=%+v want=%+v err=%v", got, want, err)
			}
			t.Logf("limit=%d items=%d: individual reads %d -> %d; batch reads=%d", limit, len(got.Items), before, cat.callCount("get")-before, batch.batches)
			wantBatches := 1
			if limit == 100 {
				wantBatches = 2
			}
			if batch.batches != wantBatches || cat.callCount("get") != before {
				t.Fatalf("batches=%d want=%d individual reads=%d", batch.batches, wantBatches, cat.callCount("get")-before)
			}
			if len(got.Items) < 3 || got.Items[0].Key != core.ShowKey(9001) || got.Items[1].Key != core.MovieKey(10009) || got.Items[2].Key != core.MovieKey(10010) {
				t.Fatalf("lost ordered policy-filtered selection: %+v", got.Items)
			}
		})
	}
}

type pageFirstBaseCollections struct {
	core.Collections
	owned []core.Collection
	page  []core.Collection
	total int
}

func (f *pageFirstBaseCollections) List(context.Context, *core.User, core.MediaType) ([]core.Collection, error) {
	return nil, fmt.Errorf("unexpected full List call")
}
func (f *pageFirstBaseCollections) ListComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, error) {
	return nil, fmt.Errorf("unexpected full ListComplete call")
}
func (f *pageFirstBaseCollections) ListOwnedComplete(_ context.Context, _ *core.User, _ core.MediaType) ([]core.Collection, bool, error) {
	return append([]core.Collection(nil), f.owned...), true, nil
}
func (f *pageFirstBaseCollections) ListPage(_ context.Context, _ *core.User, _ core.MediaType, offset, limit int) ([]core.Collection, int, error) {
	if limit == 0 || offset >= len(f.page) {
		return nil, f.total, nil
	}
	end := min(offset+limit, len(f.page))
	return append([]core.Collection(nil), f.page[offset:end]...), f.total, nil
}
func (f *pageFirstBaseCollections) FindVisible(_ context.Context, _ *core.User, _ core.MediaType, accept func(core.Collection) (bool, error)) (bool, error) {
	for _, c := range f.owned {
		if ok, err := accept(c); ok || err != nil {
			return ok, err
		}
	}
	return false, nil
}

func TestReviewCollectionsForwardsPageFirstInterfaces(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	key := movieKey(0, 1)
	cat.set(core.Title{Key: key, Type: core.TypeMovie, ContentRating: "PG"})
	st := state.New(db, state.Options{Now: func() time.Time { return time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) }})
	if err := st.Scrobble(ctx, 1, key); err != nil {
		t.Fatal(err)
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return testNow }
	base := &pageFirstBaseCollections{
		owned: []core.Collection{{Key: core.SmartKey(1), Title: "Owned", Type: core.TypeMovie}},
		page: []core.Collection{
			{Key: core.SmartKey(1), Title: "Owned", Type: core.TypeMovie},
			{Key: core.CollectionKey(10), Title: "Franchise", Type: core.TypeMovie},
		},
		total: 2,
	}
	wrapped := d.ReviewCollections(base)
	viewer := &core.User{ID: 1}

	ownedLister, ok := wrapped.(interface {
		ListOwnedComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, bool, error)
	})
	if !ok {
		t.Fatal("ReviewCollections hid ListOwnedComplete")
	}
	owned, hasFr, err := ownedLister.ListOwnedComplete(ctx, viewer, core.TypeMovie)
	if err != nil || !hasFr || len(owned) != 2 {
		t.Fatalf("ListOwnedComplete = len=%d hasFr=%v err=%v, want 2 true nil", len(owned), hasFr, err)
	}

	pager, ok := wrapped.(interface {
		ListPage(context.Context, *core.User, core.MediaType, int, int) ([]core.Collection, int, error)
	})
	if !ok {
		t.Fatal("ReviewCollections hid ListPage")
	}
	page, total, err := pager.ListPage(ctx, viewer, core.TypeMovie, 0, 10)
	if err != nil || total != 3 || len(page) != 3 {
		t.Fatalf("ListPage(0,10) = len=%d total=%d err=%v, want 3 3 nil", len(page), total, err)
	}
	probe, probeTotal, err := pager.ListPage(ctx, viewer, core.TypeMovie, 0, 0)
	if err != nil || probeTotal != 3 || len(probe) != 0 {
		t.Fatalf("ListPage(0,0) = len=%d total=%d err=%v, want 0 3 nil", len(probe), probeTotal, err)
	}

	finder, ok := wrapped.(interface {
		FindVisible(context.Context, *core.User, core.MediaType, func(core.Collection) (bool, error)) (bool, error)
	})
	if !ok {
		t.Fatal("ReviewCollections hid FindVisible")
	}
	found, err := finder.FindVisible(ctx, viewer, core.TypeMovie, func(c core.Collection) (bool, error) {
		return c.Key == core.SmartKey(1), nil
	})
	if err != nil || !found {
		t.Fatalf("FindVisible = %v %v, want true nil", found, err)
	}
}

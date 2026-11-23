package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLibraryItemsUnwatchedShowKeepsPartlyWatched(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	aired := now.Add(-48 * time.Hour)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	put := func(title core.Title) {
		t.Helper()
		if err := c.UpsertTitle(ctx, title, title.Type == core.TypeShow); err != nil {
			t.Fatal(err)
		}
	}

	// Show 1: partly watched (E1 watched + Plex parent touch on show/season).
	partly := core.ShowKey(11)
	put(core.Title{Key: partly, Type: core.TypeShow, Title: "Alpha Partly", Released: aired, ChildCount: 1, LeafCount: 2})
	put(core.Title{Key: core.SeasonKey(11, 1), Type: core.TypeSeason, ShowKey: partly, Season: 1, Title: "Season 1", Released: aired, ChildCount: 2})
	put(core.Title{Key: core.EpisodeKey(11, 1, 1), Type: core.TypeEpisode, ShowKey: partly, Season: 1, Episode: 1, Title: "Ep 1", Released: aired})
	put(core.Title{Key: core.EpisodeKey(11, 1, 2), Type: core.TypeEpisode, ShowKey: partly, Season: 1, Episode: 2, Title: "Ep 2", Released: aired})

	// Show 2: completely watched (episode leaves only).
	done := core.ShowKey(12)
	put(core.Title{Key: done, Type: core.TypeShow, Title: "Bravo Done", Released: aired, ChildCount: 1, LeafCount: 1})
	put(core.Title{Key: core.SeasonKey(12, 1), Type: core.TypeSeason, ShowKey: done, Season: 1, Title: "Season 1", Released: aired, ChildCount: 1})
	put(core.Title{Key: core.EpisodeKey(12, 1, 1), Type: core.TypeEpisode, ShowKey: done, Season: 1, Episode: 1, Title: "Ep 1", Released: aired})

	// Show 3: unhydrated untouched show.
	unhydrated := core.ShowKey(13)
	put(core.Title{Key: unhydrated, Type: core.TypeShow, Title: "Charlie Unhydrated", Released: aired, ChildCount: 1, LeafCount: 8})

	st := state.New(db, state.Options{})
	// Simulate Plex ScrobbleTree marking E1 watched (touches show + season).
	if err := st.ScrobbleTree(ctx, 1, core.EpisodeKey(11, 1, 1), []core.RatingKey{core.EpisodeKey(11, 1, 1)}, true); err != nil {
		t.Fatal(err)
	}
	// Mark Show 2's only episode watched (no parent touch).
	if err := st.Scrobble(ctx, 1, core.EpisodeKey(12, 1, 1)); err != nil {
		t.Fatal(err)
	}

	user := &core.User{ID: 1, Username: "owner", IsOwner: true}
	libs := []Library{{ID: 2, Name: "TV Shows", Type: core.TypeShow}}
	svc := &Service{d: ServiceDeps{
		Active:              func(context.Context, int64) (*core.User, error) { return user, nil },
		Libraries:           libs,
		Visibility:          Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: c.CachedGet},
		State:               st,
		Browse:              c.Browse,
		CountWatched:        c.CountWatched,
		WatchedTitles:       c.WatchedTitles,
		AiredEpisodeTallies: c.AiredEpisodeTallies,
		CachedGet:           c.CachedGet,
		CachedGetMany:       c.CachedGetMany,
		Protocol:            "jellyfin",
	}}

	unwatched := false
	page, err := svc.LibraryItems(ctx, user, core.Device{}, "2", frontend.ItemQuery{
		Watched: &unwatched,
		Sort:    core.SortTitle,
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("LibraryItems unwatched: %v", err)
	}
	got := map[core.RatingKey]bool{}
	for _, it := range page.Items {
		got[it.Key] = true
	}
	if page.Total != 2 || len(page.Items) != 2 || !got[partly] || !got[unhydrated] || got[done] {
		t.Fatalf("unwatched shows via experience: total=%d keys=%v, want partly+unhydrated", page.Total, got)
	}

	watched := true
	wPage, err := svc.LibraryItems(ctx, user, core.Device{}, "2", frontend.ItemQuery{
		Watched: &watched,
		Sort:    core.SortTitle,
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("LibraryItems watched: %v", err)
	}
	if wPage.Total != 1 || len(wPage.Items) != 1 || wPage.Items[0].Key != done {
		t.Fatalf("watched shows via experience: total=%d items=%v, want done only", wPage.Total, wPage.Items)
	}

	// An unflushed pending watch of partly's final episode (E2) must drop
	// partly from Unwatched immediately via the pending overlay.
	pendingSvc := &Service{d: ServiceDeps{
		Active:              func(context.Context, int64) (*core.User, error) { return user, nil },
		Libraries:           libs,
		Visibility:          Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: c.CachedGet},
		State:               pendingWatchState{State: st, writes: map[core.RatingKey]int{core.EpisodeKey(11, 1, 2): 1}},
		Browse:              c.Browse,
		CountWatched:        c.CountWatched,
		WatchedTitles:       c.WatchedTitles,
		AiredEpisodeTallies: c.AiredEpisodeTallies,
		CachedGet:           c.CachedGet,
		CachedGetMany:       c.CachedGetMany,
		Protocol:            "jellyfin",
	}}
	pPage, err := pendingSvc.LibraryItems(ctx, user, core.Device{}, "2", frontend.ItemQuery{
		Watched: &unwatched,
		Sort:    core.SortTitle,
		Limit:   20,
	})
	if err != nil {
		t.Fatalf("LibraryItems unwatched with pending write: %v", err)
	}
	if pPage.Total != 1 || len(pPage.Items) != 1 || pPage.Items[0].Key != unhydrated {
		t.Fatalf("unwatched with pending E2 completion: total=%d items=%v, want unhydrated only", pPage.Total, pPage.Items)
	}
}

type pendingWatchState struct {
	*state.State
	writes map[core.RatingKey]int
}

func (p pendingWatchState) PendingKeys(int64) []core.RatingKey {
	keys := make([]core.RatingKey, 0, len(p.writes))
	for k := range p.writes {
		keys = append(keys, k)
	}
	return keys
}

func (p pendingWatchState) GetMany(ctx context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	data, err := p.State.GetMany(ctx, userID, keys)
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = map[core.RatingKey]core.WatchStatus{}
	}
	for _, k := range keys {
		if views, ok := p.writes[k]; ok {
			st := data[k]
			st.Key = k
			st.ViewCount = views
			data[k] = st
		}
	}
	return data, nil
}

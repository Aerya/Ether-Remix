package releasedb

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Une recherche Plex ou Jellyfin met en file les titres jamais indexés ou
// sans release depuis 6 h, pas ceux qui ont déjà des releases.
func TestSearchSoonQueuesOnlyStaleTitles(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s := New(Deps{
		DB:  store.OpenTest(t),
		Now: func() time.Time { return now },
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			var items []core.Title
			for _, k := range q.Filter.IncludeKeys {
				items = append(items, core.Title{Key: k, Type: q.Filter.Type})
			}
			return core.Page{Items: items}, nil
		},
		Settings: Settings{Enabled: true, Types: []core.MediaType{core.TypeMovie, core.TypeShow}},
	})
	never, found, emptyOld, emptyRecent, show := core.MovieKey(1), core.MovieKey(2), core.MovieKey(3), core.MovieKey(4), core.ShowKey(5)
	mark := func(k core.RatingKey, n int, at time.Time) {
		if err := s.st.markIndexed(ctx, core.Title{Key: k, Type: core.TypeMovie}, n, "ok", at, at.Add(14*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	mark(found, 3, now.Add(-30*24*time.Hour))
	mark(emptyOld, 0, now.Add(-7*time.Hour))
	mark(emptyRecent, 0, now.Add(-time.Hour))

	n, err := s.SearchSoon(ctx, []core.RatingKey{never, found, emptyOld, emptyRecent, show, never})
	if err != nil || n != 3 {
		t.Fatalf("SearchSoon = %d, %v ; 3 attendus", n, err)
	}
	got := map[core.RatingKey]bool{}
	for _, typ := range []core.MediaType{core.TypeMovie, core.TypeShow} {
		for _, x := range s.urgent[typ] {
			got[x.Key] = true
		}
	}
	if !got[never] || !got[emptyOld] || !got[show] || got[found] || got[emptyRecent] {
		t.Fatalf("file = %v", got)
	}
	// Une nouvelle frappe ne duplique rien.
	if n, _ := s.SearchSoon(ctx, []core.RatingKey{never}); n != 0 {
		t.Fatalf("doublon ajouté : %d", n)
	}
}

// Une recherche n'ajoute que ses 5 premiers titres et s'arrête quand la
// file est pleine ; « Chercher maintenant » n'a pas cette limite.
func TestSearchSoonBounded(t *testing.T) {
	ctx := context.Background()
	s := New(Deps{
		DB: store.OpenTest(t),
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			var items []core.Title
			for _, k := range q.Filter.IncludeKeys {
				items = append(items, core.Title{Key: k, Type: core.TypeMovie})
			}
			return core.Page{Items: items}, nil
		},
		Settings: Settings{Enabled: true, Types: []core.MediaType{core.TypeMovie}},
	})
	var keys []core.RatingKey
	for i := int64(1); i <= 300; i++ {
		keys = append(keys, core.MovieKey(i))
	}
	if n, err := s.SearchSoon(ctx, keys); err != nil || n != searchSoonMax {
		t.Fatalf("SearchSoon = %d, %v", n, err)
	}
	if n, err := s.SearchNow(ctx, keys); err != nil || n != 300-searchSoonMax {
		t.Fatalf("SearchNow = %d, %v", n, err)
	}
	if n, _ := s.SearchSoon(ctx, []core.RatingKey{core.MovieKey(999)}); n != 0 {
		t.Fatalf("file pleine, ajout = %d", n)
	}
	off := New(Deps{DB: store.OpenTest(t)})
	if _, err := off.SearchSoon(ctx, keys); err != ErrIndexerOff {
		t.Fatalf("indexation désactivée : %v", err)
	}
}

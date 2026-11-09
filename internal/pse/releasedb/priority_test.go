package releasedb

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Les titres des bibliothèques à thème passent avant le reste du catalogue.
func TestIndexerPriority(t *testing.T) {
	ctx := context.Background()
	popular := []core.Title{{Key: 1, Type: core.TypeMovie}, {Key: 2, Type: core.TypeMovie}}
	themed := []core.Title{{Key: 9, Type: core.TypeMovie}}
	s := New(Deps{
		DB: store.OpenTest(t),
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			if len(q.Filter.IncludeKeys) > 0 {
				return core.Page{Items: themed}, nil
			}
			if q.Offset > 0 {
				return core.Page{}, nil
			}
			return core.Page{Items: popular}, nil
		},
		Priority: func(context.Context, core.MediaType) []PriorityKey { return []PriorityKey{{Key: 9}} },
		Now:      func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) },
		Settings: Settings{Types: []core.MediaType{core.TypeMovie}},
	})
	var got []core.RatingKey
	for range 3 {
		ti, err := s.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ti.Key)
	}
	if got[0] != 9 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("ordre = %v, le titre à thème d'abord", got)
	}
}

package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type pagingLibraries struct {
	fakePluginLibraries
	calls   int
	windows []int
}

// Search answers the same equal-scored titles in a different order on every
// call, as an upstream with ties does.
func (p *pagingLibraries) Search(_ context.Context, _ core.Library, _ string, _ frontend.ItemQuery, window int) ([]core.Title, error) {
	p.calls++
	p.windows = append(p.windows, window)
	out := make([]core.Title, 0, 300)
	for i := 0; i < 300; i++ {
		seq := int64(i + 1)
		if p.calls%2 == 0 {
			seq = int64(300 - i)
		}
		out = append(out, core.Title{Key: core.PluginKey(seq), Title: "Love", LibraryID: 9, SearchScore: 0.5})
	}
	return out, nil
}

// A library search pages through one ranking: every page is new titles.
func TestPluginLibrarySearchPagesAreStable(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 8, AllowedLibraries: []core.LibraryID{9}}
	libs := &pagingLibraries{}
	svc := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return viewer, nil }, PluginLibraries: libs}}
	lib := core.Library{ID: 9, Source: core.LibraryPlugin}
	seen := map[core.RatingKey]bool{}
	for page := 0; page < 5; page++ {
		q := frontend.ItemQuery{Limit: 50, Offset: page * 50}
		q.Filter.Query = "love"
		res, err := svc.readPluginLibraryItems(ctx, viewer, lib, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			if seen[it.Key] {
				t.Fatalf("page %d repeats %v", page+1, it.Key)
			}
			seen[it.Key] = true
		}
	}
	if len(seen) != 250 {
		t.Fatalf("5 pages of 50 gave %d titles", len(seen))
	}
	for _, w := range libs.windows {
		if w != libs.windows[0] {
			t.Fatalf("the candidate window changed between pages: %v", libs.windows)
		}
	}
}

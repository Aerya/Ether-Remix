package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type allowAllPolicy struct{}

func (allowAllPolicy) Allowed(*core.User, *core.Title) bool { return true }

// A configured access policy must not by itself force the in-process walk:
// when the policy is provably passive, or its exclusions fold into the
// store query, a sorted / typed / watched library listing is answered from
// SQL with the store's exact total.
func TestPluginBrowseQueryPushDown(t *testing.T) {
	lib := core.Library{ID: 9, Source: core.LibraryPlugin}
	watched := false
	baseQuery := frontend.ItemQuery{
		Limit:     50,
		Sort:      core.SortTitle,
		Types:     []core.MediaType{core.TypeMovie},
		Recursive: true,
		Watched:   &watched,
	}
	baseQuery.Filter.Person = core.PluginPersonID("Someone")

	t.Run("passive policy with viewer cap folds into SQL", func(t *testing.T) {
		s := &Service{}
		s.d.Visibility.Policy = allowAllPolicy{}
		s.d.ViewerRules = func(*core.User) (int64, bool) { return 0, false }
		v := &core.User{ID: 1, IsOwner: true, MaxContentRating: "PG"}
		out, ok := s.pluginBrowseQuery(context.Background(), v, lib, baseQuery)
		if !ok {
			t.Fatal("passive policy query was not SQL-ready")
		}
		if out.Filter.MaxContentRating != "PG" {
			t.Fatalf("folded cap = %q, want PG", out.Filter.MaxContentRating)
		}
	})

	t.Run("fact-judging viewer rules keep the walk", func(t *testing.T) {
		s := &Service{}
		s.d.Visibility.Policy = allowAllPolicy{}
		s.d.ViewerRules = func(*core.User) (int64, bool) { return 3, true }
		v := &core.User{ID: 1, IsOwner: true}
		if _, ok := s.pluginBrowseQuery(context.Background(), v, lib, baseQuery); ok {
			t.Fatal("judged viewer rules must not be SQL-ready")
		}
	})

	t.Run("restricted viewer exclusions fold into SQL", func(t *testing.T) {
		s := &Service{}
		s.d.Visibility.Policy = allowAllPolicy{}
		s.d.ViewerRules = func(*core.User) (int64, bool) { return 0, false }
		s.d.PluginLibraries = &fakePluginLibraries{}
		s.d.PageExclusions = func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{
				ExcludeGenres: []string{"Horror"},
				Movie:         core.Filter{MaxContentRating: "PG-13"},
				Television:    core.Filter{MaxContentRating: "PG-13"},
			}
		}
		v := &core.User{ID: 2, MaxContentRating: "R"}
		out, ok := s.pluginBrowseQuery(context.Background(), v, lib, baseQuery)
		if !ok {
			t.Fatal("pushable exclusions were not SQL-ready")
		}
		if out.Filter.MaxContentRating != "PG-13" {
			t.Fatalf("tightened cap = %q, want PG-13", out.Filter.MaxContentRating)
		}
		if len(out.Filter.ExcludeGenres) != 1 || out.Filter.ExcludeGenres[0] != "Horror" {
			t.Fatalf("exclude genres = %v, want [Horror]", out.Filter.ExcludeGenres)
		}
	})

	t.Run("share allow ratings keep the walk", func(t *testing.T) {
		s := &Service{}
		s.d.Visibility.Policy = allowAllPolicy{}
		s.d.ViewerRules = func(*core.User) (int64, bool) { return 0, false }
		s.d.PluginLibraries = &fakePluginLibraries{}
		s.d.PageExclusions = func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{Movie: core.Filter{ContentRatings: []string{"G", "PG"}}}
		}
		v := &core.User{ID: 2}
		if _, ok := s.pluginBrowseQuery(context.Background(), v, lib, baseQuery); ok {
			t.Fatal("share rating allow list must not be SQL-ready")
		}
	})

	t.Run("anchors keep the walk", func(t *testing.T) {
		s := &Service{}
		q := baseQuery
		q.AdjacentTo = core.PluginKey(1)
		if _, ok := s.pluginBrowseQuery(context.Background(), &core.User{ID: 1, IsOwner: true}, lib, q); ok {
			t.Fatal("AdjacentTo must not be SQL-ready")
		}
	})
}

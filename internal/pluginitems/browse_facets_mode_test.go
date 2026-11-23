package pluginitems

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestBrowseFacetAllAndNoneModes(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	if _, err := s.UpsertKind(ctx, "community.books", 10, sampleBookDecl(), false); err != nil {
		t.Fatal(err)
	}
	book := func(id, title, author, format string, pages int, year int, tags []string) core.KindItem {
		return core.KindItem{
			ID: id, Level: "book", ParentID: "s1", Title: title, Year: year, Playable: true,
			Fields: map[string]json.RawMessage{
				"author": rawJSON(t, []string{author}),
				"format": rawJSON(t, format),
				"pages":  rawJSON(t, pages),
				"tags":   rawJSON(t, tags),
			},
		}
	}
	items := []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series"},
		book("b1", "Alpha", "Ann Author", "epub", 100, 2001, []string{"scifi", "space"}),
		book("b2", "Beta", "Ann Author", "pdf", 200, 2002, []string{"scifi"}),
		book("b3", "Gamma", "Bob Writer", "epub", 300, 2003, []string{"space", "noir"}),
		book("b4", "Delta", "Bob Writer", "cbz", 0, 2004, []string{"noir"}),
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", items); err != nil {
		t.Fatal(err)
	}
	ids := func(q BrowseQuery) []string {
		t.Helper()
		q.LibraryID = 10
		q.Level = "book"
		q.Sort = "title"
		q.Limit = 50
		page, err := s.Browse(ctx, q)
		if err != nil {
			t.Fatalf("Browse %+v: %v", q, err)
		}
		if page.Total != len(page.Items) {
			t.Fatalf("Browse %+v total = %d with %d items", q, page.Total, len(page.Items))
		}
		out := make([]string, len(page.Items))
		for i, it := range page.Items {
			out[i] = it.ID
		}
		return out
	}
	for _, tc := range []struct {
		name string
		q    BrowseQuery
		want []string
	}{
		{"all tags", BrowseQuery{AllFacets: map[string][]string{"tags": {"scifi", "space"}}}, []string{"b1"}},
		{"none tags", BrowseQuery{ExcludeFacets: map[string][]string{"tags": {"scifi"}}}, []string{"b4", "b3"}},
		{"any plus none", BrowseQuery{Facets: map[string][]string{"tags": {"space"}}, ExcludeFacets: map[string][]string{"tags": {"noir"}}}, []string{"b1"}},
		{"none int range", BrowseQuery{ExcludeFacets: map[string][]string{"pages": {"150..350"}}}, []string{"b1", "b4"}},
		{"none int zero", BrowseQuery{ExcludeFacets: map[string][]string{"pages": {"0"}}}, []string{"b1", "b2", "b3"}},
		{"all int ranges", BrowseQuery{AllFacets: map[string][]string{"pages": {"50..250", "150..350"}}}, []string{"b2"}},
		{"none people", BrowseQuery{ExcludeFacets: map[string][]string{"author": {"Ann Author"}}}, []string{"b4", "b3"}},
		{"all people and format via any", BrowseQuery{Facets: map[string][]string{"format": {"epub"}}, AllFacets: map[string][]string{"author": {"Bob Writer"}}}, []string{"b3"}},
		{"none envelope year", BrowseQuery{ExcludeFacets: map[string][]string{"year": {"2002", "2004"}}}, []string{"b1", "b3"}},
	} {
		if got := ids(tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	// Search applies the same modes through its facet path.
	page, err := s.Search(ctx, SearchQuery{
		LibraryID: 10, Query: "a", Level: "book", Limit: 50,
		ExcludeFacets: map[string][]string{"tags": {"scifi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.ID == "b1" || it.ID == "b2" {
			t.Fatalf("search with excludeFacets returned excluded item %q in %+v", it.ID, page.Items)
		}
	}
	// Oversized facet sets are refused instead of building unbounded SQL.
	many := make([]string, MaxBrowseFacetValues+1)
	for i := range many {
		many[i] = "x"
	}
	if _, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "book", AllFacets: map[string][]string{"tags": many}}); err == nil {
		t.Fatal("expected facet bound error")
	}
}

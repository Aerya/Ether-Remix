package pluginitems

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Ordered policy, indexed facet and preview must all describe the same set.
func TestPluginVisibilityAndEnvelopeFacets(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	decl.Fields = append(decl.Fields, plugins.KindField{Key: "studio", Type: "string", Label: "Studio", Indexed: true})
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	items := []core.KindItem{
		{ID: "a", Level: "series", Title: "Alpha", Year: 1990, Rating: 8.5, Released: 100, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["fantasy"]`), "studio": json.RawMessage(`"Acme"`)}},
		{ID: "b", Level: "series", Title: "Beta", Year: 2020, Rating: 9.5, Released: 200, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["scifi"]`)}},
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", items); err != nil {
		t.Fatal(err)
	}
	specs := []core.LibraryExclusion{
		{Name: "Keep fantasy", Action: "show", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["fantasy"]`)}, {Field: "studio", Op: "is", Value: json.RawMessage(`"Acme"`)}}},
		{Name: "Hide high ratings", Action: "hide", Scope: "discovery", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "rating", Op: "gte", Value: json.RawMessage(`8`)}}},
	}
	policy, err := libraryfilter.CompilePolicy(specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "a" {
		t.Fatalf("ordered policy: %+v", page)
	}
	hidden, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Policy: policy, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 1 || hidden.Items[0].ID != "b" {
		t.Fatalf("hidden: %+v", hidden)
	}
	for _, field := range []string{"year", "released", "rating"} {
		page, err := s.Facets(ctx, 10, "series", field, 10)
		facets := page.Values
		if err != nil || len(facets) != 2 {
			t.Fatalf("%s facets: %+v %v", field, facets, err)
		}
		filtered, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Facets: map[string][]string{field: {facets[0].Value}}})
		if err != nil || filtered.Total != 1 {
			t.Fatalf("%s filter: %+v %v", field, filtered, err)
		}
	}
	preview, err := s.PreviewVisibility(ctx, 10, specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	p := preview.(map[string]any)
	if p["hidden"] != 1 || p["visible"] != 1 {
		t.Fatalf("preview: %+v", p)
	}
	// Dropping discovery-only rules still preserves the hide fallback.
	search, err := s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Beta", Policy: policy.ForSearch()})
	if err != nil || search.Total != 0 {
		t.Fatalf("search: %+v %v", search, err)
	}
	searchPolicy, err := libraryfilter.CompilePolicy(specs, "show")
	if err != nil {
		t.Fatal(err)
	}
	search, err = s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Beta", Policy: searchPolicy.ForSearch()})
	if err != nil || search.Total != 1 {
		t.Fatalf("discovery search: %+v %v", search, err)
	}
	lib := core.Library{ID: 10, Rules: specs, VisibilityDefault: "show"}
	hiddenItem, err := s.ItemHidden(core.WithExplicitSearch(ctx), lib, core.NewItemRef("community.books", "book", "b"))
	if err != nil || hiddenItem {
		t.Fatalf("discovery detail: %v %v", hiddenItem, err)
	}

}

// Declared predicates share indexed browse semantics, including nested groups,
// negation, previews and direct item visibility; unknown keys fail closed.
func TestDeclaredFacetRules(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "a", Level: "series", Title: "Alpha", Fields: map[string]json.RawMessage{"format": json.RawMessage(`"epub"`), "pages": json.RawMessage(`120`)}},
		{ID: "b", Level: "series", Title: "Beta", Fields: map[string]json.RawMessage{"format": json.RawMessage(`"pdf"`), "pages": json.RawMessage(`240`)}},
		{ID: "c", Level: "series", Title: "Gamma"},
	}); err != nil {
		t.Fatal(err)
	}
	specs := []core.LibraryExclusion{{Action: "show", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "group", Op: "is", Value: json.RawMessage(`[{"field":"browse:format","op":"isNot","value":"pdf"},{"field":"browse:pages","op":"is","value":"120"}]`)}}}}
	policy, err := s.CompilePolicy(ctx, 10, specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Policy: policy})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "a" {
		t.Fatalf("declared rules: %+v %v", page, err)
	}
	facet, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Facets: map[string][]string{"format": {"epub"}, "pages": {"120"}}})
	if err != nil || facet.Total != page.Total {
		t.Fatalf("browse parity: %+v %v", facet, err)
	}
	preview, err := s.PreviewVisibility(ctx, 10, specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	p := preview.(map[string]any)
	if p["visible"] != 1 || p["hidden"] != 2 {
		t.Fatalf("preview: %+v", p)
	}
	lib := core.Library{ID: 10, Rules: specs, VisibilityDefault: "hide"}
	hidden, err := s.ItemHidden(ctx, lib, core.NewItemRef("community.books", "book", "b"))
	if err != nil || !hidden {
		t.Fatalf("item visibility: %v %v", hidden, err)
	}
	specs[0].Rules = []core.CollectionRule{{Field: "browse:undeclared", Op: "is", Value: json.RawMessage(`"x"`)}}
	if _, err := s.CompilePolicy(ctx, 10, specs, "hide"); err == nil {
		t.Fatal("undeclared field accepted")
	}
}

func TestFilterVisibleIDs(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "a", Level: "series", Title: "Alpha", Rating: 9.0, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["fantasy"]`)}},
		{ID: "b", Level: "series", Title: "Beta", Rating: 6.0, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["horror"]`)}},
		{ID: "c", Level: "series", Title: "Gamma", Rating: 8.5, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["fantasy"]`)}},
	}); err != nil {
		t.Fatal(err)
	}

	// Passive library: returns existing items in caller order, deduplicated, omitting unknowns.
	passiveLib := core.Library{ID: 10, VisibilityDefault: "show"}
	got, err := s.FilterVisibleIDs(ctx, "community.books", "book", passiveLib, []string{"c", "missing", "a", "c", "", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c", "a", "b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("passive FilterVisibleIDs = %v, want %v", got, want)
	}

	// Hide rule drops matching items (horror).
	hideLib := core.Library{
		ID:                10,
		VisibilityDefault: "show",
		Rules: []core.LibraryExclusion{{
			Name:      "Hide horror",
			Action:    "hide",
			MediaType: "mixed",
			Match:     "all",
			Rules:     []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["horror"]`)}},
		}},
	}
	got, err = s.FilterVisibleIDs(ctx, "community.books", "book", hideLib, []string{"b", "c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "c" || got[1] != "a" {
		t.Fatalf("hide FilterVisibleIDs = %v, want [c a]", got)
	}

	// Default hide + explicit show rule only surfaces items matching the allow rule.
	defaultHideLib := core.Library{
		ID:                10,
		VisibilityDefault: "hide",
		Rules: []core.LibraryExclusion{{
			Name:      "Show 9+",
			Action:    "show",
			MediaType: "mixed",
			Match:     "all",
			Rules:     []core.CollectionRule{{Field: "rating", Op: "gte", Value: json.RawMessage(`9`)}},
		}},
	}
	got, err = s.FilterVisibleIDs(ctx, "community.books", "book", defaultHideLib, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("default-hide FilterVisibleIDs = %v, want [a]", got)
	}

	// Empty input returns empty non-nil slice.
	got, err = s.FilterVisibleIDs(ctx, "community.books", "book", hideLib, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty FilterVisibleIDs = %#v, err = %v", got, err)
	}
}

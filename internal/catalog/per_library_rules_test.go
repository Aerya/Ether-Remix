package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPerLibraryPoliciesScopedPreviewAndLegacyExemptions(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()

	// Legacy mixed rule migrated onto Movies (mediaType="movie") and TV Shows (mediaType="show").
	legacyMixed := core.LibraryExclusion{
		Name: "No French", MediaType: "mixed", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}},
	}
	pol := testPolicies([]core.LibraryExclusion{legacyMixed}, "")
	c, err := New(db, nil, config.Catalog{}, nil, WithLibraryPolicies(pol))
	if err != nil {
		t.Fatal(err)
	}

	rel := time.Now().Add(-48 * time.Hour)
	for _, title := range []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "French Comedy", Language: "fr", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "US Comedy", Language: "en", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "JP Comedy", Language: "ja", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.ShowKey(10), Type: core.TypeShow, Title: "French Show", Language: "fr", Genres: []string{"Drama"}, Released: rel},
	} {
		if err := c.upsert(ctx, title, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}

	// Legacy viewer exemption recorded against the pre-migration "mixed" rule identity
	// still exempts both canonical Movies and TV Shows rules.
	v := &core.User{ID: 7}
	if err := c.PutViewerRules(ctx, v.ID, ViewerRules{
		Mode:       "exempt",
		Exemptions: []string{RuleIdentity(legacyMixed)},
	}); err != nil {
		t.Fatal(err)
	}
	vCtx := core.WithCatalogViewer(ctx, v)
	mp, err := c.Browse(vCtx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || mp.Total != 3 {
		t.Fatalf("exempt viewer movies total=%d err=%v, want 3", mp.Total, err)
	}
	sp, err := c.Browse(vCtx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeShow}, Limit: 10})
	if err != nil || sp.Total != 1 {
		t.Fatalf("exempt viewer shows total=%d err=%v, want 1", sp.Total, err)
	}

	// Scoped preview on a saved-filter library (Comedy movies) applies canonical Movies
	// rules (hiding French Comedy -> 2 candidates) and evaluates extra filter-library rules.
	extraRule := []core.LibraryExclusion{{
		Name: "No JP in Standup", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"ja"`)}},
	}}
	comedyGroup := &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Operator: "and", Filter: &core.Filter{Genres: []string{"Comedy"}}}}}
	filterScope := core.Filter{
		Type:                    core.TypeMovie,
		LocalOnly:               true,
		ApplyCanonicalInPreview: true,
		Group:                   comedyGroup,
	}
	rawPrev, err := c.PreviewVisibilityScoped(ctx, extraRule, "", &filterScope)
	if err != nil {
		t.Fatal(err)
	}
	prev := rawPrev.(VisibilityPreview)
	if prev.Total != 2 || prev.Hidden != 1 || prev.Visible != 1 {
		t.Fatalf("scoped filter preview = %+v, want Total=2 Hidden=1 Visible=1", prev)
	}

	// Normal browsing of that filter library with ExtraRules matches Visible=1.
	normalPage, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{
			Type:       core.TypeMovie,
			LocalOnly:  true,
			Group:      comedyGroup,
			ExtraRules: extraRule,
		},
		Limit: 10,
	})
	if err != nil || normalPage.Total != 1 || normalPage.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("filter library browse with ExtraRules = %+v, %v", normalPage, err)
	}
}

func TestLibraryScopedTallyTotalsAndFacets(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()

	aotKey := core.ShowKey(1429)
	narutoKey := core.ShowKey(46260)
	bbKey := core.ShowKey(1396)

	showRules := []core.LibraryExclusion{
		{
			Name: "Keep AoT", Action: "show", MediaType: "show", Match: "all",
			Rules: []core.CollectionRule{{Field: "title", Op: "is", Value: json.RawMessage(`"` + aotKey.String() + `"`)}},
		},
		{
			Name: "Hide Anime From TV Shows", Action: "hide", MediaType: "show", Match: "all", Scope: core.VisibilityLibrary,
			Rules: []core.CollectionRule{{Field: "keyword", Op: "in", Value: json.RawMessage(`["anime"]`)}},
		},
	}
	c, err := New(db, nil, config.Catalog{}, nil, WithLibraryPolicies(LibraryPolicies{
		ShowRules:   showRules,
		ShowDefault: "show",
	}))
	if err != nil {
		t.Fatal(err)
	}

	rel := time.Now().Add(-72 * time.Hour)
	for _, title := range []core.Title{
		{Key: bbKey, Type: core.TypeShow, Title: "Breaking Bad", Year: 2008, Genres: []string{"Drama"}, Countries: []string{"US"}, Keywords: []string{"crime"}, Released: rel},
		{Key: narutoKey, Type: core.TypeShow, Title: "Naruto", Year: 2002, Genres: []string{"Animation", "Action"}, Countries: []string{"JP"}, Keywords: []string{"anime"}, Released: rel},
		{Key: aotKey, Type: core.TypeShow, Title: "Attack on Titan", Year: 2013, Genres: []string{"Animation", "Drama"}, Countries: []string{"JP"}, Keywords: []string{"anime"}, Released: rel},
	} {
		if err := c.upsert(ctx, title, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}

	// Canonical catalogue visibility and unscoped browse still include Naruto.
	if !c.Visible(&core.Title{Key: narutoKey, Type: core.TypeShow, Keywords: []string{"anime"}, Released: rel}) {
		t.Fatal("library-scoped hide rule must not hide title canonically")
	}
	unscoped, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeShow}, Limit: 10})
	if err != nil || unscoped.Total != 3 {
		t.Fatalf("unscoped show browse total=%d err=%v, want 3", unscoped.Total, err)
	}

	// Built-in library overlay uses tally subtraction: hides Naruto, keeps Breaking Bad + Attack on Titan.
	extra := core.BuiltinLibraryExtraRules(showRules)
	scopedFilter := core.Filter{Type: core.TypeShow, ExtraRules: extra}
	scoped, err := c.Browse(ctx, core.BrowseQuery{Filter: scopedFilter, Limit: 10})
	if err != nil || scoped.Total != 2 || len(scoped.Items) != 2 {
		t.Fatalf("scoped show browse total=%d len=%d err=%v, want 2", scoped.Total, len(scoped.Items), err)
	}
	for _, it := range scoped.Items {
		if it.Key == narutoKey {
			t.Fatal("Naruto leaked into library-scoped browse")
		}
	}

	// Verify tallyTotal directly succeeds (indexed tally path, not full scan).
	where, args, err := c.browseWhere(scopedFilter)
	if err != nil {
		t.Fatal(err)
	}
	tallyN, tallyOK, err := c.tallyTotal(ctx, scopedFilter, where, args)
	if err != nil || !tallyOK || tallyN != 2 {
		t.Fatalf("tallyTotal = (%d, %v, %v), want (2, true, nil)", tallyN, tallyOK, err)
	}

	// Facets through facetFromTally subtract Naruto's Action/Animation/JP/2002/N entries.
	genres, err := c.FacetFilter(ctx, scopedFilter, "genre")
	if err != nil {
		t.Fatal(err)
	}
	genreCounts := map[string]int{}
	for _, fv := range genres {
		genreCounts[fv.Key] = fv.Count
	}
	if genreCounts["Drama"] != 2 || genreCounts["Animation"] != 1 || genreCounts["Action"] != 0 {
		t.Fatalf("scoped genre facets = %v, want Drama=2 Animation=1 Action=0", genreCounts)
	}
	firstChars, err := c.FacetFilter(ctx, scopedFilter, "firstCharacter")
	if err != nil {
		t.Fatal(err)
	}
	for _, fv := range firstChars {
		if fv.Key == "N" {
			t.Fatalf("scoped firstCharacter should not contain N for hidden Naruto: %v", firstChars)
		}
	}

	// Per-viewer custom rules cannot use scope="library".
	if err := c.PutViewerRules(ctx, 9, ViewerRules{
		Mode:       "custom",
		Exclusions: showRules,
	}); err == nil {
		t.Fatal("expected custom viewer rules with scope=library to fail")
	}
}

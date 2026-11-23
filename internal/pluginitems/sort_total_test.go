package pluginitems

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPluginSortTitleBackfillAndCheapTotal(t *testing.T) {
	s, db := openTestService(t, Options{})
	ctx := context.Background()
	decl := sampleBookDecl()
	const libID core.LibraryID = 55
	if _, err := s.UpsertKind(ctx, "community.books", libID, decl, false); err != nil {
		t.Fatal(err)
	}

	items := []core.KindItem{
		{ID: "1", Level: "series", Title: `"Pimpernel" Smith`, Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
		{ID: "2", Level: "series", Title: `"Sr."`, Fields: map[string]json.RawMessage{"format": rawJSON(t, "pdf")}},
		{ID: "3", Level: "series", Title: "#1 Cheerleader Camp", Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
		{ID: "4", Level: "series", Title: "#Alive", Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
		{ID: "5", Level: "series", Title: "10 Cloverfield Lane", Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
		{ID: "6", Level: "series", Title: "2 Fast 2 Furious", Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
		{ID: "7", Level: "series", Title: "Custom Order", SortTitle: "000 Custom First", Fields: map[string]json.RawMessage{"format": rawJSON(t, "epub")}},
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", items); err != nil {
		t.Fatal(err)
	}

	// Unfiltered browse with NoTotal+CheapTotal uses plugin_item_tally (exact) and sorts by shared sort key.
	page, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "series",
		Sort:       "title",
		NoTotal:    true,
		CheapTotal: true,
		Limit:      20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.TotalExact || page.Total != 7 {
		t.Fatalf("tallied plugin browse Total=%d TotalExact=%v, want 7 / true", page.Total, page.TotalExact)
	}
	var gotTitles []string
	for _, it := range page.Items {
		gotTitles = append(gotTitles, it.Title)
	}
	wantTitles := []string{
		"Custom Order",
		"#1 Cheerleader Camp",
		"2 Fast 2 Furious",
		"10 Cloverfield Lane",
		"#Alive",
		`"Pimpernel" Smith`,
		`"Sr."`,
	}
	if !slices.Equal(gotTitles, wantTitles) {
		t.Fatalf("plugin title order = %q, want %q", gotTitles, wantTitles)
	}

	// Single tallied text facet with NoTotal+CheapTotal answers TotalExact from
	// plugin_item_field_tally in O(1), even when results span multiple pages (#682).
	talliedFacet, err := s.Browse(ctx, BrowseQuery{
		LibraryID:  libID,
		Level:      "series",
		Sort:       "title",
		Facets:     map[string][]string{"format": {"epub"}},
		NoTotal:    true,
		CheapTotal: true,
		Limit:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !talliedFacet.TotalExact || talliedFacet.Total != 6 {
		t.Fatalf("single tallied facet CheapTotal = %d / exact=%v, want 6 / true", talliedFacet.Total, talliedFacet.TotalExact)
	}

	// Un-tallied multi-page filter with NoTotal+CheapTotal omits exact total rather than scanning.
	filtered, err := s.Browse(ctx, BrowseQuery{
		LibraryID:     libID,
		Level:         "series",
		Sort:          "title",
		Facets:        map[string][]string{"format": {"epub"}},
		ExcludeFacets: map[string][]string{"format": {"pdf"}},
		NoTotal:       true,
		CheapTotal:    true,
		Limit:         2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.TotalExact {
		t.Fatalf("expected un-tallied multi-page plugin CheapTotal to have TotalExact=false, got total=%d", filtered.Total)
	}

	// Verify applyMigration12 fixes pre-existing v1 sort_title values while keeping custom sort_title.
	if _, err := db.ExecContext(ctx, `UPDATE plugin_items SET sort_title = '#alive' WHERE external_id = '4'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE plugin_items SET sort_title = 'zzz custom 2' WHERE external_id = '7'`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration12(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	gotAlive, _, err := s.GetByExternal(ctx, "community.books", "book", "4")
	if err != nil {
		t.Fatal(err)
	}
	if gotAlive.SortTitle != "alive" {
		t.Fatalf("migrated #Alive sort_title = %q, want alive", gotAlive.SortTitle)
	}
	gotCustom, _, err := s.GetByExternal(ctx, "community.books", "book", "7")
	if err != nil {
		t.Fatal(err)
	}
	if gotCustom.SortTitle != "zzz custom 0000000002" {
		t.Fatalf("migrated custom sort_title = %q, want zzz custom 0000000002", gotCustom.SortTitle)
	}
}

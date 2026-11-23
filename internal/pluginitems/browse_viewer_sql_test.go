package pluginitems

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Viewer predicates pushed into Browse (watched, content-rating cap,
// excluded keys) filter in SQL, so the page and the exact total agree
// without a caller walking the library.
func TestBrowseViewerPredicatesInSQL(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	setupViewerStateTables(t, db)
	decl := plugins.MediaKindMeta{
		Key:     "doc",
		Label:   "Docs",
		Library: plugins.KindLibraryDecl{Name: "Docs"},
		Levels:  []plugins.KindLevel{{Key: "doc", Label: "Doc", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "contentRating", Type: "string", Label: "Rating", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{Sorts: []string{"title"}},
		Sync:   plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}
	if _, err := s.UpsertKind(ctx, "community.docs", 40, decl, false); err != nil {
		t.Fatal(err)
	}
	rated := func(id, rating string) core.KindItem {
		it := core.KindItem{ID: id, Level: "doc", Title: "Doc " + id, Playable: true}
		if rating != "" {
			it.Fields = map[string]json.RawMessage{"contentRating": rawJSON(t, rating)}
		}
		return it
	}
	if _, err := s.UpsertItems(ctx, "community.docs", "doc", []core.KindItem{
		rated("g", "G"), rated("pg", "PG"), rated("teen", "PG-13"), rated("r", "R"), rated("unrated", ""),
	}); err != nil {
		t.Fatal(err)
	}

	pgItem, _, err := s.GetByExternal(ctx, "community.docs", "doc", "pg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at) VALUES (7,?,2,100)`, int64(pgItem.Key)); err != nil {
		t.Fatal(err)
	}

	// Rating cap: a PG viewer sees G and PG only; unrated never passes.
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: 40, Level: "doc", Sort: "title:asc", MaxContentRating: "PG", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].ID != "g" || page.Items[1].ID != "pg" {
		t.Fatalf("rating-capped browse = %+v", page)
	}

	// Watched true keeps only view_count > 0 rows for that viewer.
	yes := true
	page, err = s.Browse(ctx, BrowseQuery{LibraryID: 40, Level: "doc", ViewerID: 7, Watched: &yes, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "pg" {
		t.Fatalf("watched browse = %+v", page)
	}

	// Watched false is the complement, with an exact total.
	no := false
	page, err = s.Browse(ctx, BrowseQuery{LibraryID: 40, Level: "doc", ViewerID: 7, Watched: &no, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Items) != 2 {
		t.Fatalf("unwatched browse total = %d items = %d, want total 4 page 2", page.Total, len(page.Items))
	}

	// Excluded keys drop before counting.
	page, err = s.Browse(ctx, BrowseQuery{LibraryID: 40, Level: "doc", ExcludeKeys: []core.RatingKey{pgItem.Key}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 {
		t.Fatalf("exclude-key browse total = %d, want 4", page.Total)
	}
	for _, it := range page.Items {
		if it.ID == "pg" {
			t.Fatal("excluded item still browsed")
		}
	}
}

// A filtered browse over more than the old 25,001-row count cap reports the
// exact SQL total, so clients can page the whole library.
func TestBrowseFilteredTotalExactBeyondOldCap(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	decl := plugins.MediaKindMeta{
		Key:     "photo",
		Label:   "Photos",
		Library: plugins.KindLibraryDecl{Name: "Photos"},
		Levels:  []plugins.KindLevel{{Key: "photo", Label: "Photo", Playable: true}},
		Browse:  plugins.KindBrowseDecl{Sorts: []string{"title"}},
		Sync:    plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}
	if _, err := s.UpsertKind(ctx, "community.photos", 41, decl, false); err != nil {
		t.Fatal(err)
	}
	const total = 25_005
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, rating_level, added_at, updated_at, seen_at
) VALUES (?, ?, 'community.photos', 'photo', 41, ?, '', 'photo', ?, ?, ?, 1, 1700000000, 1700000000, 1700000000)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < total; i++ {
		extID := fmt.Sprintf("img-%06d", i)
		if _, err := stmt.ExecContext(ctx, "pk:community.photos:photo:"+extID, int64(i+1), extID, i, "Photo "+extID, "photo "+extID); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_items_seq SET next = ? WHERE id = 1`, total+1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// MaxContentRating forces the exact COUNT path (no tally row exists).
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: 41, Level: "photo", Sort: "title:asc", MaxContentRating: "G", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != total {
		t.Fatalf("filtered total = %d, want exact %d", page.Total, total)
	}
}

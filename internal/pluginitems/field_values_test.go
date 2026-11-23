package pluginitems

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestFieldValuesPagingFoldedSearchDeclaredAndPlans(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	const libID core.LibraryID = 91
	decl := plugins.MediaKindMeta{
		Key:     "book",
		Label:   "Books",
		Library: plugins.KindLibraryDecl{Name: "Books"},
		Levels:  []plugins.KindLevel{{Key: "book", Label: "Book", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "subject", Type: "strings", Label: "Subject", Indexed: true, Values: []string{"Sci-Fi", "Unseen Tag", "Café Noir", "History"}},
			{Key: "format", Type: "enum", Label: "Format", Indexed: true, Enum: []string{"Hardcover", "EPUB", "Audiobook"}},
			{Key: "author", Type: "people", Label: "Author"},
			{Key: "pages", Type: "int", Label: "Pages", Indexed: true},
			{Key: "score", Type: "float", Label: "Score", Indexed: true},
			{Key: "runtime", Type: "duration", Label: "Runtime", Indexed: true},
			{Key: "published", Type: "date", Label: "Published", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{Sorts: []string{"title", "year", "released", "rating"}, Facets: []string{"subject", "format", "author", "pages", "score", "runtime", "published"}},
		Sync:   plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	if _, err := s.UpsertKind(ctx, "test.books", libID, decl, false); err != nil {
		t.Fatal(err)
	}

	// Before any item is synced, declared strings.values and enum values are returned with count 0.
	emptyPage, err := s.FieldValues(ctx, FieldValuesQuery{
		LibraryID:       libID,
		FieldKey:        "subject",
		Order:           "count",
		IncludeDeclared: true,
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("empty FieldValues subject: %v", err)
	}
	wantEmpty := []FacetCount{
		{Value: "Sci-Fi", Count: 0},
		{Value: "Unseen Tag", Count: 0},
		{Value: "Café Noir", Count: 0},
		{Value: "History", Count: 0},
	}
	if !reflect.DeepEqual(emptyPage.Values, wantEmpty) || emptyPage.More {
		t.Fatalf("empty subject values = (%+v, more=%v), want (%+v, false)", emptyPage.Values, emptyPage.More, wantEmpty)
	}

	// Folded search over declared zero-count values (diacritic folded: "cafe" matches "Café Noir").
	foldedEmpty, err := s.FieldValues(ctx, FieldValuesQuery{
		LibraryID:       libID,
		FieldKey:        "subject",
		Query:           "cafe",
		IncludeDeclared: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(foldedEmpty.Values) != 1 || foldedEmpty.Values[0].Value != "Café Noir" || foldedEmpty.Values[0].Count != 0 {
		t.Fatalf("folded empty search = %+v, want [Café Noir:0]", foldedEmpty.Values)
	}

	// Upsert items with varied counts, diacritics, people variants, and numeric/date/duration values.
	d1965 := time.Date(1965, 8, 1, 0, 0, 0, 0, time.UTC).Unix()
	d2010 := time.Date(2010, 5, 15, 0, 0, 0, 0, time.UTC).Unix()
	d2024 := time.Date(2024, 11, 20, 0, 0, 0, 0, time.UTC).Unix()

	items := []core.KindItem{
		{
			ID: "b1", Level: "book", Title: "Dune", Year: 1965, Released: d1965, Rating: 9.2, Playable: true,
			Fields: map[string]json.RawMessage{
				"subject":   json.RawMessage(`["Sci-Fi","Desert","Café Noir"]`),
				"format":    json.RawMessage(`"Hardcover"`),
				"author":    json.RawMessage(`["Frank Herbert"]`),
				"pages":     json.RawMessage(`412`),
				"score":     json.RawMessage(`9.2`),
				"runtime":   json.RawMessage(`1800`),
				"published": json.RawMessage(fmt.Sprintf("%d", d1965)),
			},
		},
		{
			ID: "b2", Level: "book", Title: "Dune Messiah", Year: 2010, Released: d2010, Rating: 8.0, Playable: true,
			Fields: map[string]json.RawMessage{
				"subject":   json.RawMessage(`["Sci-Fi","Desert"]`),
				"format":    json.RawMessage(`"Hardcover"`),
				"author":    json.RawMessage(`["frank herbert","Brian Herbert"]`),
				"pages":     json.RawMessage(`256`),
				"score":     json.RawMessage(`8.0`),
				"runtime":   json.RawMessage(`3600`),
				"published": json.RawMessage(fmt.Sprintf("%d", d2010)),
			},
		},
		{
			ID: "b3", Level: "book", Title: "Modern Guide", Year: 2024, Released: d2024, Rating: 6.5, Playable: true,
			Fields: map[string]json.RawMessage{
				"subject":   json.RawMessage(`["Sci-Fi"]`),
				"format":    json.RawMessage(`"EPUB"`),
				"author":    json.RawMessage(`["Élodie Martin"]`),
				"pages":     json.RawMessage(`0`),
				"score":     json.RawMessage(`0.0`),
				"runtime":   json.RawMessage(`600`),
				"published": json.RawMessage(fmt.Sprintf("%d", d2024)),
			},
		},
		{
			// Unset year/released/rating (0) and missing numeric fields: must NOT match open upper bounds.
			ID: "b4", Level: "book", Title: "Untitled Draft", Year: 0, Released: 0, Rating: 0, Playable: true,
			Fields: map[string]json.RawMessage{
				"subject": json.RawMessage(`["Draft"]`),
				"format":  json.RawMessage(`"EPUB"`),
			},
		},
	}
	if _, err := s.UpsertItems(ctx, "test.books", "book", items); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	// Count-ordered paging across tally rows + unpopulated declared values ("Unseen Tag", "History").
	var all []FacetCount
	for offset := 0; ; offset += 2 {
		pg, err := s.FieldValues(ctx, FieldValuesQuery{
			LibraryID:       libID,
			FieldKey:        "subject",
			Order:           "count",
			IncludeDeclared: true,
			Offset:          offset,
			Limit:           2,
		})
		if err != nil {
			t.Fatalf("FieldValues offset=%d: %v", offset, err)
		}
		all = append(all, pg.Values...)
		if !pg.More {
			break
		}
	}
	wantAll := []FacetCount{
		{Value: "Sci-Fi", Count: 3},
		{Value: "Desert", Count: 2},
		{Value: "Café Noir", Count: 1},
		{Value: "Draft", Count: 1},
		{Value: "Unseen Tag", Count: 0},
		{Value: "History", Count: 0},
	}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("paged subject values = %+v, want %+v", all, wantAll)
	}

	// People field uses plugin_item_person_tally: deduplicates casing ("Frank Herbert" / "frank herbert") and folds accents ("elodie").
	ppl, err := s.FieldValues(ctx, FieldValuesQuery{
		LibraryID: libID,
		FieldKey:  "author",
		Order:     "count",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("FieldValues author: %v", err)
	}
	if len(ppl.Values) != 3 || ppl.Values[0].Count != 2 || !strings.EqualFold(ppl.Values[0].Value, "Frank Herbert") {
		t.Fatalf("people values = %+v, want Frank Herbert (2) first and 3 unique people", ppl.Values)
	}
	elodie, err := s.FieldValues(ctx, FieldValuesQuery{
		LibraryID: libID,
		FieldKey:  "author",
		Query:     "elodie",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("FieldValues author folded: %v", err)
	}
	if len(elodie.Values) != 1 || elodie.Values[0].Value != "Élodie Martin" {
		t.Fatalf("folded author search = %+v, want [Élodie Martin]", elodie.Values)
	}

	// Verify EXPLAIN QUERY PLAN stays strictly on the covering tally indexes (no plugin_items / plugin_item_fields scan).
	if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	checkCovering := func(label string, q FieldValuesQuery, wantIdx string) {
		t.Helper()
		qctx, qs := store.WithQueryStats(ctx)
		if _, err := s.FieldValues(qctx, q); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		checked := 0
		for _, st := range qs.Statements() {
			if strings.Contains(st.SQL, "plugin_items") || strings.Contains(st.SQL, "plugin_item_fields") {
				t.Fatalf("%s touched item/field table: %s", label, st.SQL)
			}
			if !strings.Contains(st.SQL, "plugin_item_field_tally") && !strings.Contains(st.SQL, "plugin_item_person_tally") {
				continue
			}
			if !strings.Contains(st.SQL, "ORDER BY") {
				continue
			}
			if store.DialectOf(db) == store.SQLite {
				// Build matching args for EXPLAIN QUERY PLAN.
				args := []any{int64(libID), "", q.FieldKey}
				for _, term := range foldQueryTerms(q.Query) {
					args = append(args, term+"%", "% "+term+"%")
				}
				args = append(args, q.Limit+1, q.Offset)
				plan := explainSQLitePlan(t, ctx, db, st.SQL, args...)
				t.Logf("[%s] PLAN: %s", label, plan)
				if !strings.Contains(plan, wantIdx) {
					t.Fatalf("%s expected covering index %q, got plan: %s", label, wantIdx, plan)
				}
			}
			checked++
		}
		if checked == 0 {
			t.Fatalf("%s: expected a tally ORDER BY query", label)
		}
	}
	checkCovering("field_tally_count", FieldValuesQuery{LibraryID: libID, FieldKey: "subject", Order: "count", Limit: 20}, "plugin_item_field_tally_count")
	checkCovering("field_tally_search", FieldValuesQuery{LibraryID: libID, FieldKey: "subject", Query: "cafe", Order: "count", Limit: 20}, "plugin_item_field_tally_count")
	checkCovering("person_tally_count", FieldValuesQuery{LibraryID: libID, FieldKey: "author", Order: "count", Limit: 20}, "plugin_item_person_tally_count")

	// End-to-end Browse + CompilePolicy open/exclusive ranges & multi-chip Any/All/None!
	idsFromBrowse := func(q BrowseQuery) []string {
		t.Helper()
		q.LibraryID = libID
		q.Sort = "title"
		q.Limit = 20
		pg, err := s.Browse(ctx, q)
		if err != nil {
			t.Fatalf("Browse(%+v): %v", q.Facets, err)
		}
		out := make([]string, len(pg.Items))
		for i, it := range pg.Items {
			out[i] = it.ID
		}
		return out
	}

	assertIDs := func(label string, got, want []string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}

	// Direct Browse open/exclusive bounds:
	assertIDs("pages < 256 (includes 0, excludes 256 and missing)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"pages": {"<256"}}}), []string{"b3"})
	assertIDs("pages <= 256", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"pages": {"..256"}}}), []string{"b2", "b3"})
	assertIDs("pages > 256", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"pages": {">256"}}}), []string{"b1"})
	assertIDs("pages >= 256", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"pages": {"256.."}}}), []string{"b1", "b2"})
	assertIDs("score < 8.0 (includes 0.0, excludes 8.0 and missing)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"score": {"<8.0"}}}), []string{"b3"})
	assertIDs("score <= 8.0", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"score": {"..8.0"}}}), []string{"b2", "b3"})
	assertIDs("score > 8.0", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"score": {">8.0"}}}), []string{"b1"})
	assertIDs("runtime > 1800", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"runtime": {">1800"}}}), []string{"b2"})
	assertIDs("runtime >= 1800", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"runtime": {"1800.."}}}), []string{"b1", "b2"})
	assertIDs("published < 2010-05-15 (matches pre-1970 negative timestamp!)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"published": {fmt.Sprintf("<%d", d2010)}}}), []string{"b1"})
	assertIDs("year < 2010 (excludes unset 0 on b4!)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"year": {"<2010"}}}), []string{"b1"})
	assertIDs("released < 2010-05-15 (matches 1965, excludes unset 0 on b4!)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"released": {fmt.Sprintf("<%d", d2010)}}}), []string{"b1"})
	assertIDs("rating < 8.0 (matches 6.5, excludes 8.0 and unset 0 on b4!)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"rating": {"<8.0"}}}), []string{"b3"})
	assertIDs("rating <= 8.0 (matches 8.0 and 6.5, excludes unset 0 on b4!)", idsFromBrowse(BrowseQuery{Facets: map[string][]string{"rating": {"..8.0"}}}), []string{"b2", "b3"})

	// Policy execution (CompilePolicy) for open range, Any of, All of, None of, and people without explicit Indexed flag:
	idsFromRules := func(rules []core.CollectionRule) []string {
		t.Helper()
		pol, err := s.CompilePolicy(ctx, libID, []core.LibraryExclusion{{
			Action:    "show",
			MediaType: "movie",
			Match:     "all",
			Rules:     rules,
		}}, "hide")
		if err != nil {
			t.Fatalf("CompilePolicy: %v", err)
		}
		return idsFromBrowse(BrowseQuery{Policy: pol})
	}

	assertIDs("policy any of [Desert, Draft]", idsFromRules([]core.CollectionRule{
		{Field: "browse:subject", Op: "is", Value: json.RawMessage(`["Desert","Draft"]`)},
	}), []string{"b1", "b2", "b4"})

	assertIDs("policy all of [Sci-Fi, Café Noir]", idsFromRules([]core.CollectionRule{
		{Field: "group", Op: "is", Value: json.RawMessage(`[{"field":"browse:subject","op":"is","value":"Sci-Fi"},{"field":"browse:subject","op":"is","value":"Café Noir"}]`)},
	}), []string{"b1"})

	assertIDs("policy none of [Desert, Draft]", idsFromRules([]core.CollectionRule{
		{Field: "browse:subject", Op: "isNot", Value: json.RawMessage(`["Desert","Draft"]`)},
	}), []string{"b3"})

	assertIDs("policy people without indexed:true", idsFromRules([]core.CollectionRule{
		{Field: "browse:author", Op: "is", Value: json.RawMessage(`"Frank Herbert"`)},
	}), []string{"b1", "b2"})

	assertIDs("policy open numeric runtime > 1800", idsFromRules([]core.CollectionRule{
		{Field: "browse:runtime", Op: "is", Value: json.RawMessage(`">1800"`)},
	}), []string{"b2"})
}

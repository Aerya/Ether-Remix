package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func partialIndexTestDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "entry",
		Label:   "Entries",
		Library: plugins.KindLibraryDecl{Name: "Entries"},
		Levels: []plugins.KindLevel{
			{Key: "entry", Label: "Entry", Playable: true},
		},
		Fields: []plugins.KindField{
			{Key: "genres", Type: "strings", Label: "Genres", Indexed: true},
			{Key: "tags", Type: "strings", Label: "Tags", Indexed: true},
			{Key: "studio", Type: "string", Label: "Studio", Indexed: true},
			{Key: "episodes", Type: "int", Label: "Episodes", Indexed: true},
			{Key: "runtime", Type: "duration", Label: "Runtime", Indexed: true},
			{Key: "score", Type: "float", Label: "Score", Indexed: true},
			{Key: "aired", Type: "date", Label: "Aired", Indexed: true},
			{Key: "cast", Type: "people", Label: "Cast"},
			{Key: "directors", Type: "people", Label: "Directors"},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:        []string{"released", "title", "added"},
			Facets:       []string{"genres", "studio", "episodes", "runtime", "score", "aired", "cast", "directors"},
			Search:       true,
			SearchFields: []string{"studio"},
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
}

func seedPartialIndexLibrary(t *testing.T, ctx context.Context, s *Service, db *sql.DB, libID core.LibraryID, pluginID string, count int, seqStart int64) (elenaID, marcusID int64) {
	t.Helper()
	decl := partialIndexTestDecl()
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatalf("UpsertKind lib %d: %v", libID, err)
	}

	elenaID = core.PluginPersonID("Elena Vance")
	marcusID = core.PluginPersonID("Marcus Hale")
	extraPeople := make([]struct {
		name string
		pid  int64
	}, 0, 80)
	extraPeople = append(extraPeople,
		struct {
			name string
			pid  int64
		}{"Elena Vance", elenaID},
		struct {
			name string
			pid  int64
		}{"Marcus Hale", marcusID},
	)
	for p := 0; p < 78; p++ {
		name := fmt.Sprintf("Actor %03d", p)
		extraPeople = append(extraPeople, struct {
			name string
			pid  int64
		}{name, core.PluginPersonID(name)})
	}
	directors := make([]struct {
		name string
		pid  int64
	}, 0, 30)
	directors = append(directors, struct {
		name string
		pid  int64
	}{"Elena Vance", elenaID})
	for d := 0; d < 29; d++ {
		name := fmt.Sprintf("Director %03d", d)
		directors = append(directors, struct {
			name string
			pid  int64
		}{name, core.PluginPersonID(name)})
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	itemStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_items (
		item_ref, seq, plugin_id, kind_key, library_id, external_id,
		parent_ref, level, idx, title, sort_title, summary,
		year, released, rating, playable, child_count, leaf_count,
		unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks
	) VALUES (?, ?, ?, 'entry', ?, ?, '', 'entry', ?, ?, ?, '', ?, ?, ?, 1, 0, 0, '', '{}', ?, 'e1', ?, ?, ?, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	defer itemStmt.Close()

	fieldStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO plugin_item_fields (
		item_ref, field, v_text, v_int, v_real, v_fold
	) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fieldStmt.Close()

	episodesVals := make([]int64, 50)
	for i := range episodesVals {
		episodesVals[i] = int64(i) // 0..49 (includes legitimate 0)
	}
	runtimeVals := make([]int64, 60)
	runtimeVals[0] = 0 // legitimate 0
	for i := 1; i < len(runtimeVals); i++ {
		runtimeVals[i] = int64(i) * 180 // 3m..177m across all 5 duration buckets
	}
	scoreVals := make([]float64, 50)
	for i := range scoreVals {
		scoreVals[i] = float64(i) * 0.2 // 0.0..9.8 (includes legitimate 0.0)
	}
	airedDates := make([]int64, 0, 120)
	for y := 1995; y <= 2024; y++ {
		for m := time.January; m <= time.October; m += 3 {
			airedDates = append(airedDates, time.Date(y, m, 15, 0, 0, 0, 0, time.UTC).Unix())
		}
	}
	genres := []string{"Drama", "Sci-Fi", "Documentary", "Comedy", "Thriller", "Animation"}

	baseAdded := int64(1700000000)
	for i := 0; i < count; i++ {
		extID := fmt.Sprintf("e-%d-%05d", libID, i)
		ref := string(core.NewItemRef(pluginID, "entry", extID))
		title := fmt.Sprintf("Entry %05d", i)
		ep := episodesVals[i%len(episodesVals)]
		rt := runtimeVals[i%len(runtimeVals)]
		sc := scoreVals[i%len(scoreVals)]
		aired := airedDates[i%len(airedDates)]
		yr := time.Unix(aired, 0).UTC().Year()
		rel := baseAdded + int64(i)

		// Assign Elena Vance to a small subset so people queries are selective.
		var castPerson struct {
			name string
			pid  int64
		}
		if i%50 == 0 {
			castPerson = extraPeople[0]
		} else if i%50 == 1 {
			castPerson = extraPeople[1]
		} else {
			castPerson = extraPeople[2+(i%(len(extraPeople)-2))]
		}
		dir := directors[1+(i%(len(directors)-1))]
		if i%100 == 0 {
			dir = directors[0]
		}

		fieldsMap := map[string]any{
			"studio":    fmt.Sprintf("Studio %02d", i%20),
			"episodes":  ep,
			"runtime":   rt,
			"score":     sc,
			"aired":     aired,
			"cast":      []map[string]any{{"name": castPerson.name, "role": "Lead"}},
			"directors": []map[string]any{{"name": dir.name}},
		}
		fieldsJSON, err := json.Marshal(fieldsMap)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := itemStmt.ExecContext(ctx, ref, seqStart+int64(i), pluginID, int64(libID), extID, i+1, title, strings.ToLower(title), yr, rel, sc, string(fieldsJSON), rel, rel, rel); err != nil {
			t.Fatal(err)
		}

		// Heavy text rows (6 per item) to mirror text-heavy real libraries.
		for g := 0; g < 2; g++ {
			gv := genres[(i+g)%len(genres)]
			if _, err := fieldStmt.ExecContext(ctx, ref, "genres", gv, 0, 0.0, strings.ToLower(gv)); err != nil {
				t.Fatal(err)
			}
		}
		for tg := 0; tg < 3; tg++ {
			tv := fmt.Sprintf("tag-%02d", (i+tg)%40)
			if _, err := fieldStmt.ExecContext(ctx, ref, "tags", tv, 0, 0.0, tv); err != nil {
				t.Fatal(err)
			}
		}
		st := fmt.Sprintf("Studio %02d", i%20)
		if _, err := fieldStmt.ExecContext(ctx, ref, "studio", st, 0, 0.0, strings.ToLower(st)); err != nil {
			t.Fatal(err)
		}

		// Numeric rows (v_text = '', including legitimate 0 and 0.0).
		if _, err := fieldStmt.ExecContext(ctx, ref, "episodes", "", ep, 0.0, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "runtime", "", rt, 0.0, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "score", "", 0, sc, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "aired", "", aired, 0.0, ""); err != nil {
			t.Fatal(err)
		}

		// People rows (non-empty v_text AND synthetic non-zero v_int).
		if _, err := fieldStmt.ExecContext(ctx, ref, "cast", castPerson.name, castPerson.pid, 0.0, strings.ToLower(castPerson.name)); err != nil {
			t.Fatal(err)
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "directors", dir.name, dir.pid, 0.0, strings.ToLower(dir.name)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return elenaID, marcusID
}

func bindArgsForPlan(sqlText, fieldName string, intVal int64, libID core.LibraryID) []any {
	n := strings.Count(sqlText, "?")
	args := make([]any, 0, n)
	rest := sqlText
	inFieldList := false
	for i := 0; i < n; i++ {
		idx := strings.IndexByte(rest, '?')
		prefix := rest[:idx]
		rest = rest[idx+1:]
		trim := strings.TrimSpace(prefix)
		upper := strings.ToUpper(trim)

		if inFieldList {
			if strings.Contains(prefix, ")") {
				inFieldList = false
			}
		}
		switch {
		case inFieldList:
			args = append(args, fieldName)
		case strings.HasSuffix(upper, "FIELD ="):
			args = append(args, fieldName)
		case strings.HasSuffix(upper, "FIELD IN ("):
			inFieldList = true
			args = append(args, fieldName)
		case strings.HasSuffix(upper, "LEVEL ="):
			args = append(args, "entry")
		case strings.HasSuffix(upper, "LIBRARY_ID =") || strings.HasSuffix(upper, "LIBRARY_ID + 0 ="):
			args = append(args, int64(libID))
		case strings.HasSuffix(upper, "LIKE"):
			args = append(args, "elena%")
		case strings.HasSuffix(upper, "LIMIT"):
			args = append(args, int64(20))
		case strings.HasSuffix(upper, "OFFSET"):
			args = append(args, int64(0))
		default:
			args = append(args, intVal)
		}
	}
	return args
}

func explainSQLitePlan(t *testing.T, ctx context.Context, db *sql.DB, sqlText string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sqlText, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed for %q: %v", sqlText, err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, " | ")
}

type capturedPlan struct {
	sql  string
	plan string
}

func captureFieldQueryPlans(t *testing.T, ctx context.Context, db *sql.DB, fieldName string, intVal int64, libID core.LibraryID, fn func(context.Context)) []capturedPlan {
	t.Helper()
	qctx, qs := store.WithQueryStats(ctx)
	fn(qctx)
	var out []capturedPlan
	for _, st := range qs.Statements() {
		if !strings.Contains(st.SQL, "plugin_item_fields") {
			continue
		}
		args := bindArgsForPlan(st.SQL, fieldName, intVal, libID)
		out = append(out, capturedPlan{
			sql:  st.SQL,
			plan: explainSQLitePlan(t, ctx, db, st.SQL, args...),
		})
	}
	if len(out) == 0 {
		t.Fatalf("expected at least one query touching plugin_item_fields, captured none")
	}
	return out
}

func assertAnyPlanContains(t *testing.T, label string, plans []capturedPlan, want string) {
	t.Helper()
	for _, p := range plans {
		if strings.Contains(p.plan, want) {
			return
		}
	}
	var b strings.Builder
	for _, p := range plans {
		fmt.Fprintf(&b, "\n  PLAN: %s\n  SQL:  %s", p.plan, strings.Join(strings.Fields(p.sql), " "))
	}
	t.Errorf("%s: expected EXPLAIN QUERY PLAN to contain %q, got:%s", label, want, b.String())
}

func assertNoFullFieldScan(t *testing.T, label string, plans []capturedPlan) {
	t.Helper()
	for _, p := range plans {
		for _, part := range strings.Split(p.plan, " | ") {
			part = strings.TrimSpace(part)
			if part == "SCAN f" && strings.Contains(p.plan, "CO-ROUTINE f") {
				continue
			}
			if strings.HasPrefix(part, "SCAN f") || strings.HasPrefix(part, "SCAN pf") || strings.HasPrefix(part, "SCAN plugin_item_fields") {
				t.Errorf("%s: regressed to full table scan on plugin_item_fields:\n  PLAN: %s\n  SQL:  %s", label, p.plan, strings.Join(strings.Fields(p.sql), " "))
			}
		}
	}
}

type partialIndexParitySnapshot struct {
	intBuckets      []FacetBucket
	durationBuckets []FacetBucket
	floatBuckets    []FacetBucket
	dateBuckets     []FacetBucket
	decadeBuckets   []FacetBucket
	peopleBuckets   []FacetBucket

	intColIDs    []string
	floatColIDs  []string
	peopleColIDs []string
	facetPeople  []FacetPerson

	browseIntZero       []string
	browseIntMultiRange []string
	browseDurZero       []string
	browseDurMulti      []string
	browseFloatZero     []string
	browseFloatMulti    []string
	browseDateYear      []string
	browseDateDecade    []string
	browsePeopleFacet   []string
	browsePersonID      []string

	searchPeopleNames []string
	personByIDName    string
	personByIDCredits int
	personByIDRoles   map[string]int
	findPersonID      int64
	lookupFacets      []PersonFacetMatch
}

func collectParitySnapshot(t *testing.T, ctx context.Context, s *Service, libID core.LibraryID, elenaID int64) partialIndexParitySnapshot {
	t.Helper()
	var snap partialIndexParitySnapshot
	var err error

	if snap.intBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "episodes"); err != nil {
		t.Fatalf("FacetBuckets episodes: %v", err)
	}
	if snap.durationBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "runtime"); err != nil {
		t.Fatalf("FacetBuckets runtime: %v", err)
	}
	if snap.floatBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "score"); err != nil {
		t.Fatalf("FacetBuckets score: %v", err)
	}
	if snap.dateBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "aired"); err != nil {
		t.Fatalf("FacetBuckets aired: %v", err)
	}
	if snap.decadeBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "aired-decade"); err != nil {
		t.Fatalf("FacetBuckets aired-decade: %v", err)
	}
	if snap.peopleBuckets, err = s.FacetBuckets(ctx, libID, "", nil, "cast"); err != nil {
		t.Fatalf("FacetBuckets cast: %v", err)
	}

	colIDs := func(field string) []string {
		pg, err := s.FacetCollections(ctx, FacetCollectionsQuery{LibraryID: libID, FieldKey: field, Limit: 20})
		if err != nil {
			t.Fatalf("FacetCollections %s: %v", field, err)
		}
		ids := make([]string, len(pg.Items))
		for i, c := range pg.Items {
			ids[i] = fmt.Sprintf("%s=%d", c.ID, c.Count)
		}
		return ids
	}
	snap.intColIDs = colIDs("episodes")
	snap.floatColIDs = colIDs("score")
	snap.peopleColIDs = colIDs("cast")

	fp, err := s.FacetPeople(ctx, FacetPeopleQuery{LibraryID: libID, FieldKey: "cast", Limit: 20})
	if err != nil {
		t.Fatalf("FacetPeople: %v", err)
	}
	snap.facetPeople = fp.Items

	browseRefs := func(q BrowseQuery) []string {
		q.LibraryID = libID
		q.Sort = "title"
		q.Limit = 200
		pg, err := s.Browse(ctx, q)
		if err != nil {
			t.Fatalf("Browse %+v: %v", q.Facets, err)
		}
		refs := make([]string, len(pg.Items))
		for i, it := range pg.Items {
			refs[i] = string(it.Ref)
		}
		return refs
	}
	snap.browseIntZero = browseRefs(BrowseQuery{Facets: map[string][]string{"episodes": {"0"}}})
	snap.browseIntMultiRange = browseRefs(BrowseQuery{Facets: map[string][]string{"episodes": {"0..1", "20..30"}}})
	snap.browseDurZero = browseRefs(BrowseQuery{Facets: map[string][]string{"runtime": {"0"}}})
	snap.browseDurMulti = browseRefs(BrowseQuery{Facets: map[string][]string{"runtime": {"10-30m", "over-2h"}}})
	snap.browseFloatZero = browseRefs(BrowseQuery{Facets: map[string][]string{"score": {"0"}}})
	snap.browseFloatMulti = browseRefs(BrowseQuery{Facets: map[string][]string{"score": {"0..2.5", "9.0..10.0"}}})
	snap.browseDateYear = browseRefs(BrowseQuery{Facets: map[string][]string{"aired": {"2024"}}})
	snap.browseDateDecade = browseRefs(BrowseQuery{Facets: map[string][]string{"aired-decade": {"1990"}}})
	snap.browsePeopleFacet = browseRefs(BrowseQuery{Facets: map[string][]string{"cast": {"Elena Vance"}}})
	snap.browsePersonID = browseRefs(BrowseQuery{PersonID: elenaID})

	sp, err := s.SearchPeople(ctx, libID, nil, "Elena", 10, 5)
	if err != nil {
		t.Fatalf("SearchPeople: %v", err)
	}
	for _, p := range sp {
		snap.searchPeopleNames = append(snap.searchPeopleNames, fmt.Sprintf("%d:%s:%d", p.ID, p.Name, p.CreditCount))
	}

	pm, ok, err := s.PersonByID(ctx, libID, nil, elenaID, 20)
	if err != nil || !ok {
		t.Fatalf("PersonByID: ok=%v err=%v", ok, err)
	}
	snap.personByIDName = pm.Name
	snap.personByIDCredits = pm.CreditCount
	snap.personByIDRoles = pm.Roles

	foundID, found, err := s.FindPersonID(ctx, libID, nil, func(pid int64, name string) bool {
		return name == "Elena Vance"
	})
	if err != nil || !found {
		t.Fatalf("FindPersonID: found=%v err=%v", found, err)
	}
	snap.findPersonID = foundID

	if snap.lookupFacets, err = s.LookupPersonFacetsByID(ctx, libID, []string{"cast", "directors"}, elenaID); err != nil {
		t.Fatalf("LookupPersonFacetsByID: %v", err)
	}
	return snap
}

func TestPartialNumericFieldIndexesPlanAndParity(t *testing.T) {
	ctx := context.Background()

	t.Run("migration_7_to_8_upgrade_and_parity", func(t *testing.T) {
		s, db := openTestService(t, Options{})
		if store.DialectOf(db) != store.SQLite {
			t.Skip("SQLite migration/query-plan test")
		}
		const libID core.LibraryID = 71
		elenaID, _ := seedPartialIndexLibrary(t, ctx, s, db, libID, "test.upgrade", 120, 1)

		// Roll back to pre-migration-8 state (full indexes, migration 8 unapplied).
		for _, stmt := range []string{
			`DROP INDEX IF EXISTS plugin_item_fields_lookup_real`,
			`CREATE INDEX plugin_item_fields_lookup_real ON plugin_item_fields(field, v_real, item_ref)`,
			`DROP INDEX IF EXISTS plugin_item_fields_lookup_int`,
			`CREATE INDEX plugin_item_fields_lookup_int ON plugin_item_fields(field, v_int, item_ref)`,
			`DELETE FROM schema_migrations WHERE component = 'pluginitems' AND version = 8`,
			`ANALYZE`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("setup pre-v8 (%s): %v", stmt, err)
			}
		}

		var rowsBefore int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_item_fields`).Scan(&rowsBefore); err != nil {
			t.Fatal(err)
		}
		before := collectParitySnapshot(t, ctx, s, libID, elenaID)

		// Apply migration 8 and check index DDL + unchanged table rows.
		if err := store.Migrate(ctx, db); err != nil {
			t.Fatalf("store.Migrate v7->v8: %v", err)
		}

		var ver8Count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE component = 'pluginitems' AND version = 8`).Scan(&ver8Count); err != nil {
			t.Fatal(err)
		}
		if ver8Count != 1 {
			t.Fatalf("expected pluginitems migration 8 recorded, got %d", ver8Count)
		}

		var realSQL, intSQL string
		if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = 'plugin_item_fields_lookup_real'`).Scan(&realSQL); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = 'plugin_item_fields_lookup_int'`).Scan(&intSQL); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(realSQL, "WHERE v_text = ''") {
			t.Fatalf("lookup_real missing partial predicate: %s", realSQL)
		}
		if !strings.Contains(intSQL, "WHERE v_text = '' OR v_int <> 0") {
			t.Fatalf("lookup_int missing partial predicate: %s", intSQL)
		}

		var rowsAfter int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_item_fields`).Scan(&rowsAfter); err != nil {
			t.Fatal(err)
		}
		if rowsAfter != rowsBefore {
			t.Fatalf("migration 8 changed plugin_item_fields row count: before=%d after=%d", rowsBefore, rowsAfter)
		}

		after := collectParitySnapshot(t, ctx, s, libID, elenaID)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("results differed before vs after migration 8:\nbefore=%+v\nafter =%+v", before, after)
		}

		// Explicitly confirm legitimate zero values and people rows survived.
		if len(after.browseIntZero) == 0 {
			t.Fatalf("expected episodes=0 browse hits, got none")
		}
		if len(after.browseDurZero) == 0 {
			t.Fatalf("expected runtime=0 browse hits, got none")
		}
		if len(after.browseFloatZero) == 0 {
			t.Fatalf("expected score=0.0 browse hits, got none")
		}
		if len(after.browsePeopleFacet) == 0 || after.personByIDCredits == 0 || after.findPersonID != elenaID {
			t.Fatalf("expected Elena Vance people hits, got facet=%d credits=%d findID=%d", len(after.browsePeopleFacet), after.personByIDCredits, after.findPersonID)
		}
	})

	t.Run("query_plans_use_partial_indexes", func(t *testing.T) {
		s, db := openTestService(t, Options{})
		if store.DialectOf(db) != store.SQLite {
			t.Skip("SQLite query-plan test")
		}
		const largeLib core.LibraryID = 81
		const smallLib core.LibraryID = 82

		elenaLarge, _ := seedPartialIndexLibrary(t, ctx, s, db, largeLib, "test.large", smallPluginLibraryMax+100, 1)
		_, _ = seedPartialIndexLibrary(t, ctx, s, db, smallLib, "test.small", 50, 100000)

		if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
			t.Fatalf("ANALYZE: %v", err)
		}

		check := func(name, fieldName string, intVal int64, libID core.LibraryID, wantIndex string, allowFold bool, fn func(context.Context)) {
			t.Helper()
			plans := captureFieldQueryPlans(t, ctx, db, fieldName, intVal, libID, fn)
			for _, p := range plans {
				t.Logf("[%s] PLAN: %s", name, p.plan)
			}
			assertNoFullFieldScan(t, name, plans)
			if wantIndex != "" {
				assertAnyPlanContains(t, name, plans, wantIndex)
			}
			if !allowFold {
				for _, p := range plans {
					if strings.Contains(p.plan, "plugin_item_fields_fold") {
						t.Errorf("%s: unexpectedly fell back to plugin_item_fields_fold:\n  PLAN: %s\n  SQL:  %s", name, p.plan, strings.Join(strings.Fields(p.sql), " "))
					}
				}
			}
		}

		airedSample := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

		// Large-library facet distinct walks & span buckets.
		check("FacetBuckets/int/large", "episodes", 0, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "episodes")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets episodes: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/float/large", "score", 0, largeLib, "plugin_item_fields_lookup_real", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "score")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets score: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/duration/large", "runtime", 0, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "runtime")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets runtime: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/date/large", "aired", airedSample, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "aired")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets aired: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/date_decade/large", "aired", airedSample, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "aired-decade")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets aired-decade: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/people/large", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, largeLib, "", nil, "cast")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets cast: len=%d err=%v", len(got), err)
			}
		})

		// FacetCollections (includes resolveNewestByIntBuckets / Float / PersonIDs).
		check("FacetCollections/int/large", "episodes", 0, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.FacetCollections(c, FacetCollectionsQuery{LibraryID: largeLib, FieldKey: "episodes", Limit: 10})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("FacetCollections episodes: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("FacetCollections/float/large", "score", 0, largeLib, "plugin_item_fields_lookup_real", false, func(c context.Context) {
			pg, err := s.FacetCollections(c, FacetCollectionsQuery{LibraryID: largeLib, FieldKey: "score", Limit: 10})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("FacetCollections score: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("FacetCollections/people/large", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.FacetCollections(c, FacetCollectionsQuery{LibraryID: largeLib, FieldKey: "cast", Limit: 10})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("FacetCollections cast: len=%d err=%v", len(pg.Items), err)
			}
		})
		// Folded search stays on plugin_item_fields_fold while artwork resolution uses lookup_int.
		{
			plans := captureFieldQueryPlans(t, ctx, db, "cast", elenaLarge, largeLib, func(c context.Context) {
				pg, err := s.FacetCollections(c, FacetCollectionsQuery{LibraryID: largeLib, FieldKey: "cast", Query: "elena", Limit: 10})
				if err != nil || len(pg.Items) != 1 {
					t.Fatalf("FacetCollections cast query: len=%d err=%v", len(pg.Items), err)
				}
			})
			for _, p := range plans {
				t.Logf("[FacetCollections/people_query] PLAN: %s", p.plan)
			}
			assertNoFullFieldScan(t, "FacetCollections/people_query", plans)
			assertAnyPlanContains(t, "FacetCollections/people_query", plans, "plugin_item_fields_fold")
			assertAnyPlanContains(t, "FacetCollections/people_query", plans, "plugin_item_fields_lookup_int")
		}

		// FacetPeople (unfiltered + folded search + sample-items CTE).
		check("FacetPeople/all", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.FacetPeople(c, FacetPeopleQuery{LibraryID: largeLib, FieldKey: "cast", Limit: 10})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("FacetPeople all: len=%d err=%v", len(pg.Items), err)
			}
		})
		{
			plans := captureFieldQueryPlans(t, ctx, db, "cast", elenaLarge, largeLib, func(c context.Context) {
				pg, err := s.FacetPeople(c, FacetPeopleQuery{LibraryID: largeLib, FieldKey: "cast", Query: "elena", Limit: 10})
				if err != nil || len(pg.Items) != 1 {
					t.Fatalf("FacetPeople query: len=%d err=%v", len(pg.Items), err)
				}
			})
			for _, p := range plans {
				t.Logf("[FacetPeople/query] PLAN: %s", p.plan)
			}
			assertNoFullFieldScan(t, "FacetPeople/query", plans)
			assertAnyPlanContains(t, "FacetPeople/query", plans, "plugin_item_fields_lookup_int")
		}

		// Browse exact, range, multi-range, date, duration, people & personID filters.
		check("Browse/int_zero", "episodes", 0, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"episodes": {"0"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse episodes=0: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/int_range", "episodes", 2, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"episodes": {"0..4"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse episodes=0..4: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/int_multi_range", "episodes", 2, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"episodes": {"0..1", "20..30"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse episodes multi-range: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/duration_multi", "runtime", 7200, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"runtime": {"10-30m", "over-2h"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse runtime multi: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/float_zero", "score", 0, largeLib, "plugin_item_fields_lookup_real", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"score": {"0"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse score=0: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/float_range", "score", 0, largeLib, "plugin_item_fields_lookup_real", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"score": {"0..2.5"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse score=0..2.5: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/float_multi_range", "score", 0, largeLib, "plugin_item_fields_lookup_real", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"score": {"0..2.5", "9.0..10.0"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse score multi-range: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/date_decade", "aired", airedSample, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"aired-decade": {"1990"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse aired-decade=1990: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/people_facet", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, Facets: map[string][]string{"cast": {"Elena Vance"}}, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse cast=Elena Vance: len=%d err=%v", len(pg.Items), err)
			}
		})
		check("Browse/person_id", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pg, err := s.Browse(c, BrowseQuery{LibraryID: largeLib, PersonID: elenaLarge, Limit: 20})
			if err != nil || len(pg.Items) == 0 {
				t.Fatalf("Browse PersonID: len=%d err=%v", len(pg.Items), err)
			}
		})

		// People & expand queries.
		check("PersonByID", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			pm, ok, err := s.PersonByID(c, largeLib, nil, elenaLarge, 20)
			if err != nil || !ok || pm.CreditCount == 0 {
				t.Fatalf("PersonByID: ok=%v credits=%d err=%v", ok, pm.CreditCount, err)
			}
		})
		check("LookupPersonFacetsByID", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", false, func(c context.Context) {
			ms, err := s.LookupPersonFacetsByID(c, largeLib, []string{"cast", "directors"}, elenaLarge)
			if err != nil || len(ms) != 2 {
				t.Fatalf("LookupPersonFacetsByID: len=%d err=%v", len(ms), err)
			}
		})
		check("SearchPeople", "cast", elenaLarge, largeLib, "plugin_item_fields_lookup_int", true, func(c context.Context) {
			ps, err := s.SearchPeople(c, largeLib, nil, "Elena", 10, 5)
			if err != nil || len(ps) == 0 {
				t.Fatalf("SearchPeople: len=%d err=%v", len(ps), err)
			}
		})
		check("FindPersonID", "cast", elenaLarge, largeLib, "", true, func(c context.Context) {
			id, ok, err := s.FindPersonID(c, largeLib, nil, func(pid int64, name string) bool { return name == "Elena Vance" })
			if err != nil || !ok || id != elenaLarge {
				t.Fatalf("FindPersonID: id=%d ok=%v err=%v", id, ok, err)
			}
		})

		// Small-library FacetBuckets paths (small library drives from plugin_items PK).
		check("FacetBuckets/int/small", "episodes", 0, smallLib, "", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, smallLib, "", nil, "episodes")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets small episodes: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/float/small", "score", 0, smallLib, "", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, smallLib, "", nil, "score")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets small score: len=%d err=%v", len(got), err)
			}
		})
		check("FacetBuckets/people/small", "cast", elenaLarge, smallLib, "", false, func(c context.Context) {
			got, err := s.FacetBuckets(c, smallLib, "", nil, "cast")
			if err != nil || len(got) == 0 {
				t.Fatalf("FacetBuckets small cast: len=%d err=%v", len(got), err)
			}
		})

		// libraryfilter.AppendPluginFacetClauses queries.
		fieldTypes := map[string]string{
			"episodes": "int",
			"runtime":  "duration",
			"score":    "float",
			"aired":    "date",
			"cast":     "people",
		}
		for _, tc := range []struct {
			name      string
			facets    map[string][]string
			wantIndex string
		}{
			{"libraryfilter/date_decade", map[string][]string{"aired-decade": {"1990"}}, ""},
			{"libraryfilter/int_zero", map[string][]string{"episodes": {"0"}}, ""},
			{"libraryfilter/float_zero", map[string][]string{"score": {"0"}}, "plugin_item_fields_lookup_real"},
			{"libraryfilter/people", map[string][]string{"cast": {fmt.Sprintf("%d", elenaLarge)}}, "plugin_item_fields_lookup_int"},
		} {
			var where strings.Builder
			args := []any{int64(largeLib)}
			where.WriteString(`i.library_id = ?`)
			libraryfilter.AppendPluginFacetClauses(&where, &args, tc.facets, fieldTypes)
			q := `SELECT COUNT(*) FROM plugin_items i WHERE ` + where.String()
			plan := explainSQLitePlan(t, ctx, db, q, args...)
			t.Logf("[%s] PLAN: %s", tc.name, plan)
			if strings.Contains(plan, "SCAN f") {
				t.Fatalf("%s regressed to SCAN f: %s", tc.name, plan)
			}
			if tc.wantIndex != "" && !strings.Contains(plan, tc.wantIndex) {
				t.Fatalf("%s expected %q in plan, got: %s", tc.name, tc.wantIndex, plan)
			}
			var cnt int
			if err := db.QueryRowContext(ctx, q, args...).Scan(&cnt); err != nil || cnt == 0 {
				t.Fatalf("%s expected matching rows, got cnt=%d err=%v", tc.name, cnt, err)
			}
		}
	})
}

package pluginitems

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func narrowDriverTestDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "item",
		Label:   "Items",
		Library: plugins.KindLibraryDecl{Name: "Items"},
		Levels: []plugins.KindLevel{
			{Key: "item", Label: "Item", Playable: true},
			{Key: "child", Label: "Child", Playable: true},
		},
		Fields: []plugins.KindField{
			{Key: "available", Type: "enum", Label: "Available", Indexed: true, Enum: []string{"yes", "no"}},
			{Key: "bucket", Type: "strings", Label: "Bucket", Indexed: true},
			{Key: "other_flag", Type: "enum", Label: "Other Flag", Indexed: true, Enum: []string{"yes"}},
			{Key: "episodes", Type: "int", Label: "Episodes", Indexed: true},
			{Key: "score", Type: "float", Label: "Score", Indexed: true},
			{Key: "cast", Type: "people", Label: "Cast"},
			{Key: "optional", Type: "string", Label: "Optional", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"released", "title", "added", "year", "rating", "last_viewed"},
			Facets: []string{"available", "bucket", "other_flag", "episodes", "score", "cast"},
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
}

func TestBrowseNarrowDriverEquivalence(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const (
		libID    core.LibraryID = 808
		pluginID                = "test.narrow"
		kindKey                 = "item"
	)
	if _, err := s.UpsertKind(ctx, pluginID, libID, narrowDriverTestDecl(), false); err != nil {
		t.Fatal(err)
	}

	// Build 180 root items plus 12 children under item-000.
	// Includes overlapping multi-value buckets, 255/256/300-rune prefix boundary
	// values, people credits, and numeric fields.
	prefix255 := strings.Repeat("x", 255)
	prefix256 := strings.Repeat("x", 256)
	longA := prefix256 + "-alpha"
	longB := prefix256 + "-beta"

	type seedRow struct {
		id       string
		title    string
		sort     string
		released int64
		added    int64
		year     int
		rating   float64
		avail    string
		buckets  []string
		episodes int64
		score    float64
		cast     []string
		summary  string
		optional bool
	}

	var roots []seedRow
	var items []core.KindItem
	for i := 0; i < 180; i++ {
		id := fmt.Sprintf("it-%03d", i)
		avail := "no"
		if i%10 == 0 || i < 4 {
			avail = "yes"
		}
		buckets := []string{fmt.Sprintf("b%02d", i%12)}
		// Give overlapping positive buckets on every 3rd item so multi-value
		// driver deduplication is exercised.
		if i%3 == 0 {
			buckets = append(buckets, fmt.Sprintf("b%02d", (i+1)%12))
		}
		if i%15 == 0 {
			buckets = append(buckets, "rare-tag")
		}
		switch i {
		case 0:
			buckets = append(buckets, prefix255)
		case 1:
			buckets = append(buckets, prefix256)
		case 2:
			buckets = append(buckets, longA)
		case 3:
			buckets = append(buckets, longB)
		}
		cast := []string{fmt.Sprintf("Actor %02d", i%25)}
		if i%18 == 0 {
			cast = append(cast, "Rare Lead")
		}
		ep := int64(i % 15)
		sc := float64(i%10) + 0.5
		title := fmt.Sprintf("Title %03d", 180-i)
		rel := int64(1700000000 + (i/3)*1000) // deliberate ties for item_ref tie-breaker
		added := int64(1700000000 + (i%40)*100)
		yr := 2000 + (i % 20)

		castRaw := make([]map[string]any, len(cast))
		for ci, name := range cast {
			castRaw[ci] = map[string]any{"name": name}
		}

		summary := ""
		if i%20 == 0 {
			summary = "Has a summary"
			title += " Skipmarker"
		}
		optional := i%40 == 0
		roots = append(roots, seedRow{
			id: id, title: title, sort: strings.ToLower(title),
			released: rel, added: added, year: yr, rating: sc,
			avail: avail, buckets: buckets, episodes: ep, score: sc, cast: cast, summary: summary, optional: optional,
		})
		items = append(items, core.KindItem{
			ID:       id,
			Level:    "item",
			Title:    title,
			Summary:  summary,
			Released: rel,
			AddedAt:  added,
			Year:     yr,
			Rating:   sc,
			Playable: true,
			Fields: map[string]json.RawMessage{
				"available":  rawJSON(t, avail),
				"bucket":     rawJSON(t, buckets),
				"other_flag": rawJSON(t, "yes"),
				"episodes":   rawJSON(t, ep),
				"score":      rawJSON(t, sc),
				"cast":       rawJSON(t, castRaw),
			},
		})
		if optional {
			items[len(items)-1].Fields["optional"] = rawJSON(t, "present")
		}
	}
	// Children under it-000 for parent-scoped multi-facet browse.
	for c := 0; c < 12; c++ {
		avail := "no"
		if c%2 == 0 {
			avail = "yes"
		}
		b := "b00"
		if c%3 == 0 {
			b = "b05"
		}
		items = append(items, core.KindItem{
			ID:       fmt.Sprintf("ch-%02d", c),
			ParentID: "it-000",
			Level:    "child",
			Index:    c + 1,
			Title:    fmt.Sprintf("Child %02d", c),
			Playable: true,
			Fields: map[string]json.RawMessage{
				"available": rawJSON(t, avail),
				"bucket":    rawJSON(t, []string{b}),
			},
		})
	}
	if _, err := s.UpsertItems(ctx, pluginID, kindKey, items); err != nil {
		t.Fatal(err)
	}

	// Mark a few items watched for viewer 7 so last_viewed Phase 0 + Phase 1
	// and Watched filters are covered.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS state_watch (
		user_id BIGINT NOT NULL,
		rating_key BIGINT NOT NULL,
		view_count BIGINT NOT NULL DEFAULT 0,
		last_viewed_at BIGINT NOT NULL DEFAULT 0,
		updated_at BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, rating_key)
	)`); err != nil {
		t.Fatal(err)
	}
	const viewerID int64 = 7
	watchedIDs := map[string]int64{
		"it-000": 1710000300,
		"it-010": 1710000200,
		"it-030": 1710000100,
	}
	for id, ts := range watchedIDs {
		var seq int64
		ref := string(core.NewItemRef(pluginID, kindKey, id))
		if err := db.QueryRowContext(ctx, `SELECT seq FROM plugin_items WHERE item_ref = ?`, ref).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO state_watch(user_id, rating_key, view_count, last_viewed_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
			viewerID, core.PluginKeyOffset+seq, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	hasAny := func(have, want []string) bool {
		for _, w := range want {
			if slices.Contains(have, w) {
				return true
			}
		}
		return false
	}
	hasAll := func(have, want []string) bool {
		for _, w := range want {
			if !slices.Contains(have, w) {
				return false
			}
		}
		return true
	}

	// Helper to paginate through all pages of q and return ordered IDs + total.
	browseAll := func(t *testing.T, q BrowseQuery) ([]string, int) {
		t.Helper()
		var got []string
		seen := map[string]bool{}
		total := -1
		cursor := ""
		for pageNum := 0; pageNum < 100; pageNum++ {
			q.Cursor = cursor
			pg, err := s.Browse(ctx, q)
			if err != nil {
				t.Fatalf("page %d: %v", pageNum, err)
			}
			if !pg.TotalExact {
				t.Fatalf("page %d: expected TotalExact=true, got false (total=%d)", pageNum, pg.Total)
			}
			if total < 0 {
				total = pg.Total
			} else if pg.Total != total {
				t.Fatalf("page %d: total drifted %d -> %d", pageNum, total, pg.Total)
			}
			for _, it := range pg.Items {
				if seen[it.ID] {
					t.Fatalf("duplicate item %q across pages", it.ID)
				}
				seen[it.ID] = true
				got = append(got, it.ID)
			}
			if pg.NextCursor == "" {
				break
			}
			cursor = pg.NextCursor
		}
		if len(got) != total {
			t.Fatalf("collected %d items across pages, total=%d", len(got), total)
		}
		return got, total
	}

	sortRows := func(rows []seedRow, sortKey string) []string {
		cp := append([]seedRow(nil), rows...)
		sort.Slice(cp, func(i, j int) bool {
			switch sortKey {
			case "released:desc":
				if cp[i].released != cp[j].released {
					return cp[i].released > cp[j].released
				}
			case "added:desc":
				if cp[i].added != cp[j].added {
					return cp[i].added > cp[j].added
				}
			case "rating:desc":
				if cp[i].rating != cp[j].rating {
					return cp[i].rating > cp[j].rating
				}
			case "year:desc":
				if cp[i].year != cp[j].year {
					return cp[i].year > cp[j].year
				}
			default: // title:asc
				if cp[i].sort != cp[j].sort {
					return cp[i].sort < cp[j].sort
				}
			}
			return cp[i].id < cp[j].id
		})
		ids := make([]string, len(cp))
		for i, r := range cp {
			ids[i] = r.id
		}
		return ids
	}

	filterRoots := func(pred func(seedRow) bool) []seedRow {
		var out []seedRow
		for _, r := range roots {
			if pred(r) {
				out = append(out, r)
			}
		}
		return out
	}

	rareLeadID := core.PluginPersonID("Rare Lead")
	policyExcludeB11, err := s.CompilePolicy(ctx, libID, []core.LibraryExclusion{{
		Action:    "hide",
		MediaType: "mixed",
		Match:     "all",
		Rules: []core.CollectionRule{
			{Field: "browse:bucket", Op: "is", Value: json.RawMessage(`"b11"`)},
		},
	}}, "show")
	if err != nil {
		t.Fatalf("CompilePolicy: %v", err)
	}

	type browseCase struct {
		name  string
		q     BrowseQuery
		match func(seedRow) bool
	}
	newFilterCases := []browseCase{
		{
			name: "empty_plus_facets",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}, "bucket": {"b00", "b04", "b08"}},
				Empty:     map[string]bool{"optional": true, "summary": false},
				Limit:     2,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" && hasAny(r.buckets, []string{"b00", "b04", "b08"}) && !r.optional && r.summary != ""
			},
		},
		{
			name: "cross_field_group_plus_exclusion",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}},
				AnyGroups: []core.KindBrowseFacetGroup{{Any: []core.KindBrowseFacetAlt{
					{Field: "bucket", Values: []string{"b02"}},
					{Field: "year", Values: []string{"2000"}},
					{Field: "episodes", Values: []string{"invalid"}},
				}}},
				ExcludeFacets: map[string][]string{"bucket": {"b08"}},
				Limit:         2,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" && (slices.Contains(r.buckets, "b02") || r.year == 2000) && !slices.Contains(r.buckets, "b08")
			},
		},
		{
			name: "exclude_query_plus_facets",
			q: BrowseQuery{
				LibraryID:    libID,
				Level:        "item",
				Facets:       map[string][]string{"available": {"yes"}, "bucket": {"b00", "b02", "b04", "b06", "b08"}},
				ExcludeQuery: "skipmark",
				Limit:        2,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" && hasAny(r.buckets, []string{"b00", "b02", "b04", "b06", "b08"}) && r.summary == ""
			},
		},
	}
	for _, tc := range newFilterCases {
		tally, err := s.browseTallyProbe(ctx, tc.q, "", tc.q.Level)
		if err != nil {
			t.Fatal(err)
		}
		col, kind, desc, defaultDesc := parseBrowseSort(tc.q)
		plan, err := s.buildBrowsePlan(ctx, tc.q, "", tc.q.Level, col, kind, desc, defaultDesc, tc.q.Limit, tally)
		if err != nil {
			t.Fatal(err)
		}
		if tally.totalExact || plan.driver == nil || !slices.Equal(plan.driver.cteArgs, []any{"available", "yes"}) {
			t.Fatalf("%s: expected a narrow available driver and no tally total, got driver=%v tally=%+v", tc.name, plan.driver, tally)
		}
	}

	cases := []browseCase{
		{
			name: "narrow_enum_plus_broad_multi_plus_exclude",
			q: BrowseQuery{
				LibraryID:     libID,
				Level:         "item",
				Facets:        map[string][]string{"available": {"yes"}, "bucket": {"b00", "b01", "b02", "b03"}},
				ExcludeFacets: map[string][]string{"bucket": {"b04", "b05", "b06", "b07", "b08", "b09", "b10", "b11"}},
				Limit:         4,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" &&
					hasAny(r.buckets, []string{"b00", "b01", "b02", "b03"}) &&
					!hasAny(r.buckets, []string{"b04", "b05", "b06", "b07", "b08", "b09", "b10", "b11"})
			},
		},
		{
			name: "overlapping_multi_value_driver_no_duplicates",
			q: BrowseQuery{
				LibraryID:     libID,
				Level:         "item",
				Facets:        map[string][]string{"bucket": {"b00", "b01", "b02"}},
				ExcludeFacets: map[string][]string{"available": {"no"}},
				Limit:         5,
			},
			match: func(r seedRow) bool {
				return hasAny(r.buckets, []string{"b00", "b01", "b02"}) && r.avail != "no"
			},
		},
		{
			name: "all_facets_rare_and_common",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				AllFacets: map[string][]string{"bucket": {"b00", "rare-tag"}},
				Limit:     3,
			},
			match: func(r seedRow) bool {
				return hasAll(r.buckets, []string{"b00", "rare-tag"})
			},
		},
		{
			name: "any_groups_plus_facets_plus_exclude",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}},
				AnyGroups: []core.KindBrowseFacetGroup{
					{Field: "bucket", Values: []string{"b00", "b02", "b04", "b06"}},
					{Field: "bucket", Values: []string{"rare-tag", "b01", "b03", "b04"}},
				},
				ExcludeFacets: map[string][]string{"bucket": {"b06"}},
				Limit:         3,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" &&
					hasAny(r.buckets, []string{"b00", "b02", "b04", "b06"}) &&
					hasAny(r.buckets, []string{"rare-tag", "b01", "b03", "b04"}) &&
					!hasAny(r.buckets, []string{"b06"})
			},
		},
		{
			name: "exclusion_only_not_exists",
			q: BrowseQuery{
				LibraryID:     libID,
				Level:         "item",
				ExcludeFacets: map[string][]string{"bucket": {"b00", "b01", "b02", "b03", "b04", "b05"}},
				Limit:         15,
			},
			match: func(r seedRow) bool {
				return !hasAny(r.buckets, []string{"b00", "b01", "b02", "b03", "b04", "b05"})
			},
		},
		{
			name: "long_prefix_boundary_255_vs_256_vs_300",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}, "bucket": {prefix255, longA}},
				Limit:     10,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" && hasAny(r.buckets, []string{prefix255, longA})
			},
		},
		{
			name: "exact_256_rune_must_not_match_300_rune_sharing_prefix",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}, "bucket": {prefix256}},
				Limit:     10,
			},
			match: func(r seedRow) bool {
				return r.avail == "yes" && hasAny(r.buckets, []string{prefix256})
			},
		},
		{
			name: "person_id_plus_facets_plus_numeric",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				PersonID:  rareLeadID,
				Facets:    map[string][]string{"available": {"yes"}, "episodes": {"0..6"}},
				Limit:     4,
			},
			match: func(r seedRow) bool {
				return slices.Contains(r.cast, "Rare Lead") && r.avail == "yes" && r.episodes >= 0 && r.episodes <= 6
			},
		},
		{
			name: "policy_plus_multi_facet_plus_float_range",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Policy:    policyExcludeB11,
				Facets:    map[string][]string{"available": {"yes"}, "score": {"0.5..5.5"}},
				Limit:     4,
			},
			match: func(r seedRow) bool {
				return !slices.Contains(r.buckets, "b11") && r.avail == "yes" && r.score >= 0.5 && r.score <= 5.5
			},
		},
		{
			name: "empty_from_zero_tally_facet",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				Facets:    map[string][]string{"available": {"yes"}, "bucket": {"does-not-exist"}},
				Limit:     10,
			},
			match: func(r seedRow) bool { return false },
		},
		{
			name: "empty_from_single_valued_all_contradiction",
			q: BrowseQuery{
				LibraryID: libID,
				Level:     "item",
				AllFacets: map[string][]string{"available": {"yes", "no"}},
				Limit:     10,
			},
			match: func(r seedRow) bool { return false },
		},
	}

	for _, tc := range append(cases, newFilterCases...) {
		for _, sortKey := range []string{"released:desc", "title:asc"} {
			t.Run(tc.name+"/"+sortKey, func(t *testing.T) {
				q := tc.q
				q.Sort = sortKey
				got, _ := browseAll(t, q)
				want := sortRows(filterRoots(tc.match), sortKey)
				if !slices.Equal(got, want) {
					t.Fatalf("mismatch:\ngot  (%d): %v\nwant (%d): %v", len(got), got, len(want), want)
				}
			})
		}
	}

	// Verify last_viewed sort (Phase 0 + Phase 1 + cursor across phase boundary + exact total)
	// with a narrow driver + multi-facet + exclusion.
	t.Run("last_viewed_multi_facet_paging", func(t *testing.T) {
		q := BrowseQuery{
			LibraryID:     libID,
			Level:         "item",
			Sort:          "last_viewed:desc",
			ViewerID:      viewerID,
			Facets:        map[string][]string{"available": {"yes"}, "bucket": {"b00", "b01", "b02", "b03", "b06", "b07", "b08", "b09", "b10", "b11"}},
			ExcludeFacets: map[string][]string{"bucket": {"b04", "b05"}},
			Limit:         2,
		}
		got, total := browseAll(t, q)
		matched := filterRoots(func(r seedRow) bool {
			return r.avail == "yes" &&
				hasAny(r.buckets, []string{"b00", "b01", "b02", "b03", "b06", "b07", "b08", "b09", "b10", "b11"}) &&
				!hasAny(r.buckets, []string{"b04", "b05"})
		})
		if total != len(matched) {
			t.Fatalf("last_viewed total = %d, want %d", total, len(matched))
		}
		// Phase 0: watched items descending by last_viewed_at, then Phase 1: unviewed by sort_title ASC.
		var p0, p1 []seedRow
		for _, r := range matched {
			if _, ok := watchedIDs[r.id]; ok {
				p0 = append(p0, r)
			} else {
				p1 = append(p1, r)
			}
		}
		sort.Slice(p0, func(i, j int) bool {
			ti, tj := watchedIDs[p0[i].id], watchedIDs[p0[j].id]
			if ti != tj {
				return ti > tj
			}
			return p0[i].id < p0[j].id
		})
		sort.Slice(p1, func(i, j int) bool {
			if p1[i].sort != p1[j].sort {
				return p1[i].sort < p1[j].sort
			}
			return p1[i].id < p1[j].id
		})
		var want []string
		for _, r := range append(p0, p1...) {
			want = append(want, r.id)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("last_viewed mismatch:\ngot:  %v\nwant: %v", got, want)
		}
	})

	// Verify parentRef-scoped multi-facet browse (drives from parent_ref + correlated EXISTS/NOT EXISTS).
	t.Run("parent_scoped_multi_facet", func(t *testing.T) {
		pg, err := s.Browse(ctx, BrowseQuery{
			LibraryID:     libID,
			ParentRef:     core.NewItemRef(pluginID, kindKey, "it-000"),
			Level:         "child",
			Sort:          "index:asc",
			Facets:        map[string][]string{"available": {"yes"}},
			ExcludeFacets: map[string][]string{"bucket": {"b05"}},
			Limit:         20,
		})
		if err != nil {
			t.Fatal(err)
		}
		// Children 0..11 with c%2==0 and c%3!=0 => 2, 4, 8, 10.
		want := []string{"ch-02", "ch-04", "ch-08", "ch-10"}
		got := make([]string, len(pg.Items))
		for i, it := range pg.Items {
			got[i] = it.ID
		}
		if !slices.Equal(got, want) || pg.Total != len(want) {
			t.Fatalf("parent-scoped got=%v total=%d, want=%v", got, pg.Total, want)
		}
	})

	// Verify BrowseBatch matches individual Browse across narrow-driver, exclusion-only,
	// policy-filtered, empty, and unfiltered shelves.
	t.Run("browse_batch_parity", func(t *testing.T) {
		batchQueries := []BrowseQuery{
			{
				LibraryID:     libID,
				Level:         "item",
				Sort:          "released:desc",
				Facets:        map[string][]string{"available": {"yes"}, "bucket": {"b00", "b01", "b02", "b03"}},
				ExcludeFacets: map[string][]string{"bucket": {"b04", "b05", "b06", "b07", "b08", "b09", "b10", "b11"}},
				NoTotal:       true,
				Limit:         6,
			},
			{
				LibraryID:     libID,
				Level:         "item",
				Sort:          "title:asc",
				ExcludeFacets: map[string][]string{"bucket": {"b00", "b01"}},
				NoTotal:       true,
				Limit:         6,
			},
			{
				LibraryID: libID,
				Level:     "item",
				Sort:      "added:desc",
				Policy:    policyExcludeB11,
				Facets:    map[string][]string{"available": {"yes"}, "cast": {"Rare Lead"}},
				NoTotal:   true,
				Limit:     6,
			},
			{
				LibraryID: libID,
				Level:     "item",
				Sort:      "released:desc",
				Facets:    map[string][]string{"available": {"yes"}, "bucket": {"nope"}},
				NoTotal:   true,
				Limit:     6,
			},
			{
				LibraryID: libID,
				Level:     "item",
				Sort:      "released:desc",
				NoTotal:   true,
				Limit:     6,
			},
		}
		for _, tc := range newFilterCases {
			q := tc.q
			q.NoTotal = true
			batchQueries = append(batchQueries, q)
		}
		batchPages, err := s.BrowseBatch(ctx, batchQueries)
		if err != nil {
			t.Fatalf("BrowseBatch: %v", err)
		}
		for i, q := range batchQueries {
			single, err := s.Browse(ctx, q)
			if err != nil {
				t.Fatalf("Browse[%d]: %v", i, err)
			}
			gotIDs := make([]string, len(batchPages[i].Items))
			for j, it := range batchPages[i].Items {
				gotIDs[j] = it.ID
			}
			wantIDs := make([]string, len(single.Items))
			for j, it := range single.Items {
				wantIDs[j] = it.ID
			}
			if !slices.Equal(gotIDs, wantIDs) || batchPages[i].NextCursor != single.NextCursor {
				t.Fatalf("batch[%d] diverged:\ngot  ids=%v cur=%q\nwant ids=%v cur=%q",
					i, gotIDs, batchPages[i].NextCursor, wantIDs, single.NextCursor)
			}
		}
	})
}

func TestBrowseNarrowDriverPlanRegression(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const (
		libID      core.LibraryID = 809
		pluginID                  = "test.plan808"
		kindKey                   = "item"
		totalItems                = 3000
		numBuckets                = 120
	)
	if _, err := s.UpsertKind(ctx, pluginID, libID, narrowDriverTestDecl(), false); err != nil {
		t.Fatal(err)
	}

	isPG := store.DialectOf(db) == store.Postgres
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
	) VALUES (?, ?, ?, ?, ?, ?, '', 'item', ?, ?, ?, '', ?, ?, ?, 1, 0, 0, '', '{}', '{}', 'e1', ?, ?, ?, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	insertFieldSQL := `INSERT OR IGNORE INTO plugin_item_fields (item_ref, field, v_text, v_int, v_real, v_fold) VALUES (?, ?, ?, 0, 0, ?)`
	if isPG {
		insertFieldSQL = `INSERT INTO plugin_item_fields (item_ref, field, v_text, v_int, v_real, v_fold) VALUES (?, ?, ?, 0, 0, ?) ON CONFLICT DO NOTHING`
	}
	fieldStmt, err := tx.PrepareContext(ctx, insertFieldSQL)
	if err != nil {
		t.Fatal(err)
	}

	counts := map[[2]string]int{}
	wantTotal := 0
	for g := 1; g <= totalItems; g++ {
		ext := fmt.Sprintf("i%05d", g)
		ref := string(core.NewItemRef(pluginID, kindKey, ext))
		rel := int64(1700000000 + ((g * 7919) % totalItems))
		added := int64(1700000000 + g)
		if _, err := itemStmt.ExecContext(ctx, ref, g, pluginID, kindKey, int64(libID), ext, g, "Item "+ext, "item "+ext, 2000+(g%20), rel, float64(g%10), added, added, added); err != nil {
			t.Fatal(err)
		}
		avail := "no"
		if g%150 == 0 || g == 1 { // 21 items with available='yes'
			avail = "yes"
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "available", avail, avail); err != nil {
			t.Fatal(err)
		}
		counts[[2]string{"available", avail}]++
		// Adversarial other_flag='yes' on every row so PG's independent
		// per-column MCV estimate overestimates (field='available', v_text='yes').
		if _, err := fieldStmt.ExecContext(ctx, ref, "other_flag", "yes", "yes"); err != nil {
			t.Fatal(err)
		}
		counts[[2]string{"other_flag", "yes"}]++

		var b1 string
		inPos := g%5 < 2
		if inPos {
			b1 = fmt.Sprintf("b%03d", g%6)
		} else {
			b1 = fmt.Sprintf("b%03d", 6+(g%114))
		}
		if _, err := fieldStmt.ExecContext(ctx, ref, "bucket", b1, b1); err != nil {
			t.Fatal(err)
		}
		counts[[2]string{"bucket", b1}]++
		inExc := !inPos
		if g%7 == 0 {
			b2 := fmt.Sprintf("b%03d", 6+((g*7)%114))
			if b2 != b1 {
				if _, err := fieldStmt.ExecContext(ctx, ref, "bucket", b2, b2); err != nil {
					t.Fatal(err)
				}
				counts[[2]string{"bucket", b2}]++
				inExc = true
			}
		}
		if avail == "yes" && inPos && !inExc {
			wantTotal++
		}
	}
	itemStmt.Close()
	fieldStmt.Close()

	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_item_tally (library_id, level, n) VALUES (?, 'item', ?)`, int64(libID), totalItems); err != nil {
		t.Fatal(err)
	}
	tallyStmt, err := tx.PrepareContext(ctx, `INSERT INTO plugin_item_field_tally (library_id, level, field, v_text, v_lower, v_fold, cnt) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for k, cnt := range counts {
		for _, lv := range []string{"item", ""} {
			if _, err := tallyStmt.ExecContext(ctx, int64(libID), lv, k[0], k[1], k[1], k[1], cnt); err != nil {
				t.Fatal(err)
			}
		}
	}
	tallyStmt.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_items_seq SET next = ? WHERE id = 1`, totalItems+1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	posBuckets := make([]string, 6)
	for i := range posBuckets {
		posBuckets[i] = fmt.Sprintf("b%03d", i)
	}
	excBuckets := make([]string, numBuckets-6)
	for i := range excBuckets {
		excBuckets[i] = fmt.Sprintf("b%03d", i+6)
	}

	q := BrowseQuery{
		LibraryID: libID,
		Level:     "item",
		Sort:      "released:desc",
		Facets: map[string][]string{
			"available": {"yes"},
			"bucket":    posBuckets,
		},
		ExcludeFacets: map[string][]string{
			"bucket": excBuckets,
		},
		Limit: 10,
	}

	page, err := s.Browse(ctx, q)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if !page.TotalExact || page.Total != wantTotal || len(page.Items) != min(q.Limit, wantTotal) {
		t.Fatalf("Browse got len=%d total=%d exact=%v, want total=%d", len(page.Items), page.Total, page.TotalExact, wantTotal)
	}

	col, kind, desc, defaultDesc := parseBrowseSort(q)
	tally, err := s.browseTallyProbe(ctx, q, "", "item")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.buildBrowsePlan(ctx, q, "", "item", col, kind, desc, defaultDesc, q.Limit, tally)
	if err != nil {
		t.Fatal(err)
	}
	if plan.driver == nil {
		t.Fatalf("expected narrow driver to be selected, got nil")
	}
	if len(plan.driver.cteArgs) == 0 || plan.driver.cteArgs[0] != "available" {
		t.Fatalf("expected driver field 'available', got cteArgs=%v", plan.driver.cteArgs)
	}

	pageSQL, pageArgs := plan.pageSQLAndArgs(isPG, prefixedItemCols("i."), plan.baseWhere, plan.baseArgs, "i.released DESC, i.item_ref ASC", q.Limit+1)
	countSQL, countArgs := plan.countSQLAndArgs(isPG)

	if isPG {
		// Verify both page and count on PostgreSQL stay strictly bounded to the
		// narrow driver (no Seq Scan, no hashed SubPlan, actual _drv rows <= 100
		// out of 3,000 items).
		type pgPlanNode struct {
			NodeType   string       `json:"Node Type"`
			Relation   string       `json:"Relation Name"`
			IndexName  string       `json:"Index Name"`
			CTEName    string       `json:"CTE Name"`
			Subplan    string       `json:"Subplan Name"`
			ActualRows float64      `json:"Actual Rows"`
			Loops      float64      `json:"Actual Loops"`
			Plans      []pgPlanNode `json:"Plans"`
		}
		checkPG := func(label, sqlText string, args []any) {
			t.Helper()
			var rawJSONStr string
			if err := db.QueryRowContext(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+sqlText, args...).Scan(&rawJSONStr); err != nil {
				t.Fatalf("%s EXPLAIN: %v", label, err)
			}
			var roots []struct {
				Plan pgPlanNode `json:"Plan"`
			}
			if err := json.Unmarshal([]byte(rawJSONStr), &roots); err != nil || len(roots) == 0 {
				t.Fatalf("%s decode EXPLAIN JSON: %v", label, err)
			}
			var sawLookup, sawPkeyProbe bool
			var walk func(n pgPlanNode)
			walk = func(n pgPlanNode) {
				if strings.Contains(n.NodeType, "Seq Scan") {
					t.Errorf("%s: unexpected %s on %s", label, n.NodeType, n.Relation)
				}
				if strings.Contains(n.Subplan, "hashed") {
					t.Errorf("%s: unexpected hashed subplan %s", label, n.Subplan)
				}
				if n.IndexName == "plugin_item_fields_lookup" {
					sawLookup = true
					if n.ActualRows*n.Loops > 100 {
						t.Errorf("%s: driver lookup read %.0f rows, want <= 100 (bounded to narrow facet)", label, n.ActualRows*n.Loops)
					}
				}
				if n.IndexName == "plugin_item_fields_pkey_idx" {
					sawPkeyProbe = true
					if n.Loops > 100 {
						t.Errorf("%s: correlated probe looped %.0f times, want <= 100 (bounded to narrow facet)", label, n.Loops)
					}
				}
				if n.IndexName == "plugin_items_pkey" && n.Loops > 100 {
					t.Errorf("%s: plugin_items PK looped %.0f times, want <= 100", label, n.Loops)
				}
				for _, child := range n.Plans {
					walk(child)
				}
			}
			walk(roots[0].Plan)
			if !sawLookup {
				t.Errorf("%s: expected plugin_item_fields_lookup for narrow driver", label)
			}
			if !sawPkeyProbe {
				t.Errorf("%s: expected plugin_item_fields_pkey_idx for correlated probes", label)
			}
		}
		checkPG("pg-page", pageSQL, pageArgs)
		checkPG("pg-count", countSQL, countArgs)
	} else {
		checkSQLite := func(label, sqlText string, args []any) {
			t.Helper()
			planText := explainPlanText(t, db, "EXPLAIN QUERY PLAN "+sqlText, args...)
			if strings.Contains(planText, "LIST SUBQUERY") {
				t.Errorf("%s: unexpected LIST SUBQUERY materialization:\n%s", label, planText)
			}
			if !strings.Contains(planText, "plugin_item_fields_lookup") {
				t.Errorf("%s: expected plugin_item_fields_lookup for narrow driver:\n%s", label, planText)
			}
			if !strings.Contains(planText, "sqlite_autoindex_plugin_item_fields_1") {
				t.Errorf("%s: expected covering PK probe sqlite_autoindex_plugin_item_fields_1:\n%s", label, planText)
			}
			if strings.Contains(planText, "SCAN i") || strings.Contains(planText, "SCAN plugin_items") {
				t.Errorf("%s: unexpected full scan of plugin_items:\n%s", label, planText)
			}
		}
		checkSQLite("sqlite-page", pageSQL, pageArgs)
		checkSQLite("sqlite-count", countSQL, countArgs)
	}
}

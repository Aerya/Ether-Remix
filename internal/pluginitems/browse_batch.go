package pluginitems

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

type browseBatchEntry struct {
	idx         int
	q           BrowseQuery
	limit       int
	col         string
	kind        string
	desc        bool
	defaultDesc bool
	level       string
	parentRef   string
	tally       browseTallyInfo
	baseWhere   string
	baseArgs    []any
	empty       bool
}

// BrowseBatch executes a batch of BrowseQuery shelves for the same library
// while sharing kind lookup, plugin_item_tally, batched plugin_item_field_tally
// probes, batched UNION ALL item_ref selection, deduplicated plugin_items row
// fetches, and a single batched plugin_item_art hydration. Queries that do not
// qualify for batching (cursor pagination, total counts, last_viewed, hidden,
// or mixed libraries) fall back transparently to Browse.
func (s *Service) BrowseBatch(ctx context.Context, queries []BrowseQuery) ([]BrowsePage, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pluginitems: nil service")
	}
	if len(queries) == 0 {
		return nil, nil
	}
	if len(queries) == 1 {
		p, err := s.Browse(ctx, queries[0])
		if err != nil {
			return nil, err
		}
		return []BrowsePage{p}, nil
	}

	out := make([]BrowsePage, len(queries))
	libID := queries[0].LibraryID
	if libID <= 0 {
		return nil, fmt.Errorf("pluginitems: library_id is required")
	}

	var (
		eligible  []browseBatchEntry
		fallbacks []int
	)
	for i, q := range queries {
		if q.LibraryID != libID {
			fallbacks = append(fallbacks, i)
			continue
		}
		col, kind, desc, defaultDesc := parseBrowseSort(q)
		if !q.NoTotal || q.CheapTotal || strings.TrimSpace(q.Cursor) != "" || q.Hidden || kind == "last_viewed" {
			fallbacks = append(fallbacks, i)
			continue
		}
		if err := checkFacetValueBound(q.Facets, q.AllFacets, q.ExcludeFacets); err != nil {
			return nil, err
		}
		limit := q.Limit
		if limit <= 0 {
			limit = DefaultBrowseLimit
		}
		if limit > MaxBrowseLimit {
			limit = MaxBrowseLimit
		}
		eligible = append(eligible, browseBatchEntry{
			idx:         i,
			q:           q,
			limit:       limit,
			col:         col,
			kind:        kind,
			desc:        desc,
			defaultDesc: defaultDesc,
			level:       strings.TrimSpace(q.Level),
			parentRef:   strings.TrimSpace(string(q.ParentRef)),
		})
	}

	if len(eligible) > 0 {
		if err := s.resolveBrowseBatchTallies(ctx, libID, eligible); err != nil {
			return nil, err
		}

		var active []int
		for i := range eligible {
			e := &eligible[i]
			if e.tally.totalExact && e.tally.total == 0 {
				e.empty = true
				out[e.idx] = BrowsePage{Items: []core.KindItem{}}
				continue
			}
			bw, bArgs, _, empty, err := s.buildBrowsePredicates(ctx, e.q, e.parentRef, e.level, e.col, e.kind, e.desc, e.defaultDesc, e.limit, e.tally)
			if err != nil {
				return nil, err
			}
			if empty {
				e.empty = true
				out[e.idx] = BrowsePage{Items: []core.KindItem{}}
				continue
			}
			e.baseWhere = bw
			e.baseArgs = bArgs
			active = append(active, i)
		}

		if len(active) > 0 {
			refsByEntry := make([][]core.ItemRef, len(eligible))
			uniqueRefs := make([]core.ItemRef, 0, len(active)*8)
			seenRef := make(map[core.ItemRef]struct{}, len(active)*8)

			const maxUnionArms = 40
			for start := 0; start < len(active); start += maxUnionArms {
				chunk := active[start:min(start+maxUnionArms, len(active))]
				arms := make([]string, len(chunk))
				var args []any
				for armIdx, ei := range chunk {
					e := &eligible[ei]
					dir := "ASC"
					if e.desc {
						dir = "DESC"
					}
					arms[armIdx] = fmt.Sprintf(
						`SELECT * FROM (SELECT CAST(? AS INTEGER) AS shelf_idx, sub.item_ref, ROW_NUMBER() OVER (ORDER BY sub.sort_col %s, sub.item_ref ASC) AS pos FROM (SELECT i.item_ref, i.%s AS sort_col FROM plugin_items i WHERE %s ORDER BY i.%s %s, i.item_ref ASC LIMIT ?) sub) arm%d`,
						dir, e.col, e.baseWhere, e.col, dir, armIdx,
					)
					args = append(args, ei)
					args = append(args, e.baseArgs...)
					args = append(args, e.limit+1)
				}
				querySQL := `WITH batch AS (
	` + strings.Join(arms, "\n\tUNION ALL\n\t") + `
)
SELECT shelf_idx, item_ref, pos
FROM batch
ORDER BY shelf_idx ASC, pos ASC`

				rows, err := s.db.QueryContext(ctx, querySQL, args...)
				if err != nil {
					return nil, err
				}
				for rows.Next() {
					var ei int
					var refStr string
					var pos int
					if err := rows.Scan(&ei, &refStr, &pos); err != nil {
						rows.Close()
						return nil, err
					}
					if ei < 0 || ei >= len(eligible) {
						continue
					}
					ref := core.ItemRef(refStr)
					refsByEntry[ei] = append(refsByEntry[ei], ref)
					if _, ok := seenRef[ref]; !ok {
						seenRef[ref] = struct{}{}
						uniqueRefs = append(uniqueRefs, ref)
					}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return nil, err
				}
				rows.Close()
			}

			itemsByRef, err := s.fetchItemsByRefsWithArt(ctx, uniqueRefs)
			if err != nil {
				return nil, err
			}

			for _, ei := range active {
				e := &eligible[ei]
				refs := refsByEntry[ei]
				if len(refs) == 0 {
					out[e.idx] = BrowsePage{Items: []core.KindItem{}}
					continue
				}
				var nextCursor string
				if len(refs) > e.limit {
					if last, ok := itemsByRef[refs[e.limit-1]]; ok {
						cur := browseCursor{
							Sort:    e.col,
							Desc:    e.desc,
							ItemRef: string(last.Ref),
						}
						switch e.col {
						case "sort_title":
							cur.StrVal = last.SortTitle
						case "added_at":
							cur.IntVal = last.AddedAt
						case "released":
							cur.IntVal = last.Released
						case "year":
							cur.IntVal = int64(last.Year)
						case "idx":
							cur.IntVal = int64(last.Index)
						case "rating":
							cur.FloatVal = last.Rating
						}
						nextCursor = encodeBrowseCursor(cur)
					}
					refs = refs[:e.limit]
				}
				items := make([]core.KindItem, 0, len(refs))
				for _, ref := range refs {
					if it, ok := itemsByRef[ref]; ok {
						items = append(items, it)
					}
				}
				out[e.idx] = BrowsePage{
					Items:      items,
					Total:      len(items),
					TotalExact: false,
					NextCursor: nextCursor,
				}
			}
		}
	}

	for _, i := range fallbacks {
		p, err := s.Browse(ctx, queries[i])
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

// resolveBrowseBatchTallies computes browseTallyInfo for every eligible batch
// entry using one memoized plugin_item_tally read and batched PK-prefix queries
// on plugin_item_field_tally grouped by (level, field).
func (s *Service) resolveBrowseBatchTallies(ctx context.Context, libraryID core.LibraryID, entries []browseBatchEntry) error {
	libTally, err := s.libraryTally(ctx, libraryID)
	if err != nil {
		return err
	}
	if !libTally.active {
		return nil
	}
	libTotal := libTally.libTotal

	kindRec, kindErr := s.GetKindByLibrary(ctx, libraryID)
	fieldTypes := map[string]string{}
	if kindErr == nil {
		for _, fd := range kindRec.Decl.Fields {
			fieldTypes[fd.Key] = fd.Type
		}
	}

	type pendingFieldProbe struct {
		entryIdx     int
		levels       []string // {""} for anyLevel
		field        string
		vals         []string
		mode         facetMode
		singleValued bool
		levelTotal   int
		singleLevel  bool
	}
	type groupKey struct {
		level string
		field string
	}
	groups := map[groupKey]map[string]struct{}{}
	var pending []pendingFieldProbe

	for i := range entries {
		e := &entries[i]
		q := e.q
		if e.parentRef != "" || !q.Policy.Passive() || !q.TabPolicy.Passive() || q.Hidden || q.PersonID > 0 || q.Watched != nil || q.MaxContentRating != "" || len(q.ExcludeKeys) > 0 {
			continue
		}
		var (
			levels        []string
			anyLevel      bool
			defaultedRoot bool
		)
		switch {
		case len(q.Levels) > 0:
			levels = dedupCleanValues(q.Levels)
			if len(levels) == 0 {
				continue
			}
		case e.level != "":
			levels = []string{e.level}
		case q.AnyLevel:
			anyLevel = true
		default:
			if kindErr != nil || len(kindRec.Decl.Levels) == 0 {
				continue
			}
			defaultedRoot = true
			levels = []string{kindRec.Decl.Levels[0].Key}
		}

		levelTotal := libTotal
		if !anyLevel {
			levelTotal = 0
			for _, lv := range levels {
				levelTotal += libTally.levelCounts[lv]
			}
		}
		singleLevel := libTotal > 0 && levelTotal == libTotal && (!defaultedRoot || len(kindRec.Decl.Levels) == 1)

		type activeFacet struct {
			mode facetMode
			key  string
			vals []string
		}
		var active []activeFacet
		collect := func(m map[string][]string, mode facetMode) {
			for _, k := range sortedFacetKeys(m) {
				vals := dedupCleanValues(m[k])
				if len(vals) == 0 {
					continue
				}
				active = append(active, activeFacet{mode: mode, key: k, vals: vals})
				if len(active) > 1 {
					return
				}
			}
		}
		collect(q.Facets, facetAny)
		if len(active) <= 1 {
			collect(q.AllFacets, facetAll)
		}
		if len(active) <= 1 {
			collect(q.ExcludeFacets, facetNone)
		}

		if len(active) == 0 {
			e.tally = browseTallyInfo{totalExact: true, total: levelTotal, libTotal: libTotal, singleLevel: singleLevel}
			continue
		}
		if len(active) > 1 || envelopeFacet(active[0].key) || kindErr != nil {
			e.tally = browseTallyInfo{libTotal: libTotal, singleLevel: singleLevel}
			continue
		}
		f := active[0]
		var singleValued bool
		switch fieldTypes[f.key] {
		case "string", "enum", "bool":
			singleValued = true
		case "strings":
			singleValued = false
		default:
			e.tally = browseTallyInfo{libTotal: libTotal, singleLevel: singleLevel}
			continue
		}
		if !singleValued && len(f.vals) > 1 && f.mode != facetAny {
			e.tally = browseTallyInfo{libTotal: libTotal, singleLevel: singleLevel}
			continue
		}
		if singleValued && f.mode == facetAll && len(f.vals) > 1 {
			e.tally = browseTallyInfo{totalExact: true, total: 0, libTotal: libTotal, singleLevel: singleLevel}
			continue
		}

		tallyLevels := levels
		if anyLevel {
			tallyLevels = []string{""}
		}
		for _, lv := range tallyLevels {
			gk := groupKey{level: lv, field: f.key}
			set := groups[gk]
			if set == nil {
				set = map[string]struct{}{}
				groups[gk] = set
			}
			for _, v := range f.vals {
				set[v] = struct{}{}
			}
		}
		pending = append(pending, pendingFieldProbe{
			entryIdx:     i,
			levels:       tallyLevels,
			field:        f.key,
			vals:         f.vals,
			mode:         f.mode,
			singleValued: singleValued,
			levelTotal:   levelTotal,
			singleLevel:  singleLevel,
		})
	}

	if len(pending) == 0 {
		return nil
	}

	// Execute one batched PK-prefix query per (level, field) group.
	type tallyRowKey struct {
		level string
		field string
		val   string
	}
	counts := make(map[tallyRowKey]int, len(pending)*2)
	groupKeys := make([]groupKey, 0, len(groups))
	for gk := range groups {
		groupKeys = append(groupKeys, gk)
	}
	slices.SortFunc(groupKeys, func(a, b groupKey) int {
		if c := strings.Compare(a.level, b.level); c != 0 {
			return c
		}
		return strings.Compare(a.field, b.field)
	})

	const chunkSize = 250
	for _, gk := range groupKeys {
		vals := make([]string, 0, len(groups[gk]))
		for v := range groups[gk] {
			vals = append(vals, v)
		}
		slices.Sort(vals)
		for start := 0; start < len(vals); start += chunkSize {
			chunk := vals[start:min(start+chunkSize, len(vals))]
			ph := make([]string, len(chunk))
			args := make([]any, 0, 3+len(chunk))
			args = append(args, int64(libraryID), gk.level, gk.field)
			for i, v := range chunk {
				ph[i] = "?"
				args = append(args, v)
			}
			rows, err := s.db.QueryContext(ctx,
				`SELECT v_text, cnt FROM plugin_item_field_tally WHERE library_id = ? AND level = ? AND field = ? AND v_text IN (`+strings.Join(ph, ",")+`)`,
				args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var v string
				var cnt int
				if err := rows.Scan(&v, &cnt); err != nil {
					rows.Close()
					return err
				}
				counts[tallyRowKey{level: gk.level, field: gk.field, val: v}] = cnt
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
	}

	for _, p := range pending {
		multiOrMultiVal := !p.singleValued && len(p.vals) > 1 && p.mode == facetAny
		var sumCnt, maxCnt int
		for _, v := range p.vals {
			valCnt := 0
			for _, lv := range p.levels {
				valCnt += counts[tallyRowKey{level: lv, field: p.field, val: v}]
			}
			sumCnt += valCnt
			if valCnt > maxCnt {
				maxCnt = valCnt
			}
		}
		if multiOrMultiVal {
			if sumCnt == 0 {
				entries[p.entryIdx].tally = browseTallyInfo{totalExact: true, total: 0, libTotal: libTotal, singleLevel: p.singleLevel}
			} else {
				entries[p.entryIdx].tally = browseTallyInfo{
					lowerBound:         maxCnt,
					libTotal:           libTotal,
					singleLevel:        p.singleLevel,
					positiveTextField:  p.field,
					positiveTextValues: p.vals,
				}
			}
			continue
		}
		if p.mode == facetNone {
			entries[p.entryIdx].tally = browseTallyInfo{
				totalExact:  true,
				total:       max(0, p.levelTotal-sumCnt),
				libTotal:    libTotal,
				singleLevel: p.singleLevel,
			}
			continue
		}
		entries[p.entryIdx].tally = browseTallyInfo{
			totalExact:         true,
			total:              sumCnt,
			libTotal:           libTotal,
			singleLevel:        p.singleLevel,
			positiveTextField:  p.field,
			positiveTextValues: p.vals,
		}
	}
	return nil
}

func (s *Service) fetchItemsByRefsWithArt(ctx context.Context, refs []core.ItemRef) (map[core.ItemRef]core.KindItem, error) {
	if len(refs) == 0 {
		return map[core.ItemRef]core.KindItem{}, nil
	}
	items := make([]core.KindItem, 0, len(refs))
	const chunkSize = 250
	for start := 0; start < len(refs); start += chunkSize {
		chunk := refs[start:min(start+chunkSize, len(refs))]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, r := range chunk {
			ph[i] = "?"
			args[i] = string(r)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+itemCols+` FROM plugin_items WHERE item_ref IN (`+strings.Join(ph, ",")+`)`,
			args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			it, _, _, err := scanItemRow(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	if len(items) > 0 {
		if err := s.hydrateArtSlice(ctx, items); err != nil {
			return nil, err
		}
	}
	byRef := make(map[core.ItemRef]core.KindItem, len(items))
	for _, it := range items {
		byRef[it.Ref] = it
	}
	return byRef, nil
}

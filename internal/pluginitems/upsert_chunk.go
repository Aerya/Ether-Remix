package pluginitems

import (
	"context"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// upsertChunkSize bounds one write transaction inside an upsert batch
// (#646). The per-kind lock is held across chunks, so a batch still looks
// atomic to the kind's own writers, but the database write gate is
// released between chunks and unrelated writers interleave instead of
// timing out behind a minutes-long republish.
const upsertChunkSize = 200

type upsertChunkResult struct {
	added      int
	updated    int
	unchanged  int
	totalItems int64
}

// writeUpsertChunk writes one chunk of prepared items in a single
// transaction. Existing items whose content hash matches are not
// rewritten — no plugin_items index churn, no FTS replace, no field or
// art delete/reinsert — only their seen bookkeeping is refreshed, in one
// statement, and only when it would change.
func (s *Service) writeUpsertChunk(ctx context.Context, kind KindRecord, pluginID, kindKey string, chunk []preparedItem, seenAt, nowUnix int64, bumpGen, markOK, pin bool) (upsertChunkResult, error) {
	var res upsertChunkResult
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	existing, err := lookupExistingItemsTx(ctx, tx, chunk)
	if err != nil {
		return res, err
	}

	unchangedRefs := make([]core.ItemRef, 0, len(chunk))
	changed := make([]bool, len(chunk))
	for i := range chunk {
		ref := chunk[i].validated.Item.Ref
		ex, ok := existing[ref]
		switch {
		case !ok:
			changed[i] = true
			res.added++
		case ex.hash != "" && ex.hash == chunk[i].hash:
			unchangedRefs = append(unchangedRefs, ref)
			res.unchanged++
		default:
			changed[i] = true
			res.updated++
		}
	}

	if res.added > 0 {
		maxItems := s.MaxItemsFor(pluginID)
		// The tally is maintained in the same transactions as the items,
		// so summing its per-level rows (bounded by kinds x levels)
		// replaces a COUNT(*) over the plugin's whole item table.
		var pluginTotal int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(n), 0) FROM plugin_item_tally WHERE library_id IN (SELECT library_id FROM plugin_kinds WHERE plugin_id = ?)`,
			pluginID).Scan(&pluginTotal); err != nil {
			return res, err
		}
		if pluginTotal+int64(res.added) > int64(maxItems) {
			s.noteAttention(ctx, pluginID, fmt.Sprintf("plugin exceeded max item cap (%d)", maxItems))
			return res, ErrMaxItemsExceeded
		}
	}

	var nextSeq int64
	if res.added > 0 {
		nextSeq, err = allocateSeqRangeTx(ctx, tx, int64(res.added))
		if err != nil {
			return res, err
		}
	}

	if len(unchangedRefs) > 0 {
		placeholders := make([]string, len(unchangedRefs))
		args := make([]any, 0, len(unchangedRefs)+2)
		args = append(args, seenAt)
		for i, r := range unchangedRefs {
			placeholders[i] = "?"
			args = append(args, string(r))
		}
		args = append(args, seenAt)
		updateUnchangedSQL := `UPDATE plugin_items SET seen_at = ?, missed_walks = 0 WHERE item_ref IN (` + strings.Join(placeholders, ",") + `) AND (missed_walks <> 0 OR seen_at <> ?)`
		if pin {
			updateUnchangedSQL = `UPDATE plugin_items SET seen_at = ?, missed_walks = 0, pinned = 1 WHERE item_ref IN (` + strings.Join(placeholders, ",") + `) AND (missed_walks <> 0 OR seen_at <> ? OR pinned = 0)`
		}
		if _, err := tx.ExecContext(ctx, updateUnchangedSQL, args...); err != nil {
			return res, err
		}
	}

	pinInt := 0
	if pin {
		pinInt = 1
	}

	fieldTallyDelta := map[fieldTallyKey]*fieldTallyChange{}
	personTallyDelta := map[personTallyKey]*personTallyChange{}
	changedExisting := make([]core.ItemRef, 0, len(chunk))
	for i := range chunk {
		if !changed[i] {
			continue
		}
		if _, ok := existing[chunk[i].validated.Item.Ref]; ok {
			changedExisting = append(changedExisting, chunk[i].validated.Item.Ref)
		}
	}
	var oldFieldsByRef map[core.ItemRef][]existingTextField
	if len(changedExisting) > 0 {
		oldFields, err := lookupExistingTextFieldsTx(ctx, tx, changedExisting)
		if err != nil {
			return res, err
		}
		oldFieldsByRef = make(map[core.ItemRef][]existingTextField, len(changedExisting))
		for _, of := range oldFields {
			oldFieldsByRef[of.ref] = append(oldFieldsByRef[of.ref], of)
		}
	}

	isPG := store.DialectOf(s.db) == store.Postgres
	tallyDelta := map[string]int64{}

	var parentTally *parentTallyTracker
	if len(kind.Decl.Levels) >= 2 && kind.LibraryID > 0 {
		leafLevels := kind.Decl.LeafLevels()
		parentTally = newParentTallyTracker(tx, kind.LibraryID, leafLevels, existing)
		primeParentRefs := make([]core.ItemRef, 0, len(chunk))
		primeLeafRefs := make([]core.ItemRef, 0, len(chunk))
		for i := range chunk {
			if !changed[i] {
				continue
			}
			p := &chunk[i]
			it := &p.validated.Item
			ex, isExisting := existing[it.Ref]
			newParent := core.ItemRef(p.parentRef)
			if isExisting && ex.parentRef == newParent && ex.level == it.Level {
				continue
			}
			if ex.parentRef != "" {
				primeParentRefs = append(primeParentRefs, ex.parentRef)
				primeLeafRefs = append(primeLeafRefs, ex.parentRef)
			}
			if newParent != "" {
				primeParentRefs = append(primeParentRefs, newParent)
				primeLeafRefs = append(primeLeafRefs, newParent)
			}
			if !leafLevels[it.Level] || (isExisting && !leafLevels[ex.level]) {
				primeLeafRefs = append(primeLeafRefs, it.Ref)
			}
		}
		if err := parentTally.primeParents(ctx, primeParentRefs); err != nil {
			return res, err
		}
		if err := parentTally.primeLeaves(ctx, primeLeafRefs); err != nil {
			return res, err
		}
		for i := range chunk {
			if !changed[i] {
				continue
			}
			p := &chunk[i]
			it := &p.validated.Item
			ex, isExisting := existing[it.Ref]
			if err := parentTally.apply(ctx, it.Ref, isExisting, ex.parentRef, core.ItemRef(p.parentRef), ex.level, it.Level); err != nil {
				return res, err
			}
		}
	}

	for i := range chunk {
		if !changed[i] {
			continue
		}
		p := &chunk[i]
		v := &p.validated
		it := &v.Item
		ex, isExisting := existing[it.Ref]
		var seq int64
		var addedAt int64
		if isExisting {
			seq = ex.seq
			addedAt = ex.addedAt
			if ex.level != it.Level {
				tallyDelta[ex.level]--
				tallyDelta[it.Level]++
			}
		} else {
			seq = nextSeq
			nextSeq++
			addedAt = nowUnix
			tallyDelta[it.Level]++
		}
		it.Key = core.PluginKey(seq)

		if isPG {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, summary, year, released, rating, rating_level, playable, child_count, leaf_count,
	unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, pinned, content_hash, search_tsv
) VALUES(
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, 0, ?, ?,
	setweight(to_tsvector('simple', ?), 'A') || setweight(to_tsvector('simple', ?), 'B') || setweight(to_tsvector('simple', ?), 'C')
) ON CONFLICT(item_ref) DO UPDATE SET
	parent_ref   = excluded.parent_ref,
	level        = excluded.level,
	idx          = excluded.idx,
	title        = excluded.title,
	sort_title   = excluded.sort_title,
	summary      = excluded.summary,
	year         = excluded.year,
	released     = excluded.released,
	rating       = excluded.rating,
	rating_level = excluded.rating_level,
	playable     = excluded.playable,
	child_count  = excluded.child_count,
	leaf_count   = excluded.leaf_count,
	unavailable  = excluded.unavailable,
	hints        = excluded.hints,
	fields       = excluded.fields,
	etag         = excluded.etag,
	updated_at   = excluded.updated_at,
	seen_at      = excluded.seen_at,
	missed_walks = 0,
	pinned       = CASE WHEN plugin_items.pinned <> 0 OR excluded.pinned <> 0 THEN 1 ELSE 0 END,
	content_hash = excluded.content_hash,
	search_tsv   = excluded.search_tsv`,
				string(it.Ref), seq, pluginID, kindKey, int64(kind.LibraryID), it.ID, p.parentRef, it.Level, it.Index,
				it.Title, it.SortTitle, it.Summary, it.Year, it.Released, it.Rating, contentRatingLevel(it.Fields), p.playableInt, it.ChildCount, it.LeafCount,
				p.unavailableStr, p.hintsStr, p.fieldsStr, it.ETag, addedAt, nowUnix, seenAt, pinInt, p.hash,
				it.Title, v.SearchFields, v.SearchPeople,
			); err != nil {
				return res, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, summary, year, released, rating, rating_level, playable, child_count, leaf_count,
	unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, pinned, content_hash
) VALUES(
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, 0, ?, ?
) ON CONFLICT(item_ref) DO UPDATE SET
	parent_ref   = excluded.parent_ref,
	level        = excluded.level,
	idx          = excluded.idx,
	title        = excluded.title,
	sort_title   = excluded.sort_title,
	summary      = excluded.summary,
	year         = excluded.year,
	released     = excluded.released,
	rating       = excluded.rating,
	rating_level = excluded.rating_level,
	playable     = excluded.playable,
	child_count  = excluded.child_count,
	leaf_count   = excluded.leaf_count,
	unavailable  = excluded.unavailable,
	hints        = excluded.hints,
	fields       = excluded.fields,
	etag         = excluded.etag,
	updated_at   = excluded.updated_at,
	seen_at      = excluded.seen_at,
	missed_walks = 0,
	pinned       = CASE WHEN plugin_items.pinned <> 0 OR excluded.pinned <> 0 THEN 1 ELSE 0 END,
	content_hash = excluded.content_hash`,
				string(it.Ref), seq, pluginID, kindKey, int64(kind.LibraryID), it.ID, p.parentRef, it.Level, it.Index,
				it.Title, it.SortTitle, it.Summary, it.Year, it.Released, it.Rating, contentRatingLevel(it.Fields), p.playableInt, it.ChildCount, it.LeafCount,
				p.unavailableStr, p.hintsStr, p.fieldsStr, it.ETag, addedAt, nowUnix, seenAt, pinInt, p.hash,
			); err != nil {
				return res, err
			}
			if err := sqliteIndexFTS(ctx, tx, seq, it.Ref, kind.LibraryID, it.Title, v.SearchFields, v.SearchPeople); err != nil {
				return res, err
			}
		}

		// Replace indexed fields for this item.
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_fields WHERE item_ref = ?`, string(it.Ref)); err != nil {
			return res, err
		}
		var itemPersonHasThumb map[int64]bool
		for _, fv := range v.IndexedFields {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO plugin_item_fields(item_ref, field, v_text, v_int, v_real, v_fold) VALUES(?, ?, ?, ?, ?, ?)
				 ON CONFLICT(item_ref, field, v_text, v_int) DO UPDATE SET v_real = excluded.v_real, v_fold = excluded.v_fold`,
				string(it.Ref), fv.Field, fv.Text, fv.Int, fv.Real, fv.Fold); err != nil {
				return res, err
			}
			if fv.HasThumb && core.IsPluginPersonID(fv.Int) {
				if itemPersonHasThumb == nil {
					itemPersonHasThumb = map[int64]bool{}
				}
				itemPersonHasThumb[fv.Int] = true
			}
		}

		refStr := string(it.Ref)
		newRank := tallyItemRank{ref: refStr, released: it.Released, addedAt: addedAt, year: int64(it.Year)}
		if !isExisting {
			for _, fv := range v.IndexedFields {
				if fv.Text == "" {
					continue
				}
				addFieldTallyChange(fieldTallyDelta, it.Level, fv.Field, fv.Text, 1, newRank, "")
				if core.IsPluginPersonID(fv.Int) {
					var th tallyItemRank
					if itemPersonHasThumb[fv.Int] {
						th = newRank
					}
					addPersonTallyChange(personTallyDelta, it.Level, fv.Field, fv.Int, 1, fv.Text, "", newRank, "", th, "")
				}
			}
		} else {
			oldRank := tallyItemRank{ref: refStr, released: ex.released, addedAt: ex.addedAt, year: ex.year}
			rankUp := newRank.beats(oldRank)
			rankDown := oldRank.beats(newRank)

			oldTextSet := make(map[fieldTallyKey]struct{}, len(oldFieldsByRef[it.Ref]))
			oldPersonMap := map[personTallyKey]string{}
			for _, of := range oldFieldsByRef[it.Ref] {
				if of.text == "" {
					continue
				}
				oldTextSet[fieldTallyKey{level: ex.level, field: of.field, text: of.text}] = struct{}{}
				if core.IsPluginPersonID(of.intV) {
					oldPersonMap[personTallyKey{level: ex.level, field: of.field, vInt: of.intV}] = of.text
				}
			}

			type newPersonVal struct {
				text     string
				hasThumb bool
			}
			newTextSet := make(map[fieldTallyKey]struct{}, len(v.IndexedFields))
			newPersonMap := map[personTallyKey]newPersonVal{}
			for _, fv := range v.IndexedFields {
				if fv.Text == "" {
					continue
				}
				newTextSet[fieldTallyKey{level: it.Level, field: fv.Field, text: fv.Text}] = struct{}{}
				if core.IsPluginPersonID(fv.Int) {
					newPersonMap[personTallyKey{level: it.Level, field: fv.Field, vInt: fv.Int}] = newPersonVal{
						text:     fv.Text,
						hasThumb: itemPersonHasThumb[fv.Int],
					}
				}
			}

			for k := range oldTextSet {
				if _, keep := newTextSet[k]; !keep {
					addFieldTallyChange(fieldTallyDelta, k.level, k.field, k.text, -1, tallyItemRank{}, refStr)
				}
			}
			for k := range newTextSet {
				if _, existed := oldTextSet[k]; !existed {
					addFieldTallyChange(fieldTallyDelta, k.level, k.field, k.text, 1, newRank, "")
				} else if rankUp {
					addFieldTallyChange(fieldTallyDelta, k.level, k.field, k.text, 0, newRank, "")
				} else if rankDown {
					addFieldTallyChange(fieldTallyDelta, k.level, k.field, k.text, 0, tallyItemRank{}, refStr)
				}
			}

			for k, oldText := range oldPersonMap {
				if _, keep := newPersonMap[k]; !keep {
					addPersonTallyChange(personTallyDelta, k.level, k.field, k.vInt, -1, "", oldText, tallyItemRank{}, refStr, tallyItemRank{}, refStr)
				}
			}
			for k, nv := range newPersonMap {
				oldText, existed := oldPersonMap[k]
				if !existed {
					var th tallyItemRank
					if nv.hasThumb {
						th = newRank
					}
					addPersonTallyChange(personTallyDelta, k.level, k.field, k.vInt, 1, nv.text, "", newRank, "", th, "")
					continue
				}
				var addTxt, remTxt string
				if oldText != nv.text {
					addTxt = nv.text
					remTxt = oldText
				}
				var addRep tallyItemRank
				var remRep string
				if rankUp {
					addRep = newRank
				} else if rankDown {
					remRep = refStr
				}
				var addTh tallyItemRank
				var remTh string
				if nv.hasThumb {
					addTh = newRank
					if rankDown {
						remTh = refStr
					}
				} else {
					remTh = refStr
				}
				addPersonTallyChange(personTallyDelta, k.level, k.field, k.vInt, 0, addTxt, remTxt, addRep, remRep, addTh, remTh)
			}
		}

		// Replace artwork rows for this item.
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_art WHERE item_ref = ?`, string(it.Ref)); err != nil {
			return res, err
		}
		for _, a := range it.Art {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO plugin_item_art(item_ref, role, url, width, height) VALUES(?, ?, ?, ?, ?)`,
				string(it.Ref), a.Role, a.URL, a.Width, a.Height); err != nil {
				return res, err
			}
		}
	}

	if err := applyTallyDeltaTx(ctx, tx, kind.LibraryID, tallyDelta); err != nil {
		return res, err
	}
	emptiedText, err := applyFieldTallyDeltaTx(ctx, tx, isPG, kind.LibraryID, fieldTallyDelta)
	if err != nil {
		return res, err
	}
	if err := applyPersonTallyDeltaTx(ctx, tx, isPG, kind.LibraryID, kind.Decl, personTallyDelta, emptiedText); err != nil {
		return res, err
	}
	if parentTally != nil {
		if err := applyParentTallyDeltaTx(ctx, tx, kind.LibraryID, parentTally.delta); err != nil {
			return res, err
		}
	}
	if bumpGen {
		total, err := s.bumpKindSyncStateTx(ctx, tx, pluginID, kindKey, kind.LibraryID, nowUnix, markOK)
		if err != nil {
			return res, err
		}
		res.totalItems = total
	}
	if err := tx.Commit(); err != nil {
		return upsertChunkResult{}, err
	}
	if res.added > 0 || res.updated > 0 {
		s.noteWrite()
	}
	return res, nil
}

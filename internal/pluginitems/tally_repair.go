package pluginitems

import (
	"context"
	"database/sql"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// prepareTallyRepairs derives the post-chunk representatives from the current
// store and the incoming changes. Call under the kind lock, before admitting
// a SQLite writer. A common facet can have thousands of holders, even when
// the chunk changes only one of them.
func prepareTallyRepairs(ctx context.Context, q queryer, libraryID core.LibraryID, decl plugins.MediaKindMeta, fields map[fieldTallyKey]*fieldTallyChange, people map[personTallyKey]*personTallyChange) error {
	if libraryID <= 0 {
		return nil
	}
	libID := int64(libraryID)
	total := int64(-1)
	planRepair := func(cnt int64, cur tallyItemRank, excluded map[string]struct{}) (repRecomputePlan, error) {
		if total < 0 {
			var err error
			total, err = libraryItemTotal(ctx, q, libID)
			if err != nil {
				return repRecomputePlan{}, err
			}
		}
		plan := planRepRecompute(cnt, total, cur)
		plan.excluded = excluded
		return plan, nil
	}

	emptied := map[fieldTallyKey]bool{}
	for k, ch := range fields {
		if len(ch.removedRefs) == 0 {
			continue
		}
		var cur tallyItemRank
		var cnt int64
		err := q.QueryRowContext(ctx, `
SELECT rep_ref, rep_released, rep_added, rep_year, cnt FROM plugin_item_field_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
			libID, k.level, k.field, k.text).Scan(&cur.ref, &cur.released, &cur.addedAt, &cur.year, &cnt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return wrapTallyItemErr(ch.sampleRef, k.field, err)
		}
		if cnt+ch.delta <= 0 {
			emptied[k] = true
			continue
		}
		_, removed := ch.removedRefs[cur.ref]
		if !removed && cur.valid() {
			continue
		}
		winner := ch.bestAdded
		// If the best incoming holder already beats the old representative,
		// no untouched holder can beat it either.
		if !cur.valid() || !winner.beats(cur) {
			plan, err := planRepair(cnt+ch.delta, cur, ch.removedRefs)
			if err != nil {
				return err
			}
			other, err := recomputeFieldRepTx(ctx, q, false, libID, k.level, k.field, k.text, plan)
			if err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			if other.beats(winner) {
				winner = other
			}
		}
		ch.replacement = &winner
	}

	var peopleFields []string
	for k, ch := range people {
		refreshLabel := false
		for text := range ch.removedTexts {
			if emptied[fieldTallyKey{level: k.level, field: k.field, text: text}] {
				refreshLabel = true
				break
			}
		}
		if len(ch.removedRepRefs) == 0 && len(ch.removedThumbRefs) == 0 && !refreshLabel {
			continue
		}
		var rep, thumb tallyItemRank
		var cnt int64
		err := q.QueryRowContext(ctx, `
SELECT rep_ref, rep_released, rep_added, rep_year, thumb_ref, thumb_released, thumb_added, thumb_year, cnt
FROM plugin_item_person_tally WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
			libID, k.level, k.field, k.vInt).Scan(&rep.ref, &rep.released, &rep.addedAt, &rep.year,
			&thumb.ref, &thumb.released, &thumb.addedAt, &thumb.year, &cnt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return wrapTallyItemErr(ch.sampleRef, k.field, err)
		}
		if cnt+ch.delta <= 0 {
			continue
		}
		_, removeRep := ch.removedRepRefs[rep.ref]
		if len(ch.removedRepRefs) > 0 && (removeRep || !rep.valid()) {
			winner := ch.bestRepAdded
			if !rep.valid() || !winner.beats(rep) {
				plan, err := planRepair(cnt+ch.delta, rep, ch.removedRepRefs)
				if err != nil {
					return err
				}
				other, err := recomputePersonRepTx(ctx, q, false, libID, k.level, k.field, k.vInt, plan)
				if err != nil {
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
				if other.beats(winner) {
					winner = other
				}
			}
			ch.repReplacement = &winner
		}
		if _, removeThumb := ch.removedThumbRefs[thumb.ref]; removeThumb {
			winner := ch.bestThumbAdded
			if !winner.beats(thumb) {
				plan, err := planRepair(cnt+ch.delta, thumb, ch.removedThumbRefs)
				if err != nil {
					return err
				}
				if peopleFields == nil {
					peopleFields = declaredPeopleFields(decl)
				}
				other, err := recomputePersonThumbTx(ctx, q, false, libID, k.level, k.field, k.vInt, plan, peopleFields)
				if err != nil {
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
				if other.beats(winner) {
					winner = other
				}
			}
			ch.thumbReplacement = &winner
		}
		if refreshLabel {
			label, err := preparePersonLabel(ctx, q, libID, k, ch)
			if err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			ch.labelReplacement = &label
		}
	}
	return nil
}

func preparePersonLabel(ctx context.Context, q queryer, libID int64, k personTallyKey, ch *personTallyChange) (sql.NullString, error) {
	where := `i.level <> ''`
	args := []any{k.field, k.vInt, libID}
	if k.level != "" {
		where = `i.level = ?`
		args = append(args, k.level)
	}
	exclude, excluded := (repRecomputePlan{excluded: ch.removedLabelRefs}).exclusion("i.item_ref")
	args = append(args, excluded...)
	var label sql.NullString
	err := q.QueryRowContext(ctx, `
SELECT MIN(f.v_text)
FROM plugin_item_fields f JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND f.v_text <> ''
AND i.library_id + 0 = ? AND `+where+exclude, args...).Scan(&label)
	if err == nil && ch.minAdded != "" && (!label.Valid || ch.minAdded < label.String) {
		label = sql.NullString{String: ch.minAdded, Valid: true}
	}
	return label, err
}

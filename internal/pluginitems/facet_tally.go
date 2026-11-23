package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

const (
	migration17SQLiteDDL = `
ALTER TABLE plugin_item_field_tally ADD COLUMN v_lower TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_item_field_tally ADD COLUMN v_fold  TEXT NOT NULL DEFAULT '';
CREATE TABLE plugin_item_person_tally (
	library_id  INTEGER NOT NULL,
	level       TEXT    NOT NULL,
	field       TEXT    NOT NULL,
	v_int       INTEGER NOT NULL,
	label       TEXT    NOT NULL,
	label_lower TEXT    NOT NULL DEFAULT '',
	label_fold  TEXT    NOT NULL DEFAULT '',
	cnt         INTEGER NOT NULL,
	PRIMARY KEY (library_id, level, field, v_int)
);
`
	migration17PostgresDDL = `
ALTER TABLE plugin_item_field_tally ADD COLUMN v_lower TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE plugin_item_field_tally ADD COLUMN v_fold  TEXT COLLATE "C" NOT NULL DEFAULT '';
CREATE TABLE plugin_item_person_tally (
	library_id  BIGINT           NOT NULL,
	level       TEXT COLLATE "C" NOT NULL,
	field       TEXT COLLATE "C" NOT NULL,
	v_int       BIGINT           NOT NULL,
	label       TEXT COLLATE "C" NOT NULL,
	label_lower TEXT COLLATE "C" NOT NULL DEFAULT '',
	label_fold  TEXT COLLATE "C" NOT NULL DEFAULT '',
	cnt         BIGINT           NOT NULL,
	PRIMARY KEY (library_id, level, field, v_int)
);
`
	migration17IndexesDDL = `
DROP INDEX IF EXISTS plugin_item_field_tally_count;
CREATE INDEX plugin_item_field_tally_count
	ON plugin_item_field_tally(library_id, level, field, cnt DESC, v_lower, v_text, v_fold);
CREATE INDEX plugin_item_field_tally_name
	ON plugin_item_field_tally(library_id, level, field, v_lower, v_text, cnt, v_fold);
CREATE INDEX plugin_item_person_tally_name
	ON plugin_item_person_tally(library_id, level, field, label_lower, label, v_int, cnt, label_fold);
CREATE INDEX plugin_item_person_tally_count
	ON plugin_item_person_tally(library_id, level, field, cnt DESC, label_lower, label, v_int, label_fold);
`

	migration18SQLiteDDL = `
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_ref        TEXT    NOT NULL DEFAULT '';
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_released   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_added      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_year       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_ref        TEXT    NOT NULL DEFAULT '';
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_released   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_added      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_year       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_ref      TEXT    NOT NULL DEFAULT '';
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_released INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_added    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_year     INTEGER NOT NULL DEFAULT 0;
`
	migration18PostgresDDL = `
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_ref        TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_released   BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_added      BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_field_tally  ADD COLUMN rep_year       BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_ref        TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_released   BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_added      BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN rep_year       BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_ref      TEXT COLLATE "C" NOT NULL DEFAULT '';
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_released BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_added    BIGINT           NOT NULL DEFAULT 0;
ALTER TABLE plugin_item_person_tally ADD COLUMN thumb_year     BIGINT           NOT NULL DEFAULT 0;
`
	migration18IndexesDDL = `
DROP INDEX IF EXISTS plugin_item_field_tally_count;
CREATE INDEX plugin_item_field_tally_count
	ON plugin_item_field_tally(library_id, level, field, cnt DESC, v_lower, v_text, v_fold, rep_ref);
DROP INDEX IF EXISTS plugin_item_field_tally_name;
CREATE INDEX plugin_item_field_tally_name
	ON plugin_item_field_tally(library_id, level, field, v_lower, v_text, cnt, v_fold, rep_ref);
DROP INDEX IF EXISTS plugin_item_person_tally_name;
CREATE INDEX plugin_item_person_tally_name
	ON plugin_item_person_tally(library_id, level, field, label_lower, label, v_int, cnt, label_fold, rep_ref, thumb_ref);
DROP INDEX IF EXISTS plugin_item_person_tally_count;
CREATE INDEX plugin_item_person_tally_count
	ON plugin_item_person_tally(library_id, level, field, cnt DESC, label_lower, label, v_int, label_fold, rep_ref, thumb_ref);
`
)

func applyMigration18SQLite(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration18SQLiteDDL); err != nil {
		return err
	}
	if err := backfillTallyRepsTx(ctx, tx, false, 0); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, migration18IndexesDDL)
	return err
}

func applyMigration18Postgres(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration18PostgresDDL); err != nil {
		return err
	}
	if err := backfillTallyRepsTx(ctx, tx, true, 0); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, migration18IndexesDDL)
	return err
}

func applyMigration17SQLite(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration17SQLiteDDL); err != nil {
		return err
	}
	if err := backfillFacetTalliesTx(ctx, tx, false); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, migration17IndexesDDL)
	return err
}

func applyMigration17Postgres(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration17PostgresDDL); err != nil {
		return err
	}
	if err := backfillFacetTalliesTx(ctx, tx, true); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, migration17IndexesDDL)
	return err
}

func backfillFacetTalliesTx(ctx context.Context, tx *sql.Tx, isPG bool) error {
	if err := backfillFieldTallyCaseTx(ctx, tx, 0); err != nil {
		return err
	}
	return backfillPersonTallyTx(ctx, tx, isPG, 0)
}

// backfillFieldTallyCaseTx populates v_lower and v_fold on plugin_item_field_tally
// in bounded keyset batches. libraryID == 0 backfills all libraries.
func backfillFieldTallyCaseTx(ctx context.Context, tx *sql.Tx, libraryID int64) error {
	const batchSize = 2000
	type rowKey struct {
		libID int64
		level string
		field string
		text  string
	}
	var (
		hasCursor bool
		curLib    int64
		curLevel  string
		curField  string
		curText   string
	)
	for {
		var (
			rows *sql.Rows
			err  error
		)
		if libraryID > 0 {
			if !hasCursor {
				rows, err = tx.QueryContext(ctx, `
SELECT library_id, level, field, v_text
FROM plugin_item_field_tally
WHERE library_id = ?
ORDER BY level ASC, field ASC, v_text ASC
LIMIT ?`, libraryID, batchSize)
			} else {
				rows, err = tx.QueryContext(ctx, `
SELECT library_id, level, field, v_text
FROM plugin_item_field_tally
WHERE library_id = ? AND (level, field, v_text) > (?, ?, ?)
ORDER BY level ASC, field ASC, v_text ASC
LIMIT ?`, libraryID, curLevel, curField, curText, batchSize)
			}
		} else {
			if !hasCursor {
				rows, err = tx.QueryContext(ctx, `
SELECT library_id, level, field, v_text
FROM plugin_item_field_tally
ORDER BY library_id ASC, level ASC, field ASC, v_text ASC
LIMIT ?`, batchSize)
			} else {
				rows, err = tx.QueryContext(ctx, `
SELECT library_id, level, field, v_text
FROM plugin_item_field_tally
WHERE (library_id, level, field, v_text) > (?, ?, ?, ?)
ORDER BY library_id ASC, level ASC, field ASC, v_text ASC
LIMIT ?`, curLib, curLevel, curField, curText, batchSize)
			}
		}
		if err != nil {
			return err
		}
		batch := make([]rowKey, 0, batchSize)
		for rows.Next() {
			var k rowKey
			if err := rows.Scan(&k.libID, &k.level, &k.field, &k.text); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, k)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, k := range batch {
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET v_lower = ?, v_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
				strings.ToLower(k.text), FoldFieldText(k.text),
				k.libID, k.level, k.field, k.text); err != nil {
				return err
			}
		}
		last := batch[len(batch)-1]
		hasCursor = true
		curLib, curLevel, curField, curText = last.libID, last.level, last.field, last.text
		if len(batch) < batchSize {
			break
		}
	}
	return nil
}

// backfillPersonTallyTx rebuilds plugin_item_person_tally from plugin_item_fields
// joined with plugin_items. libraryID == 0 backfills all libraries.
func backfillPersonTallyTx(ctx context.Context, tx *sql.Tx, isPG bool, libraryID int64) error {
	minPID := core.PluginPersonIDBase
	maxPID := core.PluginPersonIDBase + core.PluginPersonIDSpan
	minExpr := `MIN(f.v_text)`
	if isPG {
		minExpr = `MIN(f.v_text COLLATE "C")`
	}

	var (
		where strings.Builder
		args  []any
	)
	where.WriteString(`f.v_int <> 0 AND f.v_int > ? AND f.v_int < ? AND f.v_text <> '' AND i.level <> ''`)
	args = append(args, minPID, maxPID)
	if libraryID > 0 {
		where.WriteString(` AND i.library_id = ?`)
		args = append(args, libraryID)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT i.library_id, i.level, f.field, f.v_int, `+minExpr+`, COUNT(*)
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE `+where.String()+`
GROUP BY i.library_id, i.level, f.field, f.v_int`, args...)
	if err != nil {
		return err
	}

	type perLevelRow struct {
		libID int64
		level string
		field string
		vInt  int64
		label string
		cnt   int64
	}
	type aggKey struct {
		libID int64
		field string
		vInt  int64
	}
	type aggVal struct {
		label string
		cnt   int64
	}
	var perLevel []perLevelRow
	agg := map[aggKey]*aggVal{}
	for rows.Next() {
		var r perLevelRow
		if err := rows.Scan(&r.libID, &r.level, &r.field, &r.vInt, &r.label, &r.cnt); err != nil {
			rows.Close()
			return err
		}
		if r.label == "" || r.cnt <= 0 {
			continue
		}
		perLevel = append(perLevel, r)
		ak := aggKey{libID: r.libID, field: r.field, vInt: r.vInt}
		if cur, ok := agg[ak]; ok {
			cur.cnt += r.cnt
			if r.label < cur.label {
				cur.label = r.label
			}
		} else {
			agg[ak] = &aggVal{label: r.label, cnt: r.cnt}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, r := range perLevel {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_person_tally(library_id, level, field, v_int, label, label_lower, label_fold, cnt)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			r.libID, r.level, r.field, r.vInt, r.label, strings.ToLower(r.label), FoldFieldText(r.label), r.cnt); err != nil {
			return err
		}
	}
	if len(agg) == 0 {
		return nil
	}
	aggKeys := make([]aggKey, 0, len(agg))
	for k := range agg {
		aggKeys = append(aggKeys, k)
	}
	slices.SortFunc(aggKeys, func(a, b aggKey) int {
		if a.libID != b.libID {
			if a.libID < b.libID {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.field, b.field); c != 0 {
			return c
		}
		if a.vInt < b.vInt {
			return -1
		}
		if a.vInt > b.vInt {
			return 1
		}
		return 0
	})
	for _, k := range aggKeys {
		v := agg[k]
		if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_person_tally(library_id, level, field, v_int, label, label_lower, label_fold, cnt)
VALUES(?, '', ?, ?, ?, ?, ?, ?)`,
			k.libID, k.field, k.vInt, v.label, strings.ToLower(v.label), FoldFieldText(v.label), v.cnt); err != nil {
			return err
		}
	}
	return nil
}

type tallyItemRank struct {
	ref      string
	released int64
	addedAt  int64
	year     int64
}

func (a tallyItemRank) valid() bool { return a.ref != "" }

func (a tallyItemRank) beats(b tallyItemRank) bool {
	if a.ref == "" {
		return false
	}
	if b.ref == "" {
		return true
	}
	if a.released != b.released {
		return a.released > b.released
	}
	if a.addedAt != b.addedAt {
		return a.addedAt > b.addedAt
	}
	if a.year != b.year {
		return a.year > b.year
	}
	return a.ref < b.ref
}

func repBeatsExistingSQL(table, prefix string) string {
	return `(excluded.` + prefix + `_ref <> '' AND (` +
		table + `.` + prefix + `_ref = ''` +
		` OR excluded.` + prefix + `_released > ` + table + `.` + prefix + `_released` +
		` OR (excluded.` + prefix + `_released = ` + table + `.` + prefix + `_released AND excluded.` + prefix + `_added > ` + table + `.` + prefix + `_added)` +
		` OR (excluded.` + prefix + `_released = ` + table + `.` + prefix + `_released AND excluded.` + prefix + `_added = ` + table + `.` + prefix + `_added AND excluded.` + prefix + `_year > ` + table + `.` + prefix + `_year)` +
		` OR (excluded.` + prefix + `_released = ` + table + `.` + prefix + `_released AND excluded.` + prefix + `_added = ` + table + `.` + prefix + `_added AND excluded.` + prefix + `_year = ` + table + `.` + prefix + `_year AND excluded.` + prefix + `_ref < ` + table + `.` + prefix + `_ref)` +
		`))`
}

func paramBeatsStoredSQL(prefix string) string {
	return `(` + prefix + `_ref = ''` +
		` OR ? > ` + prefix + `_released` +
		` OR (? = ` + prefix + `_released AND ? > ` + prefix + `_added)` +
		` OR (? = ` + prefix + `_released AND ? = ` + prefix + `_added AND ? > ` + prefix + `_year)` +
		` OR (? = ` + prefix + `_released AND ? = ` + prefix + `_added AND ? = ` + prefix + `_year AND ? < ` + prefix + `_ref))`
}

func paramBeatsStoredArgs(r tallyItemRank) []any {
	return []any{
		r.released,
		r.released, r.addedAt,
		r.released, r.addedAt, r.year,
		r.released, r.addedAt, r.year, r.ref,
	}
}

// backfillTallyRepsTx populates rep_ref/rep_* on plugin_item_field_tally and
// rep_ref/rep_* + thumb_ref/thumb_* on plugin_item_person_tally from existing
// plugin_items and plugin_item_fields. libraryID == 0 backfills all libraries.
func backfillTallyRepsTx(ctx context.Context, tx *sql.Tx, isPG bool, libraryID int64) error {
	var (
		where strings.Builder
		args  []any
	)
	where.WriteString(`i.level <> '' AND f.v_text <> ''`)
	if libraryID > 0 {
		where.WriteString(` AND i.library_id = ?`)
		args = append(args, libraryID)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT i.library_id, i.level, f.field, f.v_text, f.v_int, i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
JOIN plugin_item_fields f ON f.item_ref = i.item_ref
WHERE `+where.String(), args...)
	if err != nil {
		return err
	}

	type fieldRepKey struct {
		libID int64
		level string
		field string
		text  string
	}
	type personRepKey struct {
		libID int64
		level string
		field string
		vInt  int64
	}
	fieldBest := map[fieldRepKey]tallyItemRank{}
	personBest := map[personRepKey]tallyItemRank{}

	for rows.Next() {
		var (
			libID, vInt, released, addedAt, year int64
			level, field, text, ref              string
		)
		if err := rows.Scan(&libID, &level, &field, &text, &vInt, &ref, &released, &addedAt, &year); err != nil {
			rows.Close()
			return err
		}
		rank := tallyItemRank{ref: ref, released: released, addedAt: addedAt, year: year}
		for _, lv := range [2]string{level, ""} {
			fk := fieldRepKey{libID: libID, level: lv, field: field, text: text}
			if cur, ok := fieldBest[fk]; !ok || rank.beats(cur) {
				fieldBest[fk] = rank
			}
		}
		if core.IsPluginPersonID(vInt) {
			for _, lv := range [2]string{level, ""} {
				pk := personRepKey{libID: libID, level: lv, field: field, vInt: vInt}
				if cur, ok := personBest[pk]; !ok || rank.beats(cur) {
					personBest[pk] = rank
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(fieldBest) > 0 {
		fKeys := make([]fieldRepKey, 0, len(fieldBest))
		for k := range fieldBest {
			fKeys = append(fKeys, k)
		}
		slices.SortFunc(fKeys, func(a, b fieldRepKey) int {
			if a.libID != b.libID {
				if a.libID < b.libID {
					return -1
				}
				return 1
			}
			if c := strings.Compare(a.level, b.level); c != 0 {
				return c
			}
			if c := strings.Compare(a.field, b.field); c != 0 {
				return c
			}
			return strings.Compare(a.text, b.text)
		})
		stmt, err := tx.PrepareContext(ctx, `
UPDATE plugin_item_field_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`)
		if err != nil {
			return err
		}
		for _, k := range fKeys {
			r := fieldBest[k]
			if _, err := stmt.ExecContext(ctx, r.ref, r.released, r.addedAt, r.year, k.libID, k.level, k.field, k.text); err != nil {
				stmt.Close()
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
	}

	if len(personBest) > 0 {
		pKeys := make([]personRepKey, 0, len(personBest))
		for k := range personBest {
			pKeys = append(pKeys, k)
		}
		slices.SortFunc(pKeys, func(a, b personRepKey) int {
			if a.libID != b.libID {
				if a.libID < b.libID {
					return -1
				}
				return 1
			}
			if c := strings.Compare(a.level, b.level); c != 0 {
				return c
			}
			if c := strings.Compare(a.field, b.field); c != 0 {
				return c
			}
			if a.vInt < b.vInt {
				return -1
			}
			if a.vInt > b.vInt {
				return 1
			}
			return 0
		})
		stmt, err := tx.PrepareContext(ctx, `
UPDATE plugin_item_person_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`)
		if err != nil {
			return err
		}
		for _, k := range pKeys {
			r := personBest[k]
			if _, err := stmt.ExecContext(ctx, r.ref, r.released, r.addedAt, r.year, k.libID, k.level, k.field, k.vInt); err != nil {
				stmt.Close()
				return err
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
	}

	// Scan B: populate thumb_ref on plugin_item_person_tally for libraries
	// declaring at least one type:"people" field (only items whose fields JSON
	// mentions "thumb" need parsing).
	type libPeopleFields struct {
		libID       int64
		allFields   []string
		hasObjField bool
	}
	var (
		kindRows *sql.Rows
		kErr     error
	)
	if libraryID > 0 {
		kindRows, kErr = tx.QueryContext(ctx,
			`SELECT library_id, decl FROM plugin_kinds WHERE library_id = ? ORDER BY library_id ASC`, libraryID)
	} else {
		kindRows, kErr = tx.QueryContext(ctx,
			`SELECT library_id, decl FROM plugin_kinds ORDER BY library_id ASC`)
	}
	if kErr != nil {
		return kErr
	}
	var libsWithPeople []libPeopleFields
	for kindRows.Next() {
		var libID int64
		var declJSON string
		if err := kindRows.Scan(&libID, &declJSON); err != nil {
			kindRows.Close()
			return err
		}
		var decl plugins.MediaKindMeta
		if err := json.Unmarshal([]byte(declJSON), &decl); err != nil {
			continue
		}
		var lp libPeopleFields
		lp.libID = libID
		for _, fd := range decl.Fields {
			if isPeopleIndexedField(fd) {
				lp.allFields = append(lp.allFields, fd.Key)
				if fd.Type == "people" {
					lp.hasObjField = true
				}
			}
		}
		if lp.hasObjField && len(lp.allFields) > 0 {
			libsWithPeople = append(libsWithPeople, lp)
		}
	}
	if err := kindRows.Err(); err != nil {
		kindRows.Close()
		return err
	}
	kindRows.Close()

	if len(libsWithPeople) == 0 {
		return nil
	}

	likeOp := `LIKE`
	if isPG {
		likeOp = `ILIKE`
	}
	thumbBest := map[personRepKey]tallyItemRank{}
	for _, lp := range libsWithPeople {
		tRows, err := tx.QueryContext(ctx, `
SELECT item_ref, level, released, added_at, year, fields
FROM plugin_items
WHERE library_id = ? AND level <> '' AND fields `+likeOp+` '%thumb%'`, lp.libID)
		if err != nil {
			return err
		}
		for tRows.Next() {
			var (
				ref, level, fieldsJSON string
				released, addedAt, yr  int64
			)
			if err := tRows.Scan(&ref, &level, &released, &addedAt, &yr, &fieldsJSON); err != nil {
				tRows.Close()
				return err
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil || len(fields) == 0 {
				continue
			}
			// Match extractProjectedPeople: any declared people field on the item
			// carrying a validated thumb for pid provides a portrait for every
			// people field crediting pid on that item.
			thumbPIDs := map[int64]bool{}
			fieldPIDs := make(map[string][]int64, len(lp.allFields))
			for _, fk := range lp.allFields {
				people := extractPeopleFromFields(fields, fk)
				if len(people) == 0 {
					continue
				}
				seen := map[int64]bool{}
				for _, p := range people {
					if !core.IsPluginPersonID(p.TMDB) {
						continue
					}
					if p.ThumbPath != "" {
						thumbPIDs[p.TMDB] = true
					}
					if !seen[p.TMDB] {
						seen[p.TMDB] = true
						fieldPIDs[fk] = append(fieldPIDs[fk], p.TMDB)
					}
				}
			}
			if len(thumbPIDs) == 0 {
				continue
			}
			rank := tallyItemRank{ref: ref, released: released, addedAt: addedAt, year: yr}
			for fk, pids := range fieldPIDs {
				for _, pid := range pids {
					if !thumbPIDs[pid] {
						continue
					}
					for _, lv := range [2]string{level, ""} {
						pk := personRepKey{libID: lp.libID, level: lv, field: fk, vInt: pid}
						if cur, ok := thumbBest[pk]; !ok || rank.beats(cur) {
							thumbBest[pk] = rank
						}
					}
				}
			}
		}
		if err := tRows.Err(); err != nil {
			tRows.Close()
			return err
		}
		tRows.Close()
	}

	if len(thumbBest) == 0 {
		return nil
	}
	tKeys := make([]personRepKey, 0, len(thumbBest))
	for k := range thumbBest {
		tKeys = append(tKeys, k)
	}
	slices.SortFunc(tKeys, func(a, b personRepKey) int {
		if a.libID != b.libID {
			if a.libID < b.libID {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.level, b.level); c != 0 {
			return c
		}
		if c := strings.Compare(a.field, b.field); c != 0 {
			return c
		}
		if a.vInt < b.vInt {
			return -1
		}
		if a.vInt > b.vInt {
			return 1
		}
		return 0
	})
	stmt, err := tx.PrepareContext(ctx, `
UPDATE plugin_item_person_tally
SET thumb_ref = ?, thumb_released = ?, thumb_added = ?, thumb_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`)
	if err != nil {
		return err
	}
	for _, k := range tKeys {
		r := thumbBest[k]
		if _, err := stmt.ExecContext(ctx, r.ref, r.released, r.addedAt, r.year, k.libID, k.level, k.field, k.vInt); err != nil {
			stmt.Close()
			return err
		}
	}
	return stmt.Close()
}

func itemHasPersonThumb(fieldsJSON string, peopleFields []string, targetPID int64) bool {
	if fieldsJSON == "" || len(peopleFields) == 0 || !core.IsPluginPersonID(targetPID) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil || len(fields) == 0 {
		return false
	}
	for _, p := range extractPeopleFromFields(fields, peopleFields...) {
		if p.TMDB == targetPID && p.ThumbPath != "" {
			return true
		}
	}
	return false
}

type fieldTallyChange struct {
	delta       int64
	bestAdded   tallyItemRank
	removedRefs map[string]struct{}
}

func addFieldTallyChange(delta map[fieldTallyKey]*fieldTallyChange, level, field, text string, d int64, added tallyItemRank, removedRef string) {
	if level == "" || field == "" || text == "" || (d == 0 && !added.valid() && removedRef == "") {
		return
	}
	for _, lv := range [2]string{level, ""} {
		k := fieldTallyKey{level: lv, field: field, text: text}
		ch := delta[k]
		if ch == nil {
			ch = &fieldTallyChange{}
			delta[k] = ch
		}
		ch.delta += d
		if added.valid() && added.beats(ch.bestAdded) {
			ch.bestAdded = added
		}
		if removedRef != "" {
			if ch.removedRefs == nil {
				ch.removedRefs = map[string]struct{}{}
			}
			ch.removedRefs[removedRef] = struct{}{}
		}
	}
}

const recomputeSortProbeMinCount = 512

func recomputeFieldRepTx(ctx context.Context, tx *sql.Tx, isPG bool, libID int64, level, field, text string, curCnt int64) (tallyItemRank, error) {
	if curCnt >= recomputeSortProbeMinCount {
		levelCol := `+i.level`
		if isPG {
			levelCol = `i.level`
		}
		var (
			probeSQL  string
			probeArgs []any
		)
		if level != "" {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` = ?
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 1`
			probeArgs = []any{libID, level, field, text}
		} else {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` <> ''
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 1`
			probeArgs = []any{libID, field, text}
		}
		var cand tallyItemRank
		err := tx.QueryRowContext(ctx, probeSQL, probeArgs...).Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year)
		if errors.Is(err, sql.ErrNoRows) {
			return tallyItemRank{}, nil
		}
		if err != nil {
			return tallyItemRank{}, err
		}
		// Confirm exact tie-breaking on (added_at DESC, year DESC, item_ref ASC)
		// when more than one item in the library shares cand.released.
		var tieCnt int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM (SELECT 1 FROM plugin_items WHERE library_id = ? AND released = ? LIMIT 257) s`,
			libID, cand.released).Scan(&tieCnt); err != nil {
			return tallyItemRank{}, err
		}
		if tieCnt <= 1 {
			return cand, nil
		}
		if tieCnt <= 256 {
			var (
				tieSQL  string
				tieArgs []any
			)
			if level != "" {
				tieSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND i.released = ? AND ` + levelCol + ` = ?
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = ?)
ORDER BY i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
				tieArgs = []any{libID, cand.released, level, field, text}
			} else {
				tieSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND i.released = ? AND ` + levelCol + ` <> ''
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_text = ?)
ORDER BY i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
				tieArgs = []any{libID, cand.released, field, text}
			}
			var exact tallyItemRank
			err := tx.QueryRowContext(ctx, tieSQL, tieArgs...).Scan(&exact.ref, &exact.released, &exact.addedAt, &exact.year)
			if errors.Is(err, sql.ErrNoRows) {
				return tallyItemRank{}, nil
			}
			return exact, err
		}
	}

	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_text = ? AND ` + libCheck + ` AND i.level = ?
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, text, libID, level}
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_text = ? AND ` + libCheck + ` AND i.level <> ''
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, text, libID}
	}
	var out tallyItemRank
	err := tx.QueryRowContext(ctx, q, args...).Scan(&out.ref, &out.released, &out.addedAt, &out.year)
	if errors.Is(err, sql.ErrNoRows) {
		return tallyItemRank{}, nil
	}
	return out, err
}

func applyFieldTallyDeltaTx(ctx context.Context, tx *sql.Tx, isPG bool, libraryID core.LibraryID, delta map[fieldTallyKey]*fieldTallyChange) (map[fieldTallyKey]bool, error) {
	if len(delta) == 0 || libraryID <= 0 {
		return nil, nil
	}
	keys := make([]fieldTallyKey, 0, len(delta))
	for k, ch := range delta {
		if ch.delta != 0 || ch.bestAdded.valid() || len(ch.removedRefs) > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	slices.SortFunc(keys, func(a, b fieldTallyKey) int {
		if c := strings.Compare(a.level, b.level); c != 0 {
			return c
		}
		if c := strings.Compare(a.field, b.field); c != 0 {
			return c
		}
		return strings.Compare(a.text, b.text)
	})
	libID := int64(libraryID)
	repBeats := repBeatsExistingSQL("plugin_item_field_tally", "rep")
	repParamBeats := paramBeatsStoredSQL("rep")
	var emptied map[fieldTallyKey]bool
	for _, k := range keys {
		ch := delta[k]
		deleted := false
		if ch.delta > 0 {
			r := ch.bestAdded
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_field_tally(
	library_id, level, field, v_text, v_lower, v_fold, cnt,
	rep_ref, rep_released, rep_added, rep_year
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(library_id, level, field, v_text) DO UPDATE SET
	cnt          = plugin_item_field_tally.cnt + excluded.cnt,
	rep_released = CASE WHEN `+repBeats+` THEN excluded.rep_released ELSE plugin_item_field_tally.rep_released END,
	rep_added    = CASE WHEN `+repBeats+` THEN excluded.rep_added    ELSE plugin_item_field_tally.rep_added END,
	rep_year     = CASE WHEN `+repBeats+` THEN excluded.rep_year     ELSE plugin_item_field_tally.rep_year END,
	rep_ref      = CASE WHEN `+repBeats+` THEN excluded.rep_ref      ELSE plugin_item_field_tally.rep_ref END`,
				libID, k.level, k.field, k.text, strings.ToLower(k.text), FoldFieldText(k.text), ch.delta,
				r.ref, r.released, r.addedAt, r.year); err != nil {
				return nil, err
			}
		} else if ch.delta < 0 {
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET cnt = cnt + ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
				ch.delta, libID, k.level, k.field, k.text); err != nil {
				return nil, err
			}
			res, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_field_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ? AND cnt <= 0`,
				libID, k.level, k.field, k.text)
			if err != nil {
				return nil, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if emptied == nil {
					emptied = map[fieldTallyKey]bool{}
				}
				emptied[k] = true
				deleted = true
			}
		}
		if deleted {
			continue
		}

		if ch.delta <= 0 && ch.bestAdded.valid() {
			r := ch.bestAdded
			pArgs := make([]any, 0, 18)
			pArgs = append(pArgs, r.released, r.addedAt, r.year, r.ref, libID, k.level, k.field, k.text)
			pArgs = append(pArgs, paramBeatsStoredArgs(r)...)
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET rep_released = ?, rep_added = ?, rep_year = ?, rep_ref = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ? AND `+repParamBeats, pArgs...); err != nil {
				return nil, err
			}
		}

		if len(ch.removedRefs) > 0 {
			var curRep string
			var curCnt int64
			err := tx.QueryRowContext(ctx, `
SELECT rep_ref, cnt
FROM plugin_item_field_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
				libID, k.level, k.field, k.text).Scan(&curRep, &curCnt)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if curRep != "" {
				if _, hit := ch.removedRefs[curRep]; !hit {
					continue
				}
			}
			winner, err := recomputeFieldRepTx(ctx, tx, isPG, libID, k.level, k.field, k.text, curCnt)
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
				winner.ref, winner.released, winner.addedAt, winner.year,
				libID, k.level, k.field, k.text); err != nil {
				return nil, err
			}
		}
	}
	return emptied, nil
}

type personTallyKey struct {
	level string
	field string
	vInt  int64
}

type personTallyChange struct {
	delta            int64
	minAdded         string
	removedTexts     map[string]struct{}
	bestRepAdded     tallyItemRank
	removedRepRefs   map[string]struct{}
	bestThumbAdded   tallyItemRank
	removedThumbRefs map[string]struct{}
}

func addPersonTallyChange(
	delta map[personTallyKey]*personTallyChange,
	level, field string,
	vInt int64,
	d int64,
	addedText, removedText string,
	addedRep tallyItemRank,
	removedRepRef string,
	addedThumb tallyItemRank,
	removedThumbRef string,
) {
	if level == "" || field == "" || !core.IsPluginPersonID(vInt) {
		return
	}
	if d == 0 && addedText == "" && removedText == "" && !addedRep.valid() && removedRepRef == "" && !addedThumb.valid() && removedThumbRef == "" {
		return
	}
	for _, lv := range [2]string{level, ""} {
		k := personTallyKey{level: lv, field: field, vInt: vInt}
		ch := delta[k]
		if ch == nil {
			ch = &personTallyChange{}
			delta[k] = ch
		}
		ch.delta += d
		if addedText != "" && (ch.minAdded == "" || addedText < ch.minAdded) {
			ch.minAdded = addedText
		}
		if removedText != "" {
			if ch.removedTexts == nil {
				ch.removedTexts = map[string]struct{}{}
			}
			ch.removedTexts[removedText] = struct{}{}
		}
		if addedRep.valid() && addedRep.beats(ch.bestRepAdded) {
			ch.bestRepAdded = addedRep
		}
		if removedRepRef != "" {
			if ch.removedRepRefs == nil {
				ch.removedRepRefs = map[string]struct{}{}
			}
			ch.removedRepRefs[removedRepRef] = struct{}{}
		}
		if addedThumb.valid() && addedThumb.beats(ch.bestThumbAdded) {
			ch.bestThumbAdded = addedThumb
		}
		if removedThumbRef != "" {
			if ch.removedThumbRefs == nil {
				ch.removedThumbRefs = map[string]struct{}{}
			}
			ch.removedThumbRefs[removedThumbRef] = struct{}{}
		}
	}
}

func recomputePersonRepTx(ctx context.Context, tx *sql.Tx, isPG bool, libID int64, level, field string, vInt, curCnt int64) (tallyItemRank, error) {
	if curCnt >= recomputeSortProbeMinCount {
		levelCol := `+i.level`
		if isPG {
			levelCol = `i.level`
		}
		var (
			probeSQL  string
			probeArgs []any
		)
		if level != "" {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` = ?
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 1`
			probeArgs = []any{libID, level, field, vInt}
		} else {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` <> ''
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 1`
			probeArgs = []any{libID, field, vInt}
		}
		var cand tallyItemRank
		err := tx.QueryRowContext(ctx, probeSQL, probeArgs...).Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year)
		if errors.Is(err, sql.ErrNoRows) {
			return tallyItemRank{}, nil
		}
		if err != nil {
			return tallyItemRank{}, err
		}
		var tieCnt int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM (SELECT 1 FROM plugin_items WHERE library_id = ? AND released = ? LIMIT 257) s`,
			libID, cand.released).Scan(&tieCnt); err != nil {
			return tallyItemRank{}, err
		}
		if tieCnt <= 1 {
			return cand, nil
		}
		if tieCnt <= 256 {
			var (
				tieSQL  string
				tieArgs []any
			)
			if level != "" {
				tieSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND i.released = ? AND ` + levelCol + ` = ?
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
				tieArgs = []any{libID, cand.released, level, field, vInt}
			} else {
				tieSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND i.released = ? AND ` + levelCol + ` <> ''
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
				tieArgs = []any{libID, cand.released, field, vInt}
			}
			var exact tallyItemRank
			err := tx.QueryRowContext(ctx, tieSQL, tieArgs...).Scan(&exact.ref, &exact.released, &exact.addedAt, &exact.year)
			if errors.Is(err, sql.ErrNoRows) {
				return tallyItemRank{}, nil
			}
			return exact, err
		}
	}

	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level = ? AND f.v_text <> ''
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, vInt, libID, level}
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level <> '' AND f.v_text <> ''
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, vInt, libID}
	}
	var out tallyItemRank
	err := tx.QueryRowContext(ctx, q, args...).Scan(&out.ref, &out.released, &out.addedAt, &out.year)
	if errors.Is(err, sql.ErrNoRows) {
		return tallyItemRank{}, nil
	}
	return out, err
}

func recomputePersonThumbTx(ctx context.Context, tx *sql.Tx, isPG bool, libID int64, level, field string, vInt, curCnt int64, peopleFields []string) (tallyItemRank, error) {
	if len(peopleFields) == 0 {
		return tallyItemRank{}, nil
	}
	likeOp := `LIKE`
	if isPG {
		likeOp = `ILIKE`
	}
	if curCnt >= recomputeSortProbeMinCount {
		levelCol := `+i.level`
		if isPG {
			levelCol = `i.level`
		}
		var (
			probeSQL  string
			probeArgs []any
		)
		if level != "" {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` = ?
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 16`
			probeArgs = []any{libID, level, field, vInt}
		} else {
			probeSQL = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_items i
WHERE i.library_id = ? AND ` + levelCol + ` <> ''
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND f.field = ? AND f.v_int <> 0 AND f.v_int = ?)
ORDER BY i.released DESC, i.item_ref ASC
LIMIT 16`
			probeArgs = []any{libID, field, vInt}
		}
		rows, err := tx.QueryContext(ctx, probeSQL, probeArgs...)
		if err != nil {
			return tallyItemRank{}, err
		}
		var (
			best     tallyItemRank
			lastRel  int64
			rowCount int
			probeErr error
		)
		for rows.Next() {
			var cand tallyItemRank
			var fieldsJSON string
			if err := rows.Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year, &fieldsJSON); err != nil {
				probeErr = err
				break
			}
			rowCount++
			lastRel = cand.released
			if best.valid() && cand.released < best.released {
				// All remaining rows have strictly smaller released, so best is
				// provably the exact global winner.
				rows.Close()
				return best, nil
			}
			if strings.Contains(strings.ToLower(fieldsJSON), "thumb") && itemHasPersonThumb(fieldsJSON, peopleFields, vInt) && cand.beats(best) {
				best = cand
			}
		}
		if probeErr == nil {
			probeErr = rows.Err()
		}
		rows.Close()
		if probeErr != nil {
			return tallyItemRank{}, probeErr
		}
		// If fewer than 16 rows exist in the bucket or we found a thumb whose
		// released strictly exceeds the 16th row's released, best is exact.
		if rowCount < 16 || (best.valid() && best.released > lastRel) {
			return best, nil
		}
	}

	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level = ? AND i.fields ` + likeOp + ` '%thumb%'
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC`
		args = []any{field, vInt, libID, level}
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level <> '' AND i.fields ` + likeOp + ` '%thumb%'
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC`
		args = []any{field, vInt, libID}
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return tallyItemRank{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var cand tallyItemRank
		var fieldsJSON string
		if err := rows.Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year, &fieldsJSON); err != nil {
			return tallyItemRank{}, err
		}
		if itemHasPersonThumb(fieldsJSON, peopleFields, vInt) {
			return cand, nil
		}
	}
	return tallyItemRank{}, rows.Err()
}

func applyPersonTallyDeltaTx(ctx context.Context, tx *sql.Tx, isPG bool, libraryID core.LibraryID, decl plugins.MediaKindMeta, delta map[personTallyKey]*personTallyChange, emptiedText map[fieldTallyKey]bool) error {
	if len(delta) == 0 || libraryID <= 0 {
		return nil
	}
	keys := make([]personTallyKey, 0, len(delta))
	for k, ch := range delta {
		if ch.delta != 0 || ch.minAdded != "" || len(ch.removedTexts) > 0 ||
			ch.bestRepAdded.valid() || len(ch.removedRepRefs) > 0 ||
			ch.bestThumbAdded.valid() || len(ch.removedThumbRefs) > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	slices.SortFunc(keys, func(a, b personTallyKey) int {
		if c := strings.Compare(a.level, b.level); c != 0 {
			return c
		}
		if c := strings.Compare(a.field, b.field); c != 0 {
			return c
		}
		if a.vInt < b.vInt {
			return -1
		}
		if a.vInt > b.vInt {
			return 1
		}
		return 0
	})
	libID := int64(libraryID)
	minExpr := `MIN(f.v_text)`
	libCheck := `i.library_id + 0 = ?`
	if isPG {
		minExpr = `MIN(f.v_text COLLATE "C")`
		libCheck = `i.library_id = ?`
	}
	repBeats := repBeatsExistingSQL("plugin_item_person_tally", "rep")
	thumbBeats := repBeatsExistingSQL("plugin_item_person_tally", "thumb")
	repParamBeats := paramBeatsStoredSQL("rep")
	thumbParamBeats := paramBeatsStoredSQL("thumb")
	peopleFields := declaredPeopleFields(decl)

	for _, k := range keys {
		ch := delta[k]
		deleted := false
		if ch.delta > 0 {
			lbl := ch.minAdded
			rep := ch.bestRepAdded
			th := ch.bestThumbAdded
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_person_tally(
	library_id, level, field, v_int, label, label_lower, label_fold, cnt,
	rep_ref, rep_released, rep_added, rep_year,
	thumb_ref, thumb_released, thumb_added, thumb_year
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(library_id, level, field, v_int) DO UPDATE SET
	cnt            = plugin_item_person_tally.cnt + excluded.cnt,
	label          = CASE WHEN excluded.label < plugin_item_person_tally.label THEN excluded.label ELSE plugin_item_person_tally.label END,
	label_lower    = CASE WHEN excluded.label < plugin_item_person_tally.label THEN excluded.label_lower ELSE plugin_item_person_tally.label_lower END,
	label_fold     = CASE WHEN excluded.label < plugin_item_person_tally.label THEN excluded.label_fold ELSE plugin_item_person_tally.label_fold END,
	rep_released   = CASE WHEN `+repBeats+` THEN excluded.rep_released ELSE plugin_item_person_tally.rep_released END,
	rep_added      = CASE WHEN `+repBeats+` THEN excluded.rep_added    ELSE plugin_item_person_tally.rep_added END,
	rep_year       = CASE WHEN `+repBeats+` THEN excluded.rep_year     ELSE plugin_item_person_tally.rep_year END,
	rep_ref        = CASE WHEN `+repBeats+` THEN excluded.rep_ref      ELSE plugin_item_person_tally.rep_ref END,
	thumb_released = CASE WHEN `+thumbBeats+` THEN excluded.thumb_released ELSE plugin_item_person_tally.thumb_released END,
	thumb_added    = CASE WHEN `+thumbBeats+` THEN excluded.thumb_added    ELSE plugin_item_person_tally.thumb_added END,
	thumb_year     = CASE WHEN `+thumbBeats+` THEN excluded.thumb_year     ELSE plugin_item_person_tally.thumb_year END,
	thumb_ref      = CASE WHEN `+thumbBeats+` THEN excluded.thumb_ref      ELSE plugin_item_person_tally.thumb_ref END`,
				libID, k.level, k.field, k.vInt, lbl, strings.ToLower(lbl), FoldFieldText(lbl), ch.delta,
				rep.ref, rep.released, rep.addedAt, rep.year,
				th.ref, th.released, th.addedAt, th.year); err != nil {
				return err
			}
		} else if ch.delta < 0 {
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET cnt = cnt + ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				ch.delta, libID, k.level, k.field, k.vInt); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND cnt <= 0`,
				libID, k.level, k.field, k.vInt)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				deleted = true
			}
		}
		if deleted {
			continue
		}

		needsLabelRefresh := false
		for rt := range ch.removedTexts {
			if emptiedText[fieldTallyKey{level: k.level, field: k.field, text: rt}] {
				needsLabelRefresh = true
				break
			}
		}
		if needsLabelRefresh {
			var newLabel sql.NullString
			var err error
			if k.level != "" {
				err = tx.QueryRowContext(ctx, `
SELECT `+minExpr+`
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND `+libCheck+` AND i.level = ? AND f.v_text <> ''`,
					k.field, k.vInt, libID, k.level).Scan(&newLabel)
			} else {
				err = tx.QueryRowContext(ctx, `
SELECT `+minExpr+`
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND `+libCheck+` AND i.level <> '' AND f.v_text <> ''`,
					k.field, k.vInt, libID).Scan(&newLabel)
			}
			if err != nil {
				return err
			}
			if !newLabel.Valid || newLabel.String == "" {
				if _, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					libID, k.level, k.field, k.vInt); err != nil {
					return err
				}
				continue
			}
			lbl := newLabel.String
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET label = ?, label_lower = ?, label_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				lbl, strings.ToLower(lbl), FoldFieldText(lbl), libID, k.level, k.field, k.vInt); err != nil {
				return err
			}
		} else if ch.delta <= 0 && ch.minAdded != "" {
			lbl := ch.minAdded
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET label = ?, label_lower = ?, label_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND ? < label`,
				lbl, strings.ToLower(lbl), FoldFieldText(lbl), libID, k.level, k.field, k.vInt, lbl); err != nil {
				return err
			}
		}

		if ch.delta <= 0 {
			if ch.bestRepAdded.valid() {
				r := ch.bestRepAdded
				pArgs := make([]any, 0, 18)
				pArgs = append(pArgs, r.released, r.addedAt, r.year, r.ref, libID, k.level, k.field, k.vInt)
				pArgs = append(pArgs, paramBeatsStoredArgs(r)...)
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET rep_released = ?, rep_added = ?, rep_year = ?, rep_ref = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND `+repParamBeats, pArgs...); err != nil {
					return err
				}
			}
			if ch.bestThumbAdded.valid() {
				r := ch.bestThumbAdded
				pArgs := make([]any, 0, 18)
				pArgs = append(pArgs, r.released, r.addedAt, r.year, r.ref, libID, k.level, k.field, k.vInt)
				pArgs = append(pArgs, paramBeatsStoredArgs(r)...)
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET thumb_released = ?, thumb_added = ?, thumb_year = ?, thumb_ref = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND `+thumbParamBeats, pArgs...); err != nil {
					return err
				}
			}
		}

		if len(ch.removedRepRefs) > 0 || len(ch.removedThumbRefs) > 0 {
			var curRep, curThumb string
			var curCnt int64
			err := tx.QueryRowContext(ctx, `
SELECT rep_ref, thumb_ref, cnt
FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				libID, k.level, k.field, k.vInt).Scan(&curRep, &curThumb, &curCnt)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			needRep := len(ch.removedRepRefs) > 0 && (curRep == "" || func() bool { _, hit := ch.removedRepRefs[curRep]; return hit }())
			needThumb := len(ch.removedThumbRefs) > 0 && curThumb != "" && func() bool { _, hit := ch.removedThumbRefs[curThumb]; return hit }()
			if needRep {
				winner, err := recomputePersonRepTx(ctx, tx, isPG, libID, k.level, k.field, k.vInt, curCnt)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					winner.ref, winner.released, winner.addedAt, winner.year,
					libID, k.level, k.field, k.vInt); err != nil {
					return err
				}
			}
			if needThumb {
				winner, err := recomputePersonThumbTx(ctx, tx, isPG, libID, k.level, k.field, k.vInt, curCnt, peopleFields)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET thumb_ref = ?, thumb_released = ?, thumb_added = ?, thumb_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					winner.ref, winner.released, winner.addedAt, winner.year,
					libID, k.level, k.field, k.vInt); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// libraryTallyState holds the per-level item counts read from plugin_item_tally
// for one library, along with whether the tally tables are populated for it.
// Active is false only for raw-SQL fixtures that seed plugin_items without
// going through Service write paths.
type libraryTallyState struct {
	active      bool
	libTotal    int
	levelCounts map[string]int
}

func (st libraryTallyState) countFor(level string) int {
	if level == "" {
		return st.libTotal
	}
	return st.levelCounts[level]
}

// isSingleLevel reports whether every indexed item in the library belongs to
// level, allowing sort-index scans on library_id to skip redundant level checks.
func (st libraryTallyState) isSingleLevel(level string) bool {
	return st.active && level != "" && st.libTotal > 0 && st.levelCounts[level] == st.libTotal
}

// libraryTally returns the cached per-request plugin_item_tally summary for
// libraryID. Results are memoized on ctx keyed by s.writeGen so repeated calls
// in the same request (e.g. across declared shelves) issue one query total.
func (s *Service) libraryTally(ctx context.Context, libraryID core.LibraryID) (libraryTallyState, error) {
	if s == nil || s.db == nil || libraryID <= 0 {
		return libraryTallyState{}, nil
	}
	key := fmt.Sprintf("pluginitems:libtally:%p:%d:%d", s, s.writeGen.Load(), libraryID)
	return reqmemo.Do(ctx, key, func() (libraryTallyState, error) {
		rows, err := s.db.QueryContext(ctx,
			`SELECT level, n FROM plugin_item_tally WHERE library_id = ?`,
			int64(libraryID))
		if err != nil {
			return libraryTallyState{}, err
		}
		st := libraryTallyState{levelCounts: map[string]int{}}
		for rows.Next() {
			var lv string
			var n int
			if err := rows.Scan(&lv, &n); err != nil {
				rows.Close()
				return libraryTallyState{}, err
			}
			if n > 0 {
				st.levelCounts[lv] = n
				st.libTotal += n
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return libraryTallyState{}, err
		}
		rows.Close()

		if st.libTotal > 0 {
			st.active = true
			return st, nil
		}
		var hasAny bool
		if err := s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM plugin_items WHERE library_id = ?)`,
			int64(libraryID)).Scan(&hasAny); err != nil {
			return libraryTallyState{}, err
		}
		st.active = !hasAny
		return st, nil
	})
}

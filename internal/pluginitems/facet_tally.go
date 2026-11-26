package pluginitems

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// pgIndexPrefixRunes bounds PostgreSQL btree expression index prefixes on text
// columns (left(col, 256)). At most 256 runes × 4 UTF-8 bytes = 1,024 bytes,
// keeping every composite btree index tuple well below PostgreSQL's 2,704-byte
// version-4 btree maximum even for MaxFieldRunes (4,000-rune) values (#768).
const pgIndexPrefixRunes = 256

func pgTextPrefix(s string) string {
	n := 0
	for i := range s {
		if n == pgIndexPrefixRunes {
			return s[:i]
		}
		n++
	}
	return s
}

func pgTextMD5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func pgAllStrictlyShorterThanPrefix(vals []string) bool {
	for _, v := range vals {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) >= pgIndexPrefixRunes {
			return false
		}
	}
	return true
}

func appendPGPrefixIn(where *strings.Builder, args *[]any, col string, vals []string) {
	seen := make(map[string]struct{}, len(vals))
	prefixes := make([]string, 0, len(vals))
	for _, v := range vals {
		p := pgTextPrefix(v)
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			prefixes = append(prefixes, p)
		}
	}
	if pgAllStrictlyShorterThanPrefix(vals) {
		// Every requested value is valid UTF-8 with strictly fewer than 256
		// runes: any stored string of >= 256 runes has a 256-rune prefix and
		// cannot equal a < 256-rune target, while any < 256-rune stored string
		// satisfies left(col, 256) = col. Omitting the redundant `col IN (...)`
		// avoids double-counted planner selectivity and per-row filter work.
		if len(prefixes) == 1 {
			*args = append(*args, prefixes[0])
			where.WriteString(` AND left(` + col + `, 256) = ?`)
			return
		}
		pPh := make([]string, len(prefixes))
		for i, p := range prefixes {
			pPh[i] = "?"
			*args = append(*args, p)
		}
		where.WriteString(` AND left(` + col + `, 256) IN (` + strings.Join(pPh, ", ") + `)`)
		return
	}
	pPh := make([]string, len(prefixes))
	for i, p := range prefixes {
		pPh[i] = "?"
		*args = append(*args, p)
	}
	vPh := make([]string, len(vals))
	for i, v := range vals {
		vPh[i] = "?"
		*args = append(*args, v)
	}
	where.WriteString(` AND left(` + col + `, 256) IN (` + strings.Join(pPh, ", ") + `) AND ` + col + ` IN (` + strings.Join(vPh, ", ") + `)`)
}

func appendPGMD5In(where *strings.Builder, args *[]any, col string, vals []string) {
	seen := make(map[string]struct{}, len(vals))
	hashes := make([]string, 0, len(vals))
	for _, v := range vals {
		h := pgTextMD5(v)
		if _, dup := seen[h]; !dup {
			seen[h] = struct{}{}
			hashes = append(hashes, h)
		}
	}
	hPh := make([]string, len(hashes))
	for i, h := range hashes {
		hPh[i] = "?"
		*args = append(*args, h)
	}
	vPh := make([]string, len(vals))
	for i, v := range vals {
		vPh[i] = "?"
		*args = append(*args, v)
	}
	where.WriteString(` AND md5(` + col + `) IN (` + strings.Join(hPh, ", ") + `) AND ` + col + ` IN (` + strings.Join(vPh, ", ") + `)`)
}

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

	// postgresTallyIndexesDDL creates bounded-prefix PostgreSQL tally sort
	// indexes (left(..., 256)) so v0.3.18->v0.3.19 upgrades with long field
	// values never hit PostgreSQL's 2,704-byte btree tuple limit during
	// Migration 17/18 (#768).
	postgresTallyIndexesDDL = `
DROP INDEX IF EXISTS plugin_item_field_tally_count;
CREATE INDEX plugin_item_field_tally_count
	ON plugin_item_field_tally(library_id, level, field, cnt DESC, (left(v_lower, 256)));
DROP INDEX IF EXISTS plugin_item_field_tally_name;
CREATE INDEX plugin_item_field_tally_name
	ON plugin_item_field_tally(library_id, level, field, (left(v_lower, 256)));
DROP INDEX IF EXISTS plugin_item_person_tally_name;
CREATE INDEX plugin_item_person_tally_name
	ON plugin_item_person_tally(library_id, level, field, (left(label_lower, 256)), v_int);
DROP INDEX IF EXISTS plugin_item_person_tally_count;
CREATE INDEX plugin_item_person_tally_count
	ON plugin_item_person_tally(library_id, level, field, cnt DESC, (left(label_lower, 256)), v_int);
`

	migration19PostgresDDL = `
ALTER TABLE plugin_item_fields DROP CONSTRAINT IF EXISTS plugin_item_fields_pkey;
DROP INDEX IF EXISTS plugin_item_fields_pkey_idx;
CREATE UNIQUE INDEX plugin_item_fields_pkey_idx
	ON plugin_item_fields(item_ref, field, (md5(v_text)), v_int);
DROP INDEX IF EXISTS plugin_item_fields_lookup;
CREATE INDEX plugin_item_fields_lookup
	ON plugin_item_fields(field, (left(v_text, 256)), item_ref);
DROP INDEX IF EXISTS plugin_item_fields_fold;
CREATE INDEX plugin_item_fields_fold
	ON plugin_item_fields(field, (left(v_fold, 256)), v_int, item_ref);

ALTER TABLE plugin_item_field_tally DROP CONSTRAINT IF EXISTS plugin_item_field_tally_pkey;
DROP INDEX IF EXISTS plugin_item_field_tally_pkey_idx;
CREATE UNIQUE INDEX plugin_item_field_tally_pkey_idx
	ON plugin_item_field_tally(library_id, level, field, (md5(v_text)));
` + postgresTallyIndexesDDL
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
	_, err := tx.ExecContext(ctx, postgresTallyIndexesDDL)
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
	_, err := tx.ExecContext(ctx, postgresTallyIndexesDDL)
	return err
}

func backfillFacetTalliesTx(ctx context.Context, tx *sql.Tx, isPG bool) error {
	if err := backfillFieldTallyCaseTx(ctx, tx, isPG, 0); err != nil {
		return err
	}
	return backfillPersonTallyTx(ctx, tx, isPG, 0)
}

// backfillFieldTallyCaseTx populates v_lower and v_fold on plugin_item_field_tally
// in bounded keyset batches. libraryID == 0 backfills all libraries.
func backfillFieldTallyCaseTx(ctx context.Context, tx *sql.Tx, isPG bool, libraryID int64) error {
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
			var err error
			if isPG {
				_, err = tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET v_lower = ?, v_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND md5(v_text) = ? AND v_text = ?`,
					strings.ToLower(k.text), FoldFieldText(k.text),
					k.libID, k.level, k.field, pgTextMD5(k.text), k.text)
			} else {
				_, err = tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET v_lower = ?, v_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`,
					strings.ToLower(k.text), FoldFieldText(k.text),
					k.libID, k.level, k.field, k.text)
			}
			if err != nil {
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
		updateSQL := `
UPDATE plugin_item_field_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_text = ?`
		if isPG {
			updateSQL = `
UPDATE plugin_item_field_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND md5(v_text) = ? AND v_text = ?`
		}
		stmt, err := tx.PrepareContext(ctx, updateSQL)
		if err != nil {
			return err
		}
		for _, k := range fKeys {
			r := fieldBest[k]
			if isPG {
				_, err = stmt.ExecContext(ctx, r.ref, r.released, r.addedAt, r.year, k.libID, k.level, k.field, pgTextMD5(k.text), k.text)
			} else {
				_, err = stmt.ExecContext(ctx, r.ref, r.released, r.addedAt, r.year, k.libID, k.level, k.field, k.text)
			}
			if err != nil {
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

func wrapTallyItemErr(ref, field string, err error) error {
	if _, _, extID, ok := core.ItemRef(ref).Parse(); ok && extID != "" {
		if field != "" {
			return fmt.Errorf("pluginitems: kind item %q field %q: %w", extID, field, err)
		}
		return fmt.Errorf("pluginitems: kind item %q: %w", extID, err)
	}
	if field != "" {
		return fmt.Errorf("pluginitems: field %q: %w", field, err)
	}
	return err
}

type fieldTallyChange struct {
	delta       int64
	bestAdded   tallyItemRank
	removedRefs map[string]struct{}
	replacement *tallyItemRank
	sampleRef   string
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
		if ch.sampleRef == "" {
			if added.ref != "" {
				ch.sampleRef = added.ref
			} else if removedRef != "" {
				ch.sampleRef = removedRef
			}
		}
		if removedRef != "" {
			if ch.removedRefs == nil {
				ch.removedRefs = map[string]struct{}{}
			}
			ch.removedRefs[removedRef] = struct{}{}
		}
	}
}

// The write path keeps every tally row's representative exact. When a write
// removes (or demotes) the item that is a value's representative, the new
// representative is the best remaining holder, found by one of two plans:
//
//   - index-first: read the value's holders from plugin_item_fields_lookup and
//     keep the best. Cost ~ cnt holders.
//   - window probe: walk plugin_items_released newest-first and stop at the
//     first holder. Cost ~ library / cnt items when holders are spread out.
//
// The probe runs only when the value is common enough for it to win
// (cnt² ≥ 8·library, the read-side rule in shouldProbeNewestFromSortIndex).
// It also starts at the removed representative's released, because no
// remaining holder can rank above it, and it walks at most a bounded window
// (8·library/cnt, at least repProbeMinWindow, never more than cnt items).
// If the window holds no holder, the index-first plan answers exactly. A
// recompute therefore never costs more than about twice the index-first read,
// however the value's holders are spread across the library (#805).
const (
	repProbeMinCount  = 512
	repProbeMinWindow = 256
)

// repRecomputePlan is the plan for recomputing one value's representative.
type repRecomputePlan struct {
	// window is how many library items the probe may walk; 0 = index-first.
	window int64
	// below bounds the probe to released <= below when bounded is set.
	below   int64
	bounded bool
	// Exclude pre-chunk rows that will be removed or whose rank changes.
	// The caller compares the winner with the incoming candidates.
	excluded map[string]struct{}
}

func (p repRecomputePlan) exclusion(col string) (string, []any) {
	if len(p.excluded) == 0 {
		return "", nil
	}
	args := make([]any, 0, len(p.excluded))
	for ref := range p.excluded {
		args = append(args, ref)
	}
	return " AND " + col + " NOT IN (" + store.Placeholders(len(args)) + ")", args
}

// planRepRecompute chooses the recompute plan for a value held by cnt items
// in a library of libTotal indexed items. prev is the representative being
// replaced (its stored rank); the stored representative always ranks at or
// above every holder, so the probe may start there.
func planRepRecompute(cnt, libTotal int64, prev tallyItemRank) repRecomputePlan {
	if cnt < repProbeMinCount || cnt*cnt < 8*libTotal {
		return repRecomputePlan{}
	}
	window := max(int64(repProbeMinWindow), (8*libTotal+cnt-1)/cnt)
	p := repRecomputePlan{window: window}
	if prev.valid() {
		p.bounded = true
		p.below = prev.released
	}
	return p
}

// libraryItemTotalTx returns the library's indexed item count from
// plugin_item_tally (0 when the tally is empty).
func libraryItemTotal(ctx context.Context, tx queryer, libID int64) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(n), 0) FROM plugin_item_tally WHERE library_id = ?`, libID).Scan(&n)
	return n, err
}

// repWindowProbeSQL builds the window probe: walk at most plan.window items
// of the library newest-first (plugin_items_released), keep those at level
// ("" = every indexed level) holding the value per holdsCond over alias f,
// and return the first limit of them in released DESC, item_ref ASC order.
// The level check sits outside the LIMIT so the walk is bounded in index
// entries, not in matching rows. withFields adds the item's fields JSON,
// read only for the returned rows.
func repWindowProbeSQL(libID int64, level string, plan repRecomputePlan, holdsCond string, holdsArgs []any, withFields bool, limit int) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, 5+len(holdsArgs))
	b.WriteString(`
SELECT w.item_ref, w.released, w.added_at, w.year`)
	if withFields {
		b.WriteString(`, (SELECT x.fields FROM plugin_items x WHERE x.item_ref = w.item_ref)`)
	}
	b.WriteString(`
FROM (
	SELECT i.item_ref, i.released, i.added_at, i.year, i.level
	FROM plugin_items i
	WHERE i.library_id = ?`)
	args = append(args, libID)
	if plan.bounded {
		b.WriteString(` AND i.released <= ?`)
		args = append(args, plan.below)
	}
	b.WriteString(`
	ORDER BY i.released DESC, i.item_ref ASC
	LIMIT ?
) w
WHERE `)
	args = append(args, plan.window)
	if level != "" {
		b.WriteString(`w.level = ?`)
		args = append(args, level)
	} else {
		b.WriteString(`w.level <> ''`)
	}
	exclude, excluded := plan.exclusion("w.item_ref")
	b.WriteString(`
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = w.item_ref AND ` + holdsCond + `)` + exclude + `
ORDER BY w.released DESC, w.item_ref ASC
LIMIT ` + strconv.Itoa(limit))
	args = append(args, holdsArgs...)
	args = append(args, excluded...)
	return b.String(), args
}

// probeRepTx resolves the representative with the window probe. ok is false
// when the probe cannot answer exactly (no holder inside the window, or a
// released tie too wide to break cheaply); the caller then runs index-first.
func probeRepTx(ctx context.Context, tx queryer, isPG bool, libID int64, level string, plan repRecomputePlan, holdsCond string, holdsArgs []any) (tallyItemRank, bool, error) {
	if plan.window <= 0 {
		return tallyItemRank{}, false, nil
	}
	q, args := repWindowProbeSQL(libID, level, plan, holdsCond, holdsArgs, false, 1)
	var cand tallyItemRank
	err := tx.QueryRowContext(ctx, q, args...).Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year)
	if errors.Is(err, sql.ErrNoRows) {
		return tallyItemRank{}, false, nil
	}
	if err != nil {
		return tallyItemRank{}, false, err
	}
	// cand has the highest released of any holder. Break ties on
	// (added_at DESC, year DESC, item_ref ASC) when other library items share
	// cand.released; the tie query reads every such item, inside the window
	// or not.
	var tieCnt int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT 1 FROM plugin_items WHERE library_id = ? AND released = ? LIMIT 257) s`,
		libID, cand.released).Scan(&tieCnt); err != nil {
		return tallyItemRank{}, false, err
	}
	if tieCnt <= 1 {
		return cand, true, nil
	}
	if tieCnt > 256 {
		return tallyItemRank{}, false, nil
	}
	levelCond := `+i.level <> ''`
	tieArgs := []any{libID, cand.released}
	if level != "" {
		levelCond = `+i.level = ?`
		tieArgs = append(tieArgs, level)
	}
	if isPG {
		levelCond = strings.TrimPrefix(levelCond, "+")
	}
	tieArgs = append(tieArgs, holdsArgs...)
	exclude, excluded := plan.exclusion("i.item_ref")
	tieArgs = append(tieArgs, excluded...)
	var exact tallyItemRank
	err = tx.QueryRowContext(ctx, `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_items i
WHERE i.library_id = ? AND i.released = ? AND `+levelCond+`
  AND EXISTS (SELECT 1 FROM plugin_item_fields f WHERE f.item_ref = i.item_ref AND `+holdsCond+`)`+exclude+`
ORDER BY i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`, tieArgs...).Scan(&exact.ref, &exact.released, &exact.addedAt, &exact.year)
	if errors.Is(err, sql.ErrNoRows) {
		return tallyItemRank{}, false, nil
	}
	if err != nil {
		return tallyItemRank{}, false, err
	}
	return exact, true, nil
}

func recomputeFieldRepTx(ctx context.Context, tx queryer, isPG bool, libID int64, level, field, text string, plan repRecomputePlan) (tallyItemRank, error) {
	holdsCond := `f.field = ? AND f.v_text = ?`
	holdsArgs := []any{field, text}
	if isPG {
		holdsCond = `f.field = ? AND md5(f.v_text) = ? AND f.v_text = ?`
		holdsArgs = []any{field, pgTextMD5(text), text}
	}
	if rep, ok, err := probeRepTx(ctx, tx, isPG, libID, level, plan, holdsCond, holdsArgs); err != nil || ok {
		return rep, err
	}

	libCheck := `i.library_id + 0 = ?`
	lookupCond := `f.v_text = ?`
	lookupArgs := []any{field, text}
	if isPG {
		libCheck = `i.library_id = ?`
		lookupCond = `left(f.v_text, 256) = ? AND f.v_text = ?`
		lookupArgs = []any{field, pgTextPrefix(text), text}
	}
	exclude, excluded := plan.exclusion("i.item_ref")
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND ` + lookupCond + ` AND ` + libCheck + ` AND i.level = ?` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = append(lookupArgs, libID, level)
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND ` + lookupCond + ` AND ` + libCheck + ` AND i.level <> ''` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = append(lookupArgs, libID)
	}
	var out tallyItemRank
	err := tx.QueryRowContext(ctx, q, append(args, excluded...)...).Scan(&out.ref, &out.released, &out.addedAt, &out.year)
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
	conflictCols := `library_id, level, field, v_text`
	keyCond := `library_id = ? AND level = ? AND field = ? AND v_text = ?`
	if isPG {
		conflictCols = `library_id, level, field, md5(v_text)`
		keyCond = `library_id = ? AND level = ? AND field = ? AND md5(v_text) = ? AND v_text = ?`
	}
	keyArgs := func(k fieldTallyKey) []any {
		if isPG {
			return []any{libID, k.level, k.field, pgTextMD5(k.text), k.text}
		}
		return []any{libID, k.level, k.field, k.text}
	}
	var emptied map[fieldTallyKey]bool
	libTotal := int64(-1) // read on the first recompute
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
ON CONFLICT(`+conflictCols+`) DO UPDATE SET
	cnt          = plugin_item_field_tally.cnt + excluded.cnt,
	rep_released = CASE WHEN `+repBeats+` THEN excluded.rep_released ELSE plugin_item_field_tally.rep_released END,
	rep_added    = CASE WHEN `+repBeats+` THEN excluded.rep_added    ELSE plugin_item_field_tally.rep_added END,
	rep_year     = CASE WHEN `+repBeats+` THEN excluded.rep_year     ELSE plugin_item_field_tally.rep_year END,
	rep_ref      = CASE WHEN `+repBeats+` THEN excluded.rep_ref      ELSE plugin_item_field_tally.rep_ref END`,
				libID, k.level, k.field, k.text, strings.ToLower(k.text), FoldFieldText(k.text), ch.delta,
				r.ref, r.released, r.addedAt, r.year); err != nil {
				return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
		} else if ch.delta < 0 {
			uArgs := append([]any{ch.delta}, keyArgs(k)...)
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET cnt = cnt + ?
WHERE `+keyCond, uArgs...); err != nil {
				return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			res, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_field_tally
WHERE `+keyCond+` AND cnt <= 0`, keyArgs(k)...)
			if err != nil {
				return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
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
			pArgs := make([]any, 0, 19)
			pArgs = append(pArgs, r.released, r.addedAt, r.year, r.ref)
			pArgs = append(pArgs, keyArgs(k)...)
			pArgs = append(pArgs, paramBeatsStoredArgs(r)...)
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET rep_released = ?, rep_added = ?, rep_year = ?, rep_ref = ?
WHERE `+keyCond+` AND `+repParamBeats, pArgs...); err != nil {
				return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
		}

		if len(ch.removedRefs) > 0 {
			var winner tallyItemRank
			if ch.replacement != nil {
				winner = *ch.replacement
			} else {
				var cur tallyItemRank
				var curCnt int64
				err := tx.QueryRowContext(ctx, `
SELECT rep_ref, rep_released, rep_added, rep_year, cnt
FROM plugin_item_field_tally
WHERE `+keyCond, keyArgs(k)...).Scan(&cur.ref, &cur.released, &cur.addedAt, &cur.year, &curCnt)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
				if cur.ref != "" {
					if _, hit := ch.removedRefs[cur.ref]; !hit {
						continue
					}
				}
				if libTotal < 0 {
					if libTotal, err = libraryItemTotal(ctx, tx, libID); err != nil {
						return nil, err
					}
				}
				winner, err = recomputeFieldRepTx(ctx, tx, isPG, libID, k.level, k.field, k.text, planRepRecompute(curCnt, libTotal, cur))
				if err != nil {
					return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
			}
			wArgs := append([]any{winner.ref, winner.released, winner.addedAt, winner.year}, keyArgs(k)...)
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_field_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE `+keyCond, wArgs...); err != nil {
				return nil, wrapTallyItemErr(ch.sampleRef, k.field, err)
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
	removedLabelRefs map[string]struct{}
	repReplacement   *tallyItemRank
	thumbReplacement *tallyItemRank
	labelReplacement *sql.NullString
	sampleRef        string
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
	labelRef string,
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
		if ch.sampleRef == "" {
			switch {
			case addedRep.ref != "":
				ch.sampleRef = addedRep.ref
			case addedThumb.ref != "":
				ch.sampleRef = addedThumb.ref
			case removedRepRef != "":
				ch.sampleRef = removedRepRef
			case removedThumbRef != "":
				ch.sampleRef = removedThumbRef
			}
		}
		if addedText != "" && (ch.minAdded == "" || addedText < ch.minAdded) {
			ch.minAdded = addedText
		}
		if removedText != "" {
			if ch.removedTexts == nil {
				ch.removedTexts = map[string]struct{}{}
			}
			ch.removedTexts[removedText] = struct{}{}
			if ch.removedLabelRefs == nil {
				ch.removedLabelRefs = map[string]struct{}{}
			}
			ch.removedLabelRefs[labelRef] = struct{}{}
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

func recomputePersonRepTx(ctx context.Context, tx queryer, isPG bool, libID int64, level, field string, vInt int64, plan repRecomputePlan) (tallyItemRank, error) {
	holdsCond := `f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND f.v_text <> ''`
	holdsArgs := []any{field, vInt}
	if rep, ok, err := probeRepTx(ctx, tx, isPG, libID, level, plan, holdsCond, holdsArgs); err != nil || ok {
		return rep, err
	}

	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	exclude, excluded := plan.exclusion("i.item_ref")
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level = ? AND f.v_text <> ''` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, vInt, libID, level}
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level <> '' AND f.v_text <> ''` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC
LIMIT 1`
		args = []any{field, vInt, libID}
	}
	var out tallyItemRank
	err := tx.QueryRowContext(ctx, q, append(args, excluded...)...).Scan(&out.ref, &out.released, &out.addedAt, &out.year)
	if errors.Is(err, sql.ErrNoRows) {
		return tallyItemRank{}, nil
	}
	return out, err
}

// probePersonThumbTx resolves a person's thumb representative with the window
// probe: the best holder whose credit carries a portrait. ok is false when
// the window cannot prove the answer exact.
func probePersonThumbTx(ctx context.Context, tx queryer, libID int64, level, field string, vInt int64, plan repRecomputePlan, peopleFields []string) (tallyItemRank, bool, error) {
	if plan.window <= 0 {
		return tallyItemRank{}, false, nil
	}
	const probeRows = 16
	q, args := repWindowProbeSQL(libID, level, plan,
		`f.field = ? AND f.v_int <> 0 AND f.v_int = ?`, []any{field, vInt}, true, probeRows)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return tallyItemRank{}, false, err
	}
	defer rows.Close()
	var (
		best    tallyItemRank
		lastRel int64
	)
	for rows.Next() {
		var cand tallyItemRank
		var fieldsJSON sql.NullString
		if err := rows.Scan(&cand.ref, &cand.released, &cand.addedAt, &cand.year, &fieldsJSON); err != nil {
			return tallyItemRank{}, false, err
		}
		lastRel = cand.released
		if best.valid() && cand.released < best.released {
			// Every later holder, in the window or past it, has a strictly
			// smaller released, so best is the exact winner.
			return best, true, nil
		}
		if strings.Contains(strings.ToLower(fieldsJSON.String), "thumb") && itemHasPersonThumb(fieldsJSON.String, peopleFields, vInt) && cand.beats(best) {
			best = cand
		}
	}
	if err := rows.Err(); err != nil {
		return tallyItemRank{}, false, err
	}
	// Holders not returned (later rows, or past the window) have released at
	// most lastRel, so a winner strictly above it is exact.
	if best.valid() && best.released > lastRel {
		return best, true, nil
	}
	return tallyItemRank{}, false, nil
}

func recomputePersonThumbTx(ctx context.Context, tx queryer, isPG bool, libID int64, level, field string, vInt int64, plan repRecomputePlan, peopleFields []string) (tallyItemRank, error) {
	if len(peopleFields) == 0 {
		return tallyItemRank{}, nil
	}
	if th, ok, err := probePersonThumbTx(ctx, tx, libID, level, field, vInt, plan, peopleFields); err != nil || ok {
		return th, err
	}
	likeOp := `LIKE`
	if isPG {
		likeOp = `ILIKE`
	}

	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	exclude, excluded := plan.exclusion("i.item_ref")
	var (
		q    string
		args []any
	)
	if level != "" {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level = ? AND i.fields ` + likeOp + ` '%thumb%'` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC`
		args = []any{field, vInt, libID, level}
	} else {
		q = `
SELECT i.item_ref, i.released, i.added_at, i.year, i.fields
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_int <> 0 AND f.v_int = ? AND ` + libCheck + ` AND i.level <> '' AND i.fields ` + likeOp + ` '%thumb%'` + exclude + `
ORDER BY i.released DESC, i.added_at DESC, i.year DESC, i.item_ref ASC`
		args = []any{field, vInt, libID}
	}
	rows, err := tx.QueryContext(ctx, q, append(args, excluded...)...)
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
	libTotal := int64(-1) // read on the first recompute

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
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
		} else if ch.delta < 0 {
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET cnt = cnt + ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				ch.delta, libID, k.level, k.field, k.vInt); err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			res, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND cnt <= 0`,
				libID, k.level, k.field, k.vInt)
			if err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
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
			if ch.labelReplacement != nil {
				newLabel = *ch.labelReplacement
			} else if k.level != "" {
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
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			if !newLabel.Valid || newLabel.String == "" {
				if _, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					libID, k.level, k.field, k.vInt); err != nil {
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
				continue
			}
			lbl := newLabel.String
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET label = ?, label_lower = ?, label_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				lbl, strings.ToLower(lbl), FoldFieldText(lbl), libID, k.level, k.field, k.vInt); err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
		} else if ch.delta <= 0 && ch.minAdded != "" {
			lbl := ch.minAdded
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET label = ?, label_lower = ?, label_fold = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ? AND ? < label`,
				lbl, strings.ToLower(lbl), FoldFieldText(lbl), libID, k.level, k.field, k.vInt, lbl); err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
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
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
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
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
			}
		}

		if len(ch.removedRepRefs) > 0 || len(ch.removedThumbRefs) > 0 {
			var curRep, curThumb tallyItemRank
			var curCnt int64
			err := tx.QueryRowContext(ctx, `
SELECT rep_ref, rep_released, thumb_ref, thumb_released, cnt
FROM plugin_item_person_tally
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
				libID, k.level, k.field, k.vInt).Scan(&curRep.ref, &curRep.released, &curThumb.ref, &curThumb.released, &curCnt)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return wrapTallyItemErr(ch.sampleRef, k.field, err)
			}
			needRep := len(ch.removedRepRefs) > 0 && (curRep.ref == "" || func() bool { _, hit := ch.removedRepRefs[curRep.ref]; return hit }())
			needThumb := len(ch.removedThumbRefs) > 0 && curThumb.ref != "" && func() bool { _, hit := ch.removedThumbRefs[curThumb.ref]; return hit }()
			if (needRep && ch.repReplacement == nil || needThumb && ch.thumbReplacement == nil) && libTotal < 0 {
				if libTotal, err = libraryItemTotal(ctx, tx, libID); err != nil {
					return err
				}
			}
			if needRep {
				var winner tallyItemRank
				if ch.repReplacement != nil {
					winner = *ch.repReplacement
				} else {
					winner, err = recomputePersonRepTx(ctx, tx, isPG, libID, k.level, k.field, k.vInt, planRepRecompute(curCnt, libTotal, curRep))
					if err != nil {
						return wrapTallyItemErr(ch.sampleRef, k.field, err)
					}
				}
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET rep_ref = ?, rep_released = ?, rep_added = ?, rep_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					winner.ref, winner.released, winner.addedAt, winner.year,
					libID, k.level, k.field, k.vInt); err != nil {
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
				}
			}
			if needThumb {
				var winner tallyItemRank
				if ch.thumbReplacement != nil {
					winner = *ch.thumbReplacement
				} else {
					winner, err = recomputePersonThumbTx(ctx, tx, isPG, libID, k.level, k.field, k.vInt, planRepRecompute(curCnt, libTotal, curThumb), peopleFields)
					if err != nil {
						return wrapTallyItemErr(ch.sampleRef, k.field, err)
					}
				}
				if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_person_tally
SET thumb_ref = ?, thumb_released = ?, thumb_added = ?, thumb_year = ?
WHERE library_id = ? AND level = ? AND field = ? AND v_int = ?`,
					winner.ref, winner.released, winner.addedAt, winner.year,
					libID, k.level, k.field, k.vInt); err != nil {
					return wrapTallyItemErr(ch.sampleRef, k.field, err)
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

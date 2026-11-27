package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

// ParentCounts holds the indexed child and descendant leaf counts for one
// container item in a multi-level plugin kind, read from
// plugin_item_parent_tally in O(1) without walking descendants. ByLevel holds
// direct child counts partitioned by child level key (e.g. album, concert).
type ParentCounts struct {
	ChildCount int
	LeafCount  int
	ByLevel    map[string]int
}

type parentTallyKey struct {
	parent     core.ItemRef
	childLevel string
}

type parentTallyDelta struct {
	child int64
	leaf  int64
}

// ParentCountsMany returns the stored direct-child (total and per-child-level)
// and descendant-leaf counts for each ref in a single library from
// plugin_item_parent_tally. Missing refs have no stored children and are
// omitted from the returned map.
func (s *Service) ParentCountsMany(ctx context.Context, libraryID core.LibraryID, refs []core.ItemRef) (map[core.ItemRef]ParentCounts, error) {
	if s == nil || s.db == nil || libraryID <= 0 || len(refs) == 0 {
		return nil, nil
	}
	unique := make([]core.ItemRef, 0, len(refs))
	seen := make(map[core.ItemRef]struct{}, len(refs))
	for _, r := range refs {
		if r == "" {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		unique = append(unique, r)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	out := make(map[core.ItemRef]ParentCounts, len(unique))
	const chunkSize = 250
	for start := 0; start < len(unique); start += chunkSize {
		end := min(start+chunkSize, len(unique))
		chunk := unique[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, 1+len(chunk))
		args = append(args, int64(libraryID))
		for i, r := range chunk {
			placeholders[i] = "?"
			args = append(args, string(r))
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT parent_ref, child_level, child_count, leaf_count FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref IN (`+strings.Join(placeholders, ",")+`)`,
			args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var refStr, childLevel string
			var childCount, leafCount int
			if err := rows.Scan(&refStr, &childLevel, &childCount, &leafCount); err != nil {
				rows.Close()
				return nil, err
			}
			ref := core.ItemRef(refStr)
			pc := out[ref]
			pc.ChildCount += childCount
			pc.LeafCount += leafCount
			if childLevel != "" && childCount > 0 {
				if pc.ByLevel == nil {
					pc.ByLevel = make(map[string]int, 2)
				}
				pc.ByLevel[childLevel] += childCount
			}
			out[ref] = pc
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// parentTallyTracker computes exact per-(parent_ref, child_level)
// plugin_item_parent_tally deltas from the pre-chunk state of plugin_items
// and plugin_item_parent_tally. Tracking parent links, levels, and leaf
// counts handles out-of-order inserts, reparenting, branching trees, and level
// changes within a single chunk identically to sequential writes.
type parentTallyTracker struct {
	q          queryer
	libraryID  int64
	leafLevels map[string]bool
	parentOf   map[core.ItemRef]core.ItemRef
	levelOf    map[core.ItemRef]string
	leafOf     map[core.ItemRef]int64
	delta      map[parentTallyKey]parentTallyDelta
}

func newParentTallyTracker(q queryer, libraryID core.LibraryID, leafLevels map[string]bool, existing map[core.ItemRef]existingItemMeta) *parentTallyTracker {
	parentOf := make(map[core.ItemRef]core.ItemRef, len(existing))
	levelOf := make(map[core.ItemRef]string, len(existing))
	for ref, ex := range existing {
		parentOf[ref] = ex.parentRef
		levelOf[ref] = ex.level
	}
	return &parentTallyTracker{
		q:          q,
		libraryID:  int64(libraryID),
		leafLevels: leafLevels,
		parentOf:   parentOf,
		levelOf:    levelOf,
		leafOf:     make(map[core.ItemRef]int64),
		delta:      make(map[parentTallyKey]parentTallyDelta),
	}
}

func (t *parentTallyTracker) isLeaf(level string) bool {
	return level != "" && t.leafLevels[level]
}

func (t *parentTallyTracker) ownLeaf(level string) int64 {
	if t.isLeaf(level) {
		return 1
	}
	return 0
}

func (t *parentTallyTracker) primeParents(ctx context.Context, refs []core.ItemRef) error {
	missing := make([]core.ItemRef, 0, len(refs))
	seen := make(map[core.ItemRef]struct{}, len(refs))
	for _, r := range refs {
		if r == "" {
			continue
		}
		if _, ok := t.parentOf[r]; ok {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		missing = append(missing, r)
	}
	if len(missing) == 0 {
		return nil
	}
	const chunkSize = 250
	for start := 0; start < len(missing); start += chunkSize {
		end := min(start+chunkSize, len(missing))
		chunk := missing[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, r := range chunk {
			placeholders[i] = "?"
			args[i] = string(r)
			t.parentOf[r] = ""
			t.levelOf[r] = ""
		}
		rows, err := t.q.QueryContext(ctx,
			`SELECT item_ref, parent_ref, level FROM plugin_items WHERE item_ref IN (`+strings.Join(placeholders, ",")+`)`,
			args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var refStr, parentStr, levelStr string
			if err := rows.Scan(&refStr, &parentStr, &levelStr); err != nil {
				rows.Close()
				return err
			}
			ref := core.ItemRef(refStr)
			t.parentOf[ref] = core.ItemRef(parentStr)
			t.levelOf[ref] = levelStr
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (t *parentTallyTracker) primeLeaves(ctx context.Context, refs []core.ItemRef) error {
	missing := make([]core.ItemRef, 0, len(refs))
	seen := make(map[core.ItemRef]struct{}, len(refs))
	for _, r := range refs {
		if r == "" {
			continue
		}
		if _, ok := t.leafOf[r]; ok {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		missing = append(missing, r)
	}
	if len(missing) == 0 {
		return nil
	}
	const chunkSize = 250
	for start := 0; start < len(missing); start += chunkSize {
		end := min(start+chunkSize, len(missing))
		chunk := missing[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, 1+len(chunk))
		args = append(args, t.libraryID)
		for i, r := range chunk {
			placeholders[i] = "?"
			args = append(args, string(r))
			t.leafOf[r] = 0
		}
		rows, err := t.q.QueryContext(ctx,
			`SELECT parent_ref, COALESCE(SUM(leaf_count), 0) FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref IN (`+strings.Join(placeholders, ",")+`) GROUP BY parent_ref`,
			args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var refStr string
			var leafCount int64
			if err := rows.Scan(&refStr, &leafCount); err != nil {
				rows.Close()
				return err
			}
			t.leafOf[core.ItemRef(refStr)] = leafCount
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (t *parentTallyTracker) parentAndLevel(ctx context.Context, ref core.ItemRef) (core.ItemRef, string, error) {
	if ref == "" {
		return "", "", nil
	}
	if p, ok := t.parentOf[ref]; ok {
		return p, t.levelOf[ref], nil
	}
	var parentStr, levelStr string
	err := t.q.QueryRowContext(ctx, `SELECT parent_ref, level FROM plugin_items WHERE item_ref = ?`, string(ref)).Scan(&parentStr, &levelStr)
	if errors.Is(err, sql.ErrNoRows) {
		t.parentOf[ref] = ""
		t.levelOf[ref] = ""
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	p := core.ItemRef(parentStr)
	t.parentOf[ref] = p
	t.levelOf[ref] = levelStr
	return p, levelStr, nil
}

func (t *parentTallyTracker) leafCount(ctx context.Context, ref core.ItemRef) (int64, error) {
	if ref == "" {
		return 0, nil
	}
	if n, ok := t.leafOf[ref]; ok {
		return n, nil
	}
	var leafCount int64
	err := t.q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(leaf_count), 0) FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref = ?`,
		t.libraryID, string(ref)).Scan(&leafCount)
	if errors.Is(err, sql.ErrNoRows) {
		t.leafOf[ref] = 0
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	t.leafOf[ref] = leafCount
	return leafCount, nil
}

func (t *parentTallyTracker) addChild(parent core.ItemRef, childLevel string, d int64) {
	if parent == "" || childLevel == "" || d == 0 {
		return
	}
	k := parentTallyKey{parent: parent, childLevel: childLevel}
	cur := t.delta[k]
	cur.child += d
	t.delta[k] = cur
}

func (t *parentTallyTracker) addLeafAncestors(ctx context.Context, startParent, originRef core.ItemRef, startChildLevel string, d int64, skip map[core.ItemRef]struct{}) error {
	if startParent == "" || d == 0 {
		return nil
	}
	const maxParentHops = 8
	visited := make(map[core.ItemRef]struct{}, 4)
	if originRef != "" {
		visited[originRef] = struct{}{}
	}
	cur := startParent
	viaLevel := startChildLevel
	for hops := 0; hops < maxParentHops && cur != ""; hops++ {
		if _, seen := visited[cur]; seen {
			break
		}
		visited[cur] = struct{}{}
		if _, skipped := skip[cur]; !skipped && viaLevel != "" {
			base, err := t.leafCount(ctx, cur)
			if err != nil {
				return err
			}
			t.leafOf[cur] = base + d
			k := parentTallyKey{parent: cur, childLevel: viaLevel}
			curDelta := t.delta[k]
			curDelta.leaf += d
			t.delta[k] = curDelta
		}
		next, curLevel, err := t.parentAndLevel(ctx, cur)
		if err != nil {
			return err
		}
		cur = next
		viaLevel = curLevel
	}
	return nil
}

// apply records one item insert or structural update (parent or level change).
func (t *parentTallyTracker) apply(ctx context.Context, ref core.ItemRef, existed bool, oldParent, newParent core.ItemRef, oldLevel, newLevel string) error {
	if !existed {
		var childLeaves int64
		if !t.isLeaf(newLevel) {
			var err error
			childLeaves, err = t.leafCount(ctx, ref)
			if err != nil {
				return err
			}
		}
		sub := t.ownLeaf(newLevel) + childLeaves
		t.parentOf[ref] = newParent
		t.levelOf[ref] = newLevel
		if newParent != "" {
			t.addChild(newParent, newLevel, 1)
			if sub != 0 {
				if err := t.addLeafAncestors(ctx, newParent, ref, newLevel, sub, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if oldParent == newParent && oldLevel == newLevel {
		return nil
	}
	var childLeaves int64
	if !t.isLeaf(oldLevel) || !t.isLeaf(newLevel) {
		var err error
		childLeaves, err = t.leafCount(ctx, ref)
		if err != nil {
			return err
		}
	}
	oldSub := t.ownLeaf(oldLevel) + childLeaves
	newSub := t.ownLeaf(newLevel) + childLeaves
	if oldParent != "" {
		t.addChild(oldParent, oldLevel, -1)
		if oldSub != 0 {
			if err := t.addLeafAncestors(ctx, oldParent, ref, oldLevel, -oldSub, nil); err != nil {
				return err
			}
		}
	}
	t.parentOf[ref] = newParent
	t.levelOf[ref] = newLevel
	if newParent != "" {
		t.addChild(newParent, newLevel, 1)
		if newSub != 0 {
			if err := t.addLeafAncestors(ctx, newParent, ref, newLevel, newSub, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyParentTallyDeltaTx(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, delta map[parentTallyKey]parentTallyDelta) error {
	if len(delta) == 0 || libraryID <= 0 {
		return nil
	}
	keys := make([]parentTallyKey, 0, len(delta))
	for k, d := range delta {
		if d.child != 0 || d.leaf != 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].parent != keys[j].parent {
			return keys[i].parent < keys[j].parent
		}
		return keys[i].childLevel < keys[j].childLevel
	})
	libID := int64(libraryID)
	for _, k := range keys {
		d := delta[k]
		refStr := string(k.parent)
		if d.child > 0 || d.leaf > 0 {
			initChild := max(d.child, 0)
			initLeaf := max(d.leaf, 0)
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_parent_tally(library_id, parent_ref, child_level, child_count, leaf_count)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(library_id, parent_ref, child_level) DO UPDATE SET
	child_count = CASE WHEN plugin_item_parent_tally.child_count + ? < 0 THEN 0 ELSE plugin_item_parent_tally.child_count + ? END,
	leaf_count  = CASE WHEN plugin_item_parent_tally.leaf_count  + ? < 0 THEN 0 ELSE plugin_item_parent_tally.leaf_count  + ? END`,
				libID, refStr, k.childLevel, initChild, initLeaf, d.child, d.child, d.leaf, d.leaf); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
UPDATE plugin_item_parent_tally
SET child_count = CASE WHEN child_count + ? < 0 THEN 0 ELSE child_count + ? END,
    leaf_count  = CASE WHEN leaf_count  + ? < 0 THEN 0 ELSE leaf_count  + ? END
WHERE library_id = ? AND parent_ref = ? AND child_level = ?`,
				d.child, d.child, d.leaf, d.leaf, libID, refStr, k.childLevel); err != nil {
				return err
			}
		}
		if d.child < 0 || d.leaf < 0 {
			if _, err := tx.ExecContext(ctx, `
DELETE FROM plugin_item_parent_tally
WHERE library_id = ? AND parent_ref = ? AND child_level = ? AND child_count <= 0 AND leaf_count <= 0`,
				libID, refStr, k.childLevel); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteParentTallyRefsTx(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, refs []core.ItemRef) error {
	if len(refs) == 0 || libraryID <= 0 {
		return nil
	}
	libID := int64(libraryID)
	const chunkSize = 250
	for start := 0; start < len(refs); start += chunkSize {
		end := min(start+chunkSize, len(refs))
		chunk := refs[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, 1+len(chunk))
		args = append(args, libID)
		for i, r := range chunk {
			placeholders[i] = "?"
			args = append(args, string(r))
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref IN (`+strings.Join(placeholders, ",")+`)`,
			args...); err != nil {
			return err
		}
	}
	return nil
}

func rebuildKindParentTallyTx(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, decl plugins.MediaKindMeta) error {
	if libraryID <= 0 {
		return nil
	}
	libID := int64(libraryID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ?`, libID); err != nil {
		return err
	}
	if len(decl.Levels) < 2 {
		return nil
	}
	leafSet := decl.LeafLevels()
	leafList := make([]string, 0, len(leafSet))
	for lv := range leafSet {
		leafList = append(leafList, lv)
	}
	slices.Sort(leafList)
	if len(leafList) == 0 {
		return nil
	}
	placeholders := make([]string, len(leafList))
	args := make([]any, 0, len(leafList)+1)
	for i, lv := range leafList {
		placeholders[i] = "?"
		args = append(args, lv)
	}
	args = append(args, libID)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_parent_tally(library_id, parent_ref, child_level, child_count, leaf_count)
SELECT library_id, parent_ref, level, COUNT(*), CAST(SUM(CASE WHEN level IN (`+strings.Join(placeholders, ",")+`) THEN 1 ELSE 0 END) AS BIGINT)
FROM plugin_items
WHERE library_id = ? AND parent_ref <> ''
GROUP BY library_id, parent_ref, level`,
		args...); err != nil {
		return err
	}

	// Propagate descendant leaf counts upward across intermediate container
	// levels ordered from deepest to shallowest so arbitrary branching trees
	// roll up accurately in one pass per depth.
	type innerLevel struct {
		key   string
		depth int
	}
	var inner []innerLevel
	for _, lv := range decl.Levels {
		if leafSet[lv.Key] {
			continue
		}
		depth := decl.LevelDepth(lv.Key)
		if depth >= 1 {
			inner = append(inner, innerLevel{key: lv.Key, depth: depth})
		}
	}
	sort.Slice(inner, func(i, j int) bool {
		if inner[i].depth != inner[j].depth {
			return inner[i].depth > inner[j].depth
		}
		return inner[i].key < inner[j].key
	})
	for _, lv := range inner {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_parent_tally(library_id, parent_ref, child_level, child_count, leaf_count)
SELECT i.library_id, i.parent_ref, i.level, 0, CAST(SUM(c.leaf_count) AS BIGINT)
FROM plugin_items i
JOIN (
	SELECT library_id, parent_ref, SUM(leaf_count) AS leaf_count
	FROM plugin_item_parent_tally
	WHERE library_id = ?
	GROUP BY library_id, parent_ref
) c
  ON c.library_id = i.library_id AND c.parent_ref = i.item_ref
WHERE i.library_id = ? AND i.level = ? AND i.parent_ref <> '' AND c.leaf_count > 0
GROUP BY i.library_id, i.parent_ref, i.level
ON CONFLICT(library_id, parent_ref, child_level) DO UPDATE SET
	leaf_count = plugin_item_parent_tally.leaf_count + excluded.leaf_count`,
			libID, libID, lv.key); err != nil {
			return err
		}
	}
	return nil
}

const (
	migration15SQLiteDDL = `
CREATE TABLE plugin_item_parent_tally (
	library_id  INTEGER NOT NULL,
	parent_ref  TEXT    NOT NULL,
	child_count INTEGER NOT NULL DEFAULT 0,
	leaf_count  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (library_id, parent_ref)
);
`
	migration15PostgresDDL = `
CREATE TABLE plugin_item_parent_tally (
	library_id  BIGINT           NOT NULL,
	parent_ref  TEXT COLLATE "C" NOT NULL,
	child_count BIGINT           NOT NULL DEFAULT 0,
	leaf_count  BIGINT           NOT NULL DEFAULT 0,
	PRIMARY KEY (library_id, parent_ref)
);
`
	migration16SQLiteDDL = `
DROP TABLE IF EXISTS plugin_item_parent_tally;
CREATE TABLE plugin_item_parent_tally (
	library_id  INTEGER NOT NULL,
	parent_ref  TEXT    NOT NULL,
	child_level TEXT    NOT NULL DEFAULT '',
	child_count INTEGER NOT NULL DEFAULT 0,
	leaf_count  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (library_id, parent_ref, child_level)
);
CREATE INDEX IF NOT EXISTS plugin_items_parent_level ON plugin_items(library_id, parent_ref, level, idx, item_ref);
`
	migration16PostgresDDL = `
DROP TABLE IF EXISTS plugin_item_parent_tally;
CREATE TABLE plugin_item_parent_tally (
	library_id  BIGINT           NOT NULL,
	parent_ref  TEXT COLLATE "C" NOT NULL,
	child_level TEXT             NOT NULL DEFAULT '',
	child_count BIGINT           NOT NULL DEFAULT 0,
	leaf_count  BIGINT           NOT NULL DEFAULT 0,
	PRIMARY KEY (library_id, parent_ref, child_level)
);
CREATE INDEX IF NOT EXISTS plugin_items_parent_level ON plugin_items(library_id, parent_ref, level, idx, item_ref);
`
)

func applyMigration15SQLite(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, migration15SQLiteDDL)
	return err
}

func applyMigration15Postgres(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, migration15PostgresDDL)
	return err
}

func applyMigration16SQLite(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration16SQLiteDDL); err != nil {
		return err
	}
	return backfillParentTallyTx(ctx, tx)
}

func applyMigration16Postgres(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, migration16PostgresDDL); err != nil {
		return err
	}
	return backfillParentTallyTx(ctx, tx)
}

func backfillParentTallyTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT library_id, decl FROM plugin_kinds WHERE library_id > 0`)
	if err != nil {
		return err
	}
	type kindRow struct {
		libraryID core.LibraryID
		declJSON  string
	}
	var kinds []kindRow
	for rows.Next() {
		var libID int64
		var declJSON string
		if err := rows.Scan(&libID, &declJSON); err != nil {
			rows.Close()
			return err
		}
		kinds = append(kinds, kindRow{libraryID: core.LibraryID(libID), declJSON: declJSON})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, k := range kinds {
		var decl plugins.MediaKindMeta
		if err := json.Unmarshal([]byte(k.declJSON), &decl); err != nil || len(decl.Levels) < 2 {
			continue
		}
		if err := rebuildKindParentTallyTx(ctx, tx, k.libraryID, decl); err != nil {
			return err
		}
	}
	return nil
}

// parentTallyRepairBatch bounds one write transaction of a parent tally
// repair. Each statement writes one primary-key row.
const parentTallyRepairBatch = 256

// repairKindParentTallyLocked brings plugin_item_parent_tally for the kind
// to exactly what rebuildKindParentTallyTx would write, without holding the
// writer for the rebuild. The kind's items are read on the pool, the tally
// computed in memory and compared with the stored rows, and only rows that
// differ are written, in parentTallyRepairBatch-row transactions. A full
// rebuild deletes and re-aggregates the whole library in one transaction
// (3.5-5 s on the lab's 57k-item music library). The caller holds the
// kind lock, so the kind's own writers cannot interleave.
func (s *Service) repairKindParentTallyLocked(ctx context.Context, pluginID, kindKey string, fallbackLibID core.LibraryID, decl plugins.MediaKindMeta) error {
	type itemRow struct {
		ref, parent, level string
	}
	byLib := map[int64][]itemRow{}
	if fallbackLibID > 0 {
		byLib[int64(fallbackLibID)] = nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT library_id, item_ref, parent_ref, level FROM plugin_items WHERE plugin_id = ? AND kind_key = ? AND library_id > 0`,
		pluginID, kindKey)
	if err != nil {
		return err
	}
	for rows.Next() {
		var lib int64
		var r itemRow
		if err := rows.Scan(&lib, &r.ref, &r.parent, &r.level); err != nil {
			rows.Close()
			return err
		}
		byLib[lib] = append(byLib[lib], r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	leafSet := decl.LeafLevels()
	type innerLevel struct {
		key   string
		depth int
	}
	var inner []innerLevel
	for _, lv := range decl.Levels {
		if leafSet[lv.Key] {
			continue
		}
		if depth := decl.LevelDepth(lv.Key); depth >= 1 {
			inner = append(inner, innerLevel{key: lv.Key, depth: depth})
		}
	}
	sort.Slice(inner, func(i, j int) bool {
		if inner[i].depth != inner[j].depth {
			return inner[i].depth > inner[j].depth
		}
		return inner[i].key < inner[j].key
	})

	libs := make([]int64, 0, len(byLib))
	for lib := range byLib {
		libs = append(libs, lib)
	}
	slices.Sort(libs)
	for _, lib := range libs {
		items := byLib[lib]
		want := map[parentTallyKey]parentTallyDelta{}
		if len(decl.Levels) >= 2 && len(leafSet) > 0 {
			// Direct children and their own leaves, as the rebuild's
			// first INSERT ... GROUP BY parent_ref, level.
			leafByParent := map[string]int64{}
			for _, it := range items {
				if it.parent == "" {
					continue
				}
				k := parentTallyKey{parent: core.ItemRef(it.parent), childLevel: it.level}
				d := want[k]
				d.child++
				if leafSet[it.level] {
					d.leaf++
					leafByParent[it.parent]++
				}
				want[k] = d
			}
			// Descendant leaves roll up one inner level at a time, deepest
			// first; each level reads the state before its own additions,
			// as the rebuild's INSERT ... SELECT does.
			for _, lv := range inner {
				type add struct {
					key parentTallyKey
					n   int64
				}
				var adds []add
				for _, it := range items {
					if it.level != lv.key || it.parent == "" {
						continue
					}
					if c := leafByParent[it.ref]; c > 0 {
						adds = append(adds, add{key: parentTallyKey{parent: core.ItemRef(it.parent), childLevel: it.level}, n: c})
					}
				}
				for _, a := range adds {
					d := want[a.key]
					d.leaf += a.n
					want[a.key] = d
					leafByParent[string(a.key.parent)] += a.n
				}
			}
		}

		have := map[parentTallyKey]parentTallyDelta{}
		trows, err := s.db.QueryContext(ctx,
			`SELECT parent_ref, child_level, child_count, leaf_count FROM plugin_item_parent_tally WHERE library_id = ?`, lib)
		if err != nil {
			return err
		}
		for trows.Next() {
			var parent, childLevel string
			var d parentTallyDelta
			if err := trows.Scan(&parent, &childLevel, &d.child, &d.leaf); err != nil {
				trows.Close()
				return err
			}
			have[parentTallyKey{parent: core.ItemRef(parent), childLevel: childLevel}] = d
		}
		if err := trows.Err(); err != nil {
			trows.Close()
			return err
		}
		trows.Close()

		type op struct {
			key    parentTallyKey
			d      parentTallyDelta
			delete bool
		}
		var ops []op
		for k := range have {
			if _, ok := want[k]; !ok {
				ops = append(ops, op{key: k, delete: true})
			}
		}
		for k, d := range want {
			if h, ok := have[k]; !ok || h != d {
				ops = append(ops, op{key: k, d: d})
			}
		}
		slices.SortFunc(ops, func(a, b op) int {
			if c := strings.Compare(string(a.key.parent), string(b.key.parent)); c != 0 {
				return c
			}
			return strings.Compare(a.key.childLevel, b.key.childLevel)
		})
		for start := 0; start < len(ops); start += parentTallyRepairBatch {
			if start > 0 {
				if err := store.WaitWALBudget(ctx, s.db); err != nil {
					return err
				}
			}
			end := min(start+parentTallyRepairBatch, len(ops))
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			for _, o := range ops[start:end] {
				if o.delete {
					_, err = tx.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref = ? AND child_level = ?`,
						lib, string(o.key.parent), o.key.childLevel)
				} else {
					_, err = tx.ExecContext(ctx, `
INSERT INTO plugin_item_parent_tally(library_id, parent_ref, child_level, child_count, leaf_count)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(library_id, parent_ref, child_level) DO UPDATE SET
	child_count = excluded.child_count,
	leaf_count  = excluded.leaf_count`,
						lib, string(o.key.parent), o.key.childLevel, o.d.child, o.d.leaf)
				}
				if err != nil {
					_ = tx.Rollback()
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}

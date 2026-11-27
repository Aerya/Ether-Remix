package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pluginitems", 24, `
CREATE TABLE IF NOT EXISTS plugin_kind_rebuilds (
	plugin_id TEXT NOT NULL,
	kind_key TEXT NOT NULL,
	decl TEXT NOT NULL,
	PRIMARY KEY (plugin_id, kind_key)
);
CREATE TABLE IF NOT EXISTS plugin_rebuild_keys (
	item_ref TEXT NOT NULL,
	part_index INTEGER NOT NULL,
	seq BIGINT NOT NULL,
	library_id BIGINT NOT NULL,
	added_at BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (item_ref, part_index)
);
CREATE INDEX IF NOT EXISTS plugin_rebuild_keys_library ON plugin_rebuild_keys(library_id);`)
}

var ErrKindRebuildPending = errors.New("pluginitems: library rebuild pending; retry after sync")

func kindRecordFromDecl(pluginID, kindKey string, libraryID int64, raw, hash string, version int64) (KindRecord, error) {
	rec := KindRecord{PluginID: pluginID, KindKey: kindKey, LibraryID: core.LibraryID(libraryID), DeclHash: hash, SchemaVersion: version}
	err := json.Unmarshal([]byte(raw), &rec.Decl)
	return rec, err
}

func (s *Service) pendingKindRebuild(ctx context.Context, pluginID, kindKey string) (string, error) {
	return readKindRebuild(ctx, s.db, pluginID, kindKey)
}

func readKindRebuild(ctx context.Context, q queryer, pluginID, kindKey string) (string, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT decl FROM plugin_kind_rebuilds WHERE plugin_id = ? AND kind_key = ?), '')`, pluginID, kindKey).Scan(&raw)
	return raw, err
}

func queueKindRebuild(ctx context.Context, tx *sql.Tx, pluginID, kindKey, decl string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_kind_rebuilds(plugin_id, kind_key, decl) VALUES(?, ?, ?) ON CONFLICT(plugin_id, kind_key) DO UPDATE SET decl = excluded.decl`, pluginID, kindKey, decl); err != nil {
		return err
	}
	return tx.Commit()
}

// rebuildKind runs under the sync lock. Each batch takes the kind lock
// independently so cancellation, a push, or reconciliation can make progress
// between writes. The old declaration remains authoritative until the purge
// finishes, and the pending declaration survives cancellation or restart.
func (s *Service) rebuildKind(ctx context.Context, rec KindRecord) (KindRecord, error) {
	pending, err := s.pendingKindRebuild(ctx, rec.PluginID, rec.KindKey)
	if err != nil || pending == "" {
		return rec, err
	}
	if store.DialectOf(s.db) == store.Postgres {
		unlock, err := store.SessionLock(ctx, s.db, "plugin-kind-rebuild:"+rec.PluginID+":"+rec.KindKey)
		if err != nil {
			return rec, err
		}
		defer unlock()

		// A sync on another node may have consumed the request while we
		// waited. Reload after taking ownership of the rebuild.
		s.invalidateKindCache(rec.PluginID, rec.KindKey, rec.LibraryID)
		rec, err = s.GetKind(ctx, rec.PluginID, rec.KindKey)
		if err != nil {
			return rec, err
		}
		pending, err = s.pendingKindRebuild(ctx, rec.PluginID, rec.KindKey)
		if err != nil || pending == "" {
			return rec, err
		}
	}
	var decl plugins.MediaKindMeta
	if err := json.Unmarshal([]byte(pending), &decl); err != nil {
		return rec, err
	}
	levels := slices.Clone(rec.Decl.Levels)
	slices.SortStableFunc(levels, func(a, b plugins.KindLevel) int {
		return rec.Decl.LevelDepth(b.Key) - rec.Decl.LevelDepth(a.Key)
	})
	mu := s.kindLock(rec.PluginID, rec.KindKey)
	isSQLite := store.DialectOf(s.db) == store.SQLite
	leafLevels := rec.Decl.LeafLevels()
	for _, level := range levels {
		for {
			n, err := func() (int, error) {
				mu.Lock()
				defer mu.Unlock()
				raw, err := s.pendingKindRebuild(ctx, rec.PluginID, rec.KindKey)
				if err != nil {
					return 0, err
				}
				if raw != pending {
					return 0, ErrKindRebuildPending
				}
				targets, err := s.rebuildTargets(ctx, rec.LibraryID, level.Key)
				if err != nil || len(targets) == 0 {
					return 0, err
				}
				err = s.writeChunks(ctx, len(targets), func(start, end int) (time.Duration, error) {
					return s.writeDeleteChunk(ctx, rec, targets[start:end], isSQLite, leafLevels, true, nil)
				})
				return len(targets), err
			}()
			if err != nil {
				return rec, err
			}
			if n == 0 {
				break
			}
			if err := s.betweenWriteChunks(ctx); err != nil {
				return rec, err
			}
		}
	}

	mu.Lock()
	defer mu.Unlock()
	raw, err := s.pendingKindRebuild(ctx, rec.PluginID, rec.KindKey)
	if err != nil {
		return rec, err
	}
	if raw != pending {
		return rec, ErrKindRebuildPending
	}
	// Facet records are scoped to the old declaration hash. Forget their
	// cache in bounded writes as well, including records for empty facets.
	for {
		var deleted int64
		_, err := s.runWriteChunk(ctx, func(ctx context.Context, tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `
DELETE FROM plugin_facet_expansions WHERE plugin_id = ? AND kind_key = ?
AND (facet, value_key) IN (SELECT facet, value_key FROM plugin_facet_expansions
WHERE plugin_id = ? AND kind_key = ? LIMIT 32)`, rec.PluginID, rec.KindKey, rec.PluginID, rec.KindKey)
			if err != nil {
				return err
			}
			deleted, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return rec, err
		}
		if deleted == 0 {
			break
		}
		mu.Unlock()
		err = s.betweenWriteChunks(ctx)
		mu.Lock()
		if err != nil {
			return rec, err
		}
		raw, err = s.pendingKindRebuild(ctx, rec.PluginID, rec.KindKey)
		if err != nil {
			return rec, err
		}
		if raw != pending {
			return rec, ErrKindRebuildPending
		}
	}
	return s.upsertKindLocked(ctx, rec.PluginID, rec.LibraryID, decl, false, true)
}

func (s *Service) rebuildTargets(ctx context.Context, libraryID core.LibraryID, level string) ([]deleteTarget, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT item_ref, seq, level, parent_ref, recording FROM plugin_items
WHERE library_id = ? AND level = ? ORDER BY sort_title, item_ref LIMIT ?`,
		int64(libraryID), level, upsertChunkFirst)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []deleteTarget
	for rows.Next() {
		var t deleteTarget
		if err := rows.Scan(&t.ref, &t.seq, &t.level, &t.parentRef, &t.recording); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// Preserve identity by external ID (and part index) before deleting any
// content. The same ids regain their RatingKeys when the plugin supplies them.
func saveRebuildKeys(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, isSQLite bool, targets []deleteTarget) error {
	args := make([]any, 0, len(targets))
	for _, t := range targets {
		args = append(args, string(t.ref))
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	if !isSQLite {
		// SaveParts takes a key-share lock on this row. Lock it before reading
		// the part identities and deleting their parent.
		if _, err := tx.ExecContext(ctx, `SELECT item_ref FROM plugin_items WHERE item_ref IN (`+in+`) ORDER BY item_ref FOR UPDATE`, args...); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_rebuild_keys(item_ref, part_index, seq, library_id, added_at)
SELECT item_ref, -1, seq, library_id, added_at FROM plugin_items WHERE item_ref IN (`+in+`)
ON CONFLICT(item_ref, part_index) DO NOTHING`, args...); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO plugin_rebuild_keys(item_ref, part_index, seq, library_id)
SELECT item_ref, part_index, seq, ? FROM plugin_item_parts WHERE item_ref IN (`+in+`)
ON CONFLICT(item_ref, part_index) DO NOTHING`, append([]any{int64(libraryID)}, args...)...)
	return err
}

type rebuildKey struct{ seq, addedAt int64 }

func takeRebuildItemKeys(ctx context.Context, tx *sql.Tx, chunk []preparedItem, existing map[core.ItemRef]existingItemMeta) (map[core.ItemRef]rebuildKey, error) {
	args := make([]any, 0, len(chunk))
	for _, p := range chunk {
		ref := p.validated.Item.Ref
		if _, ok := existing[ref]; !ok {
			args = append(args, string(ref))
		}
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	rows, err := tx.QueryContext(ctx, `
DELETE FROM plugin_rebuild_keys WHERE part_index = -1 AND item_ref IN (`+in+`)
RETURNING item_ref, seq, added_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make(map[core.ItemRef]rebuildKey)
	for rows.Next() {
		var ref core.ItemRef
		var key rebuildKey
		if err := rows.Scan(&ref, &key.seq, &key.addedAt); err != nil {
			return nil, err
		}
		keys[ref] = key
	}
	return keys, rows.Err()
}

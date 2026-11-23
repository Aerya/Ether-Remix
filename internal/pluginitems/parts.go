package pluginitems

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pluginitems", 13, `
CREATE TABLE plugin_item_parts (
  item_ref TEXT NOT NULL,
  part_index INTEGER NOT NULL,
  seq INTEGER NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  mime TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  duration_sec REAL NOT NULL DEFAULT 0,
  file_index INTEGER,
  source TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_ref, part_index)
);
CREATE INDEX plugin_item_parts_seq ON plugin_item_parts(seq);`)
}

// StoredPart is one persisted ordered part row for a plugin item, carrying a
// stable sequence number (and therefore RatingKey) shared across watch state
// and child listings.
type StoredPart struct {
	ItemRef     core.ItemRef
	PartIndex   int
	Seq         int64
	Key         core.RatingKey
	Name        string
	MIME        string
	Size        int64
	DurationSec float64
	FileIndex   *int
	Source      string
	UpdatedAt   int64
}

// KindItemPart converts StoredPart back to the core wire representation.
func (p StoredPart) KindItemPart() core.KindItemPart {
	var fi *int
	if p.FileIndex != nil {
		v := *p.FileIndex
		fi = &v
	}
	return core.KindItemPart{
		Name:        p.Name,
		MIME:        p.MIME,
		Size:        p.Size,
		DurationSec: p.DurationSec,
		FileIndex:   fi,
	}
}

// SaveParts upserts ordered parts for itemRef, preserving existing seq values
// per part_index so RatingKeys and watch/resume progress remain stable across
// re-resolves, allocating fresh sequence numbers from plugin_items_seq only
// for newly added part indices, and pruning stale indices >= len(parts).
func (s *Service) SaveParts(ctx context.Context, itemRef core.ItemRef, parts []core.KindItemPart, source string) ([]StoredPart, error) {
	if s == nil || s.db == nil || itemRef == "" {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing := make(map[int]int64)
	rows, err := tx.QueryContext(ctx, `SELECT part_index, seq FROM plugin_item_parts WHERE item_ref = ? ORDER BY part_index`, string(itemRef))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var idx int
		var seq int64
		if err := rows.Scan(&idx, &seq); err != nil {
			rows.Close()
			return nil, err
		}
		existing[idx] = seq
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(parts) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_parts WHERE item_ref = ?`, string(itemRef)); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		s.partsState.Store(0)
		return nil, nil
	}

	var newCount int64
	for i := range parts {
		if _, ok := existing[i]; !ok {
			newCount++
		}
	}
	var nextSeq int64
	if newCount > 0 {
		nextSeq, err = allocateSeqRangeTx(ctx, tx, newCount)
		if err != nil {
			return nil, err
		}
	}

	now := s.now().Unix()
	out := make([]StoredPart, len(parts))
	for i, p := range parts {
		seq, ok := existing[i]
		if !ok {
			seq = nextSeq
			nextSeq++
		}
		var fi any
		var fiCopy *int
		if p.FileIndex != nil && *p.FileIndex >= 0 {
			v := *p.FileIndex
			fi = v
			fiCopy = &v
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO plugin_item_parts(item_ref, part_index, seq, name, mime, size, duration_sec, file_index, source, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(item_ref, part_index) DO UPDATE SET
				name = excluded.name,
				mime = excluded.mime,
				size = excluded.size,
				duration_sec = excluded.duration_sec,
				file_index = excluded.file_index,
				source = excluded.source,
				updated_at = excluded.updated_at`,
			string(itemRef), i, seq, strings.TrimSpace(p.Name), strings.TrimSpace(p.MIME), p.Size, p.DurationSec, fi, source, now); err != nil {
			return nil, err
		}
		out[i] = StoredPart{
			ItemRef:     itemRef,
			PartIndex:   i,
			Seq:         seq,
			Key:         core.PluginKey(seq),
			Name:        strings.TrimSpace(p.Name),
			MIME:        strings.TrimSpace(p.MIME),
			Size:        p.Size,
			DurationSec: p.DurationSec,
			FileIndex:   fiCopy,
			Source:      source,
			UpdatedAt:   now,
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_parts WHERE item_ref = ? AND part_index >= ?`, string(itemRef), len(parts)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.partsState.Store(2)
	return out, nil
}

// ListParts returns all persisted parts for itemRef ordered by part_index.
func (s *Service) ListParts(ctx context.Context, itemRef core.ItemRef) ([]StoredPart, error) {
	if s == nil || s.db == nil || itemRef == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_ref, part_index, seq, name, mime, size, duration_sec, file_index, source, updated_at
		FROM plugin_item_parts
		WHERE item_ref = ?
		ORDER BY part_index`, string(itemRef))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredPart
	for rows.Next() {
		p, err := scanStoredPart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// EnsurePartsFromHints returns persisted parts for item (from /resolve or prior
// hint materialisation), materialising item's declared hints.parts / fields.parts
// into plugin_item_parts when none have been stored yet.
func (s *Service) EnsurePartsFromHints(ctx context.Context, item core.KindItem) ([]StoredPart, error) {
	if s == nil || s.db == nil || item.Ref == "" {
		return nil, nil
	}
	stored, err := s.ListParts(ctx, item.Ref)
	if err != nil || len(stored) > 0 {
		return stored, err
	}
	declared := core.ExtractKindItemPartsFromItem(item)
	if len(declared) == 0 {
		return nil, nil
	}
	return s.SaveParts(ctx, item.Ref, declared, "hint")
}

// GetPartByKey resolves a plugin RatingKey belonging to plugin_item_parts back
// to the StoredPart and its owning parent KindItem + LibraryID.
func (s *Service) GetPartByKey(ctx context.Context, key core.RatingKey) (StoredPart, core.KindItem, core.LibraryID, error) {
	if s == nil || s.db == nil || !key.IsPluginKey() {
		return StoredPart{}, core.KindItem{}, 0, core.ErrNotFound
	}
	seq := key.PluginSeq()
	if seq <= 0 {
		return StoredPart{}, core.KindItem{}, 0, core.ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT item_ref, part_index, seq, name, mime, size, duration_sec, file_index, source, updated_at
		FROM plugin_item_parts
		WHERE seq = ?`, seq)
	part, err := scanStoredPart(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredPart{}, core.KindItem{}, 0, core.ErrNotFound
		}
		return StoredPart{}, core.KindItem{}, 0, err
	}
	parent, libID, err := s.GetByRef(ctx, part.ItemRef)
	if err != nil {
		return StoredPart{}, core.KindItem{}, 0, err
	}
	return part, parent, libID, nil
}

// GetPartsByKeys batch-resolves plugin RatingKeys belonging to plugin_item_parts.
func (s *Service) GetPartsByKeys(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]StoredPart, error) {
	out := make(map[core.RatingKey]StoredPart)
	if s == nil || s.db == nil || len(keys) == 0 {
		return out, nil
	}
	var seqs []int64
	seen := make(map[int64]bool, len(keys))
	for _, k := range keys {
		if !k.IsPluginKey() {
			continue
		}
		seq := k.PluginSeq()
		if seq > 0 && !seen[seq] {
			seen[seq] = true
			seqs = append(seqs, seq)
		}
	}
	if len(seqs) == 0 {
		return out, nil
	}
	const chunkSize = 250
	for start := 0; start < len(seqs); start += chunkSize {
		chunk := seqs[start:min(start+chunkSize, len(seqs))]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, seq := range chunk {
			placeholders[i] = "?"
			args[i] = seq
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT item_ref, part_index, seq, name, mime, size, duration_sec, file_index, source, updated_at
			FROM plugin_item_parts
			WHERE seq IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			p, err := scanStoredPart(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[p.Key] = p
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// CountPartsMany returns the count of persisted parts for each ref in refs.
func (s *Service) CountPartsMany(ctx context.Context, refs []core.ItemRef) (map[core.ItemRef]int, error) {
	out := make(map[core.ItemRef]int)
	if s == nil || s.db == nil || len(refs) == 0 {
		return out, nil
	}
	s.checkRestoreCaches()
	switch s.partsState.Load() {
	case 1:
		return out, nil
	case 0:
		var hasAny bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_item_parts)`).Scan(&hasAny); err != nil {
			return nil, err
		}
		if !hasAny {
			s.partsState.CompareAndSwap(0, 1)
			return out, nil
		}
		s.partsState.CompareAndSwap(0, 2)
	}
	const chunkSize = 250
	for start := 0; start < len(refs); start += chunkSize {
		chunk := refs[start:min(start+chunkSize, len(refs))]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, ref := range chunk {
			placeholders[i] = "?"
			args[i] = string(ref)
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT item_ref, COUNT(*)
			FROM plugin_item_parts
			WHERE item_ref IN (`+strings.Join(placeholders, ",")+`)
			GROUP BY item_ref`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var refStr string
			var n int
			if err := rows.Scan(&refStr, &n); err != nil {
				rows.Close()
				return nil, err
			}
			out[core.ItemRef(refStr)] = n
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func scanStoredPart(scanner interface{ Scan(...any) error }) (StoredPart, error) {
	var (
		refStr string
		p      StoredPart
		fi     sql.NullInt64
	)
	if err := scanner.Scan(&refStr, &p.PartIndex, &p.Seq, &p.Name, &p.MIME, &p.Size, &p.DurationSec, &fi, &p.Source, &p.UpdatedAt); err != nil {
		return StoredPart{}, err
	}
	p.ItemRef = core.ItemRef(refStr)
	p.Key = core.PluginKey(p.Seq)
	if fi.Valid {
		v := int(fi.Int64)
		p.FileIndex = &v
	}
	return p, nil
}

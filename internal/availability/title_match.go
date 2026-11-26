package availability

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("availability_title_match", "availability_gate")
	store.Register("availability_title_match", 1, `
CREATE INDEX IF NOT EXISTS availability_gate_title_match ON availability_gate(rating_key);
`)
}

// RekeyTitle moves releases and gate evidence in the caller’s transaction. When
// the service is active it also rewrites sealed release payloads for the new title.
func (s *Service) RekeyTitle(ctx context.Context, tx *sql.Tx, ranges []core.TitleKeyRange, title *core.Title) error {
	for _, r := range ranges {
		if s != nil {
			if err := s.rekeyReleasePayloads(ctx, tx, r.First, r.Last, title); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM availability_releases AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM availability_releases AS dest WHERE dest.rating_key=old.rating_key+? AND dest.id=old.id AND dest.source_id=old.source_id)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: availability_releases: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE availability_releases AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: availability_releases: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM availability_gate AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM availability_gate AS dest WHERE dest.rating_key=old.rating_key+? AND dest.policy=old.policy)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: availability_gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE availability_gate AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: availability_gate: %w", err)
		}
	}
	return nil
}

func (s *Service) rekeyReleasePayloads(ctx context.Context, tx *sql.Tx, first, last int64, title *core.Title) error {
	rows, err := tx.QueryContext(ctx, `SELECT rating_key,id,source_id,payload FROM availability_releases WHERE rating_key BETWEEN ? AND ?`, first, last)
	if err != nil {
		return err
	}
	defer rows.Close()
	type entry struct {
		key             int64
		id, source, raw string
	}
	var changes []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.id, &e.source, &e.raw); err != nil {
			return err
		}
		r, err := s.decode(e.raw)
		if err != nil {
			return err
		}
		r.TitleIDs, r.TitleIDsEchoed = title.IDs, false
		e.raw, err = s.encode(r)
		if err != nil {
			return err
		}
		changes = append(changes, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, e := range changes {
		if _, err := tx.ExecContext(ctx, `UPDATE availability_releases SET payload=?,title=?,revision=revision+1 WHERE rating_key=? AND id=? AND source_id=?`, e.raw, title.Title, e.key, e.id, e.source); err != nil {
			return err
		}
	}
	return nil
}

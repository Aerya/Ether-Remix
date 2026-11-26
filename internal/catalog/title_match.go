package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// RekeyTitle moves evidence and owner bindings and exposes the destination if included.
func RekeyTitle(ctx context.Context, tx *sql.Tx, ranges []core.TitleKeyRange, to core.RatingKey) error {
	for _, r := range ranges {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_release_evidence AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM catalog_release_evidence AS dest WHERE dest.rating_key=old.rating_key+? AND dest.release_id=old.release_id AND dest.source_id=old.source_id)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: catalog_release_evidence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_release_evidence AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: catalog_release_evidence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_release_audio_langs AS old WHERE old.rating_key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM catalog_release_audio_langs AS dest WHERE dest.rating_key=old.rating_key+? AND dest.release_id=old.release_id AND dest.source_id=old.source_id AND dest.lang=old.lang)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: catalog_release_audio_langs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_release_audio_langs AS old SET rating_key=rating_key+? WHERE old.rating_key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: catalog_release_audio_langs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_owner_includes AS old WHERE old.key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM catalog_owner_includes AS dest WHERE dest.key=old.key+?)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: catalog_owner_includes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_owner_includes AS old SET key=key+? WHERE old.key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: catalog_owner_includes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_owner_tags AS old WHERE old.key BETWEEN ? AND ?
			AND EXISTS (SELECT 1 FROM catalog_owner_tags AS dest WHERE dest.key=old.key+? AND dest.tag=old.tag)`, r.First, r.Last, r.Delta); err != nil {
			return fmt.Errorf("title match: catalog_owner_tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_owner_tags AS old SET key=key+? WHERE old.key BETWEEN ? AND ?`, r.Delta, r.First, r.Last); err != nil {
			return fmt.Errorf("title match: catalog_owner_tags: %w", err)
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET browsable=1 WHERE key=?
		AND EXISTS (SELECT 1 FROM catalog_owner_includes WHERE key=?)`, to, to)
	return err
}

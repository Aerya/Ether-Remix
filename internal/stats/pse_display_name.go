package stats

import (
	"context"
	"fmt"
)

// RenameViewer relabels a viewer's recorded plays after a display-name change.
func (s *Service) RenameViewer(ctx context.Context, userID int64, name string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE stats_plays SET username=? WHERE user_id=?`, name, userID); err != nil {
		return fmt.Errorf("stats: rename viewer: %w", err)
	}
	return nil
}

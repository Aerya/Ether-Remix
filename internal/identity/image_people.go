package identity

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// authorizePersonGrant protects Silo and Vela portraits across descriptor I/O.
// Both the callback's policy-epoch fence and active viewer check and the host's
// final credited-person policy proof must succeed before bytes are published.
func (s *ImageGrantService) authorizePersonGrant(ctx context.Context, viewer *core.User, device core.Device, item core.RatingKey, image frontend.Image) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	people, ok := s.experience().(frontend.PeopleImageReader)
	if !ok {
		return core.ErrNotAvailable
	}
	started, grantStarted := time.Now(), s.now()
	epoch, err := viewerpolicy.Read(ctx, s.db)
	if err != nil {
		return err
	}
	return people.AuthorizePersonImage(ctx, viewer, device, item, image, func(ctx context.Context) error {
		checked := s.now()
		if checked.UTC().Before(grantStarted.UTC()) {
			return core.ErrUnauthorized
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := viewerpolicy.Fence(ctx, s.db, tx, epoch); err != nil {
			return err
		}
		query := `SELECT 1 FROM auth_users WHERE id=? AND active=1`
		if store.DialectOf(s.db) == store.Postgres {
			query += ` FOR SHARE`
		}
		var active int
		if err := tx.QueryRowContext(ctx, query, viewer.ID).Scan(&active); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		finished, elapsed := s.now(), time.Since(started)
		if finished.UTC().Before(checked.UTC()) || elapsed < 0 || elapsed > 4*time.Second {
			return core.ErrUnauthorized
		}
		return nil
	})
}

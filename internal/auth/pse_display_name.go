package auth

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// A separate component keeps Remix's version numbers clear of Ether's auth ones.
	store.RegisterAfter("pse_auth", "auth")
	store.Register("pse_auth", 1, `ALTER TABLE auth_users ADD COLUMN pse_display_name TEXT NOT NULL DEFAULT '';`)
}

// SetDisplayName stores the owner-chosen name of a viewer. It lives in its own
// column, so Plex synchronisation never overwrites it; "" restores the Plex name.
func (s *Service) SetDisplayName(ctx context.Context, id int64, name string) error {
	name, err := core.CleanCustomName(name)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE auth_users SET pse_display_name=?, updated_at=? WHERE id=?`, name, s.o.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("auth: set display name: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return core.ErrNotFound
	}
	s.gen.Add(1)
	err = s.reload(ctx)
	s.notify("")
	return err
}

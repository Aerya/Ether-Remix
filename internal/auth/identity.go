package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
)

// StoredIdentity reads a viewer's stored identity without granting access or
// creating an authentication service. It includes inactive viewers.
func StoredIdentity(ctx context.Context, db *sql.DB, id int64) (*core.User, error) {
	var u core.User
	err := db.QueryRowContext(ctx, `SELECT id,COALESCE(plex_id,0),username,title FROM auth_users WHERE id=?`, id).
		Scan(&u.ID, &u.PlexID, &u.Username, &u.Title)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.ErrNotFound
	}
	return &u, err
}

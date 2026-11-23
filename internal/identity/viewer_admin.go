package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// ErrUsernameExists indicates another viewer already uses the local username.
var ErrUsernameExists = errors.New("identity: username already exists")

// CreateViewer atomically creates an owner-managed local viewer with a password.
func (l *Local) CreateViewer(ctx context.Context, username, password string) (int64, error) {
	if !validUsername(username) {
		return 0, errors.New("identity: invalid username")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return 0, err
	}
	return l.insertLocalViewer(ctx, username, hash)
}

// CreateViewerPasswordless atomically creates an owner-managed local viewer
// with no local password yet (e.g. Emby POST /Users/New followed by POST
// /Users/{id}/Password). The viewer cannot sign in until a password is set.
func (l *Local) CreateViewerPasswordless(ctx context.Context, username string) (int64, error) {
	if !validUsername(username) {
		return 0, errors.New("identity: invalid username")
	}
	return l.insertLocalViewer(ctx, username, "")
}

func (l *Local) insertLocalViewer(ctx context.Context, username, hash string) (int64, error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_links WHERE provider='local' AND LOWER(subject)=LOWER(?)`, username).Scan(&exists); err != nil {
		return 0, err
	}
	if exists > 0 {
		return 0, ErrUsernameExists
	}
	now := time.Now().Unix()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO auth_users(plex_id,username,title,is_owner,active,origin,created_at,updated_at) VALUES(NULL,?,?,0,1,'local',?,?) RETURNING id`, username, username, now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at,password_hash) VALUES(?,'local',?,?,?,?)`, id, username, username, now, hash)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	l.emitViewerCreated(id, username, "local", "local")
	return id, nil
}

// CreateDLNAGuest creates an inactive, passwordless local guest with the
// given content rating limit. Callers must install its safety policy before
// activating it.
func (l *Local) CreateDLNAGuest(ctx context.Context, display, maxRating string) (int64, error) {
	if display == "" || len(display) > 128 || strings.TrimSpace(display) != display {
		return 0, errors.New("identity: invalid display")
	}
	now := time.Now().Unix()
	var id int64
	err := l.db.QueryRowContext(ctx, `INSERT INTO auth_users(plex_id,username,title,is_owner,active,origin,max_content_rating,created_at,updated_at) VALUES(NULL,'',?,0,0,'dlna_guest',?,?,?) RETURNING id`, display, strings.TrimSpace(maxRating), now, now).Scan(&id)
	return id, err
}

// ActivateDLNAGuest fails closed until an explicit safety policy exists.
func (l *Local) ActivateDLNAGuest(ctx context.Context, id int64) error {
	result, err := l.db.ExecContext(ctx, `UPDATE auth_users SET active=1,updated_at=? WHERE id=? AND origin='dlna_guest' AND active=0 AND EXISTS(SELECT 1 FROM safety_policies WHERE user_id=?)`, time.Now().Unix(), id, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrUnauthorized
	}
	return nil
}

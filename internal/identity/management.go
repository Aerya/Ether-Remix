package identity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"strconv"
	"time"
)

type LinkedIdentity struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider"`
	Display  string `json:"display"`
}

func (l *Local) Links(ctx context.Context, viewerID int64) ([]LinkedIdentity, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id,provider,display FROM identity_links WHERE viewer_id=? ORDER BY id`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LinkedIdentity{}
	for rows.Next() {
		var v LinkedIdentity
		if err = rows.Scan(&v.ID, &v.Provider, &v.Display); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (l *Local) UpsertPassword(ctx context.Context, viewerID int64, username, password string) error {
	var count int
	if err := l.db.QueryRowContext(ctx, `SELECT count(*) FROM identity_links WHERE viewer_id=? AND provider='local' AND subject=?`, viewerID, username).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return l.SetPassword(ctx, viewerID, username, password)
	}
	return l.Create(ctx, viewerID, username, password)
}

// ClearPassword removes a viewer's local password hash without removing the
// viewer row or its local link (used by Jellyfin/Emby ResetPassword).
func (l *Local) ClearPassword(ctx context.Context, viewerID int64) error {
	if viewerID <= 0 {
		return core.ErrNotFound
	}
	_, err := l.db.ExecContext(ctx, `UPDATE identity_links SET password_hash='' WHERE viewer_id=? AND provider='local' AND EXISTS(SELECT 1 FROM auth_users WHERE id=? AND origin<>'dlna_guest')`, viewerID, viewerID)
	return err
}

// LocalUsername returns the viewer's existing local link subject, or "" if none.
func (l *Local) LocalUsername(ctx context.Context, viewerID int64) (string, error) {
	var subject string
	err := l.db.QueryRowContext(ctx, `SELECT subject FROM identity_links WHERE viewer_id=? AND provider='local' ORDER BY id LIMIT 1`, viewerID).Scan(&subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return subject, nil
}

// LocalPasswordViewerIDs returns the set of viewer IDs (active or paused) that
// currently have a non-empty local password configured, in one query.
func (l *Local) LocalPasswordViewerIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT DISTINCT viewer_id FROM identity_links WHERE provider='local' AND password_hash<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

type GrantInfo struct {
	XtreamUsername    string `json:"xtreamUsername,omitempty"`
	XtreamStreamLimit int    `json:"xtreamStreamLimit,omitempty"`
	RevealAvailable   bool   `json:"revealAvailable,omitempty"`
	Hash              string `json:"hash"`
	Surface           string `json:"surface"`
	DeviceID          string `json:"deviceId"`
	Client            string `json:"client"`
	Product           string `json:"product"`
	Version           string `json:"version"`
	LastUsed          int64  `json:"lastUsed"`
}

func (g *Grants) List(ctx context.Context, viewerID int64) ([]GrantInfo, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT hash,surface,device_id,client,product,version,last_used,COALESCE(xtream_username,''),xtream_stream_limit,(xtream_password<>'') FROM identity_grants WHERE viewer_id=? AND revoked=0 AND expires>? ORDER BY last_used DESC`, viewerID, g.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []GrantInfo{}
	for rows.Next() {
		var v GrantInfo
		if err = rows.Scan(&v.Hash, &v.Surface, &v.DeviceID, &v.Client, &v.Product, &v.Version, &v.LastUsed, &v.XtreamUsername, &v.XtreamStreamLimit, &v.RevealAvailable); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// LinkPlex records an already verified Plex identity. A provider subject may
// belong to only one viewer; conflicts never move it between viewers.
// LinkPlex links an external account subject; it does not select frontend behavior.
func (l *Local) LinkPlex(ctx context.Context, viewerID, subjectID int64, display string) error {
	if viewerID <= 0 || subjectID <= 0 {
		return core.ErrUnauthorized
	}
	subject := strconv.FormatInt(subjectID, 10)
	_, err := l.db.ExecContext(ctx, `INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at) SELECT ?,'plex',?,?,? WHERE EXISTS(SELECT 1 FROM auth_users WHERE id=? AND origin<>'dlna_guest') ON CONFLICT(provider,subject) DO NOTHING`, viewerID, subject, display, time.Now().Unix(), viewerID)
	if err != nil {
		return err
	}
	var existing int64
	if err = l.db.QueryRowContext(ctx, `SELECT l.viewer_id FROM identity_links l JOIN auth_users u ON u.id=l.viewer_id WHERE l.provider='plex' AND l.subject=? AND u.origin<>'dlna_guest'`, subject).Scan(&existing); err != nil {
		return err
	}
	if existing != viewerID {
		return core.ErrUnauthorized
	}
	return nil
}

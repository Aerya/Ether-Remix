package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"strings"
	"sync/atomic"
	"time"
)

func init() {
	store.RegisterAfter("identity_invites", "auth")
	store.Register("identity_invites", 1, `CREATE TABLE identity_invites (hash TEXT PRIMARY KEY, viewer_id INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE, expires INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0);`)
}

// InviteEvent describes an invite lifecycle event. InviteID is a non-secret
// identifier derived from the invite hash (never the redeemable secret).
type InviteEvent struct {
	Kind      string // "created", "accepted", "viewer_created"
	InviteID  string
	ViewerID  int64
	Label     string
	MaxUses   int
	ExpiresAt time.Time
}

func invitePublicID(hash string) string {
	if len(hash) > 12 {
		return "inv_" + hash[:12]
	}
	return "inv_" + hash
}

// Invite issues one-use links to an existing active viewer.
type Invite struct {
	db            *sql.DB
	now           func() time.Time
	ttl, grantTTL time.Duration
	onEvent       atomic.Pointer[func(InviteEvent)]
}

// SetOnEvent installs an optional non-blocking observer for invite lifecycle events.
func (i *Invite) SetOnEvent(fn func(InviteEvent)) {
	if i == nil {
		return
	}
	if fn == nil {
		i.onEvent.Store(nil)
		return
	}
	i.onEvent.Store(&fn)
}

func (i *Invite) emitEvent(ev InviteEvent) {
	if i == nil {
		return
	}
	if fn := i.onEvent.Load(); fn != nil && *fn != nil {
		(*fn)(ev)
	}
}

func NewInvite(db *sql.DB, ttl, grantTTL time.Duration, now func() time.Time) (*Invite, error) {
	if db == nil || ttl < time.Second || grantTTL < time.Second {
		return nil, errors.New("identity: invalid invite configuration")
	}
	if now == nil {
		now = time.Now
	}
	return &Invite{db: db, now: now, ttl: ttl, grantTTL: grantTTL}, nil
}
func (i *Invite) Create(ctx context.Context, viewerID int64) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw[:])
	hash, _ := secretHash(secret)
	expires := i.now().Add(i.ttl)
	res, err := i.db.ExecContext(ctx, `INSERT INTO identity_invites(hash,viewer_id,expires) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM auth_users WHERE id=? AND active=1 AND is_owner=0 AND origin<>'dlna_guest')`, hash, viewerID, expires.UnixMilli(), viewerID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return "", core.ErrUnauthorized
	}
	i.emitEvent(InviteEvent{
		Kind:      "created",
		InviteID:  invitePublicID(hash),
		ViewerID:  viewerID,
		MaxUses:   1,
		ExpiresAt: expires.UTC(),
	})
	return secret, nil
}
func (i *Invite) Redeem(ctx context.Context, secret string) (string, error) {
	hash, ok := secretHash(secret)
	if !ok {
		return "", core.ErrUnauthorized
	}
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := i.now().UnixMilli()
	var viewer int64
	err = tx.QueryRowContext(ctx, `UPDATE identity_invites SET used=1 WHERE hash=? AND used=0 AND expires>? AND EXISTS(SELECT 1 FROM auth_users WHERE id=identity_invites.viewer_id AND active=1 AND is_owner=0 AND origin<>'dlna_guest') RETURNING viewer_id`, hash, now).Scan(&viewer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", core.ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at) VALUES(?,'invite',?,'Invite',?) RETURNING id`, viewer, hash, now).Scan(&id)
	if err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := grantPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	gh, _ := grantHash(token)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_grants(hash,viewer_id,identity_id,surface,device_id,client,product,version,created,last_used,expires) VALUES(?,?,?,'viewer-portal','','Ether browser','','',?,?,?)`, gh, viewer, id, now, now, now+i.grantTTL.Milliseconds())
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	i.emitEvent(InviteEvent{
		Kind:     "accepted",
		InviteID: invitePublicID(hash),
		ViewerID: viewer,
	})
	return token, nil
}

// CreateViewer creates an owner-managed passwordless viewer and its first
// invite atomically, so a failed invite cannot leave an unreachable account.
func (i *Invite) CreateViewer(ctx context.Context, display string) (int64, string, error) {
	if len(display) == 0 || len(display) > 128 || strings.TrimSpace(display) != display {
		return 0, "", errors.New("identity: invalid display")
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw[:])
	hash, _ := secretHash(secret)
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	now := i.now().Unix()
	expires := i.now().Add(i.ttl)
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO auth_users(plex_id,username,title,is_owner,active,origin,created_at,updated_at) VALUES(NULL,'',?,0,1,'invite',?,?) RETURNING id`, display, now, now).Scan(&id)
	if err != nil {
		return 0, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_invites(hash,viewer_id,expires) VALUES(?,?,?)`, hash, id, expires.UnixMilli())
	if err != nil {
		return 0, "", err
	}
	if err = tx.Commit(); err != nil {
		return 0, "", err
	}
	pubID := invitePublicID(hash)
	i.emitEvent(InviteEvent{
		Kind:      "created",
		InviteID:  pubID,
		ViewerID:  id,
		Label:     display,
		MaxUses:   1,
		ExpiresAt: expires.UTC(),
	})
	i.emitEvent(InviteEvent{
		Kind:     "viewer_created",
		InviteID: pubID,
		ViewerID: id,
		Label:    display,
	})
	return id, secret, nil
}

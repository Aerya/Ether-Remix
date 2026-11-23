package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("identity_api_keys", "auth")
	store.Register("identity_api_keys", 1, `
CREATE TABLE identity_api_keys (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	hash       TEXT    NOT NULL UNIQUE,
	handle     TEXT    NOT NULL UNIQUE,
	surface    TEXT    NOT NULL,
	app_name   TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	last_used  INTEGER NOT NULL DEFAULT 0,
	revoked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX identity_api_keys_surface_active ON identity_api_keys(surface, revoked_at, created_at, id);
`)
}

const (
	apiKeyPrefix       = core.APIKeyCredentialPrefix
	apiKeyHandlePrefix = "hidden-"
	apiKeyTouchWindow  = 30 * time.Second
)

// OwnerSource supplies the active server owner. API keys always resolve to the
// current owner and fail closed when no owner is configured.
type OwnerSource interface {
	Owner(context.Context) (*core.User, error)
}

// APIKeyInfo describes one active API key without its secret.
type APIKeyInfo = frontend.APIKeyInfo

// APIKeys manages hashed MediaBrowser (Jellyfin/Emby) owner API keys.
// Only SHA-256 hashes are stored; plaintext keys are returned once at creation.
type APIKeys struct {
	db    *sql.DB
	owner OwnerSource
	now   func() time.Time
	gen   atomic.Uint64
}

// NewAPIKeys creates an API key store backed by the shared database.
func NewAPIKeys(db *sql.DB, owner OwnerSource, now func() time.Time) (*APIKeys, error) {
	if db == nil || owner == nil {
		return nil, errors.New("identity: api keys require database and owner source")
	}
	if now == nil {
		now = time.Now
	}
	return &APIKeys{db: db, owner: owner, now: now}, nil
}

// Generation bumps whenever a key is created or revoked.
func (k *APIKeys) Generation() uint64 {
	if k == nil {
		return 0
	}
	return k.gen.Load()
}

func validAPIKeySurface(surface string) bool {
	return surface == "jellyfin" || surface == "emby"
}

func validAPIKeyAppName(appName string) bool {
	return len(appName) > 0 && len(appName) <= 128 && !strings.ContainsAny(appName, "\x00\r\n")
}

// IsAPIKeyCredential reports whether credential uses Ether's API key prefix.
func IsAPIKeyCredential(credential string) bool {
	return core.IsAPIKeyCredential(credential)
}

func apiKeyHash(credential string) (string, bool) {
	if !strings.HasPrefix(credential, apiKeyPrefix) {
		return "", false
	}
	body := strings.TrimPrefix(credential, apiKeyPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != body {
		return "", false
	}
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:]), true
}

// Create mints a new owner API key for surface ("jellyfin" or "emby") and
// returns its metadata alongside the one-time plaintext secret.
func (k *APIKeys) Create(ctx context.Context, surface, appName string) (APIKeyInfo, string, error) {
	if k == nil {
		return APIKeyInfo{}, "", errors.New("identity: api keys unavailable")
	}
	if !validAPIKeySurface(surface) {
		return APIKeyInfo{}, "", errors.New("identity: invalid api key surface")
	}
	appName = strings.TrimSpace(appName)
	if !validAPIKeyAppName(appName) {
		return APIKeyInfo{}, "", errors.New("identity: invalid application name")
	}
	owner, err := k.owner.Owner(ctx)
	if err != nil || owner == nil || !owner.IsOwner {
		return APIKeyInfo{}, "", core.ErrUnauthorized
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return APIKeyInfo{}, "", err
	}
	secret := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(sum[:])

	var handleRaw [8]byte
	if _, err := rand.Read(handleRaw[:]); err != nil {
		return APIKeyInfo{}, "", err
	}
	handle := apiKeyHandlePrefix + hex.EncodeToString(handleRaw[:])

	now := k.now().UTC()
	nowMs := now.UnixMilli()
	var id int64
	err = k.db.QueryRowContext(
		ctx,
		`INSERT INTO identity_api_keys(hash, handle, surface, app_name, created_at, last_used, revoked_at) VALUES (?, ?, ?, ?, ?, 0, 0) RETURNING id`,
		hash, handle, surface, appName, nowMs,
	).Scan(&id)
	if err != nil {
		return APIKeyInfo{}, "", err
	}
	k.gen.Add(1)
	return APIKeyInfo{
		ID:        id,
		Handle:    handle,
		AppName:   appName,
		CreatedAt: time.UnixMilli(nowMs).UTC(),
	}, secret, nil
}

// List returns active API keys for surface in ascending creation order (oldest
// first, matching Jellyfin's /Auth/Keys ordering).
func (k *APIKeys) List(ctx context.Context, surface string) ([]APIKeyInfo, error) {
	if k == nil {
		return nil, errors.New("identity: api keys unavailable")
	}
	if !validAPIKeySurface(surface) {
		return nil, errors.New("identity: invalid api key surface")
	}
	rows, err := k.db.QueryContext(
		ctx,
		`SELECT id, handle, app_name, created_at, last_used FROM identity_api_keys WHERE surface=? AND revoked_at=0 ORDER BY created_at ASC, id ASC`,
		surface,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKeyInfo{}
	for rows.Next() {
		var item APIKeyInfo
		var createdMs, lastUsedMs int64
		if err := rows.Scan(&item.ID, &item.Handle, &item.AppName, &createdMs, &lastUsedMs); err != nil {
			return nil, err
		}
		if createdMs > 0 {
			item.CreatedAt = time.UnixMilli(createdMs).UTC()
		}
		if lastUsedMs > 0 {
			item.LastUsed = time.UnixMilli(lastUsedMs).UTC()
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Resolve validates credential for surface directly against the database so
// revocations take effect immediately.
func (k *APIKeys) Resolve(ctx context.Context, surface, credential string) (*core.Viewer, APIKeyInfo, error) {
	if k == nil || !validAPIKeySurface(surface) {
		return nil, APIKeyInfo{}, core.ErrUnauthorized
	}
	hash, ok := apiKeyHash(credential)
	if !ok {
		return nil, APIKeyInfo{}, core.ErrUnauthorized
	}
	var (
		info       APIKeyInfo
		storedHash string
		createdMs  int64
		lastUsedMs int64
	)
	err := k.db.QueryRowContext(
		ctx,
		`SELECT id, hash, handle, app_name, created_at, last_used FROM identity_api_keys WHERE hash=? AND surface=? AND revoked_at=0`,
		hash, surface,
	).Scan(&info.ID, &storedHash, &info.Handle, &info.AppName, &createdMs, &lastUsedMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, APIKeyInfo{}, core.ErrUnauthorized
	}
	if err != nil {
		return nil, APIKeyInfo{}, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return nil, APIKeyInfo{}, core.ErrUnauthorized
	}
	owner, err := k.owner.Owner(ctx)
	if err != nil || owner == nil || !owner.IsOwner {
		return nil, APIKeyInfo{}, core.ErrUnauthorized
	}
	if createdMs > 0 {
		info.CreatedAt = time.UnixMilli(createdMs).UTC()
	}
	nowMs := k.now().UTC().UnixMilli()
	if !frontend.QuietIdentity(ctx) && (lastUsedMs == 0 || nowMs-lastUsedMs >= apiKeyTouchWindow.Milliseconds()) {
		if _, err := k.db.ExecContext(ctx, `UPDATE identity_api_keys SET last_used=? WHERE id=? AND revoked_at=0`, nowMs, info.ID); err == nil {
			lastUsedMs = nowMs
		}
	}
	if lastUsedMs > 0 {
		info.LastUsed = time.UnixMilli(lastUsedMs).UTC()
	}
	v := *owner
	v.Delegated = false
	return &v, info, nil
}

// Revoke immediately revokes an API key identified either by its plaintext key
// or by its non-secret list handle. Returns the revoked key ID (or 0 if none).
func (k *APIKeys) Revoke(ctx context.Context, surface, keyOrHandle string) (int64, error) {
	if k == nil {
		return 0, errors.New("identity: api keys unavailable")
	}
	if !validAPIKeySurface(surface) {
		return 0, errors.New("identity: invalid api key surface")
	}
	keyOrHandle = strings.TrimSpace(keyOrHandle)
	if keyOrHandle == "" {
		return 0, nil
	}
	var (
		col string
		val string
	)
	if hash, ok := apiKeyHash(keyOrHandle); ok {
		col, val = "hash", hash
	} else if strings.HasPrefix(keyOrHandle, apiKeyHandlePrefix) {
		col, val = "handle", keyOrHandle
	} else {
		return 0, nil
	}
	var id int64
	err := k.db.QueryRowContext(
		ctx,
		`SELECT id FROM identity_api_keys WHERE `+col+`=? AND surface=? AND revoked_at=0`,
		val, surface,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	res, err := k.db.ExecContext(
		ctx,
		`UPDATE identity_api_keys SET revoked_at=? WHERE id=? AND surface=? AND revoked_at=0`,
		k.now().UTC().UnixMilli(), id, surface,
	)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		k.gen.Add(1)
		return id, nil
	}
	return 0, nil
}

// RevokeByID immediately revokes an active API key by its numeric ID.
func (k *APIKeys) RevokeByID(ctx context.Context, surface string, id int64) error {
	if k == nil {
		return errors.New("identity: api keys unavailable")
	}
	if !validAPIKeySurface(surface) {
		return errors.New("identity: invalid api key surface")
	}
	if id <= 0 {
		return core.ErrNotFound
	}
	res, err := k.db.ExecContext(
		ctx,
		`UPDATE identity_api_keys SET revoked_at=? WHERE id=? AND surface=? AND revoked_at=0`,
		k.now().UTC().UnixMilli(), id, surface,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return core.ErrNotFound
	}
	k.gen.Add(1)
	return nil
}

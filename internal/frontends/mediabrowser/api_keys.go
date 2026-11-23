package mediabrowser

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

const (
	apiKeyRevealTTL        = 2 * time.Minute
	maxPendingAPIKeyReveal = 64
)

type pendingAPIKey struct {
	secret  string
	expires time.Time
}

// apiKeyRevealStore holds newly minted API keys in memory just long enough for
// the creator's immediately following GET /Auth/Keys (Jellyfin Web and
// Jellyseerr both POST /Auth/Keys -> 204 and then GET /Auth/Keys). Entries are
// keyed by the hash of the creator's credential and consumed on first read.
type apiKeyRevealStore struct {
	mu      sync.Mutex
	entries map[string]map[int64]pendingAPIKey
}

func creatorRevealKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *apiKeyRevealStore) remember(creatorToken string, keyID int64, secret string, now time.Time) {
	if creatorToken == "" || keyID <= 0 || secret == "" {
		return
	}
	ownerKey := creatorRevealKey(creatorToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]map[int64]pendingAPIKey)
	}
	s.pruneLocked(now)
	if s.countLocked() >= maxPendingAPIKeyReveal {
		s.evictOldestLocked()
	}
	bucket := s.entries[ownerKey]
	if bucket == nil {
		bucket = make(map[int64]pendingAPIKey)
		s.entries[ownerKey] = bucket
	}
	bucket[keyID] = pendingAPIKey{secret: secret, expires: now.Add(apiKeyRevealTTL)}
}

func (s *apiKeyRevealStore) consume(creatorToken string, keyID int64, now time.Time) (string, bool) {
	if creatorToken == "" || keyID <= 0 {
		return "", false
	}
	ownerKey := creatorRevealKey(creatorToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.entries[ownerKey]
	if len(bucket) == 0 {
		return "", false
	}
	entry, ok := bucket[keyID]
	if !ok {
		return "", false
	}
	delete(bucket, keyID)
	if len(bucket) == 0 {
		delete(s.entries, ownerKey)
	}
	if !now.Before(entry.expires) {
		return "", false
	}
	return entry.secret, true
}

func (s *apiKeyRevealStore) forgetKey(keyID int64) {
	if keyID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ownerKey, bucket := range s.entries {
		delete(bucket, keyID)
		if len(bucket) == 0 {
			delete(s.entries, ownerKey)
		}
	}
}

func (s *apiKeyRevealStore) forgetCreator(creatorToken string) {
	if creatorToken == "" {
		return
	}
	ownerKey := creatorRevealKey(creatorToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, ownerKey)
}

func (s *apiKeyRevealStore) pruneLocked(now time.Time) {
	for ownerKey, bucket := range s.entries {
		for keyID, entry := range bucket {
			if !now.Before(entry.expires) {
				delete(bucket, keyID)
			}
		}
		if len(bucket) == 0 {
			delete(s.entries, ownerKey)
		}
	}
}

func (s *apiKeyRevealStore) countLocked() int {
	total := 0
	for _, bucket := range s.entries {
		total += len(bucket)
	}
	return total
}

func (s *apiKeyRevealStore) evictOldestLocked() {
	var (
		oldestOwner string
		oldestID    int64
		oldestExp   time.Time
		found       bool
	)
	for ownerKey, bucket := range s.entries {
		for keyID, entry := range bucket {
			if !found || entry.expires.Before(oldestExp) {
				oldestOwner, oldestID, oldestExp, found = ownerKey, keyID, entry.expires, true
			}
		}
	}
	if !found {
		return
	}
	delete(s.entries[oldestOwner], oldestID)
	if len(s.entries[oldestOwner]) == 0 {
		delete(s.entries, oldestOwner)
	}
}

func (i *instance) currentTime() time.Time {
	if i.host.Now != nil {
		return i.host.Now().UTC()
	}
	return time.Now().UTC()
}

func (i *instance) adminRoutes() error {
	routes := []struct {
		method, path string
		h            http.HandlerFunc
	}{
		{"GET", "/Auth/Keys", i.listAPIKeys},
		{"POST", "/Auth/Keys", i.createAPIKey},
		{"DELETE", "/Auth/Keys/{key}", i.revokeAPIKey},
		{"POST", "/Auth/Keys/{key}/Delete", i.revokeAPIKey},
		{"GET", "/Users", i.listUsers},
		{"GET", "/Users/Query", i.queryUsers},
		{"POST", "/Users/New", i.createUser},
		{"POST", "/Users/{userId}/Policy", i.updateUserPolicy},
		{"POST", "/Users/{userId}/Password", i.updateUserPassword},
		{"POST", "/Users/Password", i.updateUserPassword},
		{"DELETE", "/Users/{userId}", i.deleteUser},
		{"POST", "/Users/{userId}/Delete", i.deleteUser},
		{"GET", "/Library/MediaFolders", i.mediaFolders},
	}
	for _, rt := range routes {
		if err := i.router.Handle(rt.method, rt.path, rt.h); err != nil {
			return err
		}
	}
	return nil
}

func (i *instance) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	owner, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	apiKeys, ok := i.host.Identity.(frontend.APIKeyIdentity)
	if !ok {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "API keys are unavailable.")
		return
	}
	keys, err := apiKeys.ListAPIKeys(r.Context())
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not list API keys.")
		return
	}
	total := len(keys)
	startIndex := max(queryInt(r, "StartIndex", 0), 0)
	limitValues := route.Query(r.URL.Query(), "Limit")
	hasLimit := len(limitValues) > 0
	limit := max(queryInt(r, "Limit", 0), 0)
	if startIndex >= len(keys) {
		keys = nil
	} else {
		keys = keys[startIndex:]
		if hasLimit && limit < len(keys) {
			keys = keys[:limit]
		}
	}
	ownerGUID, _ := ids.GUID(ids.User, owner.ID)
	now := i.currentTime()
	items := make([]any, 0, len(keys))
	for _, k := range keys {
		accessToken := k.Handle
		if secret, revealed := i.keyReveals.consume(token, k.ID, now); revealed {
			accessToken = secret
		}
		lastAct := k.LastUsed
		if lastAct.IsZero() {
			lastAct = k.CreatedAt
		}
		items = append(items, map[string]any{
			"Id":               k.ID,
			"AccessToken":      accessToken,
			"DeviceId":         "",
			"AppName":          k.AppName,
			"AppVersion":       "",
			"DeviceName":       "",
			"UserId":           ownerGUID,
			"IsActive":         true,
			"DateCreated":      wireTime(k.CreatedAt),
			"DateRevoked":      nil,
			"DateLastActivity": wireTime(lastAct),
			"UserName":         owner.Username,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonReply(w, i.queryResult(items, total, startIndex))
}

func (i *instance) createAPIKey(w http.ResponseWriter, r *http.Request) {
	_, token, ok := i.requireOwnerCaller(w, r)
	if !ok {
		return
	}
	apiKeys, ok := i.host.Identity.(frontend.APIKeyIdentity)
	if !ok {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "API keys are unavailable.")
		return
	}
	appValues := route.Query(r.URL.Query(), "App")
	if len(appValues) != 1 || strings.TrimSpace(appValues[0]) == "" {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Application name is required.")
		return
	}
	appName := strings.TrimSpace(appValues[0])
	info, secret, err := apiKeys.CreateAPIKey(r.Context(), appName)
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "Could not create API key.")
		return
	}
	i.keyReveals.remember(token, info.ID, secret, i.currentTime())
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (i *instance) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := i.requireOwnerCaller(w, r); !ok {
		return
	}
	apiKeys, ok := i.host.Identity.(frontend.APIKeyIdentity)
	if !ok {
		mediaBrowserProblem(w, r, http.StatusServiceUnavailable, "API keys are unavailable.")
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		mediaBrowserProblem(w, r, http.StatusBadRequest, "API key is required.")
		return
	}
	revokedID, err := apiKeys.RevokeAPIKey(r.Context(), key)
	if err != nil {
		mediaBrowserProblem(w, r, http.StatusInternalServerError, "Could not revoke API key.")
		return
	}
	if revokedID > 0 {
		i.keyReveals.forgetKey(revokedID)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

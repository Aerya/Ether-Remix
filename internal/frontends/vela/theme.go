package vela

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type themeGrant struct {
	scope, host string
	device      frontend.Device
	key         frontend.RatingKey
	expires     time.Time
}

func (i *instance) themeAvailable() bool {
	return i.host.Themes != nil && i.host.ThemeTitle != nil && i.host.Themes.ThemeEnabled()
}

// Grants have no playback session/watch side effects. Reopening detail reuses an
// active capability with comfortable lifetime remaining so detail ETags stay stable;
// revocation and current item policy apply per read.
func (i *instance) detailTheme(r *http.Request, token string, t frontend.Title) object {
	if !i.themeAvailable() || !i.host.Themes.Theme(t, true) {
		return nil
	}
	dev := requestDevice(r)
	scope, err := i.grantScope(r.Context(), token, dev)
	if err != nil {
		return nil
	}
	origin := i.themeOrigin(r)
	now := time.Now()
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.themes == nil {
		i.themes = map[string]themeGrant{}
	}
	for k, g := range i.themes {
		if !g.expires.After(now) {
			delete(i.themes, k)
			continue
		}
		if g.scope == scope && g.host == origin && g.device == dev && g.key == t.Key && g.expires.Sub(now) >= 2*time.Minute {
			return object{"url": "/vela/v0/themes/" + k + "/theme.m4a", "expiresAt": g.expires.UTC().Format(time.RFC3339)}
		}
	}
	if len(i.themes) >= 1024 {
		return nil
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil
	}
	secret := hex.EncodeToString(nonce[:])
	expiry := now.Add(10 * time.Minute)
	i.themes[secret] = themeGrant{scope: scope, host: origin, device: dev, key: t.Key, expires: expiry}
	return object{"url": "/vela/v0/themes/" + secret + "/theme.m4a", "expiresAt": expiry.UTC().Format(time.RFC3339)}
}

func (i *instance) theme(w http.ResponseWriter, r *http.Request) {
	i.mu.Lock()
	grant, ok := i.themes[r.PathValue("secret")]
	i.mu.Unlock()
	scopes, has := i.host.Identity.(frontend.GrantScopes)
	if !ok || !has || i.host.ThemeTitle == nil || !i.themeAvailable() || !grant.expires.After(time.Now()) || grant.host != i.themeOrigin(r) {
		http.NotFound(w, r)
		return
	}
	viewer, err := scopes.GrantScopeCurrent(r.Context(), grant.scope, grant.device)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title, err := i.host.ThemeTitle(r.Context(), viewer, grant.key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	i.host.Themes.ServeTheme(w, r, *title)
}

func (i *instance) themeOrigin(r *http.Request) string {
	if origin, ok := i.host.Origin(r); ok {
		return strings.ToLower(origin.Scheme + "://" + origin.Host)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

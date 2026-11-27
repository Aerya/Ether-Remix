package screens

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Studio preview (docs/plugin-screens.md, Checking your work). The owner's
// Studio compiles a working copy and gets a short-lived preview token; a
// Vela client that presents the token (X-Vela-Screen-Preview) sees the
// draft documents in place of the installed ones, through the same routes,
// sources and gates. Drafts are never cached and vanish when they expire.

const (
	previewTTL = 2 * time.Hour
	maxPreview = 16
)

type preview struct {
	plugin  *docPlugin
	expires time.Time
}

type previews struct {
	mu   sync.Mutex
	byID map[string]preview
}

// ErrPreviewInvalid reports a draft that does not compile.
var ErrPreviewInvalid = errors.New("screens: draft does not compile")

// CreatePreview compiles a draft (a Studio working copy) and returns its
// token. A draft's plugin id is its manifest id; it replaces an installed
// plugin of that id only for requests carrying the token.
func (r *DocRegistry) CreatePreview(man plugins.Manifest, files map[string][]byte, text TextFunc, now time.Time) (string, time.Time, error) {
	return r.UpdatePreview("", man, files, text, now)
}

// UpdatePreview recompiles a draft into an existing live token (so an open
// preview keeps its token across saves), or mints a new token when token is
// empty or no longer live.
func (r *DocRegistry) UpdatePreview(token string, man plugins.Manifest, files map[string][]byte, text TextFunc, now time.Time) (string, time.Time, error) {
	if r == nil {
		return "", time.Time{}, ErrNoDocuments
	}
	if !plugins.ScreensUseDocuments(man) {
		return "", time.Time{}, ErrNoDocuments
	}
	var english func(string) (string, bool)
	if text != nil {
		english = func(k string) (string, bool) { return text("en", k) }
	}
	docs, err := plugins.CompileScreenDocuments(man, files, english)
	if err != nil {
		return "", time.Time{}, err
	}
	e := &docPlugin{pluginID: man.ID, version: "draft-" + randomHex(4), enabled: true, contrib: man.Ether.Contributes, docs: docs, text: text, draft: true}
	for _, f := range e.contrib.LiveFeeds {
		e.feeds = append(e.feeds, f.ID)
	}
	// An installed plugin keeps its grants and roles; the draft only swaps
	// documents, so it inherits the installed entry's state when present.
	r.mu.RLock()
	if inst := r.byID[man.ID]; inst != nil {
		e.enabled, e.shadow, e.screensOff, e.liveOff, e.installedAt = inst.enabled, inst.shadow, inst.screensOff, inst.liveOff, inst.installedAt
	}
	r.mu.RUnlock()
	expires := now.Add(previewTTL)
	r.previews.mu.Lock()
	if r.previews.byID == nil {
		r.previews.byID = map[string]preview{}
	}
	if old, ok := r.previews.byID[token]; !ok || now.After(old.expires) || old.plugin.pluginID != man.ID {
		token = randomHex(24)
	}
	for k, p := range r.previews.byID {
		if now.After(p.expires) {
			delete(r.previews.byID, k)
		}
	}
	if len(r.previews.byID) >= maxPreview {
		oldest, first := "", true
		var at time.Time
		for k, p := range r.previews.byID {
			if first || p.expires.Before(at) {
				oldest, at, first = k, p.expires, false
			}
		}
		delete(r.previews.byID, oldest)
	}
	r.previews.byID[token] = preview{plugin: e, expires: expires}
	r.previews.mu.Unlock()
	return token, expires, nil
}

// previewFor returns the draft a request's preview token names, if any.
func (r *DocRegistry) previewFor(ctx context.Context) *docPlugin {
	token := frontend.ScreenPreviewFrom(ctx)
	if token == "" {
		return nil
	}
	r.previews.mu.Lock()
	defer r.previews.mu.Unlock()
	p, ok := r.previews.byID[token]
	if !ok || time.Now().After(p.expires) {
		return nil
	}
	return p.plugin
}

// IsPreview reports whether ctx carries a live preview token (served
// documents must then never be cached).
func (r *DocRegistry) IsPreview(ctx context.Context) bool {
	return r != nil && r.previewFor(ctx) != nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

package screens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/screendoc"
)

// DocumentRef is where a client downloads a document and the renderer it
// needs.
type DocumentRef struct {
	Href     string `json:"href"`
	Renderer int    `json:"renderer"`
}

// NavigationEntry is a document screen in bootstrap navigation.
type NavigationEntry struct {
	Kind     string      `json:"kind"`
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Icon     string      `json:"icon,omitempty"`
	Page     string      `json:"page"`
	Document DocumentRef `json:"document"`
}

// SurfaceEntry is a surface in bootstrap surfaces.
type SurfaceEntry struct {
	ID           string          `json:"id"`
	Presentation string          `json:"presentation"`
	Anchor       string          `json:"anchor,omitempty"`
	When         *SurfaceWhen    `json:"when,omitempty"`
	Visibility   string          `json:"visibility,omitempty"`
	Setting      *SurfaceSetting `json:"setting,omitempty"`
	TTL          int             `json:"ttl,omitempty"`
	Document     DocumentRef     `json:"document"`
}

// SurfaceWhen is the wire form of a surface's applicability; feeds are fully
// qualified ("pluginID:feed") so they match a session's liveEvent.feed.
type SurfaceWhen struct {
	Event     *SurfaceWhenEvent `json:"event,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Alert     *SurfaceWhenAlert `json:"alert,omitempty"`
	TitleKind string            `json:"titleKind,omitempty"`
}

// SurfaceWhenEvent matches the event on air by its feed.
type SurfaceWhenEvent struct {
	Feed string `json:"feed"`
}

// SurfaceWhenAlert matches alerts of one plugin.
type SurfaceWhenAlert struct {
	Plugin string `json:"plugin"`
}

// SurfaceSetting is the player-menu switch, with the viewer's choice.
type SurfaceSetting struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
	Enabled bool   `json:"enabled"`
}

// ClientInfo is what a request says about the client.
type ClientInfo struct {
	Frontend string
	// Renderer is the client's RENDERER_VERSION; 0 means the client draws no
	// documents.
	Renderer int
	Locale   string
}

func (e *docPlugin) localise(locale, s string) string {
	key, ok := strings.CutPrefix(s, "$t:")
	if !ok || e.text == nil {
		return s
	}
	if t, ok := e.text(locale, key); ok && strings.TrimSpace(t) != "" {
		return t
	}
	return key
}

func docVersion(e *docPlugin, d *screendoc.Compiled) string {
	sum := sha256.Sum256([]byte(e.version + "\x00" + d.Hash))
	return hex.EncodeToString(sum[:8])
}

func documentHref(base string, e *docPlugin, d *screendoc.Compiled, locale string) string {
	q := url.Values{}
	q.Set("v", docVersion(e, d))
	q.Set("r", strconv.Itoa(d.Renderer))
	if locale != "" {
		q.Set("l", locale)
	}
	return base + "?" + q.Encode()
}

// Navigation returns the document screens for bootstrap navigation: placed in
// navigation, visible to viewer, with an index variant the client draws.
func (r *DocRegistry) Navigation(ctx context.Context, viewer *core.User, c ClientInfo) []NavigationEntry {
	if r == nil || c.Renderer <= 0 {
		return nil
	}
	out := []NavigationEntry{}
	for _, t := range r.Screens(ctx, viewer, c.Frontend) {
		if t.Meta.Placement == "hidden" {
			continue
		}
		d := pickVariant(t.plugin.docs.Screens[t.Meta.ID]["index"], c.Renderer)
		if d == nil {
			continue
		}
		out = append(out, NavigationEntry{
			Kind:  "screen",
			ID:    t.WireID,
			Title: t.plugin.localise(c.Locale, t.Meta.Title),
			Icon:  t.Meta.Icon,
			Page:  "index",
			Document: DocumentRef{
				Href:     documentHref("/vela/v0/screens/"+url.PathEscape(t.WireID)+"/pages/index/document", t.plugin, d, c.Locale),
				Renderer: d.Renderer,
			},
		})
	}
	return out
}

// SurfaceEntries returns the surfaces for bootstrap. enabled reports the
// viewer's remembered setting for a player overlay (ok=false: the default).
func (r *DocRegistry) SurfaceEntries(ctx context.Context, viewer *core.User, c ClientInfo, enabled func(surfaceID string) (bool, bool)) []SurfaceEntry {
	if r == nil || c.Renderer <= 0 {
		return nil
	}
	out := []SurfaceEntry{}
	for _, t := range r.Surfaces(ctx, viewer, c.Frontend) {
		d := pickVariant(t.plugin.docs.Surfaces[t.Meta.ID], c.Renderer)
		if d == nil {
			continue
		}
		m := t.Meta
		entry := SurfaceEntry{
			ID:           t.WireID,
			Presentation: m.Presentation,
			Anchor:       m.Anchor,
			Visibility:   m.Visibility,
			TTL:          m.TTL,
			Document: DocumentRef{
				Href:     documentHref("/vela/v0/surfaces/"+url.PathEscape(t.WireID)+"/document", t.plugin, d, c.Locale),
				Renderer: d.Renderer,
			},
		}
		if m.Presentation == screendoc.PresentPlayerOverlay && entry.Visibility == "" {
			entry.Visibility = "always"
		}
		if w := m.When; w != nil || m.Presentation == screendoc.PresentToast {
			sw := &SurfaceWhen{}
			if w != nil {
				if w.Event != nil {
					sw.Event = &SurfaceWhenEvent{Feed: t.PluginID + ":" + w.Event.Feed}
				}
				sw.Kind = w.Kind
				sw.TitleKind = w.TitleKind
			}
			if m.Presentation == screendoc.PresentToast {
				sw.Alert = &SurfaceWhenAlert{Plugin: t.PluginID}
			}
			entry.When = sw
		}
		if s := m.Setting; s != nil {
			on := s.Default
			if enabled != nil {
				if v, ok := enabled(t.WireID); ok {
					on = v
				}
			}
			entry.Setting = &SurfaceSetting{ID: s.ID, Label: t.plugin.localise(c.Locale, s.Label), Default: s.Default, Enabled: on}
		}
		out = append(out, entry)
	}
	return out
}

// DocumentRequest asks for one served document.
type DocumentRequest struct {
	Kind string // "screen" or "surface"
	ID   string // wire id
	Page string // screens only
	ClientInfo
}

const maxServedDocuments = 256

// Document returns the localised client document and its version tag.
func (r *DocRegistry) Document(ctx context.Context, viewer *core.User, req DocumentRequest) ([]byte, string, error) {
	if r == nil || req.Renderer <= 0 {
		return nil, "", core.ErrNotFound
	}
	var (
		e            *docPlugin
		d            *screendoc.Compiled
		screenID     string
		page         string
		title        string
		presentation string
		actions      map[string]plugins.ScreenAction
	)
	switch req.Kind {
	case "screen":
		t, ok := r.FindScreen(ctx, viewer, req.Frontend, req.ID)
		if !ok {
			return nil, "", core.ErrNotFound
		}
		variants, ok := t.plugin.docs.Screens[t.Meta.ID][req.Page]
		if !ok {
			return nil, "", core.ErrNotFound
		}
		if d = pickVariant(variants, req.Renderer); d == nil {
			return nil, "", ErrNoVariant
		}
		e, screenID, page, title, actions = t.plugin, t.WireID, req.Page, t.Meta.Title, t.Meta.Actions
		presentation = screendoc.PresentPage
		if page == "index" {
			presentation = screendoc.PresentSection
		}
	case "surface":
		t, ok := r.FindSurface(ctx, viewer, req.Frontend, req.ID)
		if !ok {
			return nil, "", core.ErrNotFound
		}
		if d = pickVariant(t.plugin.docs.Surfaces[t.Meta.ID], req.Renderer); d == nil {
			return nil, "", ErrNoVariant
		}
		e, screenID, page, title, presentation = t.plugin, t.WireID, "main", t.Meta.Title, t.Meta.Presentation
	default:
		return nil, "", core.ErrNotFound
	}
	tag := docVersion(e, d)
	key := strings.Join([]string{e.pluginID, req.Kind, screenID, page, strconv.Itoa(d.Renderer), req.Locale, tag}, "\x00")
	if !e.draft {
		r.servedMu.Lock()
		body, ok := r.served[key]
		r.servedMu.Unlock()
		if ok {
			return body, tag, nil
		}
	}
	acts := map[string]string{}
	for id, a := range actions {
		acts[id] = ""
		if a.Confirm != "" {
			acts[id] = e.localise(req.Locale, a.Confirm)
		}
	}
	locale := req.Locale
	body, err := d.Serve(screendoc.ServeMeta{
		Screen:       screenID,
		Page:         page,
		Version:      e.version,
		Title:        e.localise(locale, title),
		Presentation: presentation,
		Actions:      acts,
		AssetURL: func(p string) string {
			return "/vela/v0/screen-assets/" + url.PathEscape(e.pluginID) + "/" + url.PathEscape(e.version) + "/" + p
		},
		Text: func(k string) (string, bool) {
			if e.text == nil {
				return "", false
			}
			return e.text(locale, k)
		},
	})
	if err != nil {
		return nil, "", err
	}
	if e.draft {
		return body, tag, nil
	}
	r.servedMu.Lock()
	if len(r.served) >= maxServedDocuments {
		for k := range r.served {
			delete(r.served, k)
			break
		}
	}
	r.served[key] = body
	r.servedMu.Unlock()
	return body, tag, nil
}

// Asset reports whether path is an asset one of pluginID's documents names,
// at version. Only named assets are served.
func (r *DocRegistry) Asset(pluginID, version, path string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	e := r.byID[pluginID]
	r.mu.RUnlock()
	if e == nil || e.version != version || !e.active() {
		return false
	}
	check := func(d *screendoc.Compiled) bool {
		for _, a := range d.Assets {
			if a == path {
				return true
			}
		}
		return false
	}
	for _, pages := range e.docs.Screens {
		for _, vs := range pages {
			for _, d := range vs {
				if check(d) {
					return true
				}
			}
		}
	}
	for _, vs := range e.docs.Surfaces {
		for _, d := range vs {
			if check(d) {
				return true
			}
		}
	}
	return false
}

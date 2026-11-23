package frontend

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	// ErrInvalidScreenDate reports a malformed date query on a screen request.
	ErrInvalidScreenDate = errors.New("frontend: invalid screen date")
	// ErrInvalidEntityKey reports a malformed entity favourite key.
	ErrInvalidEntityKey = errors.New("frontend: invalid entity key")
	// ErrForbidden reports an authenticated viewer who may not access or mutate
	// the requested resource.
	ErrForbidden = errors.New("frontend: forbidden")
)

// NavigationScreen is one plugin-contributed top-level navigation entry
// exposed in Vela bootstrap.navigation: {kind: "screen", id, title, icon?}.
type NavigationScreen struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// Screens serves plugin-declared generic screens and live entity lookup /
// favourites for frontends such as Vela (spec A3, A4).
type Screens interface {
	// Navigation returns plugin screens published to this frontend that
	// viewer may use, in deterministic order.
	Navigation(ctx context.Context, viewer *Viewer) ([]NavigationScreen, error)
	// ResolveScreen returns the JSON-encoded generic resolved screen payload
	// for GET /vela/v0/screens/{id}?league=&date=.
	ResolveScreen(ctx context.Context, viewer *Viewer, id, league, date string) (json.RawMessage, error)
	// QueryEntities returns the JSON-encoded entity search payload for
	// GET /vela/v0/live/entities?feed=&kind=&league=&q=.
	QueryEntities(ctx context.Context, viewer *Viewer, feed, kind, league, q string) (json.RawMessage, error)
	// SetFavoriteEntity sets or clears one entity favourite for viewer
	// (PUT/DELETE /vela/v0/favorites/entities/{key}).
	SetFavoriteEntity(ctx context.Context, viewer *Viewer, key string, on bool) error
}

// ScreensGeneration optionally reports a monotonic generation for screen
// navigation changes so frontend bootstrap caches invalidate promptly.
type ScreensGeneration interface {
	Generation() uint64
}

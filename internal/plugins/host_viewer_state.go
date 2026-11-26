package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

// Per-viewer state for a plugin's own kind items (likes, plays, ratings and
// playlists). A frontend adapter that speaks a protocol with its own library
// state (Subsonic stars, scrobbles and playlists) reads and writes it here,
// and it lands in the same stores every Ether frontend uses: the viewer's
// favorites, watch state and playlists. There is no plugin-private copy.
//
// Every call is scoped to the frontend-adapter viewer Ether authenticated on
// the current request (never a viewer id from guest input) and to the calling
// plugin's own items in one declared kind.

// MaxKindStateIDs bounds one kinds.state / kinds.favorite batch.
const MaxKindStateIDs = 500

// MaxKindFavorites bounds one kinds.favorites listing.
const MaxKindFavorites = 1000

// MaxKindPlaylistItems bounds the items one playlist call adds or returns.
const MaxKindPlaylistItems = 5000

// KindItemState is one item's state for the viewer.
type KindItemState struct {
	ID           string  `json:"id"`
	PlayCount    int     `json:"playCount"`
	LastPlayedAt int64   `json:"lastPlayedAt,omitempty"` // unix ms
	Favorite     bool    `json:"favorite"`
	Rating       float64 `json:"rating,omitempty"` // 0-10, 0 = unrated
}

// KindFavorite is one favorited item of the plugin's kind.
type KindFavorite struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	AddedAt int64  `json:"addedAt"` // unix ms
}

// KindFavoritesQuery filters a kinds.favorites listing.
type KindFavoritesQuery struct {
	Level string `json:"level,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// KindPlaylist is one of the viewer's playlists as the plugin sees it. Items
// lists only this plugin's items of the kind, in playlist order; Total is the
// whole playlist length, including entries from elsewhere in Ether.
type KindPlaylist struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary,omitempty"`
	Smart     bool     `json:"smart,omitempty"`
	CreatedAt int64    `json:"createdAt"` // unix ms
	UpdatedAt int64    `json:"updatedAt"` // unix ms
	Total     int      `json:"total"`
	Items     []string `json:"items,omitempty"`
	// Indexes are the positions of Items within the whole playlist, for
	// removals that must address one occurrence.
	Indexes []int `json:"indexes,omitempty"`
}

// KindPlaylistUpdate edits one playlist. Remove addresses positions within
// the whole playlist (KindPlaylist.Indexes) and runs before Add.
type KindPlaylistUpdate struct {
	Title   *string  `json:"title,omitempty"`
	Summary *string  `json:"summary,omitempty"`
	Add     []string `json:"add,omitempty"`
	Remove  []int    `json:"remove,omitempty"`
}

// KindViewerState is the app backend. Implementations re-check that the
// viewer may see the kind's library and that every id is the plugin's item.
type KindViewerState interface {
	State(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]KindItemState, error)
	SetFavorite(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string, on bool) error
	Played(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) error
	Rate(ctx context.Context, pluginID string, viewerID int64, kindKey, id string, rating float64) error
	Favorites(ctx context.Context, pluginID string, viewerID int64, kindKey string, q KindFavoritesQuery) ([]KindFavorite, error)
	Playlists(ctx context.Context, pluginID string, viewerID int64, kindKey string) ([]KindPlaylist, error)
	Playlist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) (KindPlaylist, error)
	CreatePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, title string, ids []string) (KindPlaylist, error)
	UpdatePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string, update KindPlaylistUpdate) (KindPlaylist, error)
	DeletePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) error
}

// SetKindViewerState wires the per-viewer state backend.
func (m *Manager) SetKindViewerState(s KindViewerState) {
	if s == nil {
		m.viewerState.Store(nil)
		return
	}
	m.viewerState.Store(&s)
}

// viewerStateFor checks the capability, the kind and the adapter viewer, and
// returns the backend.
func (h *host) viewerStateFor(ctx context.Context, kindKey, op string) (KindViewerState, int64, error) {
	if _, _, err := h.kindCheck(ctx, kindKey, CapFrontendAdapter); err != nil {
		return nil, 0, err
	}
	viewerID, ok := ViewerFromContext(ctx)
	if !ok || viewerID <= 0 {
		return nil, 0, fmt.Errorf("%w: %s requires an authenticated frontend adapter viewer", ErrPermissionDenied, op)
	}
	backend := h.m.viewerState.Load()
	if backend == nil || *backend == nil {
		return nil, 0, ErrUnavailable
	}
	return *backend, viewerID, nil
}

func checkStateIDs(field string, ids []string, max int) error {
	if len(ids) > max {
		return &ValidationError{Fields: []FieldError{{Field: field, Message: fmt.Sprintf("at most %d ids per call", max)}}}
	}
	for _, id := range ids {
		if !ValidExternalID(id) {
			return &ValidationError{Fields: []FieldError{{Field: field, Message: "ids must be valid external ids"}}}
		}
	}
	return nil
}

func checkPlaylistTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 500 {
		return &ValidationError{Fields: []FieldError{{Field: "title", Message: "title must be 1 to 500 characters"}}}
	}
	return nil
}

// kindGet returns the stored items for a bounded batch of external ids,
// limited to those the frontend-adapter viewer may see under the library's
// Rules and access policy, in the requested order.
func (h *host) kindGet(ctx context.Context, kindKey string, ids []string) ([]core.KindItem, error) {
	if err := checkStateIDs("ids", ids, MaxKindVisibleIDs); err != nil {
		return nil, err
	}
	visible, err := h.KindVisible(ctx, kindKey, ids)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return []core.KindItem{}, nil
	}
	backend := h.m.kindBackends().KindItemsByID
	if backend == nil {
		return nil, ErrUnavailable
	}
	items, err := backend(ctx, h.plugin, kindKey, visible)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]core.KindItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	out := make([]core.KindItem, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if it, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, it)
		}
	}
	return out, nil
}

// callViewerState dispatches the hosted kinds.* viewer-state services.
func (h *host) callViewerState(ctx context.Context, name string, args []json.RawMessage) (any, error, bool) {
	str := func(i int) string {
		var s string
		if i < len(args) {
			_ = json.Unmarshal(args[i], &s)
		}
		return s
	}
	strs := func(i int) ([]string, error) {
		var out []string
		if i < len(args) && string(args[i]) != "null" {
			if err := json.Unmarshal(args[i], &out); err != nil {
				return nil, &wasm.Fault{Code: "bad_arguments", Message: name + " takes a list of ids"}
			}
		}
		return out, nil
	}
	kind := str(0)
	switch name {
	case "kinds.get":
		ids, err := strs(1)
		if err != nil {
			return nil, err, true
		}
		out, err := h.kindGet(ctx, kind, ids)
		return out, err, true
	case "kinds.state":
		ids, err := strs(1)
		if err != nil {
			return nil, err, true
		}
		if err := checkStateIDs("ids", ids, MaxKindStateIDs); err != nil {
			return nil, err, true
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		if len(ids) == 0 {
			return []KindItemState{}, nil, true
		}
		out, err := svc.State(ctx, h.plugin, viewer, kind, ids)
		if out == nil && err == nil {
			out = []KindItemState{}
		}
		return out, err, true
	case "kinds.favorite":
		ids, err := strs(1)
		if err != nil {
			return nil, err, true
		}
		if err := checkStateIDs("ids", ids, MaxKindStateIDs); err != nil {
			return nil, err, true
		}
		var on bool
		if len(args) > 2 {
			_ = json.Unmarshal(args[2], &on)
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		if len(ids) == 0 {
			return nil, nil, true
		}
		return nil, svc.SetFavorite(ctx, h.plugin, viewer, kind, ids, on), true
	case "kinds.played", "kinds.rate":
		id := str(1)
		if err := checkStateIDs("id", []string{id}, 1); err != nil {
			return nil, err, true
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		if name == "kinds.played" {
			return nil, svc.Played(ctx, h.plugin, viewer, kind, id), true
		}
		var rating float64
		if len(args) > 2 {
			_ = json.Unmarshal(args[2], &rating)
		}
		if rating < 0 || rating > 10 {
			return nil, &ValidationError{Fields: []FieldError{{Field: "rating", Message: "rating must be 0 to 10"}}}, true
		}
		return nil, svc.Rate(ctx, h.plugin, viewer, kind, id, rating), true
	case "kinds.favorites":
		var q KindFavoritesQuery
		if len(args) > 1 && string(args[1]) != "null" {
			if err := json.Unmarshal(args[1], &q); err != nil {
				return nil, &wasm.Fault{Code: "bad_arguments", Message: "kinds.favorites takes (kind, { level?, limit? })"}, true
			}
		}
		if q.Limit <= 0 || q.Limit > MaxKindFavorites {
			q.Limit = MaxKindFavorites
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		out, err := svc.Favorites(ctx, h.plugin, viewer, kind, q)
		if out == nil && err == nil {
			out = []KindFavorite{}
		}
		return out, err, true
	case "kinds.playlists":
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		out, err := svc.Playlists(ctx, h.plugin, viewer, kind)
		if out == nil && err == nil {
			out = []KindPlaylist{}
		}
		return out, err, true
	case "kinds.playlist":
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		out, err := svc.Playlist(ctx, h.plugin, viewer, kind, str(1))
		return out, err, true
	case "kinds.playlistCreate":
		title := str(1)
		if err := checkPlaylistTitle(title); err != nil {
			return nil, err, true
		}
		ids, err := strs(2)
		if err != nil {
			return nil, err, true
		}
		if err := checkStateIDs("ids", ids, MaxKindPlaylistItems); err != nil {
			return nil, err, true
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		out, err := svc.CreatePlaylist(ctx, h.plugin, viewer, kind, strings.TrimSpace(title), ids)
		return out, err, true
	case "kinds.playlistUpdate":
		var upd KindPlaylistUpdate
		if len(args) > 2 && string(args[2]) != "null" {
			if err := json.Unmarshal(args[2], &upd); err != nil {
				return nil, &wasm.Fault{Code: "bad_arguments", Message: "kinds.playlistUpdate takes (kind, id, { title?, summary?, add?, remove? })"}, true
			}
		}
		if upd.Title != nil {
			if err := checkPlaylistTitle(*upd.Title); err != nil {
				return nil, err, true
			}
			t := strings.TrimSpace(*upd.Title)
			upd.Title = &t
		}
		if upd.Summary != nil && utf8.RuneCountInString(*upd.Summary) > 20000 {
			return nil, &ValidationError{Fields: []FieldError{{Field: "summary", Message: "summary is longer than 20000 characters"}}}, true
		}
		if err := checkStateIDs("add", upd.Add, MaxKindPlaylistItems); err != nil {
			return nil, err, true
		}
		if len(upd.Remove) > MaxKindPlaylistItems {
			return nil, &ValidationError{Fields: []FieldError{{Field: "remove", Message: fmt.Sprintf("at most %d positions per call", MaxKindPlaylistItems)}}}, true
		}
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		out, err := svc.UpdatePlaylist(ctx, h.plugin, viewer, kind, str(1), upd)
		return out, err, true
	case "kinds.playlistDelete":
		svc, viewer, err := h.viewerStateFor(ctx, kind, name)
		if err != nil {
			return nil, err, true
		}
		return nil, svc.DeletePlaylist(ctx, h.plugin, viewer, kind, str(1)), true
	}
	return h.callKindFill(ctx, name, args)
}

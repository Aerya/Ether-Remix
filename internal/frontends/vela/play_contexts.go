package vela

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type playContextRequest struct {
	Kind string `json:"kind"` // "title", "playlist", "favorites"
	ID   string `json:"id"`   // a title or playlist id, or a liked-list role
}

func (i *instance) playContexts() (frontend.PlayContexts, bool) {
	source, ok := i.host.Experience.(frontend.PlayContexts)
	return source, ok
}

func (i *instance) playContextsAvailable() bool {
	_, ok := i.playContexts()
	return ok
}

// recordPlayContext notes what a play started from, after the request and
// never in its way; a context the viewer may not see is dropped.
func (i *instance) recordPlayContext(r *http.Request, v *frontend.Viewer, req playContextRequest) {
	source, ok := i.playContexts()
	if !ok || len(req.ID) > 128 {
		return
	}
	ref := frontend.PlayContextRef{Kind: req.Kind}
	switch req.Kind {
	case "title", "playlist":
		key, err := ids.ParseRatingKeyGUID(req.ID)
		if err != nil {
			return
		}
		ref.Key = key
	case "favorites":
		ref.Role = req.ID
	default:
		return
	}
	ctx := context.WithoutCancel(r.Context())
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = source.RecordPlayContext(ctx, v, ref)
	}()
}

// recentPlayContexts serves GET /vela/v0/recent?limit=: what the viewer last
// played from (titles, playlists, liked lists), newest first.
func (i *instance) recentPlayContexts(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.playContexts()
	if !ok {
		i.unavailable(w, r)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	recent, err := source.RecentPlayContexts(r.Context(), v, n)
	if err != nil {
		fail(w, err, r)
		return
	}
	var titles []frontend.Title
	var playlists []frontend.Playlist
	for _, c := range recent {
		if c.Title != nil {
			titles = append(titles, *c.Title)
		}
		if c.Playlist != nil {
			playlists = append(playlists, *c.Playlist)
		}
	}
	wiredTitles, err := i.titles(r, v, token, titles)
	if err != nil {
		fail(w, err, r)
		return
	}
	wiredPlaylists, err := i.playlistCardsWire(r, v, token, playlists)
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(recent))
	ti, pi := 0, 0
	for _, c := range recent {
		played := c.PlayedAt.UTC().Format(time.RFC3339)
		switch {
		case c.Title != nil:
			items = append(items, object{"kind": "title", "title": wiredTitles[ti], "playedAt": played})
			ti++
		case c.Playlist != nil:
			items = append(items, object{"kind": "playlist", "playlist": wiredPlaylists[pi], "playedAt": played})
			pi++
		case c.Kind == "favorites":
			items = append(items, object{"kind": "favorites", "role": c.Role, "playedAt": played})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, object{"items": items})
}

package vela

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

var favoriteRole = regexp.MustCompile(`^[a-z][a-zA-Z]{0,31}$`)

func (i *instance) favoriteTitlesSource() (frontend.FavoriteTitles, bool) {
	source, ok := i.host.Experience.(frontend.FavoriteTitles)
	return source, ok
}

func (i *instance) favoritesListAvailable() bool {
	_, ok := i.favoriteTitlesSource()
	return ok
}

// favoriteTitles serves GET /vela/v0/favorites/titles?role=&cursor=&limit=:
// one page of the viewer's liked titles of one role (Liked Songs is
// role=track, followed artists role=artist), in the viewer's order.
func (i *instance) favoriteTitles(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.favoriteTitlesSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	query := r.URL.Query()
	role := query.Get("role")
	if !favoriteRole.MatchString(role) || len(query["role"]) != 1 {
		problem(w, 400, "request", "invalid_request")
		return
	}
	page, err := source.FavoriteTitles(r.Context(), v, frontend.FavoriteTitlesQuery{Role: role, Cursor: query.Get("cursor"), Limit: limit(r)})
	if errors.Is(err, frontend.ErrBadFavoritesCursor) {
		problem(w, 409, "conflict", "cursor_expired")
		return
	}
	if err != nil {
		fail(w, err, r)
		return
	}
	ts := make([]frontend.Title, len(page.Items))
	for n, item := range page.Items {
		ts[n] = item.Title
	}
	wired, err := i.titles(r, v, token, ts)
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(page.Items))
	for n, item := range page.Items {
		t := wired[n]
		items = append(items, object{"id": t["id"], "kind": "title", "title": t, "likedAt": item.LikedAt.UTC().Format(time.RFC3339), "receiptToken": ""})
	}
	out := object{"items": items, "total": page.Total, "continuation": "exhausted"}
	if page.Next != "" {
		out["continuation"], out["nextCursor"] = "more", page.Next
	}
	w.Header().Set("Cache-Control", "no-store")
	varyLanguage(w)
	send(w, 200, out)
}

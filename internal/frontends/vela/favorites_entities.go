package vela

import (
	"net/http"
	"strings"
)

// liveEntities serves GET /vela/v0/live/entities?feed=&kind=&league=&q=.
func (i *instance) liveEntities(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	values := r.URL.Query()
	for name, entries := range values {
		switch name {
		case "feed", "kind", "league", "q":
			if len(entries) != 1 {
				problem(w, 400, "request", "invalid_request")
				return
			}
		default:
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	feed := strings.TrimSpace(values.Get("feed"))
	if feed == "" {
		problem(w, 400, "request", "invalid_request")
		return
	}
	if i.host.Screens == nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	raw, err := i.host.Screens.QueryEntities(r.Context(), v, feed, values.Get("kind"), values.Get("league"), values.Get("q"))
	if err != nil {
		fail(w, err, r)
		return
	}
	varyLanguage(w)
	cache(w, r, raw)
}

// favoriteEntity serves PUT and DELETE /vela/v0/favorites/entities/{key}.
// Both methods are idempotent and return 204 No Content per the Vela contract.
func (i *instance) favoriteEntity(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.Screens == nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		problem(w, 400, "request", "invalid_request")
		return
	}
	on := r.Method == http.MethodPut
	if err := i.host.Screens.SetFavoriteEntity(r.Context(), v, key, on); err != nil {
		fail(w, err, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

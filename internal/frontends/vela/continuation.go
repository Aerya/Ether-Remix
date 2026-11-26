package vela

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) continuationAvailable() bool {
	source, ok := i.host.Experience.(frontend.TitleContinuations)
	return ok && source.ContinuationAvailable()
}

// titleContinuation serves GET /vela/v0/titles/{id}/continuation?limit=: the
// playable titles to queue after this one when the queue ends (radio), in
// order. 404 when its source has none.
func (i *instance) titleContinuation(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.host.Experience.(frontend.TitleContinuations)
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := key(r)
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 50 {
		n = 25
	}
	titles, err := source.PluginContinuation(r.Context(), v, k, n)
	if errors.Is(err, frontend.ErrNotFound) || (err == nil && len(titles) == 0) {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if err != nil {
		fail(w, err, r)
		return
	}
	wired, err := i.titles(r, v, token, titles)
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(wired))
	for _, t := range wired {
		items = append(items, object{"id": t["id"], "kind": "title", "title": t, "receiptToken": ""})
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, object{"items": items})
}

package vela

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) screensGeneration() uint64 {
	if gen, ok := i.host.Screens.(frontend.ScreensGeneration); ok {
		return gen.Generation()
	}
	return 0
}

func (i *instance) screenNavigation(r *http.Request, v *frontend.Viewer) ([]frontend.NavigationScreen, error) {
	if i.host.Screens == nil || v == nil {
		return []frontend.NavigationScreen{}, nil
	}
	nav, err := i.host.Screens.Navigation(r.Context(), v)
	if err != nil {
		return nil, err
	}
	if nav == nil {
		return []frontend.NavigationScreen{}, nil
	}
	return nav, nil
}

// screen serves GET /vela/v0/screens/{id}?league=&date=.
func (i *instance) screen(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	if i.host.Screens == nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		problem(w, 404, "not-found", "not_found")
		return
	}
	values := r.URL.Query()
	for name, entries := range values {
		switch name {
		case "league", "date":
			if len(entries) != 1 {
				problem(w, 400, "request", "invalid_request")
				return
			}
		default:
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	raw, err := i.host.Screens.ResolveScreen(r.Context(), v, id, values.Get("league"), values.Get("date"))
	if err != nil {
		fail(w, err, r)
		return
	}
	varyLanguage(w)
	cache(w, r, raw)
}

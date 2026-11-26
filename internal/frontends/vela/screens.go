package vela

import (
	"encoding/json"
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) screensGeneration() uint64 {
	if gen, ok := i.host.Screens.(frontend.ScreensGeneration); ok {
		return gen.Generation()
	}
	return 0
}

// screenNavigation lists plugin screens for bootstrap navigation. Only
// clients that draw documents (X-Vela-Screen-Renderer) get any.
func (i *instance) screenNavigation(r *http.Request, v *frontend.Viewer) ([]any, error) {
	out := []any{}
	ps, ok := i.pluginScreens()
	c := screenClient(r)
	if !ok || v == nil || c.Renderer == 0 {
		return out, nil
	}
	raw, err := ps.ScreenNavigation(r.Context(), v, c)
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	for _, e := range entries {
		out = append(out, e)
	}
	return out, nil
}

// screenSurfaces lists the surfaces for bootstrap.
func (i *instance) screenSurfaces(r *http.Request, v *frontend.Viewer) (json.RawMessage, error) {
	ps, ok := i.pluginScreens()
	c := screenClient(r)
	if !ok || v == nil || c.Renderer == 0 {
		return json.RawMessage("[]"), nil
	}
	return ps.ScreenSurfaces(r.Context(), v, c)
}

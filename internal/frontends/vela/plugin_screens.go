package vela

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Plugin screens and surfaces drawn from documents (docs/plugin-screens.md).
// Vela routes and authenticates; the app's PluginScreens does the rest.

func (i *instance) pluginScreens() (frontend.PluginScreens, bool) {
	ps := i.host.Screens
	return ps, ps != nil
}

// previewExpired answers 403 preview_expired when a request carries a Studio
// preview token that is no longer live, so Vela can say so instead of
// silently showing the installed documents.
func (i *instance) previewExpired(w http.ResponseWriter, r *http.Request, ps frontend.PluginScreens) bool {
	if frontend.ScreenPreviewFrom(r.Context()) == "" || ps.ScreenPreviewValid(r.Context()) {
		return false
	}
	problemMessage(w, 403, "forbidden", "preview_expired", "this preview has expired; reload it from Studio")
	return true
}

// screenClient reads the client's renderer and locale. The renderer comes
// from X-Vela-Screen-Renderer; a request without it draws no documents.
func screenClient(r *http.Request) frontend.ScreenClient {
	c := frontend.ScreenClient{}
	if n, err := strconv.Atoi(strings.TrimSpace(r.Header.Get("X-Vela-Screen-Renderer"))); err == nil && n > 0 && n < 10000 {
		c.Renderer = n
	}
	c.Locale = screenLocale(requestDevice(r).Language)
	return c
}

// screenLocale reduces a language header to its first tag ("fr-CA,fr;q=0.9"
// -> "fr-CA").
func screenLocale(raw string) string {
	tag := strings.TrimSpace(strings.SplitN(strings.SplitN(raw, ",", 2)[0], ";", 2)[0])
	if len(tag) > 35 {
		return ""
	}
	for _, r := range tag {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return tag
}

func screenFail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, frontend.ErrScreenParams):
		problem(w, 400, "request", "invalid_request")
	case errors.Is(err, frontend.ErrScreenNoVariant):
		problemMessage(w, 404, "not-found", "update_required", "this app version cannot show this screen")
	case errors.Is(err, frontend.ErrScreenTopic):
		problem(w, 400, "request", "invalid_topic")
	case errors.Is(err, frontend.ErrScreenTooManyTopics):
		problem(w, 400, "request", "too_many_topics")
	case errors.Is(err, frontend.ErrScreenRateLimited):
		w.Header().Set("Retry-After", "10")
		problem(w, 429, "rate-limit", "rate_limited")
	default:
		fail(w, err, r)
	}
}

func (i *instance) screenDocument(w http.ResponseWriter, r *http.Request) {
	i.serveScreenDocument(w, r, "screen", r.PathValue("id"), r.PathValue("page"))
}

func (i *instance) surfaceDocument(w http.ResponseWriter, r *http.Request) {
	i.serveScreenDocument(w, r, "surface", r.PathValue("id"), "")
}

func (i *instance) serveScreenDocument(w http.ResponseWriter, r *http.Request, kind, id, page string) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	ps, ok := i.pluginScreens()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if i.previewExpired(w, r, ps) {
		return
	}
	q := r.URL.Query()
	for name, vals := range q {
		if (name != "v" && name != "r" && name != "l") || len(vals) != 1 {
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	c := screenClient(r)
	if c.Renderer == 0 {
		// A document link carries its variant's renderer.
		if n, err := strconv.Atoi(q.Get("r")); err == nil && n > 0 && n < 10000 {
			c.Renderer = n
		}
	}
	if l := screenLocale(q.Get("l")); l != "" {
		c.Locale = l
	}
	body, tag, err := ps.ScreenDocument(r.Context(), v, kind, id, page, c)
	if err != nil {
		screenFail(w, r, err)
		return
	}
	etag := `"` + tag + `"`
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	if frontend.ScreenPreviewFrom(r.Context()) != "" {
		w.Header().Set("Cache-Control", "no-store")
	} else if q.Get("v") == tag {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		// An old link still answers, but never as an immutable copy.
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

func (i *instance) screenData(w http.ResponseWriter, r *http.Request) {
	i.serveScreenData(w, r, "screen", r.PathValue("id"), r.PathValue("page"))
}

func (i *instance) surfaceData(w http.ResponseWriter, r *http.Request) {
	i.serveScreenData(w, r, "surface", r.PathValue("id"), "")
}

const maxScreenParamBytes = 512

func (i *instance) serveScreenData(w http.ResponseWriter, r *http.Request, kind, id, page string) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	ps, ok := i.pluginScreens()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if i.previewExpired(w, r, ps) {
		return
	}
	req := frontend.ScreenDataRequest{Kind: kind, ID: id, Page: page, Params: map[string]string{}, Client: screenClient(r), Images: i.newLiveImages(r, token)}
	for name, vals := range r.URL.Query() {
		if len(vals) != 1 || !utf8.ValidString(vals[0]) || len(vals[0]) > maxScreenParamBytes {
			problem(w, 400, "request", "invalid_request")
			return
		}
		switch {
		case name == "sources":
			if vals[0] != "" {
				req.Sources = strings.Split(vals[0], ",")
				if len(req.Sources) > 16 {
					problem(w, 400, "request", "invalid_request")
					return
				}
			}
		case name == "tz":
			req.TZ = vals[0]
		case strings.HasPrefix(name, "p.") && len(name) > 2 && len(req.Params) < 16:
			req.Params[strings.TrimPrefix(name, "p.")] = vals[0]
		default:
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	raw, err := ps.ScreenData(r.Context(), v, req)
	if err != nil {
		screenFail(w, r, err)
		return
	}
	varyLanguage(w)
	cache(w, r, raw)
}

func (i *instance) screenAction(w http.ResponseWriter, r *http.Request) {
	i.serveScreenAction(w, r, "screen", r.PathValue("id"), r.PathValue("action"))
}

func (i *instance) surfaceAction(w http.ResponseWriter, r *http.Request) {
	i.serveScreenAction(w, r, "surface", r.PathValue("id"), r.PathValue("action"))
}

func (i *instance) serveScreenAction(w http.ResponseWriter, r *http.Request, kind, id, action string) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	ps, ok := i.pluginScreens()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if i.previewExpired(w, r, ps) {
		return
	}
	var in struct {
		Page           string                     `json:"page"`
		Params         map[string]string          `json:"params"`
		Args           map[string]json.RawMessage `json:"args"`
		Context        map[string]string          `json:"context"`
		IdempotencyKey string                     `json:"idempotencyKey"`
		TZ             string                     `json:"tz"`
	}
	b, _ := r.Context().Value(bodyKey{}).(json.RawMessage)
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || len(in.Params) > 16 || len(in.Args) > 8 || len(in.Context) > 8 {
		problem(w, 400, "request", "invalid_request")
		return
	}
	for _, raw := range in.Args {
		if len(raw) > 8<<10 {
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = in.IdempotencyKey
	}
	if kind == "screen" && in.Page == "" {
		in.Page = "index"
	}
	raw, err := ps.ScreenAction(r.Context(), v, frontend.ScreenActionRequest{Kind: kind, ID: id, Action: action, Page: in.Page, Params: in.Params,
		Args: in.Args, Context: in.Context, IdempotencyKey: key, TZ: in.TZ, Client: screenClient(r), Images: i.newLiveImages(r, token)})
	if err != nil {
		screenFail(w, r, err)
		return
	}
	send(w, http.StatusOK, raw)
}

func (i *instance) surfaceSetting(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	ps, ok := i.pluginScreens()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Enabled == nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	if err := ps.SetSurfaceSetting(r.Context(), v, r.PathValue("id"), *in.Enabled); err != nil {
		screenFail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// screenAsset serves a package file a document names. Images load without
// credentials (like /images grants), and an asset is public data of a
// published plugin, so no viewer is required; only files the plugin's
// documents name, at the installed version, are served.
func (i *instance) screenAsset(w http.ResponseWriter, r *http.Request) {
	ps, ok := i.pluginScreens()
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	data, ct, err := ps.ScreenAsset(r.Context(), r.PathValue("plugin"), r.PathValue("version"), r.PathValue("path"))
	if err != nil {
		problem(w, 404, "not-found", "not_found")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

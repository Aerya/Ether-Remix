package scrobpush

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Viewer est un spectateur d'Ether proposé dans la console.
type Viewer struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	JellyfinLastAt int64  `json:"jellyfinLastAt,omitempty"`
}

// HandlerDeps relie l'API de la console à Ether.
type HandlerDeps struct {
	Links   Links
	Viewers func(ctx context.Context) ([]Viewer, error)
	Pusher  func() *Pusher
}

type viewerView struct {
	Viewer
	URL        string `json:"url"`
	Configured bool   `json:"configured"`
	LastStatus string `json:"lastStatus,omitempty"`
	LastAt     int64  `json:"lastAt,omitempty"`
}

// Handler sert GET/PUT / et POST /test (montés sous /ether/api/pse/scrob/).
func Handler(d HandlerDeps) http.Handler {
	mux := http.NewServeMux()
	view := func(w http.ResponseWriter, r *http.Request) {
		viewers, err := d.Viewers(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Spectateurs indisponibles"})
			return
		}
		links, err := d.Links.All(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Liens indisponibles"})
			return
		}
		out := make([]viewerView, 0, len(viewers))
		for _, v := range viewers {
			vv := viewerView{Viewer: v}
			if l, ok := links[v.ID]; ok {
				vv.URL, vv.Configured, vv.LastStatus, vv.LastAt = MaskURL(l.URL), true, l.LastStatus, l.LastAt
			}
			out = append(out, vv)
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": Enabled(), "viewers": out})
	}
	mux.HandleFunc("GET /{$}", view)
	mux.HandleFunc("PUT /{$}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Links map[string]*string `json:"links"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
			return
		}
		links, err := d.Links.All(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Liens indisponibles"})
			return
		}
		errs := map[string]string{}
		for k, raw := range body.Links {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil || id <= 0 {
				errs[k] = "Spectateur inconnu"
				continue
			}
			v := ""
			if raw != nil {
				v = strings.TrimSpace(*raw)
			}
			if prev, ok := links[id]; ok && v == MaskURL(prev.URL) {
				continue // inchangé, renvoyé masqué
			}
			if strings.Contains(v, "••••••") {
				errs[k] = "Collez le lien complet"
				continue
			}
			if err := d.Links.Set(r.Context(), id, v); err != nil {
				errs[k] = err.Error()
			}
		}
		if len(errs) > 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Certains liens sont invalides", "fields": errs})
			return
		}
		view(w, r)
	})
	mux.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Viewer int64  `json:"viewer"`
			URL    string `json:"url"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
		target := strings.TrimSpace(body.URL)
		if links, err := d.Links.All(r.Context()); err == nil {
			if l, ok := links[body.Viewer]; ok && (target == "" || target == MaskURL(l.URL)) {
				target = l.URL
			}
		}
		var p *Pusher
		if d.Pusher != nil {
			p = d.Pusher()
		}
		if p == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Envoi indisponible"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), webhookTimeout)
		defer cancel()
		if err := p.Test(ctx, target); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Scrob a accepté le lien et la clé API"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

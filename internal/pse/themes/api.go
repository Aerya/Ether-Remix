package themes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// API de la console (/ether/api/pse/themes/) : bibliothèques à thème,
// collections disponibles et modèles.

type apiCollection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Items     int    `json:"items"`
	BuiltIn   bool   `json:"builtIn"`
}

// Handler renvoie le routeur de l'API. creator crée les collections des
// modèles ; changed est appelé après chaque modification.
func (s *Service) Handler(creator Creator, changed func()) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, r *http.Request, extra map[string]any) {
		v, err := s.view(r.Context())
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for k, x := range extra {
			v[k] = x
		}
		apiJSON(w, http.StatusOK, v)
	}
	done := func(w http.ResponseWriter, r *http.Request, err error, extra map[string]any) {
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrInvalid):
				code = http.StatusBadRequest
			case errors.Is(err, core.ErrNotFound):
				code = http.StatusNotFound
			}
			apiJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		if changed != nil {
			changed()
		}
		reply(w, r, extra)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "requête illisible"})
			return false
		}
		return true
	}
	pathID := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiJSON(w, http.StatusNotFound, map[string]string{"error": "bibliothèque introuvable"})
			return 0, false
		}
		return id, true
	}
	// Titres en VOSTFR : {"key": "…", "type": "movie"|"show", "add": true}.
	mux.HandleFunc("GET /vostfr", func(w http.ResponseWriter, r *http.Request) {
		keys, err := s.Vostfr(r.Context())
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, map[string]any{"keys": keys, "max": MaxVostfr})
	})
	mux.HandleFunc("PUT /vostfr", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key  string `json:"key"`
			Type string `json:"type"`
			Add  bool   `json:"add"`
		}
		if !decode(w, r, &body) {
			return
		}
		ed, _ := creator.(Editor)
		err := s.SetVostfr(r.Context(), ed, body.Key, body.Type, body.Add)
		var extra map[string]any
		if err == nil && body.Add {
			extra = map[string]any{"queued": 0}
			h := s.priorityHooks()
			if h.Changed != nil {
				h.Changed()
			}
			if h.Search != nil {
				key, searchErr := core.ParseRatingKey(strings.TrimSpace(body.Key))
				n := 0
				if searchErr == nil {
					n, searchErr = h.Search(r.Context(), []core.RatingKey{key})
				}
				extra["queued"] = n
				if searchErr != nil || n == 0 {
					extra["searchWarning"] = "Titre ajouté ; recherche non mise en file. Vérifiez que l’indexation est active dans Films & séries."
				}
			} else {
				extra["searchWarning"] = "Titre ajouté ; indexation indisponible, recherche non mise en file."
			}
		}
		done(w, r, err, extra)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		_ = s.Reload(r.Context()) // noms de collections modifiés entre-temps
		reply(w, r, nil)
	})
	mux.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		var l Library
		if !decode(w, r, &l) {
			return
		}
		_, err := s.Create(r.Context(), l)
		done(w, r, err, nil)
	})
	mux.HandleFunc("PUT /{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var l Library
		if !decode(w, r, &l) {
			return
		}
		l.ID = id
		_, err := s.Update(r.Context(), l)
		done(w, r, err, nil)
	})
	mux.HandleFunc("DELETE /{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		done(w, r, s.Delete(r.Context(), id), nil)
	})
	mux.HandleFunc("POST /{id}/move", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var body struct {
			Dir int `json:"dir"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Dir != -1 && body.Dir != 1 {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "sens -1 ou 1 attendu"})
			return
		}
		done(w, r, s.Move(r.Context(), id, body.Dir), nil)
	})
	mux.HandleFunc("PUT /exceptions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Collections []string `json:"collections"`
		}
		if !decode(w, r, &body) {
			return
		}
		done(w, r, s.SetExceptions(r.Context(), body.Collections), nil)
	})
	// Priorité de recherche : {"kind": "collection"|"library", "id": "…", "level": 0-2}.
	priorities := func(w http.ResponseWriter, r *http.Request) {
		ps, total, err := s.Priorities(r.Context())
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, map[string]any{"priorities": ps, "total": total, "max": s.priorityHooks().Max})
	}
	fail := func(w http.ResponseWriter, err error) {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrInvalid) {
			code = http.StatusBadRequest
		}
		apiJSON(w, code, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /priority", priorities)
	mux.HandleFunc("PUT /priority", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind  string `json:"kind"`
			ID    string `json:"id"`
			Level int    `json:"level"`
		}
		if !decode(w, r, &body) {
			return
		}
		if err := s.SetPriority(r.Context(), body.Kind, body.ID, body.Level); err != nil {
			fail(w, err)
			return
		}
		if h := s.priorityHooks(); h.Changed != nil {
			h.Changed()
		}
		priorities(w, r)
	})
	mux.HandleFunc("POST /priority/search", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		if !decode(w, r, &body) {
			return
		}
		h := s.priorityHooks()
		if h.Search == nil {
			fail(w, invalid("indexation indisponible"))
			return
		}
		keys, err := s.SearchKeys(r.Context(), body.Kind, body.ID)
		if err != nil {
			fail(w, err)
			return
		}
		n, err := h.Search(r.Context(), keys)
		if err != nil {
			fail(w, err)
			return
		}
		apiJSON(w, http.StatusOK, map[string]int{"queued": n})
	})
	mux.HandleFunc("POST /preset", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Preset string `json:"preset"`
			Name   string `json:"name"`
		}
		if !decode(w, r, &body) {
			return
		}
		if creator == nil {
			apiJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "collections indisponibles"})
			return
		}
		// La création des collections interroge TMDB : elle ne s'arrête
		// pas si la console ferme la page.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer cancel()
		_, warnings, err := s.ApplyPreset(ctx, creator, body.Preset, body.Name)
		if warnings == nil {
			warnings = []string{}
		}
		done(w, r, err, map[string]any{"warnings": warnings})
	})
	return mux
}

func (s *Service) view(ctx context.Context) (map[string]any, error) {
	cols := []apiCollection{}
	if s.cols != nil {
		specs, err := s.cols.ListSpecs(ctx)
		if err != nil {
			return nil, err
		}
		for _, sp := range specs {
			if sp.OwnerUserID != 0 {
				continue // collections privées d'un spectateur
			}
			cols = append(cols, apiCollection{ID: sp.ID, Name: sp.Name, MediaType: sp.MediaType, Items: sp.ItemCount, BuiltIn: sp.BuiltIn})
		}
		sort.SliceStable(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	}
	libs := s.Libraries()
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, map[string]any{
			"id": l.ID, "name": l.Name, "type": l.Type, "collections": l.Collections,
			"position": l.Position, "section": l.Section(), "showSection": l.ShowSection(),
			"allLanguages": l.AllLanguages,
		})
	}
	return map[string]any{"libraries": out, "collections": cols, "presets": Presets(), "max": MaxLibraries,
		"exceptions": s.Exceptions(), "maxExceptions": MaxExceptions}, nil
}

func apiJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

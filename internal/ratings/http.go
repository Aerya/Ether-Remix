package ratings

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

// Router is owner-only; a catalog is supplied for explicit cache warming.
func (s *Service) Router(cat core.Catalog) http.Handler {
	r := chi.NewRouter()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	writeErr := func(w http.ResponseWriter, status int, code, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    code,
				"message": msg,
			},
		})
	}
	r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Settings(r.Context())
		if e != nil {
			http.Error(w, "Ratings settings unavailable", 500)
			return
		}
		reply(w, v)
	})
	r.Put("/settings", func(w http.ResponseWriter, r *http.Request) {
		var v Settings
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&v) != nil {
			writeErr(w, http.StatusBadRequest, "bad_json", "Invalid settings")
			return
		}
		if e := s.SaveSettings(r.Context(), v); e != nil {
			writeErr(w, http.StatusBadRequest, "validation", e.Error())
			return
		}
		v, err := s.Settings(r.Context())
		if err != nil {
			http.Error(w, "Ratings settings unavailable", http.StatusInternalServerError)
			return
		}
		reply(w, v)
	})
	r.Post("/refresh/{key}", func(w http.ResponseWriter, r *http.Request) {
		if cat == nil {
			http.Error(w, "Catalog unavailable", 503)
			return
		}
		k, e := core.ParseRatingKey(chi.URLParam(r, "key"))
		if e != nil {
			http.Error(w, "Invalid title", 400)
			return
		}
		t, e := cat.Get(r.Context(), k)
		if e != nil || t == nil || t.Adult {
			http.Error(w, "Title unavailable", 404)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		v, e := s.Refresh(ctx, t)
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		reply(w, v)
	})
	return r
}

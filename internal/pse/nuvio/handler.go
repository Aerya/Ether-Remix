package nuvio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Handler sert /{id}/nuvio… (monté sous /ether/api/pse/viewers/, déjà
// authentifié par la console, CSRF compris).
func (s *Service) Handler(exists func(ctx context.Context, id int64) bool) http.Handler {
	mux := http.NewServeMux()
	viewer := func(h func(w http.ResponseWriter, r *http.Request, id int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil || id <= 0 || (exists != nil && !exists(r.Context(), id)) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "Spectateur inconnu"})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			defer cancel()
			h(w, r.WithContext(ctx), id)
		}
	}
	fail := func(w http.ResponseWriter, err error) {
		slog.Default().Warn("nuvio: requête admin en échec", "err", err)
		code, msg := http.StatusBadGateway, "Nuvio est injoignable ou a refusé la demande"
		switch {
		case errors.Is(err, ErrNoLogin):
			code, msg = http.StatusConflict, "Aucune connexion en cours"
		case errors.Is(err, ErrUnauthorized):
			code, msg = http.StatusUnauthorized, "Reconnexion à Nuvio nécessaire"
		case isUserError(err):
			code, msg = http.StatusBadRequest, err.Error()
		}
		writeJSON(w, code, map[string]any{"error": msg})
	}
	reply := func(w http.ResponseWriter, st Status, err error) {
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
	mux.HandleFunc("GET /{id}/nuvio", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		st, err := s.State(r.Context(), id)
		reply(w, st, err)
	}))
	mux.HandleFunc("POST /{id}/nuvio/start", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		st, err := s.Start(r.Context(), id)
		reply(w, st, err)
	}))
	mux.HandleFunc("POST /{id}/nuvio/poll", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		st, err := s.Poll(r.Context(), id)
		reply(w, st, err)
	}))
	mux.HandleFunc("PUT /{id}/nuvio", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		var body struct {
			ProfileIndex int  `json:"profileIndex"`
			Pull         bool `json:"pull"`
			Push         bool `json:"push"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
			return
		}
		st, err := s.Choose(r.Context(), id, body.ProfileIndex, body.Pull, body.Push)
		reply(w, st, err)
	}))
	mux.HandleFunc("POST /{id}/nuvio/sync", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		sum, err := s.Sync(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		st, _ := s.State(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"summary": sum, "status": st})
	}))
	mux.HandleFunc("DELETE /{id}/nuvio", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		if err := s.Unlink(r.Context(), id); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, Status{})
	}))
	mux.HandleFunc("POST /{id}/nuvio/install-addon", viewer(func(w http.ResponseWriter, r *http.Request, id int64) {
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
			return
		}
		if err := s.InstallAddon(r.Context(), id, body.URL, r.Host); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	return mux
}

// userError est une erreur à montrer telle quelle (saisie ou état invalide).
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func isUserError(err error) bool { var u userError; return errors.As(err, &u) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

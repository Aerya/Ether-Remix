package quality

import (
	"encoding/json"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func (s *Service) Handler(changed func()) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(v); err != nil {
			reply(w, 400, map[string]string{"error": "JSON invalide ou trop volumineux"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, s.Config()) })
	mux.HandleFunc("PUT /", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		if !decode(w, r, &c) {
			return
		}
		if err := s.Save(r.Context(), c); err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if changed != nil {
			changed()
		}
		reply(w, 200, s.Config())
	})
	mux.HandleFunc("POST /import", func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if !decode(w, r, &raw) {
			return
		}
		out, err := ImportAIO(raw)
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		reply(w, 200, out)
	})
	mux.HandleFunc("POST /preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Profile Profile  `json:"profile"`
			Names   []string `json:"names"`
		}
		if !decode(w, r, &in) {
			return
		}
		ps, err := compile(Config{Profiles: []Profile{in.Profile}})
		if err != nil || len(in.Names) > 100 {
			reply(w, 400, map[string]string{"error": "Profil invalide ou plus de 100 noms"})
			return
		}
		out := []any{}
		for _, n := range in.Names {
			if len(n) > 2048 {
				reply(w, 400, map[string]string{"error": "Nom trop long"})
				return
			}
			rel := core.Release{Name: n, Info: releaseparse.Parse(n), Protocol: core.ProtocolUnstated}
			out = append(out, map[string]any{"name": n, "result": evaluate(ps[0].policy, rel)})
		}
		reply(w, 200, out)
	})
	return mux
}

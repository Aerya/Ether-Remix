package submaker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	view := func() any {
		s.mu.Lock()
		c := s.scopes
		s.mu.Unlock()
		jobs := []Job{}
		rows, e := s.DB.Query("SELECT id,release_name,state,error,updated FROM remix_submaker_jobs ORDER BY updated DESC LIMIT 30")
		if e == nil {
			defer rows.Close()
			for rows.Next() {
				var j Job
				if rows.Scan(&j.ID, &j.Release, &j.State, &j.Error, &j.Updated) == nil {
					jobs = append(jobs, j)
				}
			}
		}
		return map[string]any{"scopes": c, "jobs": jobs}
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, view()) })
	mux.HandleFunc("PUT /", func(w http.ResponseWriter, r *http.Request) {
		var c Scopes
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&c) != nil || len(c.Rules) > 64 {
			reply(w, 400, map[string]string{"error": "Invalid scopes"})
			return
		}
		seen := map[string]bool{}
		for _, rule := range c.Rules {
			k := rule.Scope + ":" + rule.Target
			if (rule.Scope != "library" && rule.Scope != "collection") || strings.TrimSpace(rule.Target) == "" || len(rule.Target) > 120 || seen[k] {
				reply(w, 400, map[string]string{"error": "Invalid or duplicate scope"})
				return
			}
			seen[k] = true
		}
		b, _ := json.Marshal(c)
		s.mu.Lock()
		_, e := s.DB.Exec("INSERT INTO remix_submaker_scopes(id,value) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", string(b))
		if e == nil {
			s.scopes = c
		}
		s.mu.Unlock()
		if e != nil {
			reply(w, 500, map[string]string{"error": "Could not save scopes"})
			return
		}
		reply(w, 200, view())
	})
	mux.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		c := Load()
		_, _, e := Endpoint(c.Manifest)
		if e == nil {
			var b []byte
			b, e = s.request(ctx, "GET", c.Manifest, nil)
			if e == nil {
				var m struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(b, &m) != nil || !strings.Contains(strings.ToLower(m.ID), "submaker") {
					reply(w, 400, map[string]string{"error": "Not a SubMaker manifest"})
					return
				}
			}
		}
		if e != nil {
			reply(w, 400, map[string]string{"error": e.Error()})
			return
		}
		reply(w, 200, map[string]string{"message": "SubMaker manifest reachable; no translation started"})
	})
	return mux
}

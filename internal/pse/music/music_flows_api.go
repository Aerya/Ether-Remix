package music

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
)

func (a *PlaylistAPI) flowRoutes(mux *http.ServeMux) {
	fail := func(w http.ResponseWriter) {
		apiJSON(w, 400, map[string]string{"error": "Opération impossible. Vérifiez les paramètres, puis rechargez la page."})
	}
	mux.HandleFunc("GET /flows", func(w http.ResponseWriter, r *http.Request) {
		viewer, _ := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
		if !a.Known(r.Context(), viewer) {
			fail(w)
			return
		}
		fs, err := a.flows(r.Context(), viewer)
		if err != nil {
			fail(w)
			return
		}
		cfg := a.audioMuseConfig()
		apiJSON(w, 200, map[string]any{"flows": fs, "audioMuse": cfg.URL != "" && cfg.Server != "", "busy": a.Sync.Busy(viewer)})
	})
	mux.HandleFunc("GET /flows/seeds", func(w http.ResponseWriter, r *http.Request) {
		viewer, _ := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
		if !a.Known(r.Context(), viewer) {
			fail(w)
			return
		}
		// The same library access filter used for playback determines the suggestions.
		rows, err := a.Sync.Store.DB.QueryContext(r.Context(), `SELECT data FROM pse_music_owned WHERE kind='track' AND provider LIKE 'local:%' AND `+mediaaccess.Predicate("provider", viewer)+` ORDER BY name,item_id LIMIT 1000`)
		if err != nil {
			fail(w)
			return
		}
		type choice struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		out := []choice{}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var t Track
			if rows.Scan(&raw) != nil {
				fail(w)
				return
			}
			if json.Unmarshal([]byte(raw), &t) == nil && t.ID > 0 {
				out = append(out, choice{t.ID, t.Artist.Name + " — " + t.Title})
			}
		}
		if rows.Err() != nil {
			fail(w)
			return
		}
		apiJSON(w, 200, out)
	})
	mux.HandleFunc("PUT /flows", func(w http.ResponseWriter, r *http.Request) {
		var f MusicFlow
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&f) != nil || !a.Known(r.Context(), f.Viewer) {
			fail(w)
			return
		}
		if f.Ref == "" {
			var b [16]byte
			if _, err := rand.Read(b[:]); err != nil {
				fail(w)
				return
			}
			f.Ref = hex.EncodeToString(b[:])
			f.Revision = 0
		}
		if err := a.saveFlow(r.Context(), f); err != nil {
			fail(w)
			return
		}
		apiJSON(w, 200, map[string]string{"ref": f.Ref})
	})
	mux.HandleFunc("DELETE /flows", func(w http.ResponseWriter, r *http.Request) {
		var f MusicFlow
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&f) != nil || !a.Known(r.Context(), f.Viewer) {
			fail(w)
			return
		}
		if a.deleteFlow(r.Context(), f.Viewer, f.Ref) != nil {
			fail(w)
			return
		}
		apiJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /flows/run", func(w http.ResponseWriter, r *http.Request) {
		var f MusicFlow
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&f) != nil || !a.Known(r.Context(), f.Viewer) {
			fail(w)
			return
		}
		fs, err := a.flows(r.Context(), f.Viewer)
		found := false
		for _, v := range fs {
			if v.Ref == f.Ref {
				found = true
			}
		}
		if err != nil || !found {
			fail(w)
			return
		}
		if a.Sync.Busy(f.Viewer) {
			apiJSON(w, 409, map[string]string{"error": "Actualisation déjà en cours"})
			return
		}
		a.Sync.mark(f.Viewer, 1)
		run := a.Background
		if run == nil {
			run = func(fn func(context.Context)) { go fn(context.Background()) }
		}
		run(func(ctx context.Context) {
			defer a.Sync.mark(f.Viewer, -1)
			ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			defer cancel()
			_ = a.runFlow(ctx, f.Viewer, f.Ref, true)
		})
		apiJSON(w, 202, map[string]bool{"ok": true})
	})
}

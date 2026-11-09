package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/music"
)

// psePlaylistDiagnose sert POST /music/diagnose {"query": "..."} : chaque
// source audio réglée est essayée séparément sur un même titre (console
// Ether-Remix, onglet Musique). {"source": "…"} réessaie une seule source.
// Chaque appel construit des résolveurs neufs : les caches de lecture
// (absences, liens) ne sont ni lus ni modifiés. Les liens d'addons ne sont
// jamais renvoyés en entier, seulement leur hôte ; les liens audio signés
// ne sont jamais renvoyés.
func (a *App) psePlaylistDiagnose() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "méthode non prise en charge", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Query string `json:"query"`
			// Source : nom d'une source du rapport, pour la réessayer seule.
			Source string `json:"source"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		cfg := music.LoadSettings()
		w.Header().Set("Content-Type", "application/json")
		d, err := music.DiagnoseSource(ctx, cfg, a.newPSEMusicDebrid(), body.Query, body.Source, nil)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	})
}

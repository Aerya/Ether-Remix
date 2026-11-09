package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/statistics"
)

func (a *App) pseIndexHistoryHandler() http.Handler {
	type cached struct {
		at   time.Time
		data statistics.Dashboard
	}
	var mu sync.Mutex
	cache := map[string]cached{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "méthode non prise en charge", 405)
			return
		}
		media := r.URL.Query().Get("media")
		if media != "" && media != "anime" {
			http.Error(w, "type de média invalide", 400)
			return
		}
		period, source := r.URL.Query().Get("period"), r.URL.Query().Get("source")
		if period == "" {
			period = "day"
		}
		if (period != "day" && period != "week" && period != "month" && period != "year") || len(source) > 128 {
			http.Error(w, "période ou source invalide", 400)
			return
		}
		rt := pseRuntimeOf(a)
		mu.Lock()
		defer mu.Unlock()
		key := period + "|" + source + "|" + media
		value, ok := cache[key]
		if !ok || r.URL.Query().Get("refresh") == "1" || time.Since(value.at) >= time.Minute {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			data, err := rt.statistics.History(ctx, period, source, media == "anime")
			if err != nil {
				http.Error(w, "Historique indisponible", 503)
				return
			}
			names := map[string]string{}
			known := map[string]bool{}
			for _, src := range data.Sources {
				known[src.ID] = true
			}
			if a.cfg != nil {
				for _, ix := range a.cfg.Get().Indexers {
					names[ix.ID] = ix.Name
					if !known[ix.ID] {
						data.Sources = append(data.Sources, statistics.Source{ID: ix.ID, Name: ix.Name})
					}
				}
			}
			for i := range data.Sources {
				if name := names[data.Sources[i].ID]; name != "" {
					data.Sources[i].Name = name
				}
			}
			if len(cache) > 64 {
				cache = map[string]cached{}
			}
			value = cached{at: time.Now(), data: data}
			cache[key] = value
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(value.data)
	})
}

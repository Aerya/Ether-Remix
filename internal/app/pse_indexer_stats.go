package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// Détail de l'indexation des releases pour la console Ether-Remix (GET
// /indexer/details) : avancement par type, par bibliothèque à thème et
// par collection. Le calcul parcourt toutes les collections : il est gardé
// quelques minutes.

const pseIndexDetailsTTL = 5 * time.Minute

type pseIndexRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	releasedb.Counts
}

type pseIndexLibrary struct {
	pseIndexRow
	Rows []pseIndexRow `json:"rows"`
}

type pseIndexDetails struct {
	At          int64                       `json:"at"`
	Min         int                         `json:"min"`
	Types       map[string]releasedb.Counts `json:"types"`
	Libraries   []pseIndexLibrary           `json:"libraries"`
	Collections []pseIndexRow               `json:"collections"`
	// Empty : collections pas encore construites ou réservées à un
	// spectateur, non détaillées.
	Empty int `json:"empty"`
}

type pseIndexDetailsCache struct {
	mu  sync.Mutex
	at  time.Time
	val pseIndexDetails
}

var pseIndexCache pseIndexDetailsCache

func (a *App) pseIndexDetailsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "méthode non prise en charge", http.StatusMethodNotAllowed)
			return
		}
		pseIndexCache.mu.Lock()
		defer pseIndexCache.mu.Unlock()
		if r.URL.Query().Get("refresh") != "" || time.Since(pseIndexCache.at) >= pseIndexDetailsTTL {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			d, err := a.pseIndexDetails(ctx)
			cancel()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			pseIndexCache.at, pseIndexCache.val = time.Now(), d
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pseIndexCache.val)
	})
}

func (a *App) pseIndexDetails(ctx context.Context) (pseIndexDetails, error) {
	st := releasedb.NewStore(a.db)
	min := releasedb.Playable().ShowMin() // titres réellement affichés, risqués compris
	d := pseIndexDetails{At: time.Now().Unix(), Min: min}
	var err error
	if d.Types, err = st.TypeCounts(ctx, min); err != nil {
		return d, err
	}
	if a.collections == nil {
		return d, nil
	}
	specs, err := a.collections.ListSpecs(ctx)
	if err != nil {
		return d, err
	}
	names := map[string]string{}
	keys := map[string][]core.RatingKey{}
	for _, sp := range specs {
		if ctx.Err() != nil {
			return d, ctx.Err()
		}
		names[sp.ID] = sp.Name
		ks := a.collections.PSEKeys(ctx, sp.ID, nil)
		if len(ks) == 0 {
			d.Empty++
			continue
		}
		keys[sp.ID] = ks
		c, err := st.KeyCounts(ctx, ks, min)
		if err != nil {
			return d, err
		}
		d.Collections = append(d.Collections, pseIndexRow{ID: sp.ID, Name: sp.Name, Type: sp.MediaType, Counts: c})
	}
	sort.SliceStable(d.Collections, func(i, j int) bool { return d.Collections[i].Titles > d.Collections[j].Titles })
	rows := map[string]pseIndexRow{}
	for _, c := range d.Collections {
		rows[c.ID] = c
	}
	if th := a.pseThemes(); th != nil {
		for _, l := range th.Libraries() {
			lib := pseIndexLibrary{pseIndexRow: pseIndexRow{Name: l.Name, Type: l.Type}}
			var all []core.RatingKey
			for _, id := range l.Collections {
				row, ok := rows[id]
				if !ok {
					row = pseIndexRow{ID: id, Name: names[id]}
				}
				lib.Rows = append(lib.Rows, row)
				all = append(all, keys[id]...)
			}
			c, err := st.KeyCounts(ctx, all, min)
			if err != nil {
				return d, err
			}
			lib.Counts = c
			d.Libraries = append(d.Libraries, lib)
		}
	}
	return d, nil
}

// Activity is cheap and uncached; the more expensive source inventory is cached
// independently so polling never recalculates every collection.
func (a *App) pseIndexActivityHandler(sources bool) http.Handler {
	var mu sync.Mutex
	var cached releasedb.SourceOverview
	var owner *releasedb.Service
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "méthode non prise en charge", 405)
			return
		}
		rt := pseRuntimeOf(a)
		rt.mu.Lock()
		svc := rt.indexer
		rt.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if svc == nil {
			if sources {
				_ = json.NewEncoder(w).Encode(releasedb.SourceOverview{Sources: []releasedb.SourceStats{}, History: []releasedb.SourceDay{}})
			} else {
				_ = json.NewEncoder(w).Encode(releasedb.Activity{At: time.Now().UnixMilli(), State: "disabled", Active: []releasedb.SearchEvent{}, Recent: []releasedb.SearchEvent{}})
			}
			return
		}
		if !sources {
			_ = json.NewEncoder(w).Encode(svc.Activity())
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if owner != svc || time.Since(time.UnixMilli(cached.At)) >= 30*time.Second {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			val, err := svc.SourceOverview(ctx)
			if err != nil {
				http.Error(w, "Statistiques des sources indisponibles", 503)
				return
			}
			cached = val
			owner = svc
		}
		_ = json.NewEncoder(w).Encode(cached)
	})
}

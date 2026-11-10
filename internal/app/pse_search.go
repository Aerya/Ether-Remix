package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// Recherche Plex et Jellyfin (ajout Ether-Remix) : les premiers titres
// trouvés par un spectateur passent en tête de la file d'indexation, s'ils
// n'ont jamais été cherchés ou n'avaient rien donné (releasedb.SearchSoon).
// La mise en file se fait à part : la recherche n'attend pas.

// pseSearchQueued enveloppe les sources de recherche partagées.
func (a *App) pseSearchQueued(src experience.SearchSources) experience.SearchSources {
	if catalog := src.Catalog; catalog != nil {
		src.Catalog = func(ctx context.Context, q string, types []core.MediaType, limit int) ([]core.Title, error) {
			items, err := catalog(ctx, q, types, limit)
			if err == nil {
				a.pseQueueSearch(ctx, items)
			}
			return items, err
		}
	}
	if natural := src.Natural; natural != nil {
		src.Natural = func(ctx context.Context, u *core.User, q string, limit int) ([]core.Title, bool) {
			items, ok := natural(ctx, u, q, limit)
			if ok {
				a.pseQueueSearch(ctx, items)
			}
			return items, ok
		}
	}
	return src
}

// pseQueueSearch met en file les titres d'une recherche explicite.
func (a *App) pseQueueSearch(ctx context.Context, items []core.Title) {
	if !core.ExplicitSearch(ctx) || len(items) == 0 {
		return
	}
	rt := pseRuntimeOf(a)
	rt.mu.Lock()
	ix := rt.indexer
	rt.mu.Unlock()
	if ix == nil {
		return
	}
	keys := make([]core.RatingKey, 0, len(items))
	for _, t := range items {
		keys = append(keys, t.Key)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = ix.SearchSoon(ctx, keys)
	}()
}

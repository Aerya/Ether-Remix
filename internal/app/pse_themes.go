package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/themes"
)

// pseThemes prépare une fois les bibliothèques à thème de PSE et les
// publie au serveur Plex. Voir docs/pse/FORK.fr.md.
func (a *App) pseThemes() *themes.Service {
	rt := pseRuntimeOf(a)
	rt.thOnce.Do(func() {
		if a.db == nil {
			return
		}
		var cols themes.Collections
		if a.collections != nil {
			cols = a.collections
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		svc, err := themes.New(ctx, a.db, cols)
		if err != nil {
			a.log.Warn("bibliothèques à thème Ether-Remix illisibles", "err", err)
		}
		rt.themes = svc
		frontend.SetPSEThemes(svc)
		if svc != nil {
			ctx, cancel := context.WithCancel(context.Background())
			a.closers = append(a.closers, cancel)
			rt.exemptKick = make(chan struct{}, 1)
			go a.runPSELangExempt(ctx, svc, rt.exemptKick)
			if a.collections != nil {
				go func() {
					if err := svc.ShowPresetCollections(ctx, a.collections); err != nil && ctx.Err() == nil {
						a.log.Warn("collections des modèles Ether-Remix", "err", err)
					}
				}()
			}
		}
	})
	return rt.themes
}

// pseLangExemptEvery : cadence de mise à jour des exceptions de langue (les
// collections se reconstruisent en arrière-plan).
const pseLangExemptEvery = 15 * time.Minute

// runPSELangExempt publie dans le catalogue les titres qui échappent au
// filtre de langue (bibliothèques « toutes langues », collections
// d'exception), au démarrage, après chaque modification et toutes les
// 15 minutes.
func (a *App) runPSELangExempt(ctx context.Context, svc *themes.Service, kick <-chan struct{}) {
	st := releasedb.NewStore(a.db)
	t := time.NewTicker(pseLangExemptEvery)
	defer t.Stop()
	for {
		run, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := st.SetLangExempt(run, svc.ExemptKeys(run)); err != nil && ctx.Err() == nil {
			a.log.Warn("exceptions de langue Ether-Remix non enregistrées", "err", err)
		}
		a.refreshPSEQuality(run, svc)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
	}
}

// pseLangExemptChanged relance la mise à jour des exceptions de langue.
func (a *App) pseLangExemptChanged() {
	if k := pseRuntimeOf(a).exemptKick; k != nil {
		select {
		case k <- struct{}{}:
		default:
		}
	}
}

// pseThemePriority donne à l'indexeur les titres des bibliothèques à thème.
// Les priorités choisies dans la console passent devant.
func (a *App) pseThemePriority(ctx context.Context, typ core.MediaType) []releasedb.PriorityKey {
	t := a.pseThemes()
	if t == nil {
		return nil
	}
	var out []releasedb.PriorityKey
	for k, level := range t.PriorityLevels(ctx, typ) {
		out = append(out, releasedb.PriorityKey{Key: k, Level: level})
	}
	// Exceptions de langue d'abord : sans release indexée, elles ne
	// peuvent pas apparaître au catalogue.
	for _, k := range append(t.ExemptKeys(ctx), t.AllKeys(ctx, typ)...) {
		out = append(out, releasedb.PriorityKey{Key: k})
	}
	return out
}

// psePriorityHooks relie la console à l'indexeur en cours.
func (a *App) psePriorityHooks(rt *pseRuntime) themes.PriorityHooks {
	current := func() *releasedb.Service {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return rt.indexer
	}
	return themes.PriorityHooks{
		Max: releasedb.PriorityMax,
		Changed: func() {
			if ix := current(); ix != nil {
				ix.InvalidatePriority()
			}
		},
		Search: func(ctx context.Context, keys []core.RatingKey) (int, error) {
			ix := current()
			if ix == nil {
				return 0, fmt.Errorf("%w : indexation indisponible", themes.ErrInvalid)
			}
			n, err := ix.SearchNow(ctx, keys)
			if errors.Is(err, releasedb.ErrIndexerOff) {
				return 0, fmt.Errorf("%w : la recherche de releases est désactivée dans Ether-Remix", themes.ErrInvalid)
			}
			return n, err
		},
	}
}

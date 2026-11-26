package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/nuvio"
)

// Liaison Nuvio par spectateur (internal/pse/nuvio). Voir docs/pse/FORK.fr.md.

// nuvioCatalog convertit les identifiants IMDb de Nuvio en clés d'Ether.
type nuvioCatalog struct{ a *App }

func (c nuvioCatalog) KeyFor(ctx context.Context, t nuvio.Target) (core.RatingKey, bool) {
	if c.a.catalog == nil {
		return 0, false
	}
	if !t.Series {
		m, err := c.a.catalog.ByExternalIDForType(ctx, "imdb", t.IMDB, core.TypeMovie)
		if err != nil || m == nil {
			return 0, false
		}
		return m.Key, true
	}
	show, err := c.a.catalog.ByExternalIDForType(ctx, "imdb", t.IMDB, core.TypeShow)
	if err != nil || show == nil {
		return 0, false
	}
	key, err := c.a.catalog.ResolveEpisode(ctx, show.Key, t.Season, t.Episode)
	if err != nil || key == 0 {
		return 0, false
	}
	return key, true
}

func (c nuvioCatalog) TargetOf(ctx context.Context, key core.RatingKey) (nuvio.Target, string, bool) {
	if c.a.catalog == nil {
		return nuvio.Target{}, "", false
	}
	t, err := c.a.catalog.Get(ctx, key)
	if err != nil || t == nil {
		return nuvio.Target{}, "", false
	}
	switch key.Kind() {
	case core.KindMovie:
		return nuvio.Target{IMDB: t.IDs.IMDB}, t.Title, t.IDs.IMDB != ""
	case core.KindEpisode:
		imdb := t.ShowIDs.IMDB
		if imdb == "" {
			if show, err := c.a.catalog.Get(ctx, core.ShowKey(key.TMDB())); err == nil && show != nil {
				imdb = show.IDs.IMDB
			}
		}
		return nuvio.Target{IMDB: imdb, Series: true, Season: key.Season(), Episode: key.Episode()}, t.Title, imdb != ""
	}
	return nuvio.Target{}, "", false
}

// pseNuvio construit une fois le service Nuvio de l'instance.
func (a *App) pseNuvio() *nuvio.Service {
	rt := pseRuntimeOf(a)
	rt.nuvioOnce.Do(func() {
		if a.db == nil || a.state == nil || a.catalog == nil {
			return
		}
		rt.nuvio = &nuvio.Service{
			Store:   &nuvio.Store{DB: a.db, Sealer: a.cfg},
			Client:  nuvio.NewClient(nil),
			Catalog: nuvioCatalog{a},
			Applier: a.state,
			Log:     a.log.With("component", "pse-nuvio"),
		}
	})
	return rt.nuvio
}

// startPSENuvio démarre la lecture périodique et l'envoi des événements.
func (a *App) startPSENuvio() {
	svc := a.pseNuvio()
	if svc == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)
	unsub := a.state.Subscribe(svc.Handle)
	frontend.SetPSENuvioIncoming(func(user int64, key core.RatingKey) { svc.MarkIncoming(user, key) })
	a.closers = append(a.closers, func() { unsub(); cancel(); frontend.SetPSENuvioIncoming(nil) })
}

// pseNuvioAPI sert /ether/api/pse/viewers/{id}/nuvio….
func (a *App) pseNuvioAPI(w http.ResponseWriter, r *http.Request) {
	svc := a.pseNuvio()
	if svc == nil {
		http.NotFound(w, r)
		return
	}
	exists := func(ctx context.Context, id int64) bool {
		vs, err := a.pseViewers(ctx)
		if err != nil {
			return false
		}
		for _, v := range vs {
			if v.ID == id {
				return true
			}
		}
		return false
	}
	http.StripPrefix("/viewers", svc.Handler(exists)).ServeHTTP(w, r)
}

func pseIsNuvioPath(p string) bool {
	return strings.HasPrefix(p, "/viewers/") && strings.Contains(p, "/nuvio")
}

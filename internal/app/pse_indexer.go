package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// buildPSEIndexer charge les réglages PSE enregistrés depuis la console puis
// démarre l'indexation des releases en arrière-plan (désactivée sauf
// PSE_INDEXER=on). Voir docs/pse/FORK.fr.md.
func (a *App) buildPSEIndexer() {
	if a.db == nil || a.catalog == nil {
		return
	}
	rt := pseRuntimeOf(a)
	rt.prepare(a)
	a.pseQuality()
	a.pseThemes()
	a.buildPSEScrob()
	a.startPSEScrobPush()
	a.startSeerrCatalog()
	a.startPSETorrent()
	rt.restartIndexer(a)
	a.closers = append(a.closers, rt.stopIndexer)
}

func (a *App) newPSEIndexer() *releasedb.Service {
	return releasedb.New(releasedb.Deps{
		DB:     a.db,
		Config: a.cfg.Read,
		Browse: func(ctx context.Context, q core.BrowseQuery) (core.Page, error) {
			q.Filter.PSEUnplayable = true
			return a.catalog.Browse(ctx, q)
		},
		Search: func(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
			if a.indexers == nil {
				return nil, nil
			}
			rels, err := a.indexers.Search(ctx, req)
			if err == nil {
				a.prepareSubmaker(ctx, req, rels)
			}
			return rels, err
		},
		Priority: a.pseThemePriority,
		Active:   func() bool { return a.playback != nil && len(a.playback.Sessions()) > 0 },
		Logger:   a.log.With("component", "pse-indexer"),
		Settings: releasedb.LoadSettings(),
	})
}

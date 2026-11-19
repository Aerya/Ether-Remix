package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/config"
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
	a.reapplyAddonIndexers()
	a.pseQuality()
	a.pseThemes()
	a.buildPSEScrob()
	a.startPSEScrobPush()
	a.startSeerrCatalog()
	a.startPSETorrent()
	a.startPSEXdcc()
	a.startPSEFTP()
	if a.indexers != nil {
		a.indexers.SetSearchAdmission(func(ctx context.Context, req core.SearchRequest, rs []core.Release) []core.Release {
			out := make([]core.Release, 0, len(rs))
			for _, r := range rs {
				if strictReferenceAdmission(req, r) == "" {
					out = append(out, r)
				}
			}
			return out
		})
		a.indexers.SetSearchObserver(a.rememberSearchReferences)
		a.indexers.SetRetainedRefresh(releasedb.LibraryEnabled)
	}
	a.startAssociationAudit()
	a.closers = append(a.closers, rt.stopIndexer)
}

func (a *App) newPSEIndexer() *releasedb.Service {
	return releasedb.New(releasedb.Deps{
		DB: a.db,
		ReferenceReady: func(ctx context.Context, key core.RatingKey) bool {
			return len(a.releaseLibrary().Candidates(ctx, key)) > 0
		},
		Remember: func(ctx context.Context, key core.RatingKey, rs []core.Release) {
			media := key
			if key.Kind() == core.KindEpisode {
				media = core.ShowKey(key.TMDB())
			}
			requests, err := releasedb.AssociationRequestsFor(ctx, a.db, []core.RatingKey{media})
			if err != nil {
				return
			}
			req, known := releasedb.AssociationRequest(requests, key, 0, 0)
			if !known {
				return
			}
			verified := make([]core.Release, 0, len(rs))
			for _, r := range rs {
				if strictReferenceAdmission(req, r) == "" {
					verified = append(verified, r)
				}
			}
			rs = verified
			for i := range rs {
				if rs[i].IndexerID == "" {
					rs[i].IndexerID = sfrSearchID
					if !core.IsVerifiedFile(rs[i]) {
						rs[i].Indexer = "StreamFusion Reborn (base)"
					}
				}
			}
			a.rememberReleases(ctx, &core.Title{Key: key}, rs)
		},
		Config: func() config.Config { // sources XDCC et FTP comprises, si actives
			c := a.cfg.Read()
			c.Indexers = ftpIndexers(xdccIndexers(c.Indexers))
			return c
		},
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

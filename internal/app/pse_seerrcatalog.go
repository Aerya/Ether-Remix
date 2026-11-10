package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"time"
)

func (a *App) startSeerrCatalog() {
	rt := pseRuntimeOf(a)
	s := seerrcatalog.New(a.db, egress.Client(egress.CapMetadata, 15*time.Second))
	s.Ensure = func(ctx context.Context, k core.RatingKey) error { _, err := a.catalog.Get(ctx, k); return err }
	s.Changed = a.catalog.RefreshExternalEvidence
	s.Enrich = func(ctx context.Context, req core.SearchRequest) core.SearchRequest {
		key := core.MovieKey(req.IDs.TMDB)
		if req.Type == core.TypeEpisode {
			key = core.ShowKey(req.IDs.TMDB)
		}
		if title, err := a.catalog.Get(ctx, key); err == nil && title != nil {
			req.AlternativeTitles = append(append([]string{}, title.AlternativeTitles...), req.Title)
			req.Title, req.OriginalTitle = title.Title, title.OriginalTitle
			req.IDs.TVDB = title.IDs.TVDB
			if title.Year > 0 {
				req.Year = title.Year
			}
		}
		return req
	}
	rt.seerr = s
	if a.indexers != nil {
		a.indexers.SetRetainedSearch(s.Lookup)
		s.Search = a.indexers.Search
	}
	s.Start()
	a.closers = append(a.closers, s.Close)
}

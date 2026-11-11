package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// pseSubmakerRequest is the SubMaker request for a played title: an
// episode is searched under its show's IDs.
func pseSubmakerRequest(t *core.Title) core.SearchRequest {
	req := core.SearchRequest{IDs: t.IDs, Type: t.Type, Title: t.Title, Season: t.Season, Episode: t.Episode}
	if t.Type == core.TypeEpisode || t.Key.Kind() == core.KindEpisode {
		req.Type = core.TypeEpisode
		req.IDs = t.ShowIDs
		if req.IDs.TMDB == 0 {
			req.IDs.TMDB = t.Key.TMDB()
		}
	}
	if req.Type == core.TypeEpisode && req.Season == 0 {
		req.Season, req.Episode = t.Key.Season(), t.Key.Episode()
	}
	return req
}

// pseSubtitled tells the resolver which releases already have SubMaker
// subtitles, so they rank first among equal releases.
func (a *App) pseSubtitled(ctx context.Context, t *core.Title, rels []core.Release) []bool {
	if t == nil || a.db == nil {
		return nil
	}
	s := a.pseSubmaker()
	if s == nil {
		return nil
	}
	return s.ReadyReleases(ctx, pseSubmakerRequest(t), rels)
}

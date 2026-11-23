package resolver

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func applyReleaseNumbering(req core.SearchRequest, numbering core.ReleaseNumbering) core.SearchRequest {
	candidate := req
	candidate.AltSeason, candidate.AltEpisode = 0, 0
	candidate.ReleaseAliases = numbering.Aliases
	aliases := core.EpisodeAliases(candidate)
	if len(aliases) > 0 {
		req.ReleaseAliases = aliases
		req.ReleasePackTargets = numbering.PackTargets
		req.ReleaseNumberingKnown = true
		req.AltSeason, req.AltEpisode = aliases[0].Season, aliases[0].Episode
	}
	return req
}

func (s *Service) numberedRequest(ctx context.Context, req core.SearchRequest, cachedOnly bool) core.SearchRequest {
	if s.episodeNumbering == nil || req.Type != core.TypeEpisode || req.IDs.TMDB <= 0 {
		return req
	}
	if numbering, err := s.episodeNumbering(ctx, req, cachedOnly); err == nil {
		req = applyReleaseNumbering(req, numbering)
	}
	return req
}

// explicitNumberedRequest applies an owner-chosen episode group ("group" mode)
// up front so canonical and alias routes are queried and matched together.
// Automatic XEM discovery remains lazy unless canonical season-numbered
// discovery fails to settle the episode.
func (s *Service) explicitNumberedRequest(ctx context.Context, req core.SearchRequest) core.SearchRequest {
	if s.episodeNumbering == nil || req.Type != core.TypeEpisode || req.IDs.TMDB <= 0 || req.ReleaseNumberingKnown ||
		core.SpeculativeIndexerWork(ctx) || core.IsBackgroundCheck(ctx) {
		return req
	}
	numbering, err := s.episodeNumbering(ctx, req, true)
	if err != nil || !numbering.Explicit {
		return req
	}
	if len(numbering.Aliases) == 0 {
		numbering, err = s.episodeNumbering(ctx, req, false)
		if err != nil {
			return req
		}
	}
	return applyReleaseNumbering(req, numbering)
}

// Canonical discovery gets the first chance to settle automatic numbering:
// neither background/detail discovery nor an explicit canonical season match
// loads XEM. Season-less absolute releases ("Show - 750") do not prove season
// numbering, so automatic mode still checks XEM and owner-chosen groups always
// query both forms.
func (s *Service) searchWithNumbering(ctx context.Context, req *core.SearchRequest, fresh bool) ([]core.Release, bool, error) {
	*req = s.explicitNumberedRequest(ctx, *req)
	results, cached, err := s.searchCached(ctx, *req, fresh)
	if err != nil || req.Type != core.TypeEpisode || req.ReleaseNumberingKnown || core.SpeculativeIndexerWork(ctx) || core.IsBackgroundCheck(ctx) {
		return results, cached, err
	}
	for _, r := range results {
		if canonicalNumberingMatch(r, *req) {
			return results, cached, nil
		}
	}
	mapped := s.numberedRequest(ctx, *req, false)
	if mapped.AltEpisode == 0 {
		return results, cached, nil
	}
	*req = mapped
	// Reuse already returned releases before spending any further indexer calls.
	results = s.normalizeSearchResults(mapped, append([]core.Release(nil), results...))
	for _, r := range results {
		if aliasNumberingMatch(r, mapped) {
			return results, cached, nil
		}
	}
	// Keep canonical identity and the existing both-numbering indexer plumbing.
	// Progressive preparation belongs to the canonical search and must not
	// validate mapped candidates using its original episode numbers.
	aliasOnly := mapped
	aliasOnly.ReleaseAliasesOnly = true
	ctx = context.WithValue(ctx, searchPreparationKey{}, (*searchPreparation)(nil))
	extra, extraCached, extraErr := s.searchCached(ctx, aliasOnly, fresh)
	if extraErr != nil {
		return results, cached, nil
	}
	return append(results, novelReleases(results, extra)...), cached && extraCached, nil
}

func numberingTitleMatches(rel core.Release, req core.SearchRequest) bool {
	match, conflict := releaseparse.CompareTitleIDs(rel.ReportedTitleIDs(), req.IDs)
	if conflict {
		return false
	}
	if !match && !releaseparse.TitleMatchesAny(rel.Info, req.Title, req.AlternativeTitles) &&
		(req.OriginalTitle == "" || !releaseparse.TitleMatches(rel.Info, req.OriginalTitle)) &&
		(req.EnglishTitle == "" || !releaseparse.TitleMatches(rel.Info, req.EnglishTitle)) {
		return false
	}
	return true
}

// A different show's same-numbered result cannot settle this show's lookup,
// and season-less absolute releases ("Show - 750") leave season numbering
// undecided so XEM/TVDB aliases can still be discovered and matched.
func canonicalNumberingMatch(rel core.Release, req core.SearchRequest) bool {
	if !numberingTitleMatches(rel, req) || len(rel.Info.Seasons) == 0 {
		return false
	}
	return releaseparse.EpisodeMatches(rel.Info, req.Season, req.Episode) || (rel.Info.FullSeason && packCoversSeasonCour(rel.Info, req))
}

func aliasNumberingMatch(rel core.Release, req core.SearchRequest) bool {
	if !numberingTitleMatches(rel, req) || len(rel.Info.Seasons) == 0 {
		return false
	}
	if rel.Info.FullSeason {
		_, _, ok := packNumbers(rel.Info, req)
		return ok
	}
	for _, n := range core.EpisodeAliases(req) {
		if releaseparse.EpisodeMatches(rel.Info, n.Season, n.Episode) {
			return true
		}
	}
	return false
}

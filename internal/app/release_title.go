package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// releaseTitleIDs resolves the release's own name, never the requested title's
// ID. A first-ranked search hit alone is not enough: every result/page counts
// toward ambiguity. Existing catalogue aliases are checked before this lookup.
func (a *App) releaseTitleIDs(ctx context.Context, kind core.MediaType, title string) (core.IDs, error) {
	if a.tmdb == nil {
		return core.IDs{}, errors.New("metadata unavailable")
	}
	if strings.TrimSpace(title) == "" {
		return core.IDs{}, nil
	}
	switch kind {
	case core.TypeMovie:
		found, err := a.tmdb.SearchMovies(ctx, title, 1)
		if err != nil {
			return core.IDs{}, err
		}
		if found.TotalResults != 1 || found.TotalPages != 1 || len(found.Results) != 1 || found.Results[0].ID <= 0 {
			return core.IDs{}, nil
		}
		id := found.Results[0].ID
		detail, err := a.tmdb.MovieDetails(ctx, id)
		if err != nil {
			return core.IDs{}, err
		}
		if detail.ID != id {
			return core.IDs{}, errors.New("metadata detail identifier mismatch")
		}
		ids := core.IDs{TMDB: id, IMDB: detail.IMDBID}
		if detail.ExternalIDs != nil {
			if ids.IMDB == "" {
				ids.IMDB = detail.ExternalIDs.IMDBID
			}
			ids.TVDB = detail.ExternalIDs.TVDBID
		}
		return ids, nil
	case core.TypeShow:
		found, err := a.tmdb.SearchTV(ctx, title, 1)
		if err != nil {
			return core.IDs{}, err
		}
		if found.TotalResults != 1 || found.TotalPages != 1 || len(found.Results) != 1 || found.Results[0].ID <= 0 {
			return core.IDs{}, nil
		}
		id := found.Results[0].ID
		detail, err := a.tmdb.TVDetails(ctx, id)
		if err != nil {
			return core.IDs{}, err
		}
		if detail.ID != id {
			return core.IDs{}, errors.New("metadata detail identifier mismatch")
		}
		ids := core.IDs{TMDB: id}
		if detail.ExternalIDs != nil {
			ids.IMDB, ids.TVDB = detail.ExternalIDs.IMDBID, detail.ExternalIDs.TVDBID
		}
		return ids, nil
	default:
		return core.IDs{}, nil
	}
}

// verifyTitleIDs verifies whether an indexer's conflicting reported IDs
// actually map to expected on TMDB (updated external IDs, alternate/episode
// TVDB or IMDb IDs), or name only the opposite media type (e.g. a movie ID
// mis-tagged on a TV episode). Any conflict that maps to another title of the
// same media type or cannot be verified returns TitleIDConflict.
func (a *App) verifyTitleIDs(ctx context.Context, kind core.MediaType, reported, expected core.IDs) (resolver.TitleIDVerdict, error) {
	if a.tmdb == nil {
		return resolver.TitleIDConflict, errors.New("metadata unavailable")
	}
	if expected.TMDB <= 0 {
		return resolver.TitleIDConflict, nil
	}
	// First refresh TMDB's live external IDs for the expected title so stale
	// catalogue TVDB/IMDb mappings do not reject valid releases.
	liveExpected := expected
	switch kind {
	case core.TypeShow:
		detail, err := a.tmdb.TVDetails(ctx, expected.TMDB)
		if err != nil {
			if !errors.Is(err, core.ErrNotFound) {
				return resolver.TitleIDConflict, err
			}
		} else if detail != nil && detail.ID == expected.TMDB && detail.ExternalIDs != nil {
			if detail.ExternalIDs.IMDBID != "" {
				liveExpected.IMDB = detail.ExternalIDs.IMDBID
			}
			if detail.ExternalIDs.TVDBID > 0 {
				liveExpected.TVDB = detail.ExternalIDs.TVDBID
			}
		}
	case core.TypeMovie:
		detail, err := a.tmdb.MovieDetails(ctx, expected.TMDB)
		if err != nil {
			if !errors.Is(err, core.ErrNotFound) {
				return resolver.TitleIDConflict, err
			}
		} else if detail != nil && detail.ID == expected.TMDB {
			if detail.IMDBID != "" {
				liveExpected.IMDB = detail.IMDBID
			} else if detail.ExternalIDs != nil && detail.ExternalIDs.IMDBID != "" {
				liveExpected.IMDB = detail.ExternalIDs.IMDBID
			}
			if detail.ExternalIDs != nil && detail.ExternalIDs.TVDBID > 0 {
				liveExpected.TVDB = detail.ExternalIDs.TVDBID
			}
		}
	default:
		return resolver.TitleIDConflict, nil
	}
	if m, c := releaseparse.CompareTitleIDs(reported, liveExpected); m && !c {
		return resolver.TitleIDVerified, nil
	}

	var verifiedAny, wrongMediaAny, unverifiedConflict bool

	// Check conflicting TMDB ID.
	if reported.TMDB > 0 && reported.TMDB != expected.TMDB {
		if kind != core.TypeShow {
			return resolver.TitleIDConflict, nil
		}
		tv, tvErr := a.tmdb.TVDetails(ctx, reported.TMDB)
		if tvErr == nil && tv != nil && tv.ID == reported.TMDB {
			return resolver.TitleIDConflict, nil
		}
		if tvErr != nil && !errors.Is(tvErr, core.ErrNotFound) {
			return resolver.TitleIDConflict, tvErr
		}
		mv, mvErr := a.tmdb.MovieDetails(ctx, reported.TMDB)
		if mvErr == nil && mv != nil && mv.ID == reported.TMDB {
			wrongMediaAny = true
		} else if mvErr != nil && !errors.Is(mvErr, core.ErrNotFound) {
			return resolver.TitleIDConflict, mvErr
		} else {
			unverifiedConflict = true
		}
	}

	classifyFind := func(found *tmdb.FindResult) (verified, wrongMedia, conflict bool) {
		if found == nil {
			return false, false, false
		}
		if kind == core.TypeShow {
			for _, tv := range found.TVResults {
				if tv.ID == expected.TMDB {
					return true, false, false
				}
			}
			for _, ep := range found.TVEpisodeResults {
				if ep.ShowID == expected.TMDB {
					return true, false, false
				}
			}
			if len(found.TVResults) > 0 || len(found.TVEpisodeResults) > 0 {
				return false, false, true
			}
			if len(found.MovieResults) > 0 {
				return false, true, false
			}
			return false, false, false
		}
		for _, mv := range found.MovieResults {
			if mv.ID == expected.TMDB {
				return true, false, false
			}
		}
		if len(found.MovieResults) > 0 {
			return false, false, true
		}
		return false, false, false
	}

	// Check conflicting IMDb ID via TMDB /find.
	if imdb := canonicalIMDB(reported.IMDB); imdb != "" && !strings.EqualFold(imdb, canonicalIMDB(liveExpected.IMDB)) {
		found, err := a.tmdb.FindByExternalID(ctx, "imdb_id", imdb)
		if err != nil {
			return resolver.TitleIDConflict, err
		}
		v, wm, c := classifyFind(found)
		switch {
		case c:
			return resolver.TitleIDConflict, nil
		case v:
			verifiedAny = true
		case wm:
			wrongMediaAny = true
		default:
			unverifiedConflict = true
		}
	}

	// Check conflicting TVDB ID via TMDB /find.
	if reported.TVDB > 0 && reported.TVDB != liveExpected.TVDB {
		found, err := a.tmdb.FindByExternalID(ctx, "tvdb_id", strconv.FormatInt(reported.TVDB, 10))
		if err != nil {
			return resolver.TitleIDConflict, err
		}
		v, wm, c := classifyFind(found)
		switch {
		case c:
			return resolver.TitleIDConflict, nil
		case v:
			verifiedAny = true
		case wm:
			wrongMediaAny = true
		default:
			unverifiedConflict = true
		}
	}

	if unverifiedConflict {
		return resolver.TitleIDConflict, nil
	}
	if wrongMediaAny {
		return resolver.TitleIDWrongMedia, nil
	}
	if verifiedAny {
		return resolver.TitleIDVerified, nil
	}
	return resolver.TitleIDConflict, nil
}

func canonicalIMDB(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "tt") {
		digits := s[2:]
		if digits == "" {
			return ""
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return s
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return "tt" + s
}

package releaseparse

import "slices"
import "github.com/braedonsaunders/ether/internal/core"

// AssociationReason validates the identity of a source answer before indexing.
// Query identifiers (including identifiers echoed by addons) are not evidence.
// An empty result means corroborated identity, not playback availability.
func AssociationReason(req core.SearchRequest, r core.Release) string {
	if IsPlaceholderResult(r.Name) {
		return "placeholder"
	}
	info := Parse(r.Name)
	same, conflict := CompareTitleIDs(r.ReportedTitleIDs(), req.IDs)
	if conflict {
		return "identifier_conflict"
	}
	if req.Type != core.TypeMovie && req.Type != core.TypeEpisode && req.Type != core.TypeShow {
		return "unknown_media"
	}
	if req.Type == core.TypeMovie {
		if len(info.Episodes) > 0 || info.FullSeason || r.Category/1000 == 5 {
			return "media_type_conflict"
		}
	} else if r.Category/1000 == 2 {
		return "media_type_conflict"
	}
	names := append([]string{req.Title, req.OriginalTitle}, req.AlternativeTitles...)
	titleOK := false
	for _, name := range names {
		if name != "" && TitleMatches(info, name) {
			titleOK = true
			break
		}
	}
	if req.Type == core.TypeMovie {
		titleOK = titleOK || MovieTitleMatches(info, names)
		// Bilingual filenames may contain both complete catalogue titles.
		if req.Title != "" && req.OriginalTitle != "" && !TitlesMatch(req.Title, req.OriginalTitle) {
			titleOK = titleOK || TitleMatches(info, req.Title+" "+req.OriginalTitle) || TitleMatches(info, req.OriginalTitle+" "+req.Title)
		}
	}
	if !same && !titleOK {
		return "unconfirmed_title"
	}
	if req.Type == core.TypeMovie {
		if !YearMatches(info.Year, req.Year) {
			return "year_conflict"
		}
		if !same && (info.Year == 0 || req.Year == 0) {
			return "unconfirmed_year"
		}
	} else {
		if !SeriesYearMatches(EpisodeSeriesYear(r.Name, info.Year), req.Year) {
			return "year_conflict"
		}
		pack := info.FullSeason && slices.Contains(info.Seasons, req.Season) && (len(info.Episodes) == 0 || slices.Contains(info.Episodes, req.Episode))
		if req.Type == core.TypeEpisode && !pack && !EpisodeMatchesNumbering(info, req.Season, req.Episode, AnimeSearchEpisode(req)) {
			return "episode_conflict"
		}
	}
	return ""
}

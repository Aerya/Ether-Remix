package releaseparse

import (
	"regexp"
	"strings"
)
import "github.com/braedonsaunders/ether/internal/core"

var associationYearRange = regexp.MustCompile(`(?:19|20)\d{2}[ ._-]*[-–][ ._-]*(?:19|20)\d{2}`)

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
		if associationYearRange.MatchString(r.Name) {
			return "ambiguous_collection"
		}
		if len(info.Episodes) > 0 || info.FullSeason || r.Category/1000 == 5 {
			return "media_type_conflict"
		}
	} else if r.Category/1000 == 2 {
		return "media_type_conflict"
	}
	names := append([]string{req.Title, req.OriginalTitle}, req.AlternativeTitles...)
	titleOK := false
	for _, name := range names {
		if name != "" && associationTitleMatches(info.Title, name) {
			titleOK = true
			break
		}
	}
	if req.Type == core.TypeMovie {
		// No fuzzy subtitle, region or pack fallback for persistent associations.
		// Bilingual filenames may contain both complete catalogue titles.
		if req.Title != "" && req.OriginalTitle != "" && !TitlesMatch(req.Title, req.OriginalTitle) {
			titleOK = titleOK || associationTitleMatches(info.Title, req.Title+" "+req.OriginalTitle) || associationTitleMatches(info.Title, req.OriginalTitle+" "+req.Title)
		}
	}
	if !titleOK {
		return "unconfirmed_title"
	}
	if req.Type == core.TypeMovie {
		if info.Year != 0 && req.Year != 0 && info.Year != req.Year {
			return "year_conflict"
		}
		if info.Year == 0 || req.Year == 0 {
			return "unconfirmed_year"
		}
	} else {
		if !SeriesYearMatches(EpisodeSeriesYear(r.Name, info.Year), req.Year) {
			return "year_conflict"
		}
		// Yearless episode filenames need a successful local namesake check.
		// A catalogue year alone is not evidence about the source answer.
		if !same && (req.Year == 0 || (EpisodeSeriesYear(r.Name, info.Year) == 0 && (req.Type != core.TypeEpisode || !req.SeriesIdentityChecked))) {
			return "unconfirmed_series_year"
		}
		if req.Type == core.TypeEpisode && (info.FullSeason || len(info.Episodes) != 1 || !EpisodeMatchesNumbering(info, req.Season, req.Episode, AnimeSearchEpisode(req))) {
			return "episode_conflict"
		}
	}
	return ""
}

// Keep all title words: a matching ID cannot authorize a different subtitle,
// territory, sequel or collection. Punctuation and spacing are not identity.
func associationTitleMatches(a, b string) bool {
	a = strings.ReplaceAll(NormalizeTitle(a), " ", "")
	b = strings.ReplaceAll(NormalizeTitle(b), " ", "")
	return a != "" && a == b
}

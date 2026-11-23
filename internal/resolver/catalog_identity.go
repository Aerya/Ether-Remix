package resolver

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// AnimeReleaseStyleIdentityReason is recorded when anime-specific release
// packaging disambiguates an animated series from non-animated namesakes.
const AnimeReleaseStyleIdentityReason = "anime release style identifies the animated series"

type localIdentity struct{ core.EpisodeIdentity }

// Resolve only names actually used by this result set. The catalogue callback
// must be local and indexed; missing data preserves the existing identity gates.
func (s *Service) localEpisodeIdentity(ctx context.Context, t *core.Title, req core.SearchRequest, results []core.Release) localIdentity {
	if s.episodeIdentity == nil || t == nil || t.ShowKey == 0 || req.Type != core.TypeEpisode {
		return localIdentity{}
	}
	names := []string{req.Title}
	if t.ShowOriginalTitle != "" && t.ShowOriginalTitle != req.Title {
		names = append(names, t.ShowOriginalTitle)
	}
	if eng := cmp.Or(req.EnglishTitle, t.ShowEnglishTitle); eng != "" && eng != req.Title && eng != t.ShowOriginalTitle {
		names = append(names, eng)
	}
	for _, rel := range results {
		if len(names) >= 8 {
			break
		}
		if !matchesAlternativeTitle(rel.Info, req.AlternativeTitles) && !matchesAlternativeTitle(rel.Info, t.ShowAlternativeTitles) {
			continue
		}
		duplicate := false
		for _, name := range names {
			if releaseparse.TitlesMatch(rel.Info.Title, name) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			names = append(names, rel.Info.Title)
		}
	}
	facts, err := s.episodeIdentity(ctx, t.ShowKey, req.Season, req.Episode, names)
	if err != nil {
		s.log.Debug("local episode identity unavailable", "show", t.ShowKey, "err", err)
		return localIdentity{}
	}
	return localIdentity{facts}
}

func (f localIdentity) shared(req core.SearchRequest) bool {
	return len(f.OtherShows[req.Title]) > 0 ||
		(req.OriginalTitle != "" && len(f.OtherShows[req.OriginalTitle]) > 0) ||
		(req.EnglishTitle != "" && len(f.OtherShows[req.EnglishTitle]) > 0)
}

func isAnimationTag(tag string) bool {
	return strings.EqualFold(strings.TrimSpace(tag), "animation") || strings.EqualFold(strings.TrimSpace(tag), "anime")
}

func hasAnimationTag(genres, keywords []string) bool {
	return slices.ContainsFunc(genres, isAnimationTag) || slices.ContainsFunc(keywords, isAnimationTag)
}

func (f localIdentity) targetIsAnimation(t *core.Title) bool {
	if hasAnimationTag(f.Genres, f.Keywords) {
		return true
	}
	if t == nil {
		return false
	}
	return hasAnimationTag(t.Genres, t.Keywords)
}

func (f localIdentity) targetKnownNonAnimation(t *core.Title) bool {
	if f.targetIsAnimation(t) {
		return false
	}
	return len(f.Genres) > 0 || (t != nil && len(t.Genres) > 0)
}

func namesakeAnimationState(other core.EpisodeNamesake) (animated, known bool) {
	if hasAnimationTag(other.Genres, other.Keywords) {
		return true, true
	}
	if len(other.Genres) > 0 {
		return false, true
	}
	return false, false
}

func (f localIdentity) matchingNamesakes(req core.SearchRequest) []core.EpisodeNamesake {
	var out []core.EpisodeNamesake
	for name, others := range f.OtherShows {
		if releaseparse.TitlesMatch(name, req.Title) ||
			(req.OriginalTitle != "" && releaseparse.TitlesMatch(name, req.OriginalTitle)) ||
			(req.EnglishTitle != "" && releaseparse.TitlesMatch(name, req.EnglishTitle)) {
			out = append(out, others...)
		}
	}
	return out
}

// animeIdentifiesTarget reports whether the requested series is animation and
// every locally known namesake for this title is positively known not to be
// animation. Missing genre metadata is treated as unknown, never evidence.
func (f localIdentity) animeIdentifiesTarget(t *core.Title, req core.SearchRequest) bool {
	if !f.targetIsAnimation(t) {
		return false
	}
	others := f.matchingNamesakes(req)
	if len(others) == 0 {
		return false
	}
	for _, other := range others {
		animated, known := namesakeAnimationState(other)
		if !known || animated {
			return false
		}
	}
	return true
}

// animeIdentifiesNamesake is the reverse medium check: the requested series is
// known not to be animation while at least one namesake is animation.
func (f localIdentity) animeIdentifiesNamesake(t *core.Title, req core.SearchRequest) bool {
	if !f.targetKnownNonAnimation(t) {
		return false
	}
	for _, other := range f.matchingNamesakes(req) {
		if animated, known := namesakeAnimationState(other); known && animated {
			return true
		}
	}
	return false
}

func (f localIdentity) titleShared(rel core.Release) bool {
	for name, others := range f.OtherShows {
		if len(others) > 0 && releaseparse.TitleMatches(rel.Info, name) {
			return true
		}
	}
	return false
}

// evidence returns a non-empty reason when rel positively identifies the
// requested episode's series without contradicting its IDs, year, or episode.
func (f localIdentity) evidence(rel core.Release, t *core.Title, req core.SearchRequest) string {
	if req.Type != core.TypeEpisode {
		return ""
	}
	if f.reject(rel, t, req) != "" {
		return ""
	}
	matched, conflict := releaseparse.CompareTitleIDs(rel.ReportedTitleIDs(), req.IDs)
	if conflict {
		return ""
	}
	year := releaseparse.EpisodeSeriesYear(rel.Name, rel.Info.Year)
	if year > 0 && req.Year > 0 && !releaseparse.SeriesYearMatches(year, req.Year) {
		return ""
	}
	if matched || (year > 0 && req.Year > 0 && releaseparse.SeriesYearMatches(year, req.Year)) || releaseparse.EpisodeTitleEvidence(rel.Name, req.EpisodeTitle) || releaseparse.EpisodeMatchesAirDate(rel.Info, req.Released) {
		return "corroborated series identity"
	}
	// When the show's primary/English title collides with a catalogue namesake
	// (e.g. "Home Alone" 2013 vs 2017), a release named by a non-colliding
	// native or alternative title ("나 혼자 산다", "I Live Alone") disambiguates it.
	if f.shared(req) && !f.titleShared(rel) && matchesDistinctNonPrimaryTitle(rel.Info, t, req) {
		return "distinctive series title"
	}
	if f.animeIdentifiesTarget(t, req) && releaseparse.AnimeReleaseStyle(rel.Name, rel.Info) {
		return AnimeReleaseStyleIdentityReason
	}
	return ""
}

func neededIdentityEvidence(req core.SearchRequest, allowAnimeStyle, allowEpisodeTitle bool) string {
	var parts []string
	if req.Year > 0 {
		parts = append(parts, fmt.Sprintf("series year %d", req.Year))
	} else {
		parts = append(parts, "series year")
	}
	parts = append(parts, "matching title ID")
	if allowEpisodeTitle && strings.TrimSpace(req.EpisodeTitle) != "" {
		parts = append(parts, "episode title")
	}
	if allowAnimeStyle {
		parts = append(parts, "anime release style")
	}
	if len(parts) == 2 {
		return parts[0] + " or " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", or " + parts[len(parts)-1]
}

func identityAmbiguousReason(detail string, req core.SearchRequest, allowAnimeStyle, allowEpisodeTitle bool) string {
	return "identity ambiguous: " + detail + "; needs " + neededIdentityEvidence(req, allowAnimeStyle, allowEpisodeTitle)
}

// Phrase matching keeps word boundaries and ignores release punctuation. Short
// or generic episode names cannot establish a competing show's identity.
func identityWords(s string) string {
	s = strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(s))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}
func identityPhrase(name, title string) bool {
	phrase := identityWords(title)
	return phrase != "" && strings.Contains(" "+identityWords(name)+" ", " "+phrase+" ")
}
func distinctiveEpisode(name, title string) bool {
	words := identityWords(title)
	if len([]rune(words)) < 5 || strings.HasPrefix(words, "episode ") || strings.HasPrefix(words, "chapter ") {
		return false
	}
	return identityPhrase(name, title)
}

var identityYear = regexp.MustCompile(`\b(?:19|20)[0-9]{2}\b`)
var genericSeasonName = regexp.MustCompile(`(?i)^(season|series|part|book|volume|vol|specials?|extras?)(\s+[0-9ivxlcdm]+)?$`)

func (f localIdentity) reject(rel core.Release, t *core.Title, req core.SearchRequest) string {
	if req.Type != core.TypeEpisode {
		return ""
	}
	if reason := f.otherSeason(rel, t, req); reason != "" {
		return reason
	}
	if reason := f.otherEpisode(rel, t, req); reason != "" {
		return reason
	}
	idMatch, _ := releaseparse.CompareTitleIDs(rel.ReportedTitleIDs(), req.IDs)
	year := releaseparse.EpisodeSeriesYear(rel.Name, rel.Info.Year)
	primary := releaseparse.TitleMatches(rel.Info, req.Title) ||
		(t != nil && t.ShowOriginalTitle != "" && releaseparse.TitleMatches(rel.Info, t.ShowOriginalTitle)) ||
		(req.EnglishTitle != "" && releaseparse.TitleMatches(rel.Info, req.EnglishTitle)) ||
		(t != nil && t.ShowEnglishTitle != "" && releaseparse.TitleMatches(rel.Info, t.ShowEnglishTitle))
	for name, others := range f.OtherShows {
		if !releaseparse.TitleMatches(rel.Info, name) {
			continue
		}
		if !primary && year == 0 && !idMatch && !releaseparse.EpisodeMatchesAirDate(rel.Info, req.Released) && len(others) > 0 {
			return identityAmbiguousReason("alternative title names another show", req, false, false)
		}
		for _, other := range others {
			if other.Year > 0 && !releaseparse.SeriesYearMatches(other.Year, req.Year) {
				for _, raw := range identityYear.FindAllString(rel.Name, -1) {
					y, _ := strconv.Atoi(raw)
					// An episode's air year is not necessarily the show's premiere year.
					if y == other.Year && (req.Released.IsZero() || (y < req.Released.Year()-1 || y > req.Released.Year()+1)) {
						return "release names another show's premiere year"
					}
				}
			}
			otherEpEvidence := releaseparse.EpisodeTitleEvidence(rel.Name, other.EpisodeTitle) ||
				(len(rel.Info.AbsoluteEpisodes) > 0 && !identityPhrase(name, other.EpisodeTitle))
			if distinctiveEpisode(rel.Name, other.EpisodeTitle) && otherEpEvidence && !distinctiveEpisode(rel.Name, req.EpisodeTitle) && identityWords(other.EpisodeTitle) != identityWords(req.EpisodeTitle) {
				return "release names another show's episode"
			}
		}
	}
	if primary && f.animeIdentifiesNamesake(t, req) && releaseparse.AnimeReleaseStyle(rel.Name, rel.Info) && !episodeIdentityEvidence(rel, req) {
		return identityAmbiguousReason("anime release style identifies the animated namesake", req, false, true)
	}
	return ""
}

// otherEpisode rejects a release that names a different episode of the
// requested season and not the requested one. An episode the release itself
// covers (one of a combined "S01E01-E02" file's episodes) is expected in
// the name and never counts against it. Shows numbered one way on TMDB and
// another by release groups (segments vs broadcast episodes) carry the same
// SxxEyy on different content; the episode title tells them apart.
func (f localIdentity) otherEpisode(rel core.Release, t *core.Title, req core.SearchRequest) string {
	if req.Season <= 0 || rel.Info.FullSeason || len(f.Episodes) == 0 || distinctiveEpisode(rel.Name, req.EpisodeTitle) {
		return ""
	}
	own := identityWords(req.EpisodeTitle)
	var showOrig, showEng string
	if t != nil {
		showOrig = t.ShowOriginalTitle
		showEng = t.ShowEnglishTitle
	}
	shows := []string{req.Title, showOrig, cmp.Or(req.EnglishTitle, showEng), rel.Info.Title}
	for _, n := range slices.Sorted(maps.Keys(f.Episodes)) {
		title := f.Episodes[n]
		words := identityWords(title)
		if n == req.Episode || slices.Contains(rel.Info.Episodes, n) || words == "" || words == own || len(strings.Fields(words)) < 2 || (own != "" && identityPhrase(req.EpisodeTitle, title)) {
			continue
		}
		inShowTitle := false
		for _, show := range shows {
			if show != "" && identityPhrase(show, title) {
				inShowTitle = true
				break
			}
		}
		if !inShowTitle && distinctiveEpisode(rel.Name, title) {
			return "release names episode " + strconv.Itoa(n) + " of this season"
		}
	}
	return ""
}

func (f localIdentity) otherSeason(rel core.Release, t *core.Title, req core.SearchRequest) string {
	if req.Season <= 0 {
		return ""
	}
	var showOrig, showEng string
	var showAlts []string
	if t != nil {
		showOrig = t.ShowOriginalTitle
		showEng = t.ShowEnglishTitle
		showAlts = t.ShowAlternativeTitles
	}
	meaningful := func(name string) bool {
		norm := identityWords(name)
		if len(strings.Fields(norm)) < 2 || genericSeasonName.MatchString(norm) {
			return false
		}
		for _, show := range append([]string{req.Title, showOrig, cmp.Or(req.EnglishTitle, showEng)}, showAlts...) {
			if show != "" && releaseparse.NormalizeTitle(name) == releaseparse.NormalizeTitle(show) {
				return false
			}
		}
		return true
	}
	own := f.Seasons[req.Season]
	for season, name := range f.Seasons {
		if season <= 0 || season == req.Season || !meaningful(name) {
			continue
		}
		// A longer own-season name can contain a shorter sibling name.
		if meaningful(own) && identityPhrase(own, name) && identityPhrase(rel.Info.Title, own) {
			continue
		}
		if identityPhrase(rel.Info.Title, name) {
			return "release names another season"
		}
	}
	return ""
}

func (f localIdentity) ambiguous(rel core.Release, t *core.Title, req core.SearchRequest) bool {
	if !f.titleShared(rel) {
		return false
	}
	return f.evidence(rel, t, req) == ""
}

func clearIdentityVariants(cands []candidate) []candidate {
	clear := false
	for _, c := range cands {
		if !c.identityWeak {
			clear = true
			break
		}
	}
	if !clear {
		return cands
	}
	out := make([]candidate, 0, len(cands))
	for _, c := range cands {
		if !c.identityWeak {
			out = append(out, c)
		}
	}
	return out
}

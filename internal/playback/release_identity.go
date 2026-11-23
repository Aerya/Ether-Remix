package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

const debridPayloadPrefix = "ETHER-DEBRID/1\n"

// payloadMatchesTitle verifies that a cached or resumed debrid payload's
// chosen file path matches the requested episode.
func payloadMatchesTitle(nzb []byte, title *core.Title) bool {
	if title == nil || title.Type != core.TypeEpisode || !bytes.HasPrefix(nzb, []byte(debridPayloadPrefix)) {
		return true
	}
	var p struct {
		Path   string              `json:"path"`
		Target *core.ReleaseTarget `json:"target"`
	}
	if err := json.Unmarshal(nzb[len(debridPayloadPrefix):], &p); err != nil || p.Path == "" {
		return true
	}
	season, episode := title.Season, title.Episode
	if title.Key.Kind() == core.KindEpisode {
		if season == 0 {
			season = title.Key.Season()
		}
		if episode == 0 {
			episode = title.Key.Episode()
		}
	}
	if episode <= 0 {
		return true
	}
	origSeason, origEpisode := season, episode
	absolute := playbackAbsoluteEpisode(title, season, episode)
	var valid bool
	season, episode, valid = targetEpisodeNumbers(p.Target, season, episode)
	if !valid {
		return false
	}
	if season == 0 {
		if p.Target == nil || p.Target.Season != 0 || p.Target.Episode != episode {
			return false
		}
		if !releaseparse.SpecialMatches(p.Path, title.Title, p.Target.SpecialTitles, p.Target.ShowTitles) {
			return false
		}
		actual := episode
		if p.Target.AddonSpecialEpisode > 0 {
			actual = p.Target.AddonSpecialEpisode
		}
		info := releaseparse.Parse(path.Base(strings.ReplaceAll(p.Path, "\\", "/")))
		return releaseparse.EpisodeMatches(info, 0, actual) || (len(info.Seasons) == 0 && len(info.Episodes) == 0 && len(info.AbsoluteEpisodes) == 0 && releaseparse.SpecialTitleEvidence(path.Base(strings.ReplaceAll(p.Path, "\\", "/")), title.Title, p.Target.ShowTitles...))
	}
	normPath := strings.ReplaceAll(p.Path, "\\", "/")
	baseInfo := releaseparse.Parse(path.Base(normPath))
	fullInfo := releaseparse.Parse(strings.ReplaceAll(normPath, "/", " "))
	if len(baseInfo.Seasons) == 0 && len(fullInfo.Seasons) > 0 {
		baseInfo.Seasons = fullInfo.Seasons
	}
	matchesEpisode := func(info core.ReleaseInfo) bool {
		if releaseparse.AbsoluteConflict(info, absolute) {
			return false
		}
		if releaseparse.EpisodeMatchesNumbering(info, season, episode, absolute) {
			return true
		}
		if p.Target != nil && p.Target.CanonicalSeason == origSeason && p.Target.CanonicalEpisode == origEpisode {
			if releaseparse.EpisodeMatches(info, origSeason, origEpisode) {
				return true
			}
			for _, n := range p.Target.Aliases {
				if releaseparse.EpisodeMatches(info, n.Season, n.Episode) {
					return true
				}
			}
		}
		if season > 0 && !info.FullSeason && releaseparse.EpisodeMatchesAirDate(info, title.Released) {
			if len(info.Episodes) == 0 && len(info.AbsoluteEpisodes) == 0 {
				return true
			}
			if season > 1 && absolute <= 0 && len(info.Seasons) == 0 && len(info.AbsoluteEpisodes) == 0 && len(info.Episodes) == 1 && info.Episodes[0] > episode {
				return true
			}
		}
		return false
	}
	if len(baseInfo.Episodes) > 0 || len(baseInfo.AbsoluteEpisodes) > 0 || baseInfo.AirDate != "" {
		return matchesEpisode(baseInfo)
	}
	if len(fullInfo.Episodes) > 0 || len(fullInfo.AbsoluteEpisodes) > 0 || fullInfo.AirDate != "" {
		return matchesEpisode(fullInfo)
	}
	return true
}

// releaseMatchesTitle applies the same episode identity constraints to fresh,
// warmed, remembered and failover choices. Unknown filename metadata is not
// proof of identity; it only permits the resolver/file selector to continue.
func releaseMatchesTitle(rel core.Release, title *core.Title) bool {
	if title == nil {
		return true
	}
	expected := title.IDs
	if title.Type == core.TypeEpisode {
		expected = title.ShowIDs
		if expected.TMDB == 0 && title.Key.Kind() == core.KindEpisode {
			expected.TMDB = title.Key.TMDB()
		}
	}
	idMatch, idConflict := releaseparse.CompareTitleIDs(rel.ReportedTitleIDs(), expected)
	if idConflict {
		return false
	}
	if title.Type != core.TypeEpisode {
		return true
	}
	season, episode := title.Season, title.Episode
	if title.Key.Kind() == core.KindEpisode {
		if season == 0 {
			season = title.Key.Season()
		}
		if episode == 0 {
			episode = title.Key.Episode()
		}
	}
	origSeason, origEpisode := season, episode
	absolute := playbackAbsoluteEpisode(title, season, episode)
	var valid bool
	season, episode, valid = targetEpisodeNumbers(rel.Target, season, episode)
	if !valid {
		return false
	}
	// Zero season denotes a special only when an episode number is known.
	// Partially hydrated titles still carry usable series/year identity.
	if season == 0 && episode > 0 {
		shows := append([]string{title.ShowTitle, title.ShowOriginalTitle}, title.ShowAlternativeTitles...)
		var siblings []string
		if rel.Target != nil {
			siblings = rel.Target.SpecialTitles
		}
		if !releaseparse.SpecialMatches(rel.Name, title.Title, siblings, shows) {
			return false
		}
	}
	info := rel.Info
	if len(info.Episodes) == 0 && !info.FullSeason && info.AirDate == "" {
		info = releaseparse.Parse(rel.Name)
	}
	if episode > 0 && (len(info.Episodes) > 0 || len(info.AbsoluteEpisodes) > 0 || info.FullSeason || info.AirDate != "") {
		if info.FullSeason {
			if len(info.Seasons) > 0 {
				found := false
				for _, n := range info.Seasons {
					found = found || n == season
				}
				if !found && rel.Target != nil && rel.Target.CanonicalSeason == origSeason && rel.Target.CanonicalEpisode == origEpisode {
					for _, alias := range rel.Target.Aliases {
						for _, n := range info.Seasons {
							found = found || n == alias.Season
						}
					}
				}
				if !found {
					return false
				}
			}
		} else {
			matched := releaseparse.EpisodeMatchesNumbering(info, season, episode, absolute)
			if !matched && !releaseparse.AbsoluteConflict(info, absolute) && rel.Target != nil && rel.Target.CanonicalSeason == origSeason && rel.Target.CanonicalEpisode == origEpisode {
				for _, alias := range rel.Target.Aliases {
					if releaseparse.EpisodeMatches(info, alias.Season, alias.Episode) {
						matched = true
						break
					}
				}
			}
			if !matched && season > 0 && !releaseparse.AbsoluteConflict(info, absolute) && releaseparse.EpisodeMatchesAirDate(info, title.Released) {
				if len(info.Episodes) == 0 && len(info.AbsoluteEpisodes) == 0 {
					matched = true
				} else if season > 1 && absolute <= 0 && len(info.Seasons) == 0 && len(info.AbsoluteEpisodes) == 0 && len(info.Episodes) == 1 && info.Episodes[0] > episode {
					matched = true
				}
			}
			if !matched {
				return false
			}
		}
	}
	if rel.Target != nil && episode > 0 &&
		(rel.Target.Episode != episode || rel.Target.Season != season) {
		return false
	}
	year := releaseparse.EpisodeSeriesYear(rel.Name, rel.Info.Year)
	if !releaseparse.SeriesYearMatches(year, title.ShowYear) ||
		((season != 0 || episode <= 0) && year == 0 && !idMatch && !releaseparse.EpisodeTitleMatches(rel.Name, title.Title)) {
		return false
	}
	// A torrent descriptor embeds the expected identity for file selection.
	// Older cached descriptors must be resolved again to acquire that target.
	return rel.Protocol != core.ProtocolTorrent || title.ShowYear <= 0 ||
		(rel.Target != nil && rel.Target.ShowYear == title.ShowYear && rel.Target.EpisodeTitle == title.Title)
}

// rememberedReleaseMatchesTitle delegates to current resolver identity rules
// when supported, including its owner-pin exemption.
func (m *Manager) rememberedReleaseMatchesTitle(ctx context.Context, rel core.Release, title *core.Title) bool {
	if checker, ok := m.deps.Resolver.(interface {
		RememberedReleaseAllowed(context.Context, *core.Title, core.Release) bool
	}); ok {
		return checker.RememberedReleaseAllowed(ctx, title, rel)
	}
	return releaseMatchesTitle(rel, title)
}

// Use the same known absolute number as the resolver, including season one's
// natural count. Unknown later-season counts provide no fallback evidence.
func playbackAbsoluteEpisode(title *core.Title, season, episode int) int {
	if title.AbsoluteEpisode > 0 {
		return title.AbsoluteEpisode
	}
	if season == 1 {
		return episode
	}
	return 0
}

// An owner pin is checked at use time so warm entries, persisted resume picks
// and old alternates cannot bypass a pin installed after they were created.
func (m *Manager) ownerPinAllows(s *session, releaseID string) bool {
	if pins, ok := m.deps.Resolver.(interface {
		PinnedReleaseID(core.ResolveRequest) string
	}); ok {
		id := pins.PinnedReleaseID(core.ResolveRequest{Title: s.title, Client: s.caps, User: &s.user, Prefer: s.prefer})
		return id == "" || id == releaseID
	}
	return true
}

// Alternate file numbers are valid only for the canonical episode the resolver
// explicitly attached them to. Plain targets retain the existing checks.
func targetEpisodeNumbers(target *core.ReleaseTarget, season, episode int) (int, int, bool) {
	if target == nil || target.CanonicalEpisode <= 0 {
		return season, episode, true
	}
	if target.CanonicalSeason != season || target.CanonicalEpisode != episode || target.Episode <= 0 {
		return season, episode, false
	}
	return target.Season, target.Episode, true
}

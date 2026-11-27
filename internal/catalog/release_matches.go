package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

const releaseMatchCols = `SELECT t.key,t.type,t.tmdb_id,t.imdb,t.tvdb,t.title,t.original_title,t.english_title,t.year,t.adult FROM catalog_titles t `

// ReleaseMatches joins a feed page to local metadata through bounded indexed lookups. It
// never spends a metadata API call per feed item. Ambiguous names are ignored;
// explicit identifier conflicts always defeat a textual match. Season packs are
// expanded only to locally known episodes, never to invented episode identities.
func (c *Catalog) ReleaseMatches(ctx context.Context, releases []core.Release) (map[string][]core.Title, error) {
	byName := map[string][]int{}
	byID := map[string][]int{}
	for i, r := range releases {
		n := releaseparse.NormalizeTitle(r.Info.Title)
		if n != "" {
			byName[n] = append(byName[n], i)
		}
		for _, id := range feedIDs(r.TitleIDs) {
			byID[id] = append(byID[id], i)
		}
	}
	// Each key lookup is capped. If an unusually common key overflows,
	// refuse to guess for every affected release rather than scan the library
	// or choose a candidate from an incomplete set.
	const candidateLimit = 256
	matches := map[int][]core.RatingKey{}
	overflow := map[int]bool{}
	seen := map[int]map[core.RatingKey]bool{}
	lookup := func(query string, value any, indices []int) error {
		rows, err := c.db.QueryContext(ctx, query, value, candidateLimit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var titles []core.Title
		for rows.Next() {
			var t core.Title
			var adult bool
			if err := rows.Scan(&t.Key, &t.Type, &t.IDs.TMDB, &t.IDs.IMDB, &t.IDs.TVDB, &t.Title, &t.OriginalTitle, &t.EnglishTitle, &t.Year, &adult); err != nil {
				return err
			}
			if adult {
				t.Type = ""
			}
			titles = append(titles, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(titles) > candidateLimit {
			for _, i := range indices {
				overflow[i] = true
			}
			return nil
		}
		for _, t := range titles {
			if t.Type != core.TypeMovie && t.Type != core.TypeShow {
				continue
			}
			for _, i := range indices {
				if seen[i] == nil {
					seen[i] = map[core.RatingKey]bool{}
				}
				if seen[i][t.Key] {
					continue
				}
				seen[i][t.Key] = true
				r := releases[i]
				tv := len(r.Info.Seasons) > 0 || len(r.Info.Episodes) > 0 || r.Info.AirDate != "" || r.Info.FullSeason || r.Category/1000 == 5
				if (t.Type == core.TypeShow) != tv {
					continue
				}
				same, conflict := releaseparse.CompareTitleIDs(r.TitleIDs, t.IDs)
				if conflict || (!same && (!releaseparse.YearMatches(r.Info.Year, t.Year) || !(releaseparse.TitleMatches(r.Info, t.Title) || releaseparse.TitleMatches(r.Info, t.OriginalTitle) || (t.EnglishTitle != "" && releaseparse.TitleMatches(r.Info, t.EnglishTitle))))) {
					continue
				}
				if !same && t.Type == core.TypeMovie && r.Info.Year == 0 {
					continue
				}
				matches[i] = append(matches[i], t.Key)
			}
		}
		return nil
	}
	for name, indices := range byName {
		if err := lookup(releaseMatchCols+`JOIN catalog_release_names n ON n.key=t.key WHERE n.name=? LIMIT ?`, name, indices); err != nil {
			return nil, err
		}
	}
	for id, indices := range byID {
		kind, value, _ := strings.Cut(id, ":")
		column := map[string]string{"tmdb": "tmdb_id", "imdb": "imdb", "tvdb": "tvdb"}[kind]
		if column == "" {
			continue
		}
		var arg any = value
		if kind != "imdb" {
			var err error
			arg, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, err
			}
		}
		if err := lookup(releaseMatchCols+`WHERE t.`+column+`=? LIMIT ?`, arg, indices); err != nil {
			return nil, err
		}
	}
	type showLayout struct {
		title  *core.Title
		layout EpisodeLayout
	}
	shows := map[core.RatingKey]showLayout{}
	out := map[string][]core.Title{}
	for i, keys := range matches {
		if overflow[i] || len(keys) != 1 {
			continue
		}
		r := releases[i]
		key := keys[0]
		if key.Kind() == core.KindMovie {
			if t, e := c.CachedTitle(ctx, key); e == nil && t.IsReleased(c.now()) {
				out[r.ID] = []core.Title{*t}
			}
			continue
		}
		entry, ok := shows[key]
		if !ok {
			show, err := c.StoredTitle(ctx, key)
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			layout, _, err := c.CachedEpisodeLayout(ctx, key)
			if err != nil {
				return nil, err
			}
			entry = showLayout{title: show, layout: layout}
			shows[key] = entry
		}
		coords := releaseCoordinates(entry.layout, r.Info)
		if len(coords) == 0 && !hasRegularEpisode(entry.layout) && entry.layout.Order == tmdbAired &&
			entry.title.IDs.TMDB > 0 && !entry.title.Adult && entry.title.IsReleased(c.now()) &&
			(releaseparse.EpisodeMatches(r.Info, 1, 1) || releaseSeasonPack(r.Info, 1)) {
			// Feed-first evidence for an unmirrored TMDB premiere. It does
			// not insert an episode or infer a provider-only identity.
			show := entry.title
			if show.IDs.TMDB < 100_000_000 {
				out[r.ID] = []core.Title{{
					Key: core.EpisodeKey(show.IDs.TMDB, 1, 1), Type: core.TypeEpisode, EpisodeOrder: entry.layout.Order,
					ShowKey: show.Key, ShowIDs: show.IDs, ShowTitle: show.Title,
					ShowOriginalTitle: show.OriginalTitle, ShowEnglishTitle: show.EnglishTitle, ShowYear: show.Year,
					Season: 1, Episode: 1, Released: show.Released, Language: show.Language,
				}}
			}
			continue
		}
		episodes, err := c.releaseTitlesForCoordinates(ctx, entry.layout, coords)
		if err != nil {
			return nil, err
		}
		for _, t := range episodes {
			if !t.IsReleased(c.now()) {
				continue
			}
			show := entry.title
			t.ShowKey, t.ShowIDs = show.Key, show.IDs
			if t.ShowTitle == "" {
				t.ShowTitle = show.Title
			}
			t.ShowOriginalTitle, t.ShowEnglishTitle, t.ShowYear = show.OriginalTitle, show.EnglishTitle, show.Year
			if t.Language == "" {
				t.Language = show.Language
			}
			out[r.ID] = append(out[r.ID], t)
		}
	}
	return out, nil
}

func releaseSeasonPack(info core.ReleaseInfo, season int) bool {
	if info.FullSeason {
		for _, number := range info.Seasons {
			if number == season {
				return true
			}
		}
	}
	return false
}

func hasRegularEpisode(layout EpisodeLayout) bool {
	return len(layout.Episodes) > 0 && layout.Episodes[len(layout.Episodes)-1].Season > 0
}

func releaseCoordinates(layout EpisodeLayout, info core.ReleaseInfo) []EpisodeCoordinate {
	var date time.Time
	if info.AirDate != "" && !info.FullSeason {
		var err error
		date, err = time.Parse(time.DateOnly, info.AirDate)
		if err != nil {
			return nil
		}
		if len(info.Episodes) == 0 {
			if ep, ok := layout.EpisodeOn(date); ok {
				return []EpisodeCoordinate{ep}
			}
			return nil
		}
	}

	const maxMatches = 2000
	seasons := slices.Clone(info.Seasons)
	if len(seasons) == 0 && !info.FullSeason {
		for number := range layout.Seasons {
			if number > 0 {
				seasons = append(seasons, number)
			}
		}
	}
	slices.Sort(seasons)
	seasons = slices.Compact(seasons)
	episodes := slices.Clone(info.Episodes)
	slices.Sort(episodes)
	episodes = slices.Compact(episodes)
	var out []EpisodeCoordinate
	for _, season := range seasons {
		if info.FullSeason {
			pack := layoutSeasonEpisodes(layout, season)
			out = append(out, pack[:min(maxMatches-len(out), len(pack))]...)
		} else {
			for _, episode := range episodes {
				if ep, ok := layout.EpisodeAt(season, episode); ok && (date.IsZero() || releaseparse.EpisodeMatchesAirDate(info, ep.AirDate)) {
					out = append(out, ep)
					if len(out) == maxMatches {
						break
					}
				}
			}
		}
		if len(out) == maxMatches {
			break
		}
	}
	return out
}

func feedIDs(ids core.IDs) []string {
	var out []string
	if ids.TMDB > 0 {
		out = append(out, fmt.Sprintf("tmdb:%d", ids.TMDB))
	}
	if ids.TVDB > 0 {
		out = append(out, fmt.Sprintf("tvdb:%d", ids.TVDB))
	}
	if ids.IMDB != "" {
		out = append(out, "imdb:"+ids.IMDB)
	}
	return out
}

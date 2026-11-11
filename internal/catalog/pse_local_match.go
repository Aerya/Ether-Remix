package catalog

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// An exact normalized title and year (or a single explicit episode) are
// required. Ambiguous names and home videos remain local files.
func (a *Catalog) MatchLocalVideo(ctx context.Context, f localmedia.File) core.RatingKey {
	parsed := releaseparse.Parse(filepath.Base(f.Path))
	episode := len(parsed.Seasons) == 1 && len(parsed.Episodes) == 1
	if parsed.Title == "" || (!episode && parsed.Year == 0) || len(parsed.Episodes) > 1 {
		return 0
	}
	typ := core.TypeMovie
	if episode {
		typ = core.TypeShow
	}
	exact := func(title, original string, year int) bool {
		return (parsed.Year == 0 || year == parsed.Year) && (releaseparse.NormalizeTitle(title) == releaseparse.NormalizeTitle(parsed.Title) || releaseparse.NormalizeTitle(original) == releaseparse.NormalizeTitle(parsed.Title))
	}
	candidates := map[core.RatingKey]bool{}
	rows, err := a.db.QueryContext(ctx, "SELECT key,title,original_title,year FROM catalog_titles WHERE type=? AND tmdb_id>0 AND (?=0 OR year=?)", typ, parsed.Year, parsed.Year)
	if err != nil {
		return 0
	}
	for rows.Next() {
		var key core.RatingKey
		var title, original string
		var year int
		if rows.Scan(&key, &title, &original, &year) != nil {
			rows.Close()
			return 0
		}
		if exact(title, original, year) {
			candidates[key] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0
	}
	if len(candidates) == 0 && a.tmdb() != nil {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		yearOf := func(date string) int {
			if len(date) < 4 {
				return 0
			}
			n, _ := strconv.Atoi(date[:4])
			return n
		}
		if episode {
			r, e := a.tmdb().SearchTV(c, parsed.Title, 1)
			if e != nil || r == nil || r.TotalPages > 1 {
				return 0
			}
			for _, v := range r.Results {
				if exact(v.Name, v.OriginalName, yearOf(v.FirstAirDate)) {
					candidates[core.ShowKey(v.ID)] = true
				}
			}
		} else {
			r, e := a.tmdb().SearchMovies(c, parsed.Title, 1)
			if e != nil || r == nil || r.TotalPages > 1 {
				return 0
			}
			for _, v := range r.Results {
				if exact(v.Title, v.OriginalTitle, yearOf(v.ReleaseDate)) {
					candidates[core.MovieKey(v.ID)] = true
				}
			}
		}
	}
	if len(candidates) != 1 {
		return 0
	}
	for key := range candidates {
		if episode {
			key = core.EpisodeKey(key.TMDB(), parsed.Seasons[0], parsed.Episodes[0])
		}
		if title, err := a.Get(ctx, key); err == nil && title != nil {
			return key
		}
	}
	return 0
}

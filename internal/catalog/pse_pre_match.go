package catalog

import (
	"context"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// MatchRelease relie une release analysée (nom de pre) à un film ou à une
// série du catalogue. Un titre et une année exacts sont exigés pour un
// film ; une série n'a besoin que du titre exact (année facultative). Un
// nom ambigu n'est pas rapproché. Si le média manque au catalogue, il est
// cherché sur TMDB puis enregistré, sans attendre qu'il y arrive autrement.
func (c *Catalog) MatchRelease(ctx context.Context, info core.ReleaseInfo) (core.Title, bool) {
	episodic := len(info.Seasons) > 0 || len(info.Episodes) > 0
	if info.Title == "" || (!episodic && info.Year == 0) {
		return core.Title{}, false
	}
	typ := core.TypeMovie
	if episodic {
		typ = core.TypeShow
	}
	want := releaseparse.NormalizeTitle(info.Title)
	exact := func(title, original string, year int) bool {
		if info.Year != 0 && year != 0 && year != info.Year && (!episodic || year > info.Year) {
			return false
		}
		return releaseparse.NormalizeTitle(title) == want || releaseparse.NormalizeTitle(original) == want
	}
	candidates := map[core.RatingKey]bool{}
	rows, err := c.db.QueryContext(ctx, "SELECT key,title,original_title,year FROM catalog_titles WHERE type=? AND tmdb_id>0", typ)
	if err != nil {
		return core.Title{}, false
	}
	for rows.Next() {
		var key core.RatingKey
		var title, original string
		var year int
		if rows.Scan(&key, &title, &original, &year) != nil {
			rows.Close()
			return core.Title{}, false
		}
		if exact(title, original, year) {
			candidates[key] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return core.Title{}, false
	}
	if len(candidates) == 0 && c.tmdb() != nil {
		cc, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		yearOf := func(date string) int {
			if len(date) < 4 {
				return 0
			}
			n, _ := strconv.Atoi(date[:4])
			return n
		}
		if episodic {
			r, e := c.tmdb().SearchTV(cc, info.Title, 1)
			if e != nil || r == nil {
				return core.Title{}, false
			}
			for _, v := range r.Results {
				if exact(v.Name, v.OriginalName, yearOf(v.FirstAirDate)) {
					candidates[core.ShowKey(v.ID)] = true
				}
			}
		} else {
			r, e := c.tmdb().SearchMovies(cc, info.Title, 1)
			if e != nil || r == nil {
				return core.Title{}, false
			}
			for _, v := range r.Results {
				if exact(v.Title, v.OriginalTitle, yearOf(v.ReleaseDate)) {
					candidates[core.MovieKey(v.ID)] = true
				}
			}
		}
	}
	if len(candidates) != 1 {
		return core.Title{}, false
	}
	for key := range candidates {
		// get n'applique pas les règles de visibilité : une pre prouve la
		// sortie, l'affichage reste réglé par le catalogue.
		if t, err := c.get(ctx, key); err == nil && t != nil {
			return core.Title{Key: key, Type: typ}, true
		}
	}
	return core.Title{}, false
}

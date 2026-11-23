package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// ReleaseEpisodeLayout reads only one show's indexed local rows. It never
// fetches metadata, and incomplete seasons remain explicitly unknown.
func (c *Catalog) ReleaseEpisodeLayout(ctx context.Context, show core.RatingKey) (core.EpisodeLayout, error) {
	out := core.EpisodeLayout{Numbers: map[core.EpisodeNumber]int{}, Seasons: map[int]bool{}, Complete: map[int]bool{}}
	rows, err := c.db.QueryContext(ctx, `SELECT season,child_count FROM catalog_titles WHERE show_key=? AND type='season' AND season>0 ORDER BY season LIMIT 1001`, show)
	if err != nil {
		return out, err
	}
	counts := map[int]int{}
	for rows.Next() {
		var season, count int
		if err = rows.Scan(&season, &count); err != nil {
			break
		}
		counts[season] = count
		out.Seasons[season] = true
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if scanErr != nil {
		return out, scanErr
	}
	if len(counts) == 0 || len(counts) > 1000 {
		out.Seasons = nil
		return out, nil
	}
	rows, err = c.db.QueryContext(ctx, `SELECT season,episode FROM catalog_titles WHERE show_key=? AND type='episode' AND season>0 ORDER BY season,episode LIMIT 20001`, show)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	seen := map[int]int{}
	totalRows := 0
	for rows.Next() {
		var season, episode int
		if err = rows.Scan(&season, &episode); err != nil {
			return out, err
		}
		totalRows++
		if totalRows > 20000 {
			out.Seasons = nil
			out.Numbers = nil
			return out, nil
		}
		absolute := episode
		if season > 1 {
			total := 0
			known := true
			for n := 1; n < season; n++ {
				if counts[n] <= 0 {
					known = false
					break
				}
				total += counts[n]
			}
			if !known {
				continue
			}
			if !(counts[season] > 0 && episode > counts[season] && episode > total) {
				absolute = total + episode
			}
		}
		out.Numbers[core.EpisodeNumber{Season: season, Episode: episode}] = absolute
		seen[season]++
	}
	for season, count := range counts {
		out.Complete[season] = count > 0 && seen[season] == count
	}
	return out, rows.Err()
}

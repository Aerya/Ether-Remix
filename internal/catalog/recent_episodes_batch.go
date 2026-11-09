package catalog

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RecentEpisodesMany reads only the local identities needed for status badges.
// Small bounded batches also cover an empty result without a query per series.
func (c *Catalog) RecentEpisodesMany(ctx context.Context, shows []core.RatingKey, since time.Time) ([]core.Title, error) {
	unique := make([]core.RatingKey, 0, len(shows))
	seen := map[core.RatingKey]bool{}
	for _, k := range shows {
		if !seen[k] {
			unique = append(unique, k)
			seen[k] = true
		}
	}
	out := []core.Title{}
	for start := 0; start < len(unique); start += 200 {
		part := unique[start:min(start+200, len(unique))]
		args := make([]any, 0, len(part)+2)
		for _, k := range part {
			args = append(args, k)
		}
		args = append(args, since.Unix(), c.now().Unix())
		rows, err := c.db.QueryContext(ctx, `SELECT key,show_key,episode FROM catalog_titles WHERE show_key IN (`+placeholders(len(part))+`) AND type='episode' AND adult=0 AND released_ts BETWEEN ? AND ?`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			t := core.Title{Type: core.TypeEpisode}
			if err := rows.Scan(&t.Key, &t.ShowKey, &t.Episode); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

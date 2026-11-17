package catalog

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
)

// ReleaseLabels reads only local catalogue metadata, including summary-tier
// titles. It never triggers a metadata provider lookup or playback search.
func (c *Catalog) ReleaseLabels(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
	out := make(map[core.RatingKey]core.Title, len(keys))
	for start := 0; start < len(keys); start += 500 {
		end := min(start+500, len(keys))
		raw, _ := json.Marshal(keys[start:end])
		rows, err := c.db.QueryContext(ctx, `SELECT key,title,original_title,language,
   EXISTS(SELECT 1 FROM catalog_keywords k WHERE k.key=t.key AND lower(k.keyword)='anime'),
   EXISTS(SELECT 1 FROM catalog_genres g WHERE g.key=t.key AND lower(g.genre)='animation')
   FROM catalog_titles t WHERE key IN (SELECT value FROM json_each(?))`, string(raw))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var t core.Title
			var anime, animation bool
			if err = rows.Scan(&t.Key, &t.Title, &t.OriginalTitle, &t.Language, &anime, &animation); err != nil {
				break
			}
			if anime {
				t.Keywords = []string{"anime"}
			}
			if animation {
				t.Genres = []string{"Animation"}
			}
			out[t.Key] = t
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, key := range keys[start:end] {
			if _, ok := out[key]; !ok {
				if t, ok := c.summaryTitle(key); ok {
					out[key] = t
				}
			}
		}
	}
	return out, nil
}

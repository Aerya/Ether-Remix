package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type ChildAncestry struct {
	ShowKey, ParentKey core.RatingKey
}

// ChildAncestry returns the durable parent relationship, including when
// the provider has withdrawn the child's metadata. The displayed parent
// can instead be obtained from the projected cached title.
func (c *Catalog) ChildAncestry(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]ChildAncestry, error) {
	out := make(map[core.RatingKey]ChildAncestry, len(keys))
	var allocated []core.RatingKey
	for _, key := range uniqueRatingKeys(keys) {
		switch key.Kind() {
		case core.KindShow:
			out[key] = ChildAncestry{ShowKey: key}
		case core.KindSeason, core.KindEpisode:
			if key.Allocated() {
				allocated = append(allocated, key)
			} else {
				out[key] = ChildAncestry{ShowKey: core.ShowKey(key.TMDB()), ParentKey: key.Parent()}
			}
		}
	}
	for part := range store.Chunks(allocated, 200) {
		where, args := c.keyMembership("key", false, part)
		rows, err := c.db.QueryContext(ctx, `SELECT key,show_key,parent_key FROM catalog_allocated_child WHERE `+where, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key core.RatingKey
			var child ChildAncestry
			if err := rows.Scan(&key, &child.ShowKey, &child.ParentKey); err != nil {
				rows.Close()
				return nil, err
			}
			out[key] = child
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

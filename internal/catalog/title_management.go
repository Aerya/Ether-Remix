package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func (c *Catalog) refreshShowTree(ctx context.Context, client TMDB, show core.Title, details *tmdb.TVDetails) error {
	fresh, ok := client.(interface {
		SeasonDetailsFresh(context.Context, int64, int) (*tmdb.SeasonDetails, error)
	})
	if !ok {
		return core.ErrNotAvailable
	}
	seasons := make([]core.RatingKey, 0, len(details.Seasons))
	for _, season := range details.Seasons {
		if !episodeKeyFits(season.SeasonNumber, 0) {
			continue
		}
		sd, err := fresh.SeasonDetailsFresh(ctx, show.IDs.TMDB, season.SeasonNumber)
		if err != nil {
			return err
		}
		if sd == nil || sd.ID != season.ID || sd.SeasonNumber != season.SeasonNumber {
			return fmt.Errorf("catalog: invalid season details for %s", show.Key)
		}
		if err := c.storeSeasonDetails(ctx, &show, sd, true); err != nil {
			return err
		}
		seasons = append(seasons, core.RatingKey(season.SeasonNumber))
	}
	opts := upsertOpts{browsable: c.meetsThreshold(show), detail: true, replaceMetadata: true}
	return c.upsertItems(ctx, []upsertItem{{t: show, o: opts}}, func(ctx context.Context, tx *sql.Tx) error {
		return c.removeMissingChildren(ctx, tx, show.Key, nil, seasons)
	})
}

// Remove provider rows withdrawn from a complete payload. Viewer and
// release bindings live independently of these mirror rows.
func (c *Catalog) removeMissingChildren(ctx context.Context, tx *sql.Tx, show core.RatingKey, season *int, keep []core.RatingKey) error {
	q := `DELETE FROM catalog_titles WHERE show_key=? AND tmdb_id<>0 AND (type='season' AND key<3900000000000000 OR type='episode' AND key<5000000000000000)`
	args := []any{int64(show)}
	column := "season"
	if season != nil {
		q += ` AND type='episode' AND season=?`
		args = append(args, *season)
		column = "key"
	}
	if len(keep) > 0 {
		predicate, values := c.keyMembership(column, true, keep)
		q += " AND " + predicate
		args = append(args, values...)
	}
	rows, err := tx.QueryContext(ctx, q+" RETURNING key", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var removed []core.RatingKey
	for rows.Next() {
		var key core.RatingKey
		if err := rows.Scan(&key); err != nil {
			return err
		}
		removed = append(removed, key)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(removed) == 0 {
		return nil
	}
	where, values := c.keyMembership("key", false, removed)
	for _, table := range []string{"catalog_genres", "catalog_countries", "catalog_studios", "catalog_keywords", "catalog_credits", "catalog_alt_titles", "catalog_release_names", "catalog_episode_release"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+where, values...); err != nil {
			return err
		}
	}
	return nil
}

// TitleTreeArtwork reads only the named title and its stored descendants.
func (c *Catalog) TitleTreeArtwork(ctx context.Context, key core.RatingKey) ([]string, error) {
	query := "SELECT poster,backdrop,still,logo FROM catalog_titles WHERE key=?"
	args := []any{int64(key)}
	if key.Kind() == core.KindShow {
		query += " UNION ALL SELECT poster,backdrop,still,logo FROM catalog_titles WHERE show_key=?"
		args = append(args, int64(key))
	}
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var poster, backdrop, still, logo string
		if err := rows.Scan(&poster, &backdrop, &still, &logo); err != nil {
			return nil, err
		}
		paths = append(paths, poster, backdrop, still, logo)
	}
	return paths, rows.Err()
}

// TitleBindingsChanged invalidates catalogue reads affected by a rematch.
func (c *Catalog) TitleBindingsChanged(from, to core.RatingKey) {
	c.ownerIncludes.Lock()
	_, included := c.ownerIncludes.keys[from]
	if included {
		delete(c.ownerIncludes.keys, from)
		c.ownerIncludes.keys[to] = struct{}{}
	}
	bus := c.ownerIncludes.bus
	c.ownerIncludes.Unlock()
	c.writeGen.Add(1)
	if included {
		bus.Notify(from.String())
		bus.Notify(to.String())
	}
}

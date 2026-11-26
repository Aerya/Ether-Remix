package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog", 67, `
		CREATE INDEX catalog_genres_folded_key ON catalog_genres(LOWER(genre), key);
		CREATE INDEX catalog_studios_folded_key ON catalog_studios(LOWER(studio), key);`)
}

// CollectionCardSample reads just one authorized artwork record from the
// indexed membership or facet. It never loads the matching titles.
func (c *Catalog) CollectionCardSample(ctx context.Context, f core.Filter, id int64, facet, value string, anyPersonal *core.FilterGroup, releaseMode string, lookahead int) (core.Title, error) {
	f = c.viewerPaging(ctx, normalizeConsoleFilter(f))
	f.CatalogCandidates = false
	upcoming := releaseMode == "mixed" || releaseMode == "upcoming"
	where, args, err := c.browseWhereMode(f, upcoming)
	if err != nil {
		return core.Title{}, err
	}
	if upcoming {
		rules, ruleArgs := c.browseViewerRules(f)
		if len(rules) > 0 {
			where += " AND " + strings.Join(rules, " AND ")
			args = append(args, ruleArgs...)
		}
		now := c.now()
		where += " AND t.adult=0 AND t.release_known=1 AND t.released_ts<=?"
		args = append(args, now.AddDate(0, 0, lookahead).Unix())
		if releaseMode == "upcoming" {
			where += " AND t.released_ts>?"
			args = append(args, now.Unix())
		}
	}
	from, seek, bind := "collections_members m", "m.spec_id=? AND m.shared=1", []any{id}
	switch facet {
	case "genre":
		from, seek, bind = "catalog_genres m", "LOWER(m.genre)=?", []any{value}
	case "studio":
		from, seek, bind = "catalog_studios m", "LOWER(m.studio)=?", []any{value}
	case "":
		if anyPersonal != nil {
			expr, values, err := c.compileBrowseGroup(anyPersonal, "t", 0, f.Type)
			if err != nil {
				return core.Title{}, err
			}
			seek = "m.spec_id=? AND (m.shared=1 OR (" + expr + "))"
			bind = append(bind, values...)
		}
	}
	// Keep SQLite on the narrow member/facet index even when the viewer
	// filter also has a usable catalogue-wide index.
	query := "SELECT t.key,t.type,t.poster,t.backdrop FROM " + from + " CROSS JOIN catalog_titles t WHERE t.key=m.key AND (" + seek + ") AND " + where + " LIMIT 1"
	var sample core.Title
	err = c.db.QueryRowContext(ctx, query, append(bind, args...)...).Scan(&sample.Key, &sample.Type, &sample.PosterPath, &sample.BackdropPath)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Title{}, nil
	}
	return sample, err
}

package releasedb

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// CheckSeriesIdentity checks the local catalogue before allowing yearless
// episode names. It never calls an addon or a debrid service. Missing catalogue
// facts and lookup failures leave the strict rule in place. The result is not
// serialized, cached on releases, or trusted from callers.
func CheckSeriesIdentity(ctx context.Context, db *sql.DB, req core.SearchRequest) core.SearchRequest {
	req.SeriesIdentityChecked = false
	if db == nil || (req.Type != core.TypeEpisode && req.Type != core.TypeShow) || req.Year == 0 || req.IDs.TMDB <= 0 {
		return req
	}
	key := core.ShowKey(req.IDs.TMDB)
	var year int
	if err := db.QueryRowContext(ctx, `SELECT year FROM catalog_titles WHERE key=? AND type='show'`, key).Scan(&year); err != nil || year != req.Year {
		return req
	}
	names := append([]string{req.Title, req.OriginalTitle}, req.AlternativeTitles...)
	// Include the catalogue's own aliases even when a search omitted them.
	rows, err := db.QueryContext(ctx, `SELECT title FROM catalog_titles WHERE key=? UNION SELECT original_title FROM catalog_titles WHERE key=? UNION SELECT title FROM catalog_alt_titles WHERE key=?`, key, key, key)
	if err != nil {
		return req
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			break
		}
		names = append(names, name)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		return req
	}
	variants := map[string]bool{}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		variants[name] = true
		base := name
		for _, article := range []string{"the ", "a ", "an "} {
			if strings.HasPrefix(base, article) {
				base = strings.TrimSpace(strings.TrimPrefix(base, article))
				break
			}
		}
		variants[base] = true
		for _, article := range []string{"the ", "a ", "an "} {
			variants[article+base] = true
		}
	}
	if len(variants) == 0 {
		return req
	}
	args := []any{key}
	for name := range variants {
		args = append(args, name)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(variants)), ",")
	// Check all years, including unknown and identical years: a second show is
	// ambiguous regardless of its premiere date. Original and alternative titles
	// matter as much as the localized display title.
	for _, column := range []string{"title", "original_title"} {
		var found int
		err = db.QueryRowContext(ctx, `SELECT 1 FROM catalog_titles WHERE type='show' AND key<>? AND LOWER(`+column+`) IN (`+placeholders+`) LIMIT 1`, args...).Scan(&found)
		if err != sql.ErrNoRows {
			return req
		}
	}
	var found int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM catalog_alt_titles a JOIN catalog_titles t ON t.key=a.key WHERE t.type='show' AND t.key<>? AND LOWER(a.title) IN (`+placeholders+`) LIMIT 1`, args...).Scan(&found)
	if err != sql.ErrNoRows {
		return req
	}
	req.SeriesIdentityChecked = true
	return req
}

package catalog

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// ExternalTitleRef identifies a movie or show by TMDB and/or IMDb id for
// batched local-only resolution (#594).
type ExternalTitleRef struct {
	Type core.MediaType
	TMDB int64
	IMDB string
}

// ResolveExternalTitles maps each (type, tmdb, imdb) reference to its local
// canonical RatingKey in catalog_titles (TMDB first, then IMDb), returning 0
// for titles not yet in the local catalogue. It never calls external TMDB.
func (c *Catalog) ResolveExternalTitles(ctx context.Context, refs []ExternalTitleRef) ([]core.RatingKey, error) {
	out := make([]core.RatingKey, len(refs))
	if c == nil || c.db == nil || len(refs) == 0 {
		return out, nil
	}

	seenTMDB := make(map[int64]bool, len(refs))
	var tmdbArgs []any
	for _, r := range refs {
		if (r.Type == core.TypeMovie || r.Type == core.TypeShow) && r.TMDB > 0 && !seenTMDB[r.TMDB] {
			seenTMDB[r.TMDB] = true
			tmdbArgs = append(tmdbArgs, r.TMDB)
		}
	}
	if len(tmdbArgs) > 0 {
		q := `SELECT key, type, tmdb_id FROM catalog_titles WHERE tmdb_id IN (` + placeholders(len(tmdbArgs)) + `) AND type IN ('movie','show') ORDER BY key ASC`
		rows, err := c.db.QueryContext(ctx, q, tmdbArgs...)
		if err != nil {
			return nil, err
		}
		type tmdbPair struct {
			typ  string
			tmdb int64
		}
		byTMDB := make(map[tmdbPair]core.RatingKey, len(tmdbArgs))
		for rows.Next() {
			var k int64
			var typ string
			var tmdbID int64
			if err := rows.Scan(&k, &typ, &tmdbID); err != nil {
				rows.Close()
				return nil, err
			}
			pair := tmdbPair{typ: typ, tmdb: tmdbID}
			if _, exists := byTMDB[pair]; !exists {
				byTMDB[pair] = core.RatingKey(k)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for i, r := range refs {
			if r.TMDB > 0 {
				if k, ok := byTMDB[tmdbPair{typ: string(r.Type), tmdb: r.TMDB}]; ok {
					out[i] = k
				}
			}
		}
	}

	seenIMDB := make(map[string]bool)
	var imdbArgs []any
	for i, r := range refs {
		if out[i] != 0 || (r.Type != core.TypeMovie && r.Type != core.TypeShow) {
			continue
		}
		imdb := strings.ToLower(strings.TrimSpace(r.IMDB))
		if imdb != "" && !seenIMDB[imdb] {
			seenIMDB[imdb] = true
			imdbArgs = append(imdbArgs, imdb)
		}
	}
	if len(imdbArgs) > 0 {
		q := `SELECT key, type, imdb FROM catalog_titles WHERE imdb IN (` + placeholders(len(imdbArgs)) + `) AND type IN ('movie','show') ORDER BY key ASC`
		rows, err := c.db.QueryContext(ctx, q, imdbArgs...)
		if err != nil {
			return nil, err
		}
		type imdbPair struct {
			typ  string
			imdb string
		}
		byIMDB := make(map[imdbPair]core.RatingKey, len(imdbArgs))
		for rows.Next() {
			var k int64
			var typ, imdb string
			if err := rows.Scan(&k, &typ, &imdb); err != nil {
				rows.Close()
				return nil, err
			}
			pair := imdbPair{typ: typ, imdb: strings.ToLower(strings.TrimSpace(imdb))}
			if _, exists := byIMDB[pair]; !exists {
				byIMDB[pair] = core.RatingKey(k)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		for i, r := range refs {
			if out[i] != 0 || r.IMDB == "" {
				continue
			}
			imdb := strings.ToLower(strings.TrimSpace(r.IMDB))
			if k, ok := byIMDB[imdbPair{typ: string(r.Type), imdb: imdb}]; ok {
				out[i] = k
			}
		}
	}

	return out, nil
}

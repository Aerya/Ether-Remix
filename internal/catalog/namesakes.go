package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

// Two catalogue titles can share a normalized title and a near year ("Runner"
// and "The Runner", both 2026). Release names cannot tell such namesakes
// apart, so a playback decision needs to know whether another title of the
// same media type claims this one's name. Namesakes answers that with an
// on-demand indexed lookup over a handful of article variants of the title;
// it never loads the catalogue or scans it, and the caller caches the answer
// per title key.

// namesakeMaxRows bounds the candidate rows an indexed title lookup reads.
const namesakeMaxRows = 16

// namesakeTitleVariants are the article-prefixed spellings an exact
// case-insensitive title lookup tries, so "Runner" and "The Runner" find each
// other. "The"/"A"/"An" are dropped by NormalizeTitle, so they are the only
// article differences a normalized-title match can hide.
func namesakeTitleVariants(title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	out := []string{title}
	lower := strings.ToLower(title)
	hasArticle := false
	for _, art := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, art) {
			hasArticle = true
			rest := strings.TrimSpace(title[len(art):])
			if rest != "" {
				out = append(out, rest)
			}
		}
	}
	if !hasArticle {
		for _, art := range []string{"The ", "A ", "An "} {
			out = append(out, art+title)
		}
	}
	seen := make(map[string]bool, len(out))
	uniq := out[:0]
	for _, v := range out {
		if !seen[v] {
			seen[v] = true
			uniq = append(uniq, v)
		}
	}
	return uniq
}

// Namesakes reports whether another catalogue title of the same media type
// shares this title's normalized name and a year within one year of it. The
// lookup filters by (type, case-insensitive title IN article variants, year
// window) through the catalog_titles_type_lower_title index and reads a few rows.
func (c *Catalog) Namesakes(ctx context.Context, key core.RatingKey) (bool, error) {
	self, err := c.loadByKey(ctx, key)
	if err != nil {
		return false, err
	}
	t := self.t
	if t.Type != core.TypeMovie && t.Type != core.TypeShow {
		return false, nil
	}
	variants := namesakeTitleVariants(t.Title)
	if len(variants) == 0 {
		variants = namesakeTitleVariants(t.OriginalTitle)
	}
	if len(variants) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(variants)), ",")
		// Year is bound once in each bound (>= y-1, <= y+1) rather than as
		// ABS(year-?)<=1 so the window reads the index range directly. Lower-cased
		// comparison keeps one statement for every dialect (SQLite and PostgreSQL).
		args := make([]any, 0, len(variants)+5)
		args = append(args, string(t.Type))
		for _, v := range variants {
			args = append(args, strings.ToLower(v))
		}
		args = append(args, t.Year-1, t.Year+1, key)
		rows, err := c.db.QueryContext(ctx, fmt.Sprintf(`
SELECT key, title, year FROM catalog_titles
WHERE type=? AND LOWER(title) IN (%s)
  AND year>=? AND year<=?
  AND key<>?
LIMIT %d`, placeholders, namesakeMaxRows), args...)
		if err != nil {
			return false, err
		}
		wantNorm := releaseparse.NormalizeTitle(t.Title)
		if wantNorm == "" {
			wantNorm = releaseparse.NormalizeTitle(t.OriginalTitle)
		}
		for rows.Next() {
			var other int64
			var title string
			var year int
			if err := rows.Scan(&other, &title, &year); err != nil {
				rows.Close()
				return false, err
			}
			// The IN filter is case-insensitive, not article-insensitive; the
			// normalized comparison is the final word on sameness.
			if releaseparse.NormalizeTitle(title) == wantNorm {
				rows.Close()
				return true, nil
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	if norm := releaseparse.NormalizeTitle(t.EnglishTitle); norm != "" {
		var hit int
		err := c.db.QueryRowContext(ctx, `SELECT 1 FROM catalog_release_names rn JOIN catalog_titles other ON other.key=rn.key WHERE rn.name=? AND other.type=? AND other.year>=? AND other.year<=? AND other.key<>? LIMIT 1`,
			norm, string(t.Type), t.Year-1, t.Year+1, key).Scan(&hit)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

// The additive index the Namesakes lookup relies on; SQLite and PostgreSQL
// both support a functional index over LOWER(title).
func init() {
	store.Register("catalog", 59, `CREATE INDEX IF NOT EXISTS catalog_titles_type_lower_title ON catalog_titles(type, LOWER(title), year);`)
}

// SeriesNamesake checks indexed title variants for a differently dated show.
// The resolver caches the result; no mirror scan or upstream fetch is needed.
func (c *Catalog) SeriesNamesake(ctx context.Context, key core.RatingKey, title string, year int) (bool, error) {
	variants := namesakeTitleVariants(title)
	if len(variants) == 0 || year == 0 {
		return false, nil
	}
	args := []any{string(core.TypeShow)}
	for _, v := range variants {
		args = append(args, strings.ToLower(v))
	}
	args = append(args, year, key)
	rows, err := c.db.QueryContext(ctx, `SELECT title FROM catalog_titles WHERE type=? AND LOWER(title) IN (`+strings.TrimSuffix(strings.Repeat("?,", len(variants)), ",")+`) AND year>0 AND year<>? AND key<>? LIMIT 16`, args...)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			rows.Close()
			return false, err
		}
		if releaseparse.NormalizeTitle(other) == releaseparse.NormalizeTitle(title) {
			rows.Close()
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	if norm := releaseparse.NormalizeTitle(title); norm != "" {
		var hit int
		err := c.db.QueryRowContext(ctx, `SELECT 1 FROM catalog_release_names rn JOIN catalog_titles t ON t.key=rn.key WHERE rn.name=? AND t.type=? AND t.year>0 AND t.year<>? AND t.key<>? LIMIT 1`,
			norm, string(core.TypeShow), year, key).Scan(&hit)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return false, nil
}

// UpdateRuntime persists a consensus media duration onto catalog_titles when
// independent probes agree on a runtime different from what the metadata
// provider reported. It is an O(1) primary-key update and invalidates the
// title cache entry when the row changes.
func (c *Catalog) UpdateRuntime(ctx context.Context, key core.RatingKey, runtime time.Duration) error {
	sec := int64(runtime.Round(time.Second) / time.Second)
	if key == 0 || sec <= 0 {
		return nil
	}
	res, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET runtime_sec=? WHERE key=? AND runtime_sec<>?`, sec, key, sec)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		c.cacheMu.Lock()
		delete(c.titleCache, key)
		c.cacheMu.Unlock()
		c.writeGen.Add(1)
	}
	return nil
}

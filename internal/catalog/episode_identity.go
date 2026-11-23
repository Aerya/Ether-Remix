package catalog

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog_episode_identity", 2, `CREATE INDEX IF NOT EXISTS catalog_alt_titles_lower_title ON catalog_alt_titles(LOWER(title), key);`)
	store.Register("catalog_episode_identity", 1, `CREATE INDEX IF NOT EXISTS catalog_titles_type_lower_original ON catalog_titles(type, LOWER(original_title), year);`)
}

// EpisodeIdentity reads the local mirror only. Every lookup uses a title or
// show index and a fixed row limit, independent of the library's size. It
// neither fetches missing episodes nor schedules background work.
func (c *Catalog) EpisodeIdentity(ctx context.Context, show core.RatingKey, season, episode int, names []string) (core.EpisodeIdentity, error) {
	out := core.EpisodeIdentity{OtherShows: map[string][]core.EpisodeNamesake{}, Seasons: map[int]string{}, Episodes: map[int]string{}}
	if len(names) > 8 {
		names = names[:8]
	}
	type namesakeRef struct {
		name  string
		index int
	}
	byKey := map[int64][]namesakeRef{}
	keys := []any{int64(show)}
	keySeen := map[int64]bool{int64(show): true}
	for _, name := range names {
		variants := namesakeTitleVariants(name)
		if len(variants) == 0 {
			continue
		}
		args := []any{string(core.TypeShow)}
		for _, v := range variants {
			args = append(args, strings.ToLower(v))
		}
		args = append(args, show)
		seen := map[int64]bool{}
		record := func(key int64, other core.EpisodeNamesake) {
			seen[key] = true
			idx := len(out.OtherShows[name])
			out.OtherShows[name] = append(out.OtherShows[name], other)
			byKey[key] = append(byKey[key], namesakeRef{name: name, index: idx})
			if !keySeen[key] {
				keySeen[key] = true
				keys = append(keys, key)
			}
		}
		for _, column := range []string{"title", "original_title"} {
			index := "catalog_titles_type_lower_title"
			if column == "original_title" {
				index = "catalog_titles_type_lower_original"
			}
			// The scalar episode lookup uses catalog_titles_show and never fetches
			// remote metadata. Keeping it in SQL avoids per-match round trips.
			queryArgs := []any{season, episode}
			queryArgs = append(queryArgs, args...)
			rows, err := c.db.QueryContext(ctx, `SELECT s.key, s.`+column+`, s.year, s.language,
                COALESCE((SELECT e.title FROM catalog_titles e`+c.indexedBy("catalog_titles_show")+` WHERE e.show_key=s.key AND e.season=? AND e.episode=? AND e.type='episode' LIMIT 1), '')
                FROM catalog_titles s`+c.indexedBy(index)+` WHERE s.type=? AND LOWER(s.`+column+`) IN (`+strings.TrimSuffix(strings.Repeat("?,", len(variants)), ",")+`) AND s.key<>? LIMIT 16`, queryArgs...)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var key int64
				var title string
				var other core.EpisodeNamesake
				if err = rows.Scan(&key, &title, &other.Year, &other.Language, &other.EpisodeTitle); err != nil {
					break
				}
				if !seen[key] && releaseparse.NormalizeTitle(title) == releaseparse.NormalizeTitle(name) {
					record(key, other)
				}
			}
			scanErr := rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
			if scanErr != nil {
				return out, scanErr
			}
		}
		if norm := releaseparse.NormalizeTitle(name); norm != "" {
			rows, err := c.db.QueryContext(ctx, `SELECT s.key, s.year, s.language,
                COALESCE((SELECT e.title FROM catalog_titles e`+c.indexedBy("catalog_titles_show")+` WHERE e.show_key=s.key AND e.season=? AND e.episode=? AND e.type='episode' LIMIT 1), '')
                FROM catalog_release_names rn JOIN catalog_titles s ON s.key=rn.key
                WHERE rn.name=? AND s.type=? AND s.key<>? LIMIT 16`, season, episode, norm, string(core.TypeShow), show)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var key int64
				var other core.EpisodeNamesake
				if err = rows.Scan(&key, &other.Year, &other.Language, &other.EpisodeTitle); err != nil {
					break
				}
				if !seen[key] {
					record(key, other)
				}
			}
			scanErr := rows.Err()
			rows.Close()
			if err != nil {
				return out, err
			}
			if scanErr != nil {
				return out, scanErr
			}
		}
	}
	if len(byKey) > 0 || show > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
		grows, err := c.db.QueryContext(ctx, `SELECT key, genre FROM catalog_genres WHERE key IN (`+placeholders+`) ORDER BY key, genre`, keys...)
		if err != nil {
			return out, err
		}
		for grows.Next() {
			var key int64
			var genre string
			if err = grows.Scan(&key, &genre); err != nil {
				break
			}
			if key == int64(show) {
				out.Genres = append(out.Genres, genre)
			}
			for _, ref := range byKey[key] {
				out.OtherShows[ref.name][ref.index].Genres = append(out.OtherShows[ref.name][ref.index].Genres, genre)
			}
		}
		gscanErr := grows.Err()
		grows.Close()
		if err != nil {
			return out, err
		}
		if gscanErr != nil {
			return out, gscanErr
		}
		krows, err := c.db.QueryContext(ctx, `SELECT key, keyword FROM catalog_keywords WHERE key IN (`+placeholders+`) AND LOWER(keyword) IN ('anime', 'animation') ORDER BY key, keyword`, keys...)
		if err != nil {
			return out, err
		}
		for krows.Next() {
			var key int64
			var kw string
			if err = krows.Scan(&key, &kw); err != nil {
				break
			}
			if key == int64(show) {
				out.Keywords = append(out.Keywords, kw)
			}
			for _, ref := range byKey[key] {
				out.OtherShows[ref.name][ref.index].Keywords = append(out.OtherShows[ref.name][ref.index].Keywords, kw)
			}
		}
		kscanErr := krows.Err()
		krows.Close()
		if err != nil {
			return out, err
		}
		if kscanErr != nil {
			return out, kscanErr
		}
	}
	rows, err := c.db.QueryContext(ctx, `SELECT season, title FROM catalog_titles`+c.indexedBy("catalog_titles_show")+` WHERE show_key=? AND type='season' ORDER BY season LIMIT 128`, show)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var title string
		if err := rows.Scan(&n, &title); err != nil {
			return out, err
		}
		out.Seasons[n] = title
	}
	if err := rows.Err(); err != nil || season <= 0 {
		return out, err
	}
	// Sibling titles let the resolver reject a release that names another
	// episode of this season (TMDB segment vs broadcast numbering).
	eps, err := c.db.QueryContext(ctx, `SELECT episode, title FROM catalog_titles`+c.indexedBy("catalog_titles_show")+` WHERE show_key=? AND season=? AND type='episode' LIMIT 512`, show, season)
	if err != nil {
		return out, err
	}
	defer eps.Close()
	for eps.Next() {
		var n int
		var title string
		if err := eps.Scan(&n, &title); err != nil {
			return out, err
		}
		if n != episode && title != "" {
			out.Episodes[n] = title
		}
	}
	return out, eps.Err()
}

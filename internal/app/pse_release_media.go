package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

func mediaCategory(key core.RatingKey, t core.Title) string {
	if t.IsAnime() {
		return "anime"
	}
	if key.Kind() == core.KindShow {
		return "show"
	}
	return "movie"
}
func mediaLabel(key core.RatingKey, t core.Title) string {
	if t.Title != "" {
		return t.Title
	}
	return "TMDB " + strconv.FormatInt(key.TMDB(), 10)
}

// The media list never decodes descriptors or performs per-release anti-joins.
// Only catalogue labels needed for a page (or a textual/category filter) are read.
func (a *App) pseReleaseMedia(w http.ResponseWriter, r *http.Request, from, to int64, page int) {
	ctx := r.Context()
	q := r.URL.Query()
	field := "first_seen"
	if q.Get("date") == "lastSeen" {
		field = "last_seen"
	}
	predicate := ""
	args := []any{}
	if from > 0 {
		predicate += " AND " + field + ">=?"
		args = append(args, from)
	}
	if to > 0 {
		predicate += " AND " + field + "<?"
		args = append(args, to)
	}
	query := `SELECT media_key,MIN(first_seen),MAX(last_seen) FROM (
 SELECT ` + browseMediaKey + ` AS media_key,MIN(first_seen) AS first_seen,MAX(last_seen) AS last_seen FROM pse_releases WHERE 1=1` + predicate + ` GROUP BY ` + browseMediaKey + `
 UNION ALL
 SELECT ` + browseMediaKey + `,MIN(first_seen),MAX(last_seen) FROM pse_release_library WHERE 1=1` + predicate + ` GROUP BY ` + browseMediaKey + `
 ) GROUP BY media_key`
	rows, err := a.db.QueryContext(ctx, query, append(args, args...)...)
	fail := func() { http.Error(w, "Media browser unavailable", 500) }
	if err != nil {
		fail()
		return
	}
	type media struct {
		key         core.RatingKey
		first, last int64
	}
	all := []media{}
	keys := []core.RatingKey{}
	for rows.Next() {
		var m media
		if err = rows.Scan(&m.key, &m.first, &m.last); err != nil {
			break
		}
		all = append(all, m)
		keys = append(keys, m.key)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail()
		return
	}
	labels := map[core.RatingKey]core.Title{}
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	category := q.Get("category")
	if search != "" || category != "" {
		if a.catalog != nil {
			labels, err = a.catalog.ReleaseLabels(ctx, keys)
			if err != nil {
				fail()
				return
			}
		}
		filtered := all[:0]
		for _, m := range all {
			t := labels[m.key]
			if category != "" && mediaCategory(m.key, t) != category {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(mediaLabel(m.key, t)+" "+t.OriginalTitle+" "+m.key.String()), search) {
				continue
			}
			filtered = append(filtered, m)
		}
		all = filtered
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		switch q.Get("sort") {
		case "oldest":
			if a.first != b.first {
				return a.first < b.first
			}
		case "indexed":
			if a.first != b.first {
				return a.first > b.first
			}
		default:
			if a.last != b.last {
				return a.last > b.last
			}
		}
		return a.key < b.key
	})
	total := len(all)
	page = min(page, max(0, (total-1)/50))
	selected := all[min(page*50, total):min((page+1)*50, total)]
	if search == "" && category == "" && a.catalog != nil {
		keys = keys[:0]
		for _, m := range selected {
			keys = append(keys, m.key)
		}
		labels, err = a.catalog.ReleaseLabels(ctx, keys)
		if err != nil {
			fail()
			return
		}
	}
	items := []browseItem{}
	for _, m := range selected {
		items = append(items, browseItem{Key: m.key.String(), Title: mediaLabel(m.key, labels[m.key]), Category: mediaCategory(m.key, labels[m.key]), FirstSeen: m.first, LastSeen: m.last})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "items": items, "total": total, "page": page})
}

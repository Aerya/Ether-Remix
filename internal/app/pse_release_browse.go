package app

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Canonical media grouping folds episodes into their parent series.
const browseMediaKey = `CASE WHEN title_key/1000000000000000=4 THEN 2000000000000000+(title_key%1000000000000000)/10000000 ELSE title_key END`
const browseRows = `WITH releases AS (
 SELECT rowid AS rid,title_key, ` + browseMediaKey + ` AS media_key,
 json_extract(browse,'$.name') AS name,json_extract(browse,'$.source') AS source,
 first_seen,last_seen,1 AS retained
 FROM pse_release_library WHERE browse<>'' AND browse<>'{}'
 UNION ALL
 SELECT rowid,CASE WHEN title_key/1000000000000000=2 AND episode>0 THEN 4000000000000000+(title_key%1000000000000000)*10000000+season*10000+episode ELSE title_key END,
 ` + browseMediaKey + `,name,source_name,
 first_seen,last_seen,0 FROM pse_releases m
 WHERE NOT EXISTS(SELECT 1 FROM pse_release_library l
 WHERE l.title_key=CASE WHEN m.title_key/1000000000000000=2 AND m.episode>0 THEN 4000000000000000+(m.title_key%1000000000000000)*10000000+m.season*10000+m.episode ELSE m.title_key END
 AND json_extract(l.browse,'$.name')=m.name AND json_extract(l.browse,'$.source')=m.source_name AND l.browse<>'' AND l.browse<>'{}')
), filtered AS (SELECT * FROM releases WHERE media_key IN (SELECT value FROM json_each(?))
 AND (?='' OR instr(lower(name||' '||source),?)>0)
 AND (?=0 OR first_seen>=?) AND (?=0 OR first_seen<?)
 AND (?=0 OR last_seen>=?) AND (?=0 OR last_seen<?)
), matched AS (SELECT * FROM filtered) `

type browseItem struct {
	Key       string      `json:"key"`
	Title     string      `json:"title"`
	Category  string      `json:"category"`
	Count     int64       `json:"count"`
	FirstSeen int64       `json:"firstSeen"`
	LastSeen  int64       `json:"lastSeen"`
	Release   *libraryRow `json:"release,omitempty"`
}

func (a *App) pseReleaseBrowse(w http.ResponseWriter, r *http.Request) {
	if !releasedb.LibraryEnabled() {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	fail := func() { http.Error(w, "Release browser unavailable", 500) }
	pending, err := a.releaseLibrary().PrepareBrowse(ctx)
	if err != nil {
		fail()
		return
	}
	if pending {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "preparing": true, "items": []browseItem{}})
		return
	}
	mode := q.Get("mode")
	if mode != "" && mode != "media" && mode != "releases" {
		http.Error(w, "Invalid mode", 400)
		return
	}
	category := q.Get("category")
	if category != "" && category != "movie" && category != "show" && category != "anime" {
		http.Error(w, "Invalid category", 400)
		return
	}
	from, to := int64(0), int64(0)
	for _, field := range []string{"from", "to"} {
		if q.Get(field) != "" {
			v, e := strconv.ParseInt(q.Get(field), 10, 64)
			if e != nil || v < 0 {
				http.Error(w, "Invalid period", 400)
				return
			}
			if field == "from" {
				from = v
			} else {
				to = v
			}
		}
	}
	if to != 0 && from >= to {
		http.Error(w, "Invalid period", 400)
		return
	}
	dateField := q.Get("date")
	if dateField != "" && dateField != "firstSeen" && dateField != "lastSeen" {
		http.Error(w, "Invalid date", 400)
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(0, min(page, 1000000))
	keysRows, err := a.db.QueryContext(ctx, `SELECT DISTINCT `+browseMediaKey+` FROM (SELECT title_key FROM pse_releases UNION SELECT title_key FROM pse_release_library)`)
	if err != nil {
		fail()
		return
	}
	keys := []core.RatingKey{}
	for keysRows.Next() {
		var k core.RatingKey
		if err = keysRows.Scan(&k); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = keysRows.Err()
	}
	keysRows.Close()
	if err != nil {
		fail()
		return
	}
	labels := map[core.RatingKey]core.Title{}
	if a.catalog != nil {
		labels, err = a.catalog.ReleaseLabels(ctx, keys)
		if err != nil {
			fail()
			return
		}
	}
	query := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if len(query) > 300 {
		http.Error(w, "Query too long", 400)
		return
	}
	allowed := []core.RatingKey{}
	categories := map[core.RatingKey]string{}
	for _, k := range keys {
		if q.Get("media") != "" && q.Get("media") != k.String() {
			continue
		}
		t := labels[k]
		if t.Title == "" {
			t.Title = "TMDB " + strconv.FormatInt(k.TMDB(), 10)
			labels[k] = t
		}
		cat := "movie"
		if k.Kind() == core.KindShow {
			cat = "show"
		}
		if t.IsAnime() {
			cat = "anime"
		}
		categories[k] = cat
		if category != "" && category != cat {
			continue
		}
		if mode != "releases" && query != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.OriginalTitle+" "+k.String()), query) {
			continue
		}
		allowed = append(allowed, k)
	}
	raw, _ := json.Marshal(allowed)
	rlzQuery := ""
	if mode == "releases" {
		rlzQuery = query
	}
	firstFrom, firstTo, lastFrom, lastTo := from, to, int64(0), int64(0)
	if dateField == "lastSeen" {
		firstFrom, firstTo, lastFrom, lastTo = 0, 0, from, to
	}
	args := []any{string(raw), rlzQuery, rlzQuery, firstFrom, firstFrom, firstTo, firstTo, lastFrom, lastFrom, lastTo, lastTo}
	countSQL := `SELECT COUNT(DISTINCT media_key) FROM filtered`
	if mode == "releases" {
		countSQL = `SELECT COUNT(*) FROM matched`
	}
	var total int64
	if err = a.db.QueryRowContext(ctx, browseRows+countSQL, args...).Scan(&total); err != nil {
		fail()
		return
	}
	page = min(page, max(0, int((total-1)/50)))
	order := "last_seen DESC"
	if q.Get("sort") == "oldest" {
		order = "first_seen ASC"
	} else if q.Get("sort") == "indexed" {
		order = "first_seen DESC"
	}
	selectSQL := `SELECT media_key,COUNT(*),MIN(first_seen) AS first_seen,MAX(last_seen) AS last_seen FROM matched GROUP BY media_key ORDER BY ` + order + `,media_key LIMIT 50 OFFSET ?`
	if mode == "releases" {
		selectSQL = `SELECT p.media_key,p.title_key,CASE WHEN p.retained=1 THEN l.browse ELSE
 json_object('name',m.name,'source',m.source_name,'protocol',m.protocol,'size',m.size,'height',m.height,'videoCodec',m.video_codec,'audioCodec',m.audio_codec,'languages',m.languages,'cached',m.debrid_cached,'published',0) END,
 p.first_seen,p.last_seen,COALESCE(l.expires,0),COALESCE(l.failed,0),COALESCE(l.source_signature,''),p.retained
 FROM (SELECT * FROM matched ORDER BY ` + order + `,title_key,name,source LIMIT 50 OFFSET ?) p
 LEFT JOIN pse_release_library l ON p.retained=1 AND l.rowid=p.rid
 LEFT JOIN pse_releases m ON p.retained=0 AND m.rowid=p.rid`
	}
	rows, err := a.db.QueryContext(ctx, browseRows+selectSQL, append(args, page*50)...)
	if err != nil {
		fail()
		return
	}
	items := []browseItem{}
	signature := a.releaseLibrary().Signature
	for rows.Next() {
		var item browseItem
		var media, key core.RatingKey
		if mode == "releases" {
			var raw, sig string
			var expires int64
			var failed, retained bool
			err = rows.Scan(&media, &key, &raw, &item.FirstSeen, &item.LastSeen, &expires, &failed, &sig, &retained)
			if err != nil {
				break
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal([]byte(raw), &fields); err != nil {
				break
			}
			// Legacy metadata stores comma-separated language/cache lists.
			for _, f := range []string{"languages", "cached"} {
				var s string
				if json.Unmarshal(fields[f], &s) == nil {
					list := []string{}
					if s != "" {
						list = strings.Split(s, ",")
					}
					fields[f], _ = json.Marshal(list)
				}
			}
			rawBytes, _ := json.Marshal(fields)
			rel := libraryRow{}
			if err = json.Unmarshal(rawBytes, &rel); err != nil {
				break
			}
			rel.FirstSeen, rel.LastSeen, rel.State = item.FirstSeen, item.LastSeen, "metadata"
			if retained {
				rel.State = "ready"
				if sig != signature {
					rel.State = "source_changed"
				} else if failed {
					rel.State = "failed"
				} else if expires <= time.Now().Unix() {
					rel.State = "expired"
				}
			}
			item.Release = &rel
			item.Key = key.String()
			item.Count = 1
		} else {
			err = rows.Scan(&media, &item.Count, &item.FirstSeen, &item.LastSeen)
			if err != nil {
				break
			}
			item.Key = media.String()
		}
		item.Title = labels[media].Title
		item.Category = categories[media]
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail()
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "items": items, "total": total, "page": page, "preparing": false})
}

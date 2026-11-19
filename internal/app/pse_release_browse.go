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

// Apply filters before taking each source's top rows. This lets SQLite walk
// date indexes instead of sorting the complete UNION for a 50-row page.
func releaseBrowseSQL(media core.RatingKey, allowed []core.RatingKey, restricted bool, query, dateField, sortOrder string, from, to int64, limit int) (string, []any) {
	metadataKey := `CASE WHEN title_key/1000000000000000=2 AND episode>0 THEN 4000000000000000+(title_key%1000000000000000)*10000000+season*10000+episode ELSE title_key END`
	common := ""
	commonArgs := []any{}
	if media != 0 {
		common += " AND (" + browseMediaKey + ")=?"
		commonArgs = append(commonArgs, int64(media))
	}
	if restricted {
		raw, _ := json.Marshal(allowed)
		common += " AND (" + browseMediaKey + ") IN (SELECT value FROM json_each(?))"
		commonArgs = append(commonArgs, string(raw))
	}
	field := "first_seen"
	if dateField == "lastSeen" {
		field = "last_seen"
	}
	if from > 0 {
		common += " AND " + field + ">=?"
		commonArgs = append(commonArgs, from)
	}
	if to > 0 {
		common += " AND " + field + "<?"
		commonArgs = append(commonArgs, to)
	}
	makeArm := func(retained bool) (string, []any) {
		name, source, table, key, kind := "name", "source_name", "pse_releases", metadataKey, "0"
		where := "1=1"
		if retained {
			name = "json_extract(browse,'$.name')"
			source = "json_extract(browse,'$.source')"
			table = "pse_release_library"
			key = "title_key"
			kind = "1"
			where = "browse<>'' AND browse<>'{}'"
		}
		where += common
		args := append([]any{}, commonArgs...)
		if query != "" {
			where += " AND instr(lower(" + name + "||' '||" + source + "),?)>0"
			args = append(args, query)
		}
		if !retained {
			where += ` AND NOT EXISTS(SELECT 1 FROM pse_release_library l WHERE l.title_key=CASE WHEN m.title_key/1000000000000000=2 AND m.episode>0 THEN 4000000000000000+(m.title_key%1000000000000000)*10000000+m.season*10000+m.episode ELSE m.title_key END AND json_extract(l.browse,'$.name')=m.name AND json_extract(l.browse,'$.source')=m.source_name AND l.browse<>'' AND l.browse<>'{}'`
			if from > 0 {
				where += " AND l." + field + ">=?"
				args = append(args, from)
			}
			if to > 0 {
				where += " AND l." + field + "<?"
				args = append(args, to)
			}
			where += ")"
		}
		indexHint := ""
		// Browse recent rows in date order, including category filters. A global
		// substring search instead scans sequentially: a date-index walk fetches
		// every table row randomly when only a few names match.
		if media == 0 && limit > 0 && query == "" {
			suffix := "_browse_last"
			if strings.HasPrefix(sortOrder, "first_seen") {
				suffix = "_browse_first"
			}
			indexHint = " INDEXED BY " + table + suffix
		} else if media == 0 && !restricted && query != "" && from == 0 && to == 0 {
			indexHint = " NOT INDEXED"
		}
		sql := `SELECT rowid AS rid,title_key AS sort_key,` + key + ` AS title_key,` + browseMediaKey + ` AS media_key,` + name + ` AS name,` + source + ` AS source,first_seen,last_seen,` + kind + ` AS retained FROM ` + table + ` m` + indexHint + ` WHERE ` + where
		if limit > 0 {
			sql += " ORDER BY " + sortOrder + ",sort_key,rid LIMIT ?"
			args = append(args, limit)
		}
		return sql, args
	}
	left, leftArgs := makeArm(true)
	right, rightArgs := makeArm(false)
	return `WITH matched AS (SELECT * FROM (` + left + `) UNION ALL SELECT * FROM (` + right + `)) `, append(leftArgs, rightArgs...)
}

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
	query := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if len(query) > 300 {
		http.Error(w, "Query too long", 400)
		return
	}
	if mode != "releases" {
		a.pseReleaseMedia(w, r, from, to, page)
		return
	}
	var prepareKey core.RatingKey
	if value := q.Get("media"); value != "" {
		key, e := core.ParseRatingKey(value)
		if e != nil || (key.Kind() != core.KindMovie && key.Kind() != core.KindShow) {
			http.Error(w, "Invalid media", 400)
			return
		}
		prepareKey = key
	}
	pending, err := a.releaseLibrary().PrepareBrowseMedia(ctx, prepareKey)
	if err != nil {
		fail()
		return
	}
	if pending {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "preparing": true, "items": []browseItem{}})
		return
	}

	keys := []core.RatingKey{}
	mediaFilter := q.Get("media")
	if mediaFilter != "" {
		key, e := core.ParseRatingKey(mediaFilter)
		if e != nil || (key.Kind() != core.KindMovie && key.Kind() != core.KindShow) {
			http.Error(w, "Invalid media", 400)
			return
		}
		keys = append(keys, key)
	} else if category != "" {
		keysRows, err := a.db.QueryContext(ctx, `SELECT DISTINCT `+browseMediaKey+` FROM (SELECT title_key FROM pse_releases UNION SELECT title_key FROM pse_release_library)`)
		if err != nil {
			fail()
			return
		}
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

	}
	labels := map[core.RatingKey]core.Title{}
	if a.catalog != nil {
		labels, err = a.catalog.ReleaseLabels(ctx, keys)
		if err != nil {
			fail()
			return
		}
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

	order := "last_seen DESC"
	if q.Get("sort") == "oldest" {
		order = "first_seen ASC"
	} else if q.Get("sort") == "indexed" {
		order = "first_seen DESC"
	}
	var total int64
	if mediaFilter != "" {
		sql, args := releaseBrowseSQL(prepareKey, allowed, category != "", query, dateField, order, from, to, 0)
		if err = a.db.QueryRowContext(ctx, sql+`SELECT COUNT(*) FROM matched`, args...).Scan(&total); err != nil {
			fail()
			return
		}
		page = min(page, max(0, int((total-1)/50)))
	}
	sqlRows, args := releaseBrowseSQL(prepareKey, allowed, category != "", query, dateField, order, from, to, (page+1)*50+1)
	selectSQL := `SELECT p.media_key,p.title_key,CASE WHEN p.retained=1 THEN l.browse ELSE
 json_object('name',m.name,'source',m.source_name,'protocol',m.protocol,'size',m.size,'height',m.height,'videoCodec',m.video_codec,'audioCodec',m.audio_codec,'languages',m.languages,'cached',m.debrid_cached,'published',0) END,
 p.first_seen,p.last_seen,COALESCE(l.expires,0),COALESCE(l.failed,0),COALESCE(l.source_signature,''),p.retained,COALESCE(l.payload,'')
 FROM (SELECT * FROM matched ORDER BY ` + order + `,sort_key,rid,retained LIMIT 51 OFFSET ?) p
 LEFT JOIN pse_release_library l ON p.retained=1 AND l.rowid=p.rid
 LEFT JOIN pse_releases m ON p.retained=0 AND m.rowid=p.rid`
	rows, err := a.db.QueryContext(ctx, sqlRows+selectSQL, append(args, page*50)...)
	if err != nil {
		fail()
		return
	}
	items := []browseItem{}
	payloads := []string{}
	library := a.releaseLibrary()
	signature := library.Signature
	for rows.Next() {
		var item browseItem
		var media, key core.RatingKey
		var raw, sig, payload string
		var expires int64
		var failed, retained bool
		err = rows.Scan(&media, &key, &raw, &item.FirstSeen, &item.LastSeen, &expires, &failed, &sig, &retained, &payload)
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
		item.Title = mediaLabel(media, labels[media])
		item.Category = categories[media]
		items = append(items, item)
		payloads = append(payloads, payload)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail()
		return
	}
	// Selection may read catalogue tables; close the browse cursor first.
	for i, payload := range payloads {
		if payload == "" {
			continue
		}
		raw, e := library.Open(payload)
		if e != nil {
			fail()
			return
		}
		var rel core.Release
		if e = json.Unmarshal([]byte(raw), &rel); e != nil {
			fail()
			return
		}
		key, _ := core.ParseRatingKey(items[i].Key)
		accepted, e := a.acceptReference(ctx, key, rel)
		if e != nil {
			fail()
			return
		}
		if !accepted {
			items[i].Release.State = "excluded"
		}
	}
	hasMore := len(items) > 50
	if hasMore {
		items = items[:50]
	}
	if a.catalog != nil && category == "" && mediaFilter == "" {
		pageKeys := []core.RatingKey{}
		for _, item := range items {
			k, _ := core.ParseRatingKey(item.Key)
			if k.Kind() == core.KindEpisode {
				k = core.ShowKey(k.TMDB())
			}
			pageKeys = append(pageKeys, k)
		}
		pageLabels, e := a.catalog.ReleaseLabels(ctx, pageKeys)
		if e != nil {
			fail()
			return
		}
		for i, k := range pageKeys {
			items[i].Title = mediaLabel(k, pageLabels[k])
			items[i].Category = mediaCategory(k, pageLabels[k])
		}
	}
	if mediaFilter == "" {
		total = -1
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "items": items, "total": total, "page": page, "hasMore": hasMore, "preparing": false})
}

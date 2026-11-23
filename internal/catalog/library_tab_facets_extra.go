package catalog

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const catalogFacetCountCap = 25001

type catalogBandSpec struct {
	id       string
	name     string
	sqlCond  string
	minFloat float64
	maxFloat float64
	maxExcl  bool
	minSec   int64
	maxSec   int64
}

var catalogRatingBands = []catalogBandSpec{
	{id: "9-10", name: "9+", sqlCond: "t.rating >= 9.0 AND t.rating <= 10.0", minFloat: 9.0, maxFloat: 10.0, maxExcl: false},
	{id: "8-8.9", name: "8–8.9", sqlCond: "t.rating >= 8.0 AND t.rating < 9.0", minFloat: 8.0, maxFloat: 9.0, maxExcl: true},
	{id: "7-7.9", name: "7–7.9", sqlCond: "t.rating >= 7.0 AND t.rating < 8.0", minFloat: 7.0, maxFloat: 8.0, maxExcl: true},
	{id: "6-6.9", name: "6–6.9", sqlCond: "t.rating >= 6.0 AND t.rating < 7.0", minFloat: 6.0, maxFloat: 7.0, maxExcl: true},
	{id: "under-6", name: "Under 6", sqlCond: "t.rating > 0 AND t.rating < 6.0", minFloat: 0, maxFloat: 6.0, maxExcl: true},
}

var catalogRuntimeBands = []catalogBandSpec{
	{id: "under-30m", name: "Under 30m", sqlCond: "t.runtime_sec >= 1 AND t.runtime_sec <= 1799", minSec: 1, maxSec: 1799},
	{id: "30-60m", name: "30–60m", sqlCond: "t.runtime_sec >= 1800 AND t.runtime_sec <= 3599", minSec: 1800, maxSec: 3599},
	{id: "60-90m", name: "1–1.5h", sqlCond: "t.runtime_sec >= 3600 AND t.runtime_sec <= 5399", minSec: 3600, maxSec: 5399},
	{id: "90-120m", name: "1.5–2h", sqlCond: "t.runtime_sec >= 5400 AND t.runtime_sec <= 7199", minSec: 5400, maxSec: 7199},
	{id: "120-180m", name: "2–3h", sqlCond: "t.runtime_sec >= 7200 AND t.runtime_sec <= 10799", minSec: 7200, maxSec: 10799},
	{id: "180m-plus", name: "3h+", sqlCond: "t.runtime_sec >= 10800", minSec: 10800, maxSec: 0},
}

var catalogResolutionBands = []catalogBandSpec{
	{id: "4k", name: "4K", sqlCond: "ev.height >= 2160"},
	{id: "1080p", name: "1080p", sqlCond: "ev.height >= 1080 AND ev.height < 2160"},
	{id: "720p", name: "720p", sqlCond: "ev.height >= 720 AND ev.height < 1080"},
	{id: "sd", name: "SD", sqlCond: "ev.height > 0 AND ev.height < 720"},
}

var catalogHDRBands = []catalogBandSpec{
	{id: "dv", name: "Dolby Vision", sqlCond: "ev.dv = 1"},
	{id: "hdr10", name: "HDR10", sqlCond: "ev.hdr = 1 AND ev.dv = 0"},
	{id: "sdr", name: "SDR", sqlCond: "ev.height > 0 AND ev.hdr = 0 AND ev.dv = 0"},
}

// ParseCatalogRatingBand maps a canonical rating band ID onto (min, max, maxExclusive).
func ParseCatalogRatingBand(id string) (minR, maxR float64, maxExclusive, ok bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, b := range catalogRatingBands {
		if b.id == id {
			return b.minFloat, b.maxFloat, b.maxExcl, true
		}
	}
	return 0, 0, false, false
}

// ParseCatalogRuntimeBand maps a canonical runtime band ID onto (minSec, maxSec).
func ParseCatalogRuntimeBand(id string) (minSec, maxSec int64, ok bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, b := range catalogRuntimeBands {
		if b.id == id {
			return b.minSec, b.maxSec, true
		}
	}
	return 0, 0, false
}

// ParseCatalogAddedMonth parses a canonical "YYYY-MM" bucket ID into inclusive UTC Unix bounds.
func ParseCatalogAddedMonth(id string) (fromTS, toTS int64, ok bool) {
	id = strings.TrimSpace(id)
	if len(id) != 7 || id[4] != '-' {
		return 0, 0, false
	}
	t, err := time.Parse("2006-01", id)
	if err != nil {
		return 0, 0, false
	}
	y, m, _ := t.UTC().Date()
	if y < 1970 || y > 9999 {
		return 0, 0, false
	}
	start := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0).Add(-time.Second)
	return start.Unix(), end.Unix(), true
}

var catalogLanguageLabels = map[string]string{
	"af":    "Afrikaans",
	"am":    "Amharic",
	"ar":    "Arabic",
	"az":    "Azerbaijani",
	"bg":    "Bulgarian",
	"bn":    "Bengali",
	"bs":    "Bosnian",
	"ca":    "Catalan",
	"cn":    "Cantonese",
	"cs":    "Czech",
	"cy":    "Welsh",
	"da":    "Danish",
	"de":    "German",
	"el":    "Greek",
	"en":    "English",
	"eo":    "Esperanto",
	"es":    "Spanish",
	"et":    "Estonian",
	"eu":    "Basque",
	"fa":    "Persian",
	"fi":    "Finnish",
	"fr":    "French",
	"ga":    "Irish",
	"gl":    "Galician",
	"gu":    "Gujarati",
	"he":    "Hebrew",
	"hi":    "Hindi",
	"hr":    "Croatian",
	"hu":    "Hungarian",
	"hy":    "Armenian",
	"id":    "Indonesian",
	"is":    "Icelandic",
	"it":    "Italian",
	"ja":    "Japanese",
	"ka":    "Georgian",
	"kk":    "Kazakh",
	"km":    "Khmer",
	"kn":    "Kannada",
	"ko":    "Korean",
	"la":    "Latin",
	"lo":    "Lao",
	"lt":    "Lithuanian",
	"lv":    "Latvian",
	"mk":    "Macedonian",
	"ml":    "Malayalam",
	"mn":    "Mongolian",
	"mr":    "Marathi",
	"ms":    "Malay",
	"my":    "Burmese",
	"nb":    "Norwegian Bokmål",
	"ne":    "Nepali",
	"nl":    "Dutch",
	"nn":    "Norwegian Nynorsk",
	"no":    "Norwegian",
	"pa":    "Punjabi",
	"pl":    "Polish",
	"pt":    "Portuguese",
	"pt-br": "Portuguese (Brazil)",
	"pt-pt": "Portuguese (Portugal)",
	"ro":    "Romanian",
	"ru":    "Russian",
	"sk":    "Slovak",
	"sl":    "Slovenian",
	"sq":    "Albanian",
	"sr":    "Serbian",
	"sv":    "Swedish",
	"sw":    "Swahili",
	"ta":    "Tamil",
	"te":    "Telugu",
	"th":    "Thai",
	"tl":    "Filipino",
	"tr":    "Turkish",
	"uk":    "Ukrainian",
	"ur":    "Urdu",
	"uz":    "Uzbek",
	"vi":    "Vietnamese",
	"zh":    "Chinese",
	"zh-cn": "Chinese (Simplified)",
	"zh-hk": "Cantonese",
	"zh-tw": "Chinese (Traditional)",
	"zu":    "Zulu",
}

func catalogLanguageDisplayName(code string) string {
	norm := strings.ToLower(strings.TrimSpace(code))
	if label, ok := catalogLanguageLabels[norm]; ok {
		return label
	}
	if base, _, ok := strings.Cut(norm, "-"); ok {
		if label, found := catalogLanguageLabels[base]; found {
			return label + " (" + strings.ToUpper(strings.TrimPrefix(norm, base+"-")) + ")"
		}
	}
	if norm == "" {
		return ""
	}
	return strings.ToUpper(norm)
}

// HasStudioData reports whether the scoped library has at least one studio/network
// entry, using precomputed tallies or bounded index lookups so large libraries
// never scan catalog_titles.
func (c *Catalog) HasStudioData(ctx context.Context, f core.Filter) bool {
	where, args, err := c.browseWhere(f)
	if err != nil {
		return false
	}
	if vals, ok, tErr := c.facetFromTally(ctx, f, "studio", where, args); tErr == nil && ok {
		for _, v := range vals {
			if v.Key != "" && v.Count > 0 {
				return true
			}
		}
		return false
	}
	small, err := c.libraryHasAtMostTitles(ctx, where, args, 5000)
	if err != nil {
		return false
	}
	var one int
	if small {
		if err := c.db.QueryRowContext(ctx,
			`SELECT 1 FROM catalog_titles t JOIN catalog_studios st ON st.key = t.key WHERE (`+where+`) AND st.studio <> '' LIMIT 1`,
			args...,
		).Scan(&one); err == nil {
			return true
		}
		return false
	}
	if err := c.db.QueryRowContext(ctx,
		`SELECT 1 FROM (SELECT studio FROM catalog_studio_names WHERE titles > 0 ORDER BY titles DESC LIMIT 64) sn
		 WHERE EXISTS (
			SELECT 1 FROM catalog_studios st
			JOIN catalog_titles t ON t.key = st.key
			WHERE st.studio = sn.studio AND (`+where+`)
		 ) LIMIT 1`,
		args...,
	).Scan(&one); err == nil {
		return true
	}
	return false
}

func isExtraCatalogTabFacet(storageFacet string) bool {
	switch storageFacet {
	case "director", "writer", "language", "contentRating", "tag", "keyword",
		"resolution", "hdr", "audioLanguage", "rating", "runtime", "addedMonth":
		return true
	default:
		return false
	}
}

func (c *Catalog) libraryFacetCollectionsExtra(
	ctx context.Context,
	f core.Filter,
	storageFacet string,
	orderByName bool,
	rawQuery, foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	switch storageFacet {
	case "director", "writer":
		peopleOrder := "count"
		if orderByName {
			peopleOrder = "name"
		}
		pPage, err := c.LibraryFacetPeople(ctx, core.CatalogFacetPeopleQuery{
			Filter: f,
			Facet:  storageFacet,
			Order:  peopleOrder,
			Query:  rawQuery,
			Offset: offset,
			Limit:  limit,
		})
		if err != nil {
			return nil, false, err
		}
		out := make([]core.CatalogFacetCollection, 0, len(pPage.Items))
		for _, p := range pPage.Items {
			if p.PersonTMDB <= 0 || p.Name == "" || p.ItemCount <= 0 {
				continue
			}
			out = append(out, core.CatalogFacetCollection{
				ID:        strconv.FormatInt(p.PersonTMDB, 10),
				Name:      p.Name,
				ItemCount: p.ItemCount,
			})
		}
		return out, pPage.More, nil

	case "language":
		return c.libraryFacetTitleColumn(ctx, f, "language", catalogLanguageDisplayName, orderByName, foldedQuery, offset, limit, where, args)

	case "contentRating":
		return c.libraryFacetTitleColumn(ctx, f, "content_rating", func(v string) string { return v }, orderByName, foldedQuery, offset, limit, where, args)

	case "tag":
		return c.libraryFacetOwnerTags(ctx, orderByName, rawQuery, foldedQuery, offset, limit, where, args)

	case "keyword":
		return c.libraryFacetKeywords(ctx, orderByName, rawQuery, foldedQuery, offset, limit, where, args)

	case "resolution":
		return c.libraryFacetEvidenceBands(ctx, catalogResolutionBands, orderByName, foldedQuery, offset, limit, where, args)

	case "hdr":
		return c.libraryFacetEvidenceBands(ctx, catalogHDRBands, orderByName, foldedQuery, offset, limit, where, args)

	case "audioLanguage":
		return c.libraryFacetAudioLanguages(ctx, orderByName, foldedQuery, offset, limit, where, args)

	case "rating":
		return c.libraryFacetTitleBands(ctx, catalogRatingBands, orderByName, foldedQuery, offset, limit, where, args)

	case "runtime":
		return c.libraryFacetTitleBands(ctx, catalogRuntimeBands, orderByName, foldedQuery, offset, limit, where, args)

	case "addedMonth":
		return c.libraryFacetAddedMonths(ctx, orderByName, foldedQuery, offset, limit, where, args)
	}
	return []core.CatalogFacetCollection{}, false, nil
}

func sliceCatalogFacetPage(all []core.CatalogFacetCollection, offset, limit int) ([]core.CatalogFacetCollection, bool) {
	if offset >= len(all) {
		return []core.CatalogFacetCollection{}, false
	}
	end := min(offset+limit, len(all))
	return all[offset:end], end < len(all)
}

func matchesFoldedFacetQuery(foldedQuery string, candidates ...string) bool {
	if foldedQuery == "" {
		return true
	}
	for _, cand := range candidates {
		if cand != "" && strings.Contains(foldPersonQueryTerm(cand), foldedQuery) {
			return true
		}
	}
	return false
}

func (c *Catalog) scopedTitleTypes(f core.Filter) []string {
	if f.Type == core.TypeMovie || f.Type == core.TypeShow {
		return []string{string(f.Type)}
	}
	return []string{"movie", "show"}
}

func (c *Catalog) libraryFacetTitleColumn(
	ctx context.Context,
	f core.Filter,
	col string,
	labelFn func(string) string,
	orderByName bool,
	foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	small, err := c.libraryHasAtMostTitles(ctx, where, args, 5000)
	if err != nil {
		return nil, false, err
	}
	if small {
		rows, qErr := c.db.QueryContext(ctx,
			`SELECT t.`+col+`, COUNT(*) AS cnt FROM catalog_titles t WHERE (`+where+`) AND t.`+col+` <> '' GROUP BY t.`+col,
			args...,
		)
		if qErr != nil {
			return nil, false, qErr
		}
		defer rows.Close()
		var all []core.CatalogFacetCollection
		for rows.Next() {
			var v string
			var cnt int
			if sErr := rows.Scan(&v, &cnt); sErr != nil {
				return nil, false, sErr
			}
			v = strings.TrimSpace(v)
			if v == "" || cnt <= 0 {
				continue
			}
			name := labelFn(v)
			if !matchesFoldedFacetQuery(foldedQuery, name, v) {
				continue
			}
			all = append(all, core.CatalogFacetCollection{ID: v, Name: name, ItemCount: cnt})
		}
		if rErr := rows.Err(); rErr != nil {
			return nil, false, rErr
		}
		sortCatalogFacetCollections(all, orderByName)
		out, more := sliceCatalogFacetPage(all, offset, limit)
		return out, more, nil
	}

	seen := make(map[string]bool)
	var distinctVals []string
	skipSQL := `WITH RECURSIVE d(v) AS (
		SELECT (SELECT ` + col + ` FROM catalog_titles WHERE type = ? AND browsable = 1 AND adult = 0 AND ` + col + ` > '' ORDER BY ` + col + ` ASC LIMIT 1)
		UNION ALL
		SELECT (SELECT ` + col + ` FROM catalog_titles WHERE type = ? AND browsable = 1 AND adult = 0 AND ` + col + ` > d.v ORDER BY ` + col + ` ASC LIMIT 1)
		FROM d WHERE d.v IS NOT NULL
	)
	SELECT v FROM d WHERE v IS NOT NULL LIMIT 300`
	for _, typ := range c.scopedTitleTypes(f) {
		rows, qErr := c.db.QueryContext(ctx, skipSQL, typ, typ)
		if qErr != nil {
			return nil, false, qErr
		}
		for rows.Next() {
			var v string
			if sErr := rows.Scan(&v); sErr != nil {
				rows.Close()
				return nil, false, sErr
			}
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			name := labelFn(v)
			if matchesFoldedFacetQuery(foldedQuery, name, v) {
				distinctVals = append(distinctVals, v)
			}
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return nil, false, rErr
		}
		rows.Close()
	}
	if len(distinctVals) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	var all []core.CatalogFacetCollection
	const batchSize = 24
	for start := 0; start < len(distinctVals); start += batchSize {
		chunk := distinctVals[start:min(start+batchSize, len(distinctVals))]
		branches := make([]string, 0, len(chunk))
		qArgs := make([]any, 0, len(chunk)*(len(args)+3))
		for _, v := range chunk {
			branches = append(branches, `SELECT ? AS v, (SELECT COUNT(*) FROM (SELECT 1 FROM catalog_titles t WHERE (`+where+`) AND t.`+col+` = ? LIMIT ?) s) AS cnt`)
			qArgs = append(qArgs, v)
			qArgs = append(qArgs, args...)
			qArgs = append(qArgs, v, catalogFacetCountCap)
		}
		rows, qErr := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
		if qErr != nil {
			return nil, false, qErr
		}
		for rows.Next() {
			var v string
			var cnt int
			if sErr := rows.Scan(&v, &cnt); sErr != nil {
				rows.Close()
				return nil, false, sErr
			}
			if cnt > 0 {
				all = append(all, core.CatalogFacetCollection{
					ID:        v,
					Name:      labelFn(v),
					ItemCount: cnt,
				})
			}
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return nil, false, rErr
		}
		rows.Close()
	}
	sortCatalogFacetCollections(all, orderByName)
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetOwnerTags(
	ctx context.Context,
	orderByName bool,
	rawQuery, foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	small, err := c.libraryHasAtMostTitles(ctx, where, args, 5000)
	if err != nil {
		return nil, false, err
	}
	if small {
		qArgs := make([]any, 0, len(args)+4)
		qArgs = append(qArgs, args...)
		extra := ""
		if rawQuery != "" {
			extra = ` AND LOWER(ot.tag) LIKE ? ESCAPE '\'`
			qArgs = append(qArgs, "%"+escapeLike(strings.ToLower(rawQuery))+"%")
		}
		orderClause := `cnt DESC, LOWER(ot.tag) ASC, ot.tag ASC`
		if orderByName {
			orderClause = `LOWER(ot.tag) ASC, ot.tag ASC`
		}
		qArgs = append(qArgs, limit+1, offset)
		rows, qErr := c.db.QueryContext(ctx,
			`SELECT ot.tag, ot.tag, COUNT(*) AS cnt
			 FROM catalog_owner_tags ot
			 JOIN catalog_titles t ON t.key = ot.key
			 WHERE (`+where+`) AND ot.tag <> ''`+extra+`
			 GROUP BY ot.tag
			 ORDER BY `+orderClause+`
			 LIMIT ? OFFSET ?`,
			qArgs...,
		)
		if qErr != nil {
			return nil, false, qErr
		}
		defer rows.Close()
		var out []core.CatalogFacetCollection
		for rows.Next() {
			var it core.CatalogFacetCollection
			if sErr := rows.Scan(&it.ID, &it.Name, &it.ItemCount); sErr != nil {
				return nil, false, sErr
			}
			if it.ID != "" && it.ItemCount > 0 {
				out = append(out, it)
			}
		}
		if rErr := rows.Err(); rErr != nil {
			return nil, false, rErr
		}
		more := false
		if len(out) > limit {
			out = out[:limit]
			more = true
		}
		return out, more, nil
	}

	rows, err := c.db.QueryContext(ctx, `WITH RECURSIVE d(v) AS (
		SELECT (SELECT tag FROM catalog_owner_tags WHERE tag > '' ORDER BY tag ASC LIMIT 1)
		UNION ALL
		SELECT (SELECT tag FROM catalog_owner_tags WHERE tag > d.v ORDER BY tag ASC LIMIT 1)
		FROM d WHERE d.v IS NOT NULL
	)
	SELECT v FROM d WHERE v IS NOT NULL LIMIT 500`)
	if err != nil {
		return nil, false, err
	}
	var tags []string
	for rows.Next() {
		var tag string
		if sErr := rows.Scan(&tag); sErr != nil {
			rows.Close()
			return nil, false, sErr
		}
		tag = strings.TrimSpace(tag)
		if tag != "" && matchesFoldedFacetQuery(foldedQuery, tag) {
			tags = append(tags, tag)
		}
	}
	if rErr := rows.Err(); rErr != nil {
		rows.Close()
		return nil, false, rErr
	}
	rows.Close()
	if len(tags) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	var all []core.CatalogFacetCollection
	const batchSize = 24
	for start := 0; start < len(tags); start += batchSize {
		chunk := tags[start:min(start+batchSize, len(tags))]
		branches := make([]string, 0, len(chunk))
		qArgs := make([]any, 0, len(chunk)*(len(args)+3))
		for _, tag := range chunk {
			branches = append(branches, `SELECT ? AS v, (SELECT COUNT(*) FROM (SELECT 1 FROM catalog_owner_tags ot JOIN catalog_titles t ON t.key = ot.key WHERE ot.tag = ? AND (`+where+`) LIMIT ?) s) AS cnt`)
			qArgs = append(qArgs, tag, tag)
			qArgs = append(qArgs, args...)
			qArgs = append(qArgs, catalogFacetCountCap)
		}
		bRows, qErr := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
		if qErr != nil {
			return nil, false, qErr
		}
		for bRows.Next() {
			var tag string
			var cnt int
			if sErr := bRows.Scan(&tag, &cnt); sErr != nil {
				bRows.Close()
				return nil, false, sErr
			}
			if cnt > 0 {
				all = append(all, core.CatalogFacetCollection{ID: tag, Name: tag, ItemCount: cnt})
			}
		}
		if rErr := bRows.Err(); rErr != nil {
			bRows.Close()
			return nil, false, rErr
		}
		bRows.Close()
	}
	sortCatalogFacetCollections(all, orderByName)
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetKeywords(
	ctx context.Context,
	orderByName bool,
	rawQuery, foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	small, err := c.libraryHasAtMostTitles(ctx, where, args, 5000)
	if err != nil {
		return nil, false, err
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(rawQuery))
	if small {
		qArgs := make([]any, 0, len(args)+4)
		qArgs = append(qArgs, args...)
		extra := ""
		if lowerQuery != "" {
			extra = ` AND LOWER(kw.keyword) LIKE ? ESCAPE '\'`
			qArgs = append(qArgs, "%"+escapeLike(lowerQuery)+"%")
		}
		orderClause := `cnt DESC, kid ASC`
		if orderByName {
			orderClause = `kid ASC`
		}
		qArgs = append(qArgs, limit+1, offset)
		rows, qErr := c.db.QueryContext(ctx,
			`SELECT LOWER(kw.keyword) AS kid, MIN(kw.keyword) AS kname, COUNT(DISTINCT kw.key) AS cnt
			 FROM catalog_titles t
			 JOIN catalog_keywords kw ON kw.key = t.key
			 WHERE (`+where+`) AND kw.keyword <> ''`+extra+`
			 GROUP BY LOWER(kw.keyword)
			 ORDER BY `+orderClause+`
			 LIMIT ? OFFSET ?`,
			qArgs...,
		)
		if qErr != nil {
			return nil, false, qErr
		}
		defer rows.Close()
		var out []core.CatalogFacetCollection
		for rows.Next() {
			var it core.CatalogFacetCollection
			if sErr := rows.Scan(&it.ID, &it.Name, &it.ItemCount); sErr != nil {
				return nil, false, sErr
			}
			it.Name = strings.TrimSpace(it.Name)
			if it.ID != "" && it.Name != "" && it.ItemCount > 0 {
				out = append(out, it)
			}
		}
		if rErr := rows.Err(); rErr != nil {
			return nil, false, rErr
		}
		more := false
		if len(out) > limit {
			out = out[:limit]
			more = true
		}
		return out, more, nil
	}

	window := min(max((offset+limit)*3, 180), 450)
	nameByID := make(map[string]string, window)
	var candIDs []string
	collectSkip := func(sqlStr string, sqlArgs ...any) error {
		rows, qErr := c.db.QueryContext(ctx, sqlStr, sqlArgs...)
		if qErr != nil {
			return qErr
		}
		defer rows.Close()
		for rows.Next() {
			var kw string
			if sErr := rows.Scan(&kw); sErr != nil {
				return sErr
			}
			kw = strings.TrimSpace(kw)
			if kw == "" || !matchesFoldedFacetQuery(foldedQuery, kw) {
				continue
			}
			kid := strings.ToLower(kw)
			if _, exists := nameByID[kid]; !exists {
				nameByID[kid] = kw
				candIDs = append(candIDs, kid)
			}
		}
		return rows.Err()
	}

	if lowerQuery != "" {
		upperPrefix := lowerQuery + "\uffff"
		if err := collectSkip(`WITH RECURSIVE d(v) AS (
			SELECT (SELECT keyword FROM catalog_keywords WHERE keyword >= ? AND keyword < ? ORDER BY keyword ASC LIMIT 1)
			UNION ALL
			SELECT (SELECT keyword FROM catalog_keywords WHERE keyword > d.v AND keyword < ? ORDER BY keyword ASC LIMIT 1)
			FROM d WHERE d.v IS NOT NULL
		)
		SELECT v FROM d WHERE v IS NOT NULL LIMIT ?`, lowerQuery, upperPrefix, upperPrefix, window); err != nil {
			return nil, false, err
		}
	}
	if len(candIDs) < offset+limit+1 {
		scanLimit := window
		if lowerQuery != "" {
			scanLimit = 500
		}
		if err := collectSkip(`WITH RECURSIVE d(v) AS (
			SELECT (SELECT keyword FROM catalog_keywords WHERE keyword > '' ORDER BY keyword ASC LIMIT 1)
			UNION ALL
			SELECT (SELECT keyword FROM catalog_keywords WHERE keyword > d.v ORDER BY keyword ASC LIMIT 1)
			FROM d WHERE d.v IS NOT NULL
		)
		SELECT v FROM d WHERE v IS NOT NULL LIMIT ?`, scanLimit); err != nil {
			return nil, false, err
		}
	}
	if len(candIDs) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	var all []core.CatalogFacetCollection
	const batchSize = 24
	const kwCountCap = 5001
	for start := 0; start < len(candIDs); start += batchSize {
		chunk := candIDs[start:min(start+batchSize, len(candIDs))]
		branches := make([]string, 0, len(chunk))
		qArgs := make([]any, 0, len(chunk)*(len(args)+3))
		for _, kid := range chunk {
			branches = append(branches, `SELECT ? AS v, (SELECT COUNT(*) FROM (SELECT 1 FROM catalog_keywords kw JOIN catalog_titles t ON t.key = kw.key WHERE LOWER(kw.keyword) = ? AND (`+where+`) LIMIT ?) s) AS cnt`)
			qArgs = append(qArgs, kid, kid)
			qArgs = append(qArgs, args...)
			qArgs = append(qArgs, kwCountCap)
		}
		rows, qErr := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
		if qErr != nil {
			return nil, false, qErr
		}
		for rows.Next() {
			var kid string
			var cnt int
			if sErr := rows.Scan(&kid, &cnt); sErr != nil {
				rows.Close()
				return nil, false, sErr
			}
			if cnt > 0 {
				all = append(all, core.CatalogFacetCollection{
					ID:        kid,
					Name:      nameByID[kid],
					ItemCount: cnt,
				})
			}
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return nil, false, rErr
		}
		rows.Close()
	}
	sortCatalogFacetCollections(all, orderByName)
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetTitleBands(
	ctx context.Context,
	bands []catalogBandSpec,
	orderByName bool,
	foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	var matched []catalogBandSpec
	for _, b := range bands {
		if matchesFoldedFacetQuery(foldedQuery, b.name, b.id) {
			matched = append(matched, b)
		}
	}
	if len(matched) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	branches := make([]string, 0, len(matched))
	qArgs := make([]any, 0, len(matched)*(len(args)+2))
	for _, b := range matched {
		branches = append(branches, `SELECT ? AS b, (SELECT COUNT(*) FROM (SELECT 1 FROM catalog_titles t WHERE (`+where+`) AND `+b.sqlCond+` LIMIT ?) s) AS cnt`)
		qArgs = append(qArgs, b.id)
		qArgs = append(qArgs, args...)
		qArgs = append(qArgs, catalogFacetCountCap)
	}
	rows, err := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	countByID := make(map[string]int, len(matched))
	for rows.Next() {
		var id string
		var cnt int
		if sErr := rows.Scan(&id, &cnt); sErr != nil {
			return nil, false, sErr
		}
		countByID[id] = cnt
	}
	if rErr := rows.Err(); rErr != nil {
		return nil, false, rErr
	}

	var all []core.CatalogFacetCollection
	for _, b := range matched {
		if cnt := countByID[b.id]; cnt > 0 {
			all = append(all, core.CatalogFacetCollection{
				ID:        b.id,
				Name:      b.name,
				ItemCount: cnt,
			})
		}
	}
	if !orderByName {
		slices.SortStableFunc(all, func(a, b core.CatalogFacetCollection) int {
			return cmp.Compare(b.ItemCount, a.ItemCount)
		})
	}
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetEvidenceBands(
	ctx context.Context,
	bands []catalogBandSpec,
	orderByName bool,
	foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	var matched []catalogBandSpec
	for _, b := range bands {
		if matchesFoldedFacetQuery(foldedQuery, b.name, b.id) {
			matched = append(matched, b)
		}
	}
	if len(matched) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	policy := c.evidencePolicy()
	now := c.now().Unix()
	branches := make([]string, 0, len(matched))
	qArgs := make([]any, 0, len(matched)*(len(args)+4))
	for _, b := range matched {
		branches = append(branches, `SELECT ? AS b, (SELECT COUNT(*) FROM (
			SELECT DISTINCT ev.rating_key
			FROM catalog_release_evidence ev
			JOIN catalog_titles t ON t.key = ev.rating_key
			WHERE ev.policy = ? AND ev.verified_until > ? AND (`+b.sqlCond+`) AND (`+where+`)
			LIMIT ?
		) s) AS cnt`)
		qArgs = append(qArgs, b.id, policy, now)
		qArgs = append(qArgs, args...)
		qArgs = append(qArgs, catalogFacetCountCap)
	}
	rows, err := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	countByID := make(map[string]int, len(matched))
	for rows.Next() {
		var id string
		var cnt int
		if sErr := rows.Scan(&id, &cnt); sErr != nil {
			return nil, false, sErr
		}
		countByID[id] = cnt
	}
	if rErr := rows.Err(); rErr != nil {
		return nil, false, rErr
	}

	var all []core.CatalogFacetCollection
	for _, b := range matched {
		if cnt := countByID[b.id]; cnt > 0 {
			all = append(all, core.CatalogFacetCollection{
				ID:        b.id,
				Name:      b.name,
				ItemCount: cnt,
			})
		}
	}
	if !orderByName {
		slices.SortStableFunc(all, func(a, b core.CatalogFacetCollection) int {
			return cmp.Compare(b.ItemCount, a.ItemCount)
		})
	}
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetAudioLanguages(
	ctx context.Context,
	orderByName bool,
	foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	policy := c.evidencePolicy()
	now := c.now().Unix()
	qArgs := make([]any, 0, 2+len(args))
	qArgs = append(qArgs, policy, now)
	qArgs = append(qArgs, args...)

	rows, err := c.db.QueryContext(ctx,
		`SELECT al.lang, COUNT(DISTINCT al.rating_key) AS cnt
		 FROM (SELECT lang, rating_key FROM catalog_release_audio_langs WHERE policy = ? AND verified_until > ? LIMIT 50000) al
		 JOIN catalog_titles t ON t.key = al.rating_key
		 WHERE (`+where+`)
		 GROUP BY al.lang`,
		qArgs...,
	)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var all []core.CatalogFacetCollection
	for rows.Next() {
		var lang string
		var cnt int
		if sErr := rows.Scan(&lang, &cnt); sErr != nil {
			return nil, false, sErr
		}
		lang = strings.TrimSpace(lang)
		if lang == "" || cnt <= 0 {
			continue
		}
		name := catalogLanguageDisplayName(lang)
		if !matchesFoldedFacetQuery(foldedQuery, name, lang) {
			continue
		}
		all = append(all, core.CatalogFacetCollection{
			ID:        lang,
			Name:      name,
			ItemCount: cnt,
		})
	}
	if rErr := rows.Err(); rErr != nil {
		return nil, false, rErr
	}
	sortCatalogFacetCollections(all, orderByName)
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func (c *Catalog) libraryFacetAddedMonths(
	ctx context.Context,
	orderByName bool,
	foldedQuery string,
	offset, limit int,
	where string,
	args []any,
) ([]core.CatalogFacetCollection, bool, error) {
	type monthWindow struct {
		id     string
		name   string
		full   string
		fromTS int64
		toTS   int64
		count  int
	}

	small, err := c.libraryHasAtMostTitles(ctx, where, args, 5000)
	if err != nil {
		return nil, false, err
	}
	if small {
		rows, qErr := c.db.QueryContext(ctx,
			`SELECT t.added_at FROM catalog_titles t WHERE (`+where+`) AND t.added_at > 0`,
			args...,
		)
		if qErr != nil {
			return nil, false, qErr
		}
		defer rows.Close()
		byID := make(map[string]*monthWindow)
		for rows.Next() {
			var ts int64
			if sErr := rows.Scan(&ts); sErr != nil {
				return nil, false, sErr
			}
			if ts <= 0 {
				continue
			}
			tm := time.Unix(ts, 0).UTC()
			id := tm.Format("2006-01")
			mw, ok := byID[id]
			if !ok {
				mw = &monthWindow{
					id:   id,
					name: tm.Format("Jan 2006"),
					full: tm.Format("January 2006"),
				}
				byID[id] = mw
			}
			mw.count++
		}
		if rErr := rows.Err(); rErr != nil {
			return nil, false, rErr
		}
		var all []core.CatalogFacetCollection
		for _, mw := range byID {
			if matchesFoldedFacetQuery(foldedQuery, mw.name, mw.full, mw.id) {
				all = append(all, core.CatalogFacetCollection{
					ID:        mw.id,
					Name:      mw.name,
					ItemCount: mw.count,
				})
			}
		}
		sortAddedMonthCollections(all, orderByName)
		out, more := sliceCatalogFacetPage(all, offset, limit)
		return out, more, nil
	}

	var months []monthWindow
	var upperExclusive int64
	for step := 0; step < 120; step++ {
		var ts int64
		qArgs := make([]any, 0, len(args)+1)
		qArgs = append(qArgs, args...)
		var sqlStr string
		if upperExclusive > 0 {
			sqlStr = `SELECT COALESCE((SELECT t.added_at FROM catalog_titles t WHERE (` + where + `) AND t.added_at > 0 AND t.added_at < ? ORDER BY t.added_at DESC, t.key ASC LIMIT 1), 0)`
			qArgs = append(qArgs, upperExclusive)
		} else {
			sqlStr = `SELECT COALESCE((SELECT t.added_at FROM catalog_titles t WHERE (` + where + `) AND t.added_at > 0 ORDER BY t.added_at DESC, t.key ASC LIMIT 1), 0)`
		}
		if qErr := c.db.QueryRowContext(ctx, sqlStr, qArgs...).Scan(&ts); qErr != nil {
			return nil, false, qErr
		}
		if ts <= 0 {
			break
		}
		tm := time.Unix(ts, 0).UTC()
		y, m, _ := tm.Date()
		start := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0).Add(-time.Second)
		id := start.Format("2006-01")
		name := start.Format("Jan 2006")
		full := start.Format("January 2006")
		if matchesFoldedFacetQuery(foldedQuery, name, full, id) {
			months = append(months, monthWindow{
				id:     id,
				name:   name,
				full:   full,
				fromTS: start.Unix(),
				toTS:   end.Unix(),
			})
		}
		upperExclusive = start.Unix()
		if upperExclusive <= 0 {
			break
		}
	}
	if len(months) == 0 {
		return []core.CatalogFacetCollection{}, false, nil
	}

	var all []core.CatalogFacetCollection
	const batchSize = 24
	for start := 0; start < len(months); start += batchSize {
		chunk := months[start:min(start+batchSize, len(months))]
		branches := make([]string, 0, len(chunk))
		qArgs := make([]any, 0, len(chunk)*(len(args)+4))
		for _, mw := range chunk {
			branches = append(branches, `SELECT ? AS b, (SELECT COUNT(*) FROM (SELECT 1 FROM catalog_titles t WHERE (`+where+`) AND t.added_at >= ? AND t.added_at <= ? LIMIT ?) s) AS cnt`)
			qArgs = append(qArgs, mw.id)
			qArgs = append(qArgs, args...)
			qArgs = append(qArgs, mw.fromTS, mw.toTS, catalogFacetCountCap)
		}
		rows, qErr := c.db.QueryContext(ctx, strings.Join(branches, " UNION ALL "), qArgs...)
		if qErr != nil {
			return nil, false, qErr
		}
		nameByID := make(map[string]string, len(chunk))
		for _, mw := range chunk {
			nameByID[mw.id] = mw.name
		}
		for rows.Next() {
			var id string
			var cnt int
			if sErr := rows.Scan(&id, &cnt); sErr != nil {
				rows.Close()
				return nil, false, sErr
			}
			if cnt > 0 {
				all = append(all, core.CatalogFacetCollection{
					ID:        id,
					Name:      nameByID[id],
					ItemCount: cnt,
				})
			}
		}
		if rErr := rows.Err(); rErr != nil {
			rows.Close()
			return nil, false, rErr
		}
		rows.Close()
	}
	sortAddedMonthCollections(all, orderByName)
	out, more := sliceCatalogFacetPage(all, offset, limit)
	return out, more, nil
}

func sortAddedMonthCollections(items []core.CatalogFacetCollection, orderByName bool) {
	if orderByName {
		slices.SortFunc(items, func(a, b core.CatalogFacetCollection) int {
			return cmp.Compare(a.ID, b.ID)
		})
		return
	}
	slices.SortFunc(items, func(a, b core.CatalogFacetCollection) int {
		if d := cmp.Compare(b.ItemCount, a.ItemCount); d != 0 {
			return d
		}
		return cmp.Compare(b.ID, a.ID)
	})
}

func (c *Catalog) appendHydrateExtraNewestBranch(
	storageFacet string,
	b core.CatalogFacetCollection,
	broadLibrary bool,
	where string,
	whereArgs []any,
	branches *[]string,
	qArgs *[]any,
) {
	switch storageFacet {
	case "director", "writer":
		pid, err := strconv.ParseInt(b.ID, 10, 64)
		if err != nil || pid <= 0 {
			return
		}
		*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_credits cr JOIN catalog_titles t ON t.key = cr.key WHERE cr.person_tmdb = ? AND cr.kind = ? AND (`+where+`) ORDER BY t.released_ts DESC, t.popularity DESC, t.key ASC LIMIT 1), 0) AS k`)
		*qArgs = append(*qArgs, b.ID, pid, storageFacet)
		*qArgs = append(*qArgs, whereArgs...)

	case "language":
		*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND t.language = ? ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
		*qArgs = append(*qArgs, b.ID)
		*qArgs = append(*qArgs, whereArgs...)
		*qArgs = append(*qArgs, b.ID)

	case "contentRating":
		*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND t.content_rating = ? ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
		*qArgs = append(*qArgs, b.ID)
		*qArgs = append(*qArgs, whereArgs...)
		*qArgs = append(*qArgs, b.ID)

	case "tag":
		if broadLibrary && b.ItemCount <= 512 {
			*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_owner_tags ot JOIN catalog_titles t ON t.key = ot.key WHERE ot.tag = ? AND (`+where+`) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
			*qArgs = append(*qArgs, b.ID, b.ID)
			*qArgs = append(*qArgs, whereArgs...)
		} else {
			*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND EXISTS (SELECT 1 FROM catalog_owner_tags ot WHERE ot.key = t.key AND ot.tag = ?) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
			*qArgs = append(*qArgs, b.ID)
			*qArgs = append(*qArgs, whereArgs...)
			*qArgs = append(*qArgs, b.ID)
		}

	case "keyword":
		kid := strings.ToLower(strings.TrimSpace(b.ID))
		if broadLibrary && b.ItemCount <= 512 {
			*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_keywords kw JOIN catalog_titles t ON t.key = kw.key WHERE LOWER(kw.keyword) = ? AND (`+where+`) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
			*qArgs = append(*qArgs, b.ID, kid)
			*qArgs = append(*qArgs, whereArgs...)
		} else {
			*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND EXISTS (SELECT 1 FROM catalog_keywords kw WHERE kw.key = t.key AND LOWER(kw.keyword) = ?) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
			*qArgs = append(*qArgs, b.ID)
			*qArgs = append(*qArgs, whereArgs...)
			*qArgs = append(*qArgs, kid)
		}

	case "resolution":
		for _, spec := range catalogResolutionBands {
			if spec.id == b.ID {
				*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM (SELECT DISTINCT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy = ? AND ev.verified_until > ? AND (`+spec.sqlCond+`) LIMIT 5000) ev JOIN catalog_titles t ON t.key = ev.rating_key WHERE (`+where+`) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
				*qArgs = append(*qArgs, b.ID, c.evidencePolicy(), c.now().Unix())
				*qArgs = append(*qArgs, whereArgs...)
				break
			}
		}

	case "hdr":
		for _, spec := range catalogHDRBands {
			if spec.id == b.ID {
				*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM (SELECT DISTINCT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy = ? AND ev.verified_until > ? AND (`+spec.sqlCond+`) LIMIT 5000) ev JOIN catalog_titles t ON t.key = ev.rating_key WHERE (`+where+`) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
				*qArgs = append(*qArgs, b.ID, c.evidencePolicy(), c.now().Unix())
				*qArgs = append(*qArgs, whereArgs...)
				break
			}
		}

	case "audioLanguage":
		*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM (SELECT DISTINCT rating_key FROM catalog_release_audio_langs WHERE policy = ? AND verified_until > ? AND lang = ? LIMIT 5000) al JOIN catalog_titles t ON t.key = al.rating_key WHERE (`+where+`) ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
		*qArgs = append(*qArgs, b.ID, c.evidencePolicy(), c.now().Unix(), b.ID)
		*qArgs = append(*qArgs, whereArgs...)

	case "rating":
		for _, spec := range catalogRatingBands {
			if spec.id == b.ID {
				if b.ItemCount <= 1024 {
					*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT s.key FROM (SELECT t.key, t.released_ts FROM catalog_titles t WHERE (`+where+`) AND `+spec.sqlCond+` LIMIT 1024) s ORDER BY s.released_ts DESC, s.key ASC LIMIT 1), 0) AS k`)
				} else {
					*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND `+spec.sqlCond+` ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
				}
				*qArgs = append(*qArgs, b.ID)
				*qArgs = append(*qArgs, whereArgs...)
				break
			}
		}

	case "runtime":
		for _, spec := range catalogRuntimeBands {
			if spec.id == b.ID {
				if b.ItemCount <= 1024 {
					*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT s.key FROM (SELECT t.key, t.released_ts FROM catalog_titles t WHERE (`+where+`) AND `+spec.sqlCond+` LIMIT 1024) s ORDER BY s.released_ts DESC, s.key ASC LIMIT 1), 0) AS k`)
				} else {
					*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND `+spec.sqlCond+` ORDER BY t.released_ts DESC, t.key ASC LIMIT 1), 0) AS k`)
				}
				*qArgs = append(*qArgs, b.ID)
				*qArgs = append(*qArgs, whereArgs...)
				break
			}
		}

	case "addedMonth":
		if fromTS, toTS, ok := core.ParseCatalogueAddedMonth(b.ID); ok {
			*branches = append(*branches, `SELECT ? AS b, COALESCE((SELECT t.key FROM catalog_titles t WHERE (`+where+`) AND t.added_at >= ? AND t.added_at <= ? ORDER BY t.added_at DESC, t.key ASC LIMIT 1), 0) AS k`)
			*qArgs = append(*qArgs, b.ID)
			*qArgs = append(*qArgs, whereArgs...)
			*qArgs = append(*qArgs, fromTS, toTS)
		}
	}
}

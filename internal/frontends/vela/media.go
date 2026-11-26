package vela

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func titleMediaWire(t frontend.Title) object {
	if t.MediaClass == "" || (t.MediaClass == frontend.MediaClassVideo && t.MediaRole == "") {
		return nil
	}
	role := t.MediaRole
	if role == "" {
		role = frontend.MediaRoleItem
	}
	list := role == frontend.MediaRolePlaylist
	if list {
		// A list (an editorial playlist) is an album to a Vela build that
		// predates lists: a container whose children play in its order.
		// "list" tells a newer build what it really is.
		role = frontend.MediaRoleAlbum
	}
	media := object{
		"class": string(t.MediaClass),
		"role":  string(role),
	}
	if list {
		media["list"] = "playlist"
	}
	idx := t.ItemIndex
	if idx <= 0 {
		switch t.Type {
		case frontend.TypeEpisode:
			if t.Episode > 0 {
				idx = t.Episode
			}
		case frontend.TypeSeason:
			if t.Season > 0 {
				idx = t.Season
			}
		}
	}
	if idx > 0 {
		media["index"] = idx
	}
	if t.IndexDisplay != "" {
		media["indexDisplay"] = t.IndexDisplay
	}
	if t.Disc > 0 {
		media["disc"] = t.Disc
	}
	if t.RecordingID != "" {
		media["recordingId"] = t.RecordingID
	}
	if len(t.MediaParents) > 0 {
		parents := make([]object, 0, len(t.MediaParents))
		for _, p := range t.MediaParents {
			pid := p.FacetID
			if p.Key != 0 {
				pid = ids.RatingKeyGUID(p.Key)
			} else if pid != "" && t.LibraryID > 0 {
				pid = scopeSyntheticFacetID(pid, t.LibraryID)
			}
			if pid == "" {
				continue
			}
			pr := p.Role
			if pr == "" {
				pr = frontend.MediaRoleCollection
			}
			entry := object{
				"id":   pid,
				"name": p.Title,
				"role": string(pr),
			}
			if p.Index > 0 {
				entry["index"] = p.Index
			}
			if p.IndexDisplay != "" {
				entry["indexDisplay"] = p.IndexDisplay
			}
			parents = append(parents, entry)
		}
		if len(parents) > 0 {
			media["parents"] = parents
		}
	}
	if t.ChildCount > 0 {
		media["childCount"] = t.ChildCount
	}
	if len(t.ChildCounts) > 0 {
		media["childCounts"] = t.ChildCounts
	}
	if len(t.MediaCredits) > 0 {
		credits := make([]object, 0, len(t.MediaCredits))
		for _, c := range t.MediaCredits {
			if c.Name == "" {
				continue
			}
			entry := object{
				"name": c.Name,
				"role": c.Role,
			}
			if c.PersonID != "" {
				entry["personId"] = c.PersonID
			} else if c.TMDB > 0 {
				entry["personId"] = frontend.FacetID("person", strconv.FormatInt(c.TMDB, 10))
			}
			credits = append(credits, entry)
		}
		if len(credits) > 0 {
			media["credits"] = credits
		}
	}
	if t.MediaReading != nil && t.MediaReading.Format != "" {
		reading := object{"format": t.MediaReading.Format}
		if t.MediaReading.Pages > 0 {
			reading["pages"] = t.MediaReading.Pages
		}
		media["reading"] = reading
	}
	if len(t.MediaFacts) > 0 {
		facts := make([]object, 0, len(t.MediaFacts))
		for _, f := range t.MediaFacts {
			if f.Label != "" && f.Value != "" {
				facts = append(facts, object{"label": f.Label, "value": f.Value})
			}
		}
		if len(facts) > 0 {
			media["facts"] = facts
		}
	}
	return media
}

func isPositiveIntToken(s string) bool {
	if s == "" || s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func scopeSyntheticFacetID(raw string, libID frontend.LibraryID) string {
	if libID <= 0 || !strings.HasPrefix(raw, "facet:") {
		return raw
	}
	rest := strings.TrimPrefix(raw, "facet:")
	first, after, ok := strings.Cut(rest, ":")
	if !ok || first == "" || after == "" {
		return raw
	}
	if isPositiveIntToken(first) {
		return raw
	}
	return "facet:" + strconv.FormatInt(int64(libID), 10) + ":" + rest
}

// parseSyntheticFacetID parses either a library-scoped synthetic facet ID
// ("facet:<libraryId>:<field>:<value>") or a legacy unscoped ID
// ("facet:<field>:<value>"). Declared field keys always start with a letter,
// so a leading positive integer segment unambiguously names the library.
func parseSyntheticFacetID(raw string) (libraryID, field, value string, ok bool) {
	if !strings.HasPrefix(raw, "facet:") {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(raw, "facet:")
	first, tail, hasColon := strings.Cut(rest, ":")
	if !hasColon || first == "" || tail == "" {
		return "", "", "", false
	}
	if isPositiveIntToken(first) {
		f, v, ok2 := strings.Cut(tail, ":")
		if !ok2 || f == "" || v == "" || len(f) > 64 || len(v) > 256 {
			return "", "", "", false
		}
		return first, f, v, true
	}
	if len(first) > 64 || len(tail) > 256 {
		return "", "", "", false
	}
	return "", first, tail, true
}

func viewOffersFacetField(view frontend.View, field string) bool {
	if view.PluginID == "" && view.KindKey == "" {
		return false
	}
	for _, f := range view.Facets {
		if f.ID == field {
			return true
		}
	}
	for _, tab := range view.Tabs {
		if tab.Kind == "collections" && tab.Facet == field {
			return true
		}
	}
	return false
}

func seriesSortFloat(t frontend.Title) (float64, bool) {
	if t.IndexDisplay != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(t.IndexDisplay), 64); err == nil && f >= 0 {
			return f, true
		}
	}
	if t.ItemIndex > 0 {
		return float64(t.ItemIndex), true
	}
	return 0, false
}

func sortFacetSeriesItems(items []frontend.Title) {
	sort.SliceStable(items, func(a, b int) bool {
		fa, oka := seriesSortFloat(items[a])
		fb, okb := seriesSortFloat(items[b])
		if oka && okb && fa != fb {
			return fa < fb
		}
		if oka != okb {
			return oka
		}
		sa := strings.ToLower(items[a].SortTitle)
		sb := strings.ToLower(items[b].SortTitle)
		if sa != sb {
			return sa < sb
		}
		return items[a].Key < items[b].Key
	})
}

func (i *instance) loadFacetSeriesItems(ctx context.Context, v *frontend.Viewer, dev frontend.Device, libraryID, field, value string) (frontend.View, []frontend.Title, bool, error) {
	views, err := i.host.Experience.Views(ctx, v)
	if err != nil {
		return frontend.View{}, nil, false, err
	}
	candidates := make([]frontend.View, 0, len(views))
	for _, view := range views {
		if !viewOffersFacetField(view, field) {
			continue
		}
		if libraryID != "" {
			if view.ID != libraryID {
				continue
			}
			candidates = append(candidates, view)
			break
		}
		candidates = append(candidates, view)
	}
	for _, view := range candidates {
		q := frontend.ItemQuery{
			Filter: frontend.Filter{
				Facets: map[string][]string{field: {value}},
			},
			Sort:    frontend.SortIndex,
			Desc:    false,
			Limit:   200,
			NoTotal: true,
		}
		page, err := i.host.Experience.LibraryItems(ctx, v, dev, view.ID, q)
		if err != nil {
			continue
		}
		if len(page.Items) > 0 {
			items := append([]frontend.Title(nil), page.Items...)
			sortFacetSeriesItems(items)
			return view, items, true, nil
		}
	}
	return frontend.View{}, nil, false, nil
}

func (i *instance) facetSeriesDetail(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, token, rawID string) {
	libID, field, value, ok := parseSyntheticFacetID(rawID)
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/extras") {
		cache(w, r, object{"items": []object{}})
		return
	}
	if libID == "" {
		libID = strings.TrimSpace(r.URL.Query().Get("library"))
	}
	view, items, found, err := i.loadFacetSeriesItems(r.Context(), v, requestDevice(r), libID, field, value)
	if err != nil {
		fail(w, err, r)
		return
	}
	if !found {
		problem(w, 404, "not-found", "not_found")
		return
	}
	mediaClass := view.Media
	if mediaClass == "" {
		mediaClass = items[0].MediaClass
	}
	if mediaClass == "" {
		mediaClass = frontend.MediaClassBook
	}
	genreSeen := map[string]bool{}
	genres := []string{}
	for _, it := range items {
		for _, g := range it.Genres {
			if g != "" && !genreSeen[g] {
				genreSeen[g] = true
				genres = append(genres, g)
			}
		}
	}
	artMap, err := i.titleArtworkBatch(r, v, token, items[:1])
	if err != nil {
		fail(w, err, r)
		return
	}
	artwork := artMap[items[0].Key]
	if artwork == nil {
		artwork = []object{}
	}
	media := object{
		"class":      string(mediaClass),
		"role":       string(frontend.MediaRoleSeries),
		"childCount": len(items),
	}
	if len(items[0].MediaCredits) > 0 {
		if firstMedia := titleMediaWire(items[0]); firstMedia != nil {
			if credits, ok := firstMedia["credits"]; ok {
				media["credits"] = credits
			}
		}
	}
	canonicalID := "facet:" + view.ID + ":" + field + ":" + value
	titleObj := object{
		"id":      canonicalID,
		"kind":    "show",
		"name":    value,
		"summary": "",
		"genres":  genres,
		"artwork": artwork,
		"media":   media,
	}
	out := object{
		"title":    titleObj,
		"canPlay":  false,
		"extras":   []object{},
		"chapters": []object{},
		"cast":     []object{},
		"crew":     []object{},
	}
	if mediaClass == frontend.MediaClassBook {
		out["canRead"] = false
	}
	varyLanguage(w)
	cache(w, r, out)
}

func (i *instance) facetSeriesChildren(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, token, rawID string) {
	libID, field, value, ok := parseSyntheticFacetID(rawID)
	if !ok {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if libID == "" {
		libID = strings.TrimSpace(r.URL.Query().Get("library"))
	}
	lim := limit(r)
	var cur liveCursor
	if i.seal != nil {
		scope, err := i.livePageScope(r, v, token)
		if err != nil {
			fail(w, err, r)
			return
		}
		cur, err = i.readLiveCursor(r, scope, hashJSON([]any{"children", rawID, lim}))
		if err != nil {
			fail(w, err, r)
			return
		}
	} else if !noCursor(w, r) {
		return
	}
	_, items, found, err := i.loadFacetSeriesItems(r.Context(), v, requestDevice(r), libID, field, value)
	if err != nil {
		fail(w, err, r)
		return
	}
	if !found {
		problem(w, 404, "not-found", "not_found")
		return
	}
	start := min(cur.Offset, len(items))
	end := min(start+lim, len(items))
	hasMore := end < len(items)
	ts, err := i.titles(r, v, token, items[start:end])
	if err != nil {
		fail(w, err, r)
		return
	}
	resp := object{"items": ts, "continuation": "exhausted"}
	if hasMore {
		if i.seal != nil {
			next, err := i.nextLiveCursor(cur, end, "children")
			if err != nil {
				fail(w, err, r)
				return
			}
			resp["continuation"] = "more"
			resp["nextCursor"] = next
		} else {
			resp["continuation"] = "unknown"
		}
	}
	varyLanguage(w)
	cache(w, r, resp)
}

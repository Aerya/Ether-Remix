package mediabrowser

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

// artists answers GET /Artists and GET /Artists/AlbumArtists for music
// libraries, supporting both hierarchical MusicArtist libraries (artist ->
// album -> track) and flat album/track libraries whose artists live in a
// declared people facet field (like Deezer). Queries stay O(limit) in SQL.
func (i *instance) artists(albumArtists bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, _, err := i.viewer(r)
		if err != nil || v == nil {
			http.Error(w, "Unauthorized", 401)
			return
		}
		offset := max(queryInt(r, "StartIndex", 0), 0)
		limit := queryInt(r, "Limit", 100)
		if limit <= 0 {
			limit = 100
		}
		if limit > 200 {
			limit = 200
		}
		if i.host.Experience == nil {
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		views, err := i.host.Experience.Views(r.Context(), v)
		if err != nil {
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		targetViewID := facetView(r)
		var target *frontend.View
		for idx := range views {
			vw := views[idx]
			if targetViewID != "" {
				if vw.ID == targetViewID {
					target = &vw
					break
				}
				continue
			}
			if mediaBrowserCollectionType(vw) == "music" {
				target = &vw
				break
			}
		}
		if target == nil {
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}

		searchTerm := ""
		if terms := route.Query(r.URL.Query(), "SearchTerm"); len(terms) > 0 {
			searchTerm = strings.TrimSpace(terms[0])
		}

		// Shape A: hierarchical library declaring a MusicArtist level.
		artistQuery := frontend.ItemQuery{
			UnservedTypes: []string{"musicartist"},
			Recursive:     true,
			Offset:        offset,
			Limit:         limit,
			Sort:          frontend.SortTitle,
			Filter:        frontend.Filter{Query: searchTerm},
		}
		if page, err := i.host.Experience.LibraryItems(r.Context(), v, device(r), target.ID, artistQuery); err == nil && (len(page.Items) > 0 || page.Total > 0) {
			i.page(w, r, v, page, offset)
			return
		}

		// Shape B: flat album/track library with an indexed people facet field.
		facetReader, ok := i.host.Experience.(frontend.LibraryFacetPeople)
		if !ok {
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		field := musicArtistFacetField(*target, albumArtists)
		if field == "" {
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		order := "name"
		for _, raw := range route.Query(r.URL.Query(), "SortBy") {
			if strings.Contains(strings.ToLower(raw), "playcount") || strings.Contains(strings.ToLower(raw), "albumcount") {
				order = "count"
				break
			}
		}
		page, err := facetReader.LibraryFacetPeople(r.Context(), v, device(r), target.ID, field, order, frontend.PageQuery{
			Query:  searchTerm,
			Offset: offset,
			Limit:  limit,
		})
		if err != nil {
			i.logFacetFailure("artists", err)
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		people := make([]frontend.PersonDetail, len(page.Items))
		for n, entry := range page.Items {
			p := entry.Person
			p.KnownFor = "MusicArtist"
			people[n] = p
		}
		grants, imgs := i.issuePersonGrants(r, people)
		items := make([]any, 0, len(page.Items))
		for n, entry := range page.Items {
			dto := i.personItemDTO(people[n], grants[n], imgs[n], false)
			if entry.ItemCount > 0 {
				dto["AlbumCount"] = entry.ItemCount
			}
			items = append(items, dto)
		}
		total := page.Total
		if total < offset+len(items) {
			total = offset + len(items)
		}
		i.replyItems(w, r, i.queryResult(items, total, offset))
	}
}

// artistByName answers GET /Artists/{name} for Jellyfin clients resolving an
// artist by display name.
func (i *instance) artistByName(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" || i.host.Experience == nil {
		http.NotFound(w, r)
		return
	}
	if strings.EqualFold(name, "AlbumArtists") {
		i.artists(true)(w, r)
		return
	}
	// Try synthetic plugin artist ID directly first (O(1) indexed lookup).
	pid := frontend.PluginPersonID(name)
	fid := frontend.FacetID("person", strconv.FormatInt(pid, 10))
	if i.writePersonItem(w, r, v, fid) {
		return
	}
	// Fallback: search hierarchical MusicArtist items by exact title.
	views, err := i.host.Experience.Views(r.Context(), v)
	if err == nil {
		for _, vw := range views {
			if mediaBrowserCollectionType(vw) != "music" {
				continue
			}
			page, err := i.host.Experience.LibraryItems(r.Context(), v, device(r), vw.ID, frontend.ItemQuery{
				UnservedTypes: []string{"musicartist"},
				Recursive:     true,
				Limit:         10,
				Filter:        frontend.Filter{Query: name},
			})
			if err != nil {
				continue
			}
			for _, item := range page.Items {
				if strings.EqualFold(strings.TrimSpace(item.Title), name) {
					dto := i.itemDTO(item, frontend.WatchStatus{})
					i.replyItems(w, r, dto)
					return
				}
			}
		}
	}
	http.NotFound(w, r)
}

func musicArtistFacetField(view frontend.View, preferAlbumArtists bool) string {
	var prefs []string
	if preferAlbumArtists {
		prefs = []string{"album_artists", "album_artist", "albumartists", "albumartist", "artists", "artist", "performers", "performer"}
	} else {
		prefs = []string{"artists", "artist", "album_artists", "album_artist", "albumartists", "albumartist", "performers", "performer"}
	}
	firstPeople := ""
	for _, f := range view.Facets {
		if f.Type == "people" && firstPeople == "" {
			firstPeople = f.ID
		}
	}
	for _, want := range prefs {
		for _, f := range view.Facets {
			if f.Type == "people" && strings.EqualFold(f.ID, want) {
				return f.ID
			}
		}
	}
	// Plugins like Deezer Music declare "artists" as a string/strings facet
	// rather than type:"people"; pluginitems indexes those with synthetic
	// person IDs so they answer LibraryFacetPeople identically.
	for _, want := range prefs {
		if want == "performers" || want == "performer" {
			continue
		}
		for _, f := range view.Facets {
			if (f.Type == "strings" || f.Type == "string") && strings.EqualFold(f.ID, want) {
				return f.ID
			}
		}
	}
	return firstPeople
}

// artistParentKey returns a hierarchical MusicArtist RatingKey when the client
// passed a title RatingKey GUID in ArtistIds / AlbumArtistIds /
// ContributingArtistIds (rather than a person facet GUID).
func artistParentKey(r *http.Request) (frontend.RatingKey, bool) {
	for _, param := range []string{"AlbumArtistIds", "ArtistIds", "ContributingArtistIds"} {
		for _, value := range route.Query(r.URL.Query(), param) {
			for _, id := range strings.Split(value, ",") {
				if key, err := ids.ParseRatingKeyGUID(strings.TrimSpace(id)); err == nil && key != 0 {
					return key, true
				}
			}
		}
	}
	return 0, false
}

func hasEmptyArtistFilter(r *http.Request) bool {
	for _, param := range []string{"AlbumArtistIds", "ArtistIds", "ContributingArtistIds"} {
		values := route.Query(r.URL.Query(), param)
		if len(values) == 0 {
			continue
		}
		anyNonEmpty := false
		for _, value := range values {
			for _, id := range strings.Split(value, ",") {
				if strings.TrimSpace(id) != "" {
					anyNonEmpty = true
					break
				}
			}
		}
		if !anyNonEmpty {
			return true
		}
	}
	return false
}

// attachMusicArtistCredits populates Jellyfin's standard AlbumArtist, Artists,
// AlbumArtists, and ArtistItems fields on MusicAlbum and Audio items. Jellyfin
// Web calls .map() on ArtistItems and AlbumArtists unconditionally on album
// track lists and track details, so both slices are always emitted (non-nil).
func attachMusicArtistCredits(m map[string]any, t frontend.Title) {
	parentIDs := map[string]string{}
	for _, p := range t.MediaParents {
		if p.Role == frontend.MediaRoleArtist && p.Key != 0 {
			if name := strings.TrimSpace(p.Title); name != "" {
				parentIDs[strings.ToLower(name)] = ids.RatingKeyGUID(p.Key)
			}
		}
	}
	names := []string{}
	items := []map[string]any{}
	seen := map[string]bool{}
	addArtist := func(rawName, id string, tmdb int64) {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return
		}
		low := strings.ToLower(name)
		if seen[low] {
			return
		}
		seen[low] = true
		if id == "" {
			if pid := parentIDs[low]; pid != "" {
				id = pid
			} else if tmdb > 0 {
				id = frontend.FacetID("person", strconv.FormatInt(tmdb, 10))
			} else {
				id = frontend.FacetID("person", low)
			}
		}
		names = append(names, name)
		items = append(items, map[string]any{"Name": name, "Id": id})
	}
	for _, p := range t.Cast {
		addArtist(p.Name, p.ID, p.TMDB)
	}
	for _, c := range t.MediaCredits {
		switch strings.ToLower(strings.TrimSpace(c.Role)) {
		case "", "artist", "albumartist", "album_artist", "performer":
			addArtist(c.Name, c.PersonID, c.TMDB)
		}
	}
	for _, p := range t.MediaParents {
		if p.Role == frontend.MediaRoleArtist {
			id := ""
			if p.Key != 0 {
				id = ids.RatingKeyGUID(p.Key)
			}
			addArtist(p.Title, id, 0)
		}
	}
	if len(names) > 0 {
		m["AlbumArtist"] = names[0]
	}
	m["Artists"] = names
	m["AlbumArtists"] = items
	m["ArtistItems"] = items
}

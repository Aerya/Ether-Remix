package mediabrowser

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/paging"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func facetView(r *http.Request) string {
	values := route.Query(r.URL.Query(), "ParentId")
	if len(values) == 1 {
		if n, e := ids.ParseGUID(ids.View, values[0]); e == nil {
			return ids.Decimal(n)
		}
	}
	return ""
}
func (i *instance) facets(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, _, err := i.viewer(r)
		if err != nil || v == nil {
			http.Error(w, "Unauthorized", 401)
			return
		}
		if i.host.Experience == nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		if kind == "person" {
			// Without a term this is the most credited visible people: the
			// person facet walked every credited title in the library.
			term := ""
			if terms := route.Query(r.URL.Query(), "SearchTerm"); len(terms) == 1 {
				term = strings.TrimSpace(terms[0])
			}
			if _, ok := i.host.Experience.(frontend.PeopleReader); ok {
				i.searchPersons(w, r, v, term)
				return
			}
		}
		queryKind := kind
		if kind == "musicgenre" {
			queryKind = "genre"
		}
		offset, limit := queryInt(r, "StartIndex", 0), queryInt(r, "Limit", 50)
		var page []frontend.FacetValue
		total := 0
		viewID := facetView(r)
		levelHint := ""
		if viewID != "" {
			if rawTypes := route.Query(r.URL.Query(), "IncludeItemTypes"); len(rawTypes) > 0 {
				parts := strings.Split(strings.TrimSpace(rawTypes[0]), ",")
				if len(parts) == 1 {
					levelHint = strings.TrimSpace(parts[0])
				}
			}
		}
		if levelHint != "" {
			if lp, ok := i.host.Experience.(frontend.LevelFacetPager); ok {
				page, total, err = lp.FacetPageAtLevel(r.Context(), v, device(r), viewID, levelHint, queryKind, offset, limit)
			} else if lr, ok := i.host.Experience.(frontend.LevelFacetReader); ok {
				var facets []frontend.FacetValue
				facets, err = lr.FacetsAtLevel(r.Context(), v, device(r), viewID, levelHint, queryKind)
				page, total = paging.Slice(facets, offset, limit), len(facets)
			} else if pager, ok := i.host.Experience.(frontend.FacetPager); ok {
				page, total, err = pager.FacetPage(r.Context(), v, device(r), viewID, queryKind, offset, limit)
			} else {
				var facets []frontend.FacetValue
				facets, err = i.host.Experience.Facets(r.Context(), v, device(r), viewID, queryKind)
				page, total = paging.Slice(facets, offset, limit), len(facets)
			}
		} else if pager, ok := i.host.Experience.(frontend.FacetPager); ok {
			// A page of names and their count, not every studio listed
			// to slice fifty.
			page, total, err = pager.FacetPage(r.Context(), v, device(r), viewID, queryKind, offset, limit)
		} else {
			var facets []frontend.FacetValue
			facets, err = i.host.Experience.Facets(r.Context(), v, device(r), viewID, queryKind)
			page, total = paging.Slice(facets, offset, limit), len(facets)
		}
		if err != nil {
			i.logFacetFailure(kind, err)
			jsonReply(w, i.queryResult([]any{}, 0, offset))
			return
		}
		items := make([]any, 0, len(page))
		typeName := map[string]string{"genre": "Genre", "musicgenre": "MusicGenre", "studio": "Studio", "person": "Person"}[kind]
		for _, facet := range page {
			items = append(items, map[string]any{"Id": facet.ID, "Name": facet.Title, "Type": typeName, "ServerId": i.info.ID})
		}
		jsonReply(w, i.queryResult(items, total, offset))
	}
}

// searchPersons answers /Persons?searchTerm= from the people-name index:
// the people whose names match, visible to the viewer through at least one
// credited title, at most 100.
func (i *instance) searchPersons(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, term string) {
	reader, ok := i.host.Experience.(frontend.PeopleReader)
	if !ok || utf8.RuneCountInString(term) > 200 {
		http.Error(w, "Unavailable", 503)
		return
	}
	offset, limit := max(queryInt(r, "StartIndex", 0), 0), queryInt(r, "Limit", 50)
	if limit <= 0 {
		limit = 50
	}
	people, err := reader.People(r.Context(), v, device(r), frontend.PeopleQuery{Query: term, Limit: min(offset+limit, 100)})
	if err != nil {
		// A browse list answers empty, never an error the app shows (#421).
		i.logFacetFailure("person", err)
		jsonReply(w, i.queryResult([]any{}, 0, offset))
		return
	}
	page := paging.Slice(people, offset, limit)
	grants, imgs := i.issuePersonGrants(r, page)
	items := make([]any, 0, len(page))
	for n, person := range page {
		items = append(items, i.personItemDTO(person, grants[n], imgs[n], false))
	}
	jsonReply(w, i.queryResult(items, len(people), offset))
}

// logFacetFailure records a genre/studio/person list that could not be
// built; the app is answered an empty list instead of an error.
func (i *instance) logFacetFailure(kind string, err error) {
	if i.host.Log != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, frontend.ErrNotFound) {
		i.host.Log.Warn("mediabrowser facet list unavailable", "frontend", i.protocol, "kind", kind, "err", err)
	}
}

func (i *instance) resolveFilters(r *http.Request, v *frontend.Viewer, q *frontend.ItemQuery) error {
	view := facetView(r)
	for _, field := range []struct {
		name, kind string
		allowKey   bool
	}{
		{"GenreIds", "genre", false},
		{"StudioIds", "studio", false},
		{"PersonIds", "person", false},
		{"ArtistIds", "person", true},
		{"AlbumArtistIds", "person", true},
		{"ContributingArtistIds", "person", true},
	} {
		for _, value := range route.Query(r.URL.Query(), field.name) {
			for _, id := range strings.Split(value, ",") {
				id = strings.TrimSpace(id)
				if id == "" {
					continue
				}
				if field.allowKey {
					// Hierarchical MusicArtist items carry a RatingKey GUID
					// handled by artistParentKey in items(); skip person-facet
					// resolution for those.
					if _, e := ids.ParseRatingKeyGUID(id); e == nil {
						continue
					}
				}
				id, err := i.resolveNamedFacetWireID(r.Context(), field.kind, id)
				if err != nil {
					return err
				}
				facet, e := i.host.Experience.ResolveFacet(r.Context(), v, device(r), view, field.kind, id)
				if e != nil {
					return e
				}
				switch field.kind {
				case "genre":
					q.Filter.Genres = append(q.Filter.Genres, facet.Key)
				case "studio":
					if q.Filter.Studio != "" && q.Filter.Studio != facet.Key {
						return strconv.ErrSyntax
					}
					q.Filter.Studio = facet.Key
				case "person":
					n, e := strconv.ParseInt(facet.Key, 10, 64)
					if e != nil || n <= 0 || (q.Filter.Person != 0 && q.Filter.Person != n) {
						return strconv.ErrSyntax
					}
					q.Filter.Person = n
				}
			}
		}
	}
	if q.Filter.Person == 0 {
		for _, value := range route.Query(r.URL.Query(), "Artists") {
			for _, name := range strings.Split(value, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				pid := frontend.PluginPersonID(name)
				fid := frontend.FacetID("person", strconv.FormatInt(pid, 10))
				facet, e := i.host.Experience.ResolveFacet(r.Context(), v, device(r), view, "person", fid)
				if e != nil {
					return e
				}
				n, e := strconv.ParseInt(facet.Key, 10, 64)
				if e != nil || n <= 0 || (q.Filter.Person != 0 && q.Filter.Person != n) {
					return strconv.ErrSyntax
				}
				q.Filter.Person = n
			}
		}
	}
	return nil
}

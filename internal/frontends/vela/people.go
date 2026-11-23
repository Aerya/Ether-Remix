package vela

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func personPhotoObject(image frontend.Image, secret string) object {
	return object{
		"id":        hash(image.Path),
		"kind":      frontend.ImagePerson,
		"tag":       image.Tag,
		"sizes":     []object{imageSize(image, secret)},
		"blurHash":  image.BlurHash,
		"thumbHash": image.ThumbHash,
	}
}

func (i *instance) personPhotoWire(r *http.Request, token string, person frontend.PersonDetail) (any, error) {
	if person.ArtworkItem == 0 || person.TMDB <= 0 || i.host.ImageGrants == nil {
		return nil, nil
	}
	subject := "tmdb:" + strconv.FormatInt(person.TMDB, 10)
	for _, image := range person.Images {
		if image.Kind != frontend.ImagePerson || image.Subject != subject || image.Path == "" {
			continue
		}
		var secret string
		if batch, ok := i.host.ImageGrants.(frontend.PersonImageGrantBatch); ok {
			secrets, err := batch.IssuePersonImages(r.Context(), token, requestDevice(r), []frontend.PersonImageTarget{{Item: person.ArtworkItem, Image: image}})
			if err != nil {
				return nil, err
			}
			if len(secrets) == 1 {
				secret = secrets[0]
			}
		} else {
			issued, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{Credential: token, Device: requestDevice(r), Item: person.ArtworkItem, Image: image})
			if err == nil {
				secret = issued
			}
		}
		if secret == "" {
			continue
		}
		return personPhotoObject(image, secret), nil
	}
	return nil, nil
}

func (i *instance) facetPeopleWire(r *http.Request, token string, items []frontend.FacetPerson) ([]object, error) {
	photos := make([]any, len(items))
	if len(items) > 0 && i.host.ImageGrants != nil {
		type targetPos struct {
			entryIdx int
			target   frontend.PersonImageTarget
		}
		var pending []targetPos
		for idx, fp := range items {
			if fp.Person.ArtworkItem == 0 || fp.Person.TMDB <= 0 {
				continue
			}
			subject := "tmdb:" + strconv.FormatInt(fp.Person.TMDB, 10)
			for _, image := range fp.Person.Images {
				if image.Kind == frontend.ImagePerson && image.Subject == subject && image.Path != "" {
					pending = append(pending, targetPos{
						entryIdx: idx,
						target:   frontend.PersonImageTarget{Item: fp.Person.ArtworkItem, Image: image},
					})
					break
				}
			}
		}
		dev := requestDevice(r)
		if batch, ok := i.host.ImageGrants.(frontend.PersonImageGrantBatch); ok {
			const chunkSize = 100
			for start := 0; start < len(pending); start += chunkSize {
				chunk := pending[start:min(start+chunkSize, len(pending))]
				targets := make([]frontend.PersonImageTarget, len(chunk))
				for j, p := range chunk {
					targets[j] = p.target
				}
				secrets, err := batch.IssuePersonImages(r.Context(), token, dev, targets)
				if err != nil {
					return nil, err
				}
				for j, p := range chunk {
					if j < len(secrets) && secrets[j] != "" {
						photos[p.entryIdx] = personPhotoObject(p.target.Image, secrets[j])
					}
				}
			}
		} else {
			for _, p := range pending {
				secret, err := i.host.ImageGrants.Issue(r.Context(), frontend.ImageGrantRequest{
					Credential: token,
					Device:     dev,
					Item:       p.target.Item,
					Image:      p.target.Image,
				})
				if err == nil && secret != "" {
					photos[p.entryIdx] = personPhotoObject(p.target.Image, secret)
				}
			}
		}
	}

	entries := make([]object, 0, len(items))
	for idx, fp := range items {
		entries = append(entries, object{
			"id":   "person:" + fp.Person.ID,
			"kind": "person",
			"person": object{
				"id":    fp.Person.ID,
				"name":  fp.Person.Name,
				"photo": photos[idx],
			},
			"itemCount":    fp.ItemCount,
			"receiptToken": "",
		})
	}
	return entries, nil
}

func (i *instance) person(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(i.host.Experience.NoProviderWait(r.Context()))
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	reader, ok := i.host.Experience.(frontend.PeopleReader)
	if !ok {
		i.unavailable(w, r)
		return
	}
	id := r.PathValue("id")
	person, err := reader.Person(r.Context(), v, requestDevice(r), id, frontend.PersonReadOptions{})
	if err != nil {
		fail(w, err, r)
		return
	}
	if person.ID != id || person.Name == "" {
		problem(w, 404, "not-found", "not_found")
		return
	}
	photo, err := i.personPhotoWire(r, token, person)
	if err != nil {
		fail(w, err, r)
		return
	}
	roles := person.Roles
	if roles == nil {
		roles = []string{}
	}
	knownFor := person.KnownFor
	if knownFor == "" && len(roles) > 0 {
		knownFor = roles[0]
	}
	out := object{
		"id":       person.ID,
		"name":     person.Name,
		"photo":    photo,
		"knownFor": knownFor,
		"roles":    roles,
	}
	if person.Biography != "" {
		out["biography"] = person.Biography
	}
	if person.Birthday != "" {
		out["birthday"] = person.Birthday
	}
	cache(w, r, out)
}

func personTitleRole(t frontend.Title, tmdb int64) string {
	var roles []string
	seen := map[string]bool{}
	add := func(role string) {
		role = strings.TrimSpace(role)
		if role != "" && !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}
	for _, group := range []struct {
		fallback string
		people   []frontend.Person
	}{
		{"Actor", t.Cast},
		{"Director", t.Directors},
		{"Writer", t.Writers},
		{"Crew", t.Crew},
	} {
		for _, p := range group.people {
			if p.TMDB != tmdb {
				continue
			}
			if strings.TrimSpace(p.Role) != "" {
				add(p.Role)
			} else {
				add(group.fallback)
			}
		}
	}
	return strings.Join(roles, ", ")
}

func personFilmographyBefore(a, b frontend.Title) bool {
	if !a.Released.Equal(b.Released) {
		return a.Released.After(b.Released)
	}
	if a.Year != b.Year {
		return a.Year > b.Year
	}
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return a.Key < b.Key
}

func (i *instance) personTitles(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(i.host.Experience.NoProviderWait(r.Context()))
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	reader, ok := i.host.Experience.(frontend.PeopleReader)
	if !ok {
		i.unavailable(w, r)
		return
	}
	values := r.URL.Query()
	for name, entries := range values {
		switch name {
		case "libraryId", "cursor", "limit":
		default:
			problem(w, 400, "request", "invalid_request")
			return
		}
		if len(entries) != 1 {
			problem(w, 400, "request", "invalid_request")
			return
		}
	}
	personID := r.PathValue("id")
	person, err := reader.Person(r.Context(), v, requestDevice(r), personID, frontend.PersonReadOptions{Prefetch: true})
	if err != nil {
		fail(w, err, r)
		return
	}
	if person.ID != personID || person.TMDB <= 0 {
		problem(w, 404, "not-found", "not_found")
		return
	}
	views, err := i.host.Experience.Views(r.Context(), v)
	if err != nil {
		fail(w, err, r)
		return
	}
	libraryID := values.Get("libraryId")
	wantPlugin := frontend.IsPluginPersonID(person.TMDB)
	isPluginView := func(candidate frontend.View) bool {
		return candidate.PluginID != "" || candidate.KindKey != ""
	}
	var targetViews []frontend.View
	if libraryID != "" {
		found := false
		for _, candidate := range views {
			if candidate.ID == libraryID {
				found = true
				if isPluginView(candidate) == wantPlugin {
					targetViews = append(targetViews, candidate)
				}
				break
			}
		}
		if !found {
			problem(w, 404, "not-found", "not_found")
			return
		}
	} else {
		for _, candidate := range views {
			if isPluginView(candidate) == wantPlugin {
				targetViews = append(targetViews, candidate)
			}
		}
	}

	pageLimit := limit(r)
	scope, err := i.livePageScope(r, v, token)
	if err != nil {
		fail(w, err, r)
		return
	}
	c, err := i.readLiveCursor(r, scope, hashJSON([]any{"person-titles", personID, libraryID, pageLimit}))
	if err != nil {
		fail(w, err, r)
		return
	}
	snapshot := hashJSON(views)
	if c.Offset > 10000 || (c.Snapshot != "" && c.Snapshot != snapshot) {
		fail(w, frontend.ErrLiveGuideStale)
		return
	}
	if len(targetViews) == 0 {
		cache(w, r, object{"items": []object{}, "continuation": "exhausted"})
		return
	}

	var pageItems []frontend.Title
	hasMore := false
	expanding := false
	nextOffset := 0

	if len(targetViews) == 1 {
		q := frontend.ItemQuery{
			Filter:  frontend.Filter{Person: person.TMDB},
			Sort:    frontend.SortReleased,
			Desc:    true,
			Offset:  c.Offset,
			Limit:   pageLimit,
			NoTotal: true,
		}
		page, err := i.host.Experience.LibraryItems(r.Context(), v, requestDevice(r), targetViews[0].ID, q)
		if err != nil {
			fail(w, err, r)
			return
		}
		expanding = expanding || page.Expanding
		if len(page.Items) > pageLimit {
			page.Items = page.Items[:pageLimit]
			hasMore = true
		} else if len(page.Items) == pageLimit {
			lookahead := q
			lookahead.Offset = c.Offset + len(page.Items)
			lookahead.Limit = 1
			lookahead.SuppressExpand = true
			more, err := i.host.Experience.LibraryItems(r.Context(), v, requestDevice(r), targetViews[0].ID, lookahead)
			if err != nil {
				fail(w, err, r)
				return
			}
			expanding = expanding || more.Expanding
			hasMore = len(more.Items) > 0
		}
		pageItems = page.Items
		nextOffset = c.Offset + len(pageItems)
	} else {
		window := c.Offset + pageLimit + 1
		dev := requestDevice(r)
		type viewResult struct {
			page frontend.Page
			err  error
		}
		results := make([]viewResult, len(targetViews))
		viewCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var wg sync.WaitGroup
		for idx, view := range targetViews {
			wg.Add(1)
			go func(idx int, viewID string) {
				defer wg.Done()
				q := frontend.ItemQuery{
					Filter:  frontend.Filter{Person: person.TMDB},
					Sort:    frontend.SortReleased,
					Desc:    true,
					Offset:  0,
					Limit:   window,
					NoTotal: true,
				}
				page, err := i.host.Experience.LibraryItems(viewCtx, v, dev, viewID, q)
				if err != nil {
					cancel()
				}
				results[idx] = viewResult{page: page, err: err}
			}(idx, view.ID)
		}
		wg.Wait()

		merged := make([]frontend.Title, 0, pageLimit)
		seen := map[frontend.RatingKey]bool{}
		for _, res := range results {
			if res.err != nil {
				fail(w, res.err, r)
				return
			}
			expanding = expanding || res.page.Expanding
			for _, item := range res.page.Items {
				if !seen[item.Key] {
					seen[item.Key] = true
					merged = append(merged, item)
				}
			}
		}
		sort.Slice(merged, func(a, b int) bool {
			return personFilmographyBefore(merged[a], merged[b])
		})
		start := min(c.Offset, len(merged))
		end := min(start+pageLimit, len(merged))
		pageItems = merged[start:end]
		hasMore = end < len(merged)
		nextOffset = end
	}

	converted, err := i.titles(r, v, token, pageItems)
	if err != nil {
		fail(w, err, r)
		return
	}
	entries := make([]object, 0, len(converted))
	for idx, title := range converted {
		entry := object{
			"id":           title["id"],
			"kind":         "title",
			"title":        title,
			"role":         personTitleRole(pageItems[idx], person.TMDB),
			"receiptToken": "",
		}
		entries = append(entries, entry)
	}
	out := object{"items": entries, "continuation": "exhausted"}
	if expanding {
		out["expanding"] = true
	}
	if hasMore {
		next, err := i.nextLiveCursor(c, nextOffset, snapshot)
		if err != nil {
			fail(w, err, r)
			return
		}
		out["continuation"], out["nextCursor"] = "more", next
	}
	varyLanguage(w)
	cache(w, r, out)
}

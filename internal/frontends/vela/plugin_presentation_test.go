package vela

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type pluginPresExperience struct {
	frontendtest.Experience
	images             map[core.RatingKey][]frontend.Image
	buckets            map[string][]frontend.FacetBucket
	colPages           map[string]frontend.FacetCollectionPage
	peoplePages        map[string]frontend.FacetPersonPage
	peopleByID         map[string]frontend.PersonDetail
	pluginRows         map[string][]frontend.Row
	lastLibID          string
	lastQuery          frontend.ItemQuery
	lastColOrder       string
	lastColQuery       string
	lastPeopleOrder    string
	lastPeopleQuery    string
	personImageBatches int
	rowRequests        []frontend.RowRequest
}

func (e *pluginPresExperience) ItemArtworkPage(_ context.Context, _ *core.Viewer, _ core.Device, keys []core.RatingKey) (map[core.RatingKey][]frontend.Image, error) {
	out := make(map[core.RatingKey][]frontend.Image, len(keys))
	for _, k := range keys {
		out[k] = e.images[k]
	}
	return out, nil
}

func (e *pluginPresExperience) LibraryFacetBuckets(_ context.Context, _ *core.Viewer, _ core.Device, viewID, field string) ([]frontend.FacetBucket, error) {
	if viewID != "77" {
		return nil, core.ErrNotFound
	}
	return append([]frontend.FacetBucket(nil), e.buckets[field]...), nil
}

func testFoldWords(s string) []string {
	repl := strings.NewReplacer("ë", "e", "Ë", "e", "é", "e", "É", "e")
	s = strings.ToLower(repl.Replace(s))
	return strings.FieldsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

func testMatchesQuery(name, query string) bool {
	qWords := testFoldWords(query)
	if len(qWords) == 0 {
		return strings.TrimSpace(query) == ""
	}
	nWords := testFoldWords(name)
	for _, qt := range qWords {
		ok := false
		for _, nw := range nWords {
			if strings.HasPrefix(nw, qt) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (e *pluginPresExperience) LibraryFacetCollections(_ context.Context, _ *core.Viewer, _ core.Device, viewID, field, order string, page frontend.PageQuery) (frontend.FacetCollectionPage, error) {
	if viewID != "77" {
		return frontend.FacetCollectionPage{}, core.ErrNotFound
	}
	e.lastColOrder = order
	e.lastColQuery = page.Query
	all := append([]frontend.FacetCollection(nil), e.colPages[field].Items...)
	if page.Query != "" {
		filtered := all[:0]
		for _, c := range all {
			if testMatchesQuery(c.Name, page.Query) {
				filtered = append(filtered, c)
			}
		}
		all = filtered
	}
	if order == "name" {
		sort.Slice(all, func(i, j int) bool {
			return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name)
		})
	}
	if page.Offset >= len(all) {
		return frontend.FacetCollectionPage{Items: []frontend.FacetCollection{}}, nil
	}
	end := min(page.Offset+page.Limit, len(all))
	return frontend.FacetCollectionPage{
		Items: all[page.Offset:end],
		More:  end < len(all),
	}, nil
}

func (e *pluginPresExperience) LibraryFacetPeople(_ context.Context, _ *core.Viewer, _ core.Device, viewID, field, order string, page frontend.PageQuery) (frontend.FacetPersonPage, error) {
	if viewID != "77" {
		return frontend.FacetPersonPage{}, core.ErrNotFound
	}
	e.lastPeopleOrder = order
	e.lastPeopleQuery = page.Query
	all := append([]frontend.FacetPerson(nil), e.peoplePages[field].Items...)
	if page.Query != "" {
		filtered := all[:0]
		for _, p := range all {
			if testMatchesQuery(p.Person.Name, page.Query) {
				filtered = append(filtered, p)
			}
		}
		all = filtered
	}
	if order == "count" {
		sort.Slice(all, func(i, j int) bool {
			if all[i].ItemCount != all[j].ItemCount {
				return all[i].ItemCount > all[j].ItemCount
			}
			return strings.ToLower(all[i].Person.Name) < strings.ToLower(all[j].Person.Name)
		})
	} else {
		sort.Slice(all, func(i, j int) bool {
			return strings.ToLower(all[i].Person.Name) < strings.ToLower(all[j].Person.Name)
		})
	}
	if page.Offset >= len(all) {
		return frontend.FacetPersonPage{Items: []frontend.FacetPerson{}}, nil
	}
	end := min(page.Offset+page.Limit, len(all))
	return frontend.FacetPersonPage{
		Items: all[page.Offset:end],
		More:  end < len(all),
	}, nil
}

func (e *pluginPresExperience) People(_ context.Context, _ *core.Viewer, _ core.Device, _ frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	return nil, nil
}

func (e *pluginPresExperience) Person(_ context.Context, _ *core.Viewer, _ core.Device, id string, _ frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	p, ok := e.peopleByID[id]
	if !ok {
		return frontend.PersonDetail{}, core.ErrNotFound
	}
	return p, nil
}

func (e *pluginPresExperience) PersonCredit(_ context.Context, _ *core.Viewer, _ core.Device, _ core.RatingKey, tmdb int64) (frontend.PersonDetail, error) {
	for _, p := range e.peopleByID {
		if p.TMDB == tmdb {
			return p, nil
		}
	}
	return frontend.PersonDetail{}, core.ErrNotFound
}

func (e *pluginPresExperience) AuthorizePersonImages(_ context.Context, _ *core.Viewer, _ core.Device, targets []frontend.PersonImageTarget) ([]bool, error) {
	e.personImageBatches++
	allowed := make([]bool, len(targets))
	for i, target := range targets {
		allowed[i] = target.Item != 0 && target.Image.Kind == frontend.ImagePerson && target.Image.Path != ""
	}
	return allowed, nil
}

func (e *pluginPresExperience) AuthorizePersonImage(ctx context.Context, _ *core.Viewer, _ core.Device, item core.RatingKey, image frontend.Image, admission func(context.Context) error) error {
	if item == 0 || image.Kind != frontend.ImagePerson || image.Path == "" {
		return core.ErrUnauthorized
	}
	if admission != nil {
		return admission(ctx)
	}
	return nil
}

func (e *pluginPresExperience) PluginLibraryRows(_ context.Context, _ *core.Viewer, _ core.Device, viewID string) ([]frontend.Row, error) {
	return append([]frontend.Row(nil), e.pluginRows[viewID]...), nil
}

func TestVelaPluginLibraryPresentationFacetsCollectionsRowsAndScopedSearch(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, castMembers{}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := grants.Issue(ctx, 1, nil, "vela", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	game1 := frontend.Title{
		Key:       core.PluginKey(501),
		LibraryID: 77,
		Type:      core.TypeMovie,
		Title:     "Chrono Quest",
		Year:      2025,
	}
	game2 := frontend.Title{
		Key:       core.PluginKey(502),
		LibraryID: 77,
		Type:      core.TypeEpisode,
		Title:     "Galaxy Racers",
		Year:      2024,
	}
	catMovie := frontend.Title{
		Key:       core.MovieKey(10),
		LibraryID: 23,
		Type:      core.TypeMovie,
		Title:     "Arrival",
		Year:      2016,
	}

	ashleyNum := core.PluginPersonID("Ashley Johnson")
	lauraNum := core.PluginPersonID("Laura Bailey")
	troyNum := core.PluginPersonID("Troy Baker")
	ashleyID := core.FacetID("person", fmt.Sprintf("%d", ashleyNum))
	lauraID := core.FacetID("person", fmt.Sprintf("%d", lauraNum))
	troyID := core.FacetID("person", fmt.Sprintf("%d", troyNum))

	ashleyImg := frontend.Image{Kind: frontend.ImagePerson, Path: "cast-ashley", Tag: "v1", Subject: fmt.Sprintf("tmdb:%d", ashleyNum), Width: 185, Height: 278}
	lauraImg := frontend.Image{Kind: frontend.ImagePerson, Path: "cast-laura", Tag: "v1", Subject: fmt.Sprintf("tmdb:%d", lauraNum), Width: 185, Height: 278}

	ashleyDetail := frontend.PersonDetail{
		ID:          ashleyID,
		TMDB:        ashleyNum,
		Name:        "Ashley Johnson",
		ArtworkItem: game1.Key,
		Images:      []frontend.Image{ashleyImg},
		KnownFor:    "Acting",
	}
	lauraDetail := frontend.PersonDetail{
		ID:          lauraID,
		TMDB:        lauraNum,
		Name:        "Laura Bailey",
		ArtworkItem: game1.Key,
		Images:      []frontend.Image{lauraImg},
		KnownFor:    "Acting",
	}
	troyDetail := frontend.PersonDetail{
		ID:       troyID,
		TMDB:     troyNum,
		Name:     "Troy Baker",
		KnownFor: "Acting",
	}

	views := []frontend.View{
		{
			ID:        "23",
			LibraryID: 23,
			Name:      "Movies",
			Type:      frontend.TypeMovie,
			Revision:  "r1",
		},
		{
			ID:        "77",
			LibraryID: 77,
			Name:      "Video Games",
			Type:      frontend.TypeMovie,
			PluginID:  "community.games",
			KindKey:   "game",
			Card:      "landscape",
			Tabs: []frontend.ViewTab{
				{ID: "recommended", Label: "Recommended", Kind: "recommended"},
				{ID: "library", Label: "Library", Kind: "library"},
				{ID: "developers", Label: "Developers", Kind: "collections", Facet: "developers", Order: "count", Search: true, SearchPlaceholder: "Search developers"},
				{ID: "cast", Label: "Voice cast", Kind: "people", Facet: "voiceCast", Order: "name", Search: true, SearchPlaceholder: "Search voice cast"},
				{ID: "publishers", Label: "Publishers", Kind: "collections", Facet: "platforms", Order: "name", Search: false},
			},
			Sorts: []string{"released", "added", "title", "year", "rating", "index"},
			Facets: []frontend.ViewFacet{
				{ID: "platforms", Label: "Platforms", Type: "strings", Multi: true},
				{ID: "developers", Label: "Developers", Type: "strings", Multi: true},
				{ID: "voiceCast", Label: "Voice cast", Type: "people", Multi: true},
				{ID: "genres", Label: "Genres", Type: "strings", Multi: true},
				{ID: "released", Label: "Released", Type: "date", Multi: true},
				{ID: "playtime", Label: "Playtime", Type: "duration", Multi: true},
			},
			Revision: "r1",
		},
	}

	exp := &pluginPresExperience{
		images: map[core.RatingKey][]frontend.Image{
			game1.Key:    {{Kind: frontend.ImageThumb, Path: "g1-thumb", Width: 1280, Height: 720}},
			game2.Key:    {{Kind: frontend.ImageThumb, Path: "g2-backdrop-as-thumb", Width: 1920, Height: 1080}, {Kind: frontend.ImageBackdrop, Path: "g2-backdrop", Width: 1920, Height: 1080}},
			catMovie.Key: {{Kind: frontend.ImagePoster, Path: "cat-poster", Width: 1000, Height: 1500}},
		},
		buckets: map[string][]frontend.FacetBucket{
			"platforms": {
				{ID: "snes", Label: "Super Nintendo", Count: 12},
				{ID: "gba", Label: "Game Boy Advance", Count: 7},
				{ID: "ps2", Label: "PlayStation 2", Count: 4},
			},
			"playtime": {
				{ID: "under-10m", Label: "Under 10 min", Count: 3},
				{ID: "1-2h", Label: "1-2 h", Count: 9},
			},
		},
		colPages: map[string]frontend.FacetCollectionPage{
			"developers": {
				Items: []frontend.FacetCollection{
					{ID: "nintendo", Name: "Nintendo", ItemCount: 15, NewestItem: &game1},
					{ID: "capcom", Name: "Capcom", ItemCount: 8, NewestItem: &game2},
					{ID: "zoe-mode", Name: "Zoë Mode", ItemCount: 5, NewestItem: &game1},
					{ID: "zoe-interactive", Name: "Zoë Interactive", ItemCount: 2, NewestItem: &game2},
				},
			},
			"platforms": {
				Items: []frontend.FacetCollection{
					{ID: "snes", Name: "Super Nintendo", ItemCount: 12, NewestItem: &game1},
				},
			},
		},
		peoplePages: map[string]frontend.FacetPersonPage{
			"voiceCast": {
				Items: []frontend.FacetPerson{
					{Person: troyDetail, ItemCount: 1},
					{Person: lauraDetail, ItemCount: 3},
					{Person: ashleyDetail, ItemCount: 2},
				},
			},
		},
		peopleByID: map[string]frontend.PersonDetail{
			ashleyID: ashleyDetail,
			lauraID:  lauraDetail,
			troyID:   troyDetail,
		},
		pluginRows: map[string][]frontend.Row{
			"77": {
				{
					ID:      "plugin:77:added",
					Title:   "Recently Added",
					Purpose: frontend.RowRecentlyAdded,
					Kind:    "recentlyAdded",
					Style:   "shelf",
					Items:   []frontend.Title{game1, game2},
				},
				{
					ID:      "plugin:77:facet:developers:nintendo",
					Title:   "From Nintendo",
					Purpose: frontend.RowRecommendation,
					Kind:    "facet",
					Style:   "shelf",
					Items:   []frontend.Title{game1},
				},
			},
		},
	}
	exp.ViewsFunc = func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
		return views, nil
	}
	exp.FacetsFunc = func(_ context.Context, _ *frontend.Viewer, _ core.Device, viewID, facet string) ([]core.FacetValue, error) {
		if viewID == "23" && facet == "genre" {
			return []core.FacetValue{{Title: "Sci-Fi"}}, nil
		}
		return nil, nil
	}
	exp.ItemArtworkFunc = func(_ context.Context, _ *frontend.Viewer, _ core.Device, k core.RatingKey) ([]frontend.Image, bool, error) {
		return exp.images[k], true, nil
	}
	exp.LibraryItemsFunc = func(_ context.Context, _ *frontend.Viewer, _ core.Device, id string, q frontend.ItemQuery) (frontend.Page, error) {
		exp.lastLibID = id
		exp.lastQuery = q
		if q.Offset > 0 {
			return frontend.Page{Items: []frontend.Title{}}, nil
		}
		if id == "23" {
			return frontend.Page{Items: []frontend.Title{catMovie}}, nil
		}
		return frontend.Page{Items: []frontend.Title{game1, game2}}, nil
	}
	exp.RowsFunc = func(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
		exp.rowRequests = append(exp.rowRequests, req)
		if req.Placement == frontend.PlacementLibrary && req.ViewID == "23" {
			return []frontend.Row{{
				ID:      "cat-lib-23",
				Title:   "Recently Added Movies",
				Purpose: frontend.RowRecentlyAdded,
				Style:   "shelf",
				Items:   []frontend.Title{catMovie},
			}}, nil
		}
		return []frontend.Row{}, nil
	}

	imgSvc, err := identity.NewArtworkImageGrantService(db, "vela", grants, func() frontend.Experience { return exp }, castArtwork{}, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{
		ctx:     ctx,
		db:      db,
		seal:    seal,
		replays: make(map[string]*replay),
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity:       identityFake{},
			Experience:     exp,
			ImageGrants:    imgSvc,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.15"},
		},
	}
	inst.routes()

	idemSeq := 0
	doReq := func(method, path string, reqBody object) (int, object) {
		t.Helper()
		var r *http.Request
		if reqBody != nil {
			idemSeq++
			if _, ok := reqBody["idempotencyKey"]; !ok {
				reqBody["idempotencyKey"] = fmt.Sprintf("test-idem-%d", idemSeq)
			}
			raw, err := json.Marshal(reqBody)
			if err != nil {
				t.Fatal(err)
			}
			r = httptest.NewRequest(method, path, bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Vela-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.serve(w, r)
		var out object
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s decode: %v body=%s", method, path, err, w.Body.String())
		}
		return w.Code, out
	}

	// 1. Capabilities and bootstrap advertise searchLibraries: true.
	code, caps := doReq(http.MethodGet, "/vela/v0/capabilities", nil)
	if code != 200 || caps["searchLibraries"] != true {
		t.Fatalf("capabilities searchLibraries = %v (code=%d)", caps["searchLibraries"], code)
	}
	code, boot := doReq(http.MethodGet, "/vela/v0/bootstrap", nil)
	if code != 200 || boot["capabilities"].(map[string]any)["searchLibraries"] != true {
		t.Fatalf("bootstrap capabilities.searchLibraries = %v (code=%d)", boot["capabilities"], code)
	}

	// 2. GET /vela/v0/libraries: presentation.card, collections+people tabs (with order), plugin sorts & facets, catalogue genres untouched.
	code, libsResp := doReq(http.MethodGet, "/vela/v0/libraries", nil)
	if code != 200 {
		t.Fatalf("libraries code=%d body=%v", code, libsResp)
	}
	libItems := libsResp["items"].([]any)
	catLib := libItems[0].(map[string]any)
	plugLib := libItems[1].(map[string]any)

	if catLib["presentation"].(map[string]any)["card"] != "poster" {
		t.Fatalf("catalogue presentation = %v", catLib["presentation"])
	}
	catFilters := catLib["browse"].(map[string]any)["filters"].(map[string]any)
	if catFilters["facets"] != nil || !reflect.DeepEqual(catFilters["genres"], []any{"Sci-Fi"}) {
		t.Fatalf("catalogue filters altered: %v", catFilters)
	}
	if len(catLib["tabs"].([]any)) != 3 {
		t.Fatalf("catalogue tabs = %v", catLib["tabs"])
	}

	if plugLib["presentation"].(map[string]any)["card"] != "landscape" {
		t.Fatalf("plugin presentation = %v", plugLib["presentation"])
	}
	plugTabs := plugLib["tabs"].([]any)
	if len(plugTabs) != 5 {
		t.Fatalf("expected 5 plugin tabs, got %v", plugTabs)
	}
	for _, idx := range []int{0, 1} {
		nonSearchTab := plugTabs[idx].(map[string]any)
		if _, has := nonSearchTab["search"]; has {
			t.Fatalf("recommended/library tab must not carry search: %v", nonSearchTab)
		}
	}
	devTab := plugTabs[2].(map[string]any)
	if devTab["id"] != "developers" || devTab["kind"] != "collections" || devTab["facet"] != "developers" || devTab["order"] != "count" || devTab["search"] != true || devTab["searchPlaceholder"] != "Search developers" || devTab["sort"] != "released" || devTab["direction"] != "desc" {
		t.Fatalf("unexpected developers collections tab: %v", devTab)
	}
	castTab := plugTabs[3].(map[string]any)
	if castTab["id"] != "cast" || castTab["label"] != "Voice cast" || castTab["kind"] != "people" || castTab["facet"] != "voiceCast" || castTab["order"] != "name" || castTab["search"] != true || castTab["searchPlaceholder"] != "Search voice cast" {
		t.Fatalf("unexpected voiceCast people tab: %v", castTab)
	}
	pubTab := plugTabs[4].(map[string]any)
	if pubTab["id"] != "publishers" || pubTab["search"] != false || pubTab["searchPlaceholder"] != nil {
		t.Fatalf("unexpected search:false publishers tab: %v", pubTab)
	}
	plugBrowse := plugLib["browse"].(map[string]any)
	if plugBrowse["defaultSort"] != "released" || plugBrowse["defaultDirection"] != "desc" || len(plugBrowse["sorts"].([]any)) != 7 {
		t.Fatalf("unexpected plugin browse sorts: %v", plugBrowse)
	}
	plugFilters := plugBrowse["filters"].(map[string]any)
	if plugFilters["genre"] != nil || plugFilters["genres"] != nil || len(plugFilters["facets"].([]any)) != 6 {
		t.Fatalf("unexpected plugin browse filters: %v", plugFilters)
	}

	// 3. GET /vela/v0/libraries/{id}/facets/{field}: paging, query filter, 404 on unknown.
	code, facetPage1 := doReq(http.MethodGet, "/vela/v0/libraries/77/facets/platforms?limit=2", nil)
	if code != 200 || facetPage1["continuation"] != "more" || len(facetPage1["items"].([]any)) != 2 {
		t.Fatalf("facets page1 = %d %v", code, facetPage1)
	}
	fCursor := facetPage1["nextCursor"].(string)
	code, facetPage2 := doReq(http.MethodGet, "/vela/v0/libraries/77/facets/platforms?limit=2&cursor="+url.QueryEscape(fCursor), nil)
	if code != 200 || facetPage2["continuation"] != "exhausted" || len(facetPage2["items"].([]any)) != 1 {
		t.Fatalf("facets page2 = %d %v", code, facetPage2)
	}
	code, facetFiltered := doReq(http.MethodGet, "/vela/v0/libraries/77/facets/platforms?query=advance", nil)
	if code != 200 || len(facetFiltered["items"].([]any)) != 1 || facetFiltered["items"].([]any)[0].(map[string]any)["id"] != "gba" {
		t.Fatalf("facets query=advance = %d %v", code, facetFiltered)
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/facets/unknown", nil); code != 404 {
		t.Fatalf("undeclared facet status=%d, want 404", code)
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/23/facets/genre", nil); code != 404 {
		t.Fatalf("catalogue facet route status=%d, want 404", code)
	}

	// 4. GET /vela/v0/libraries/{id}/items: repeatable facet.<field>=, query=, sort=index, landscape kind="thumb" art.
	code, itemsResp := doReq(http.MethodGet, "/vela/v0/libraries/77/items?facet.platforms=snes&facet.platforms=gba&facet.genres=rpg&query=chrono&sort=index&direction=asc", nil)
	if code != 200 {
		t.Fatalf("plugin items status=%d body=%v", code, itemsResp)
	}
	if exp.lastQuery.Sort != frontend.SortIndex || exp.lastQuery.Desc || exp.lastQuery.Filter.Query != "chrono" {
		t.Fatalf("plugin items query not mapped: %+v", exp.lastQuery)
	}
	if !reflect.DeepEqual(exp.lastQuery.Filter.Facets, map[string][]string{"platforms": {"snes", "gba"}, "genres": {"rpg"}}) {
		t.Fatalf("plugin items facets = %+v", exp.lastQuery.Filter.Facets)
	}
	for idx, rawEntry := range itemsResp["items"].([]any) {
		art := rawEntry.(map[string]any)["title"].(map[string]any)["artwork"].([]any)
		if len(art) == 0 || art[0].(map[string]any)["kind"] != "thumb" {
			t.Fatalf("item[%d] expected first artwork kind=thumb on landscape library, got %v", idx, art)
		}
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/23/items?facet.platforms=snes", nil); code != 400 {
		t.Fatalf("catalogue facet.* status=%d, want 400", code)
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/items?facet.undeclared=x", nil); code != 400 {
		t.Fatalf("undeclared facet.* status=%d, want 400", code)
	}

	// 5. GET /vela/v0/libraries/{id}/collections?tab=developers: paged facet collections with landscape thumb art.
	code, colP1 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&limit=1", nil)
	if code != 200 || colP1["continuation"] != "more" || exp.lastColOrder != "count" {
		t.Fatalf("facet collections page1 = %d order=%q %v", code, exp.lastColOrder, colP1)
	}
	cEntry := colP1["items"].([]any)[0].(map[string]any)
	cObj := cEntry["collection"].(map[string]any)
	if cEntry["id"] != "facet:developers:nintendo" || cObj["name"] != "Nintendo" || cObj["itemCount"] != float64(15) {
		t.Fatalf("unexpected facet collection entry: %v", cEntry)
	}
	cArt := cObj["artwork"].([]any)
	if len(cArt) != 1 || cArt[0].(map[string]any)["kind"] != "thumb" {
		t.Fatalf("expected signed thumb art on landscape facet collection, got %v", cArt)
	}
	cNext := colP1["nextCursor"].(string)
	code, colP2 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&limit=1&cursor="+url.QueryEscape(cNext), nil)
	if code != 200 || colP2["continuation"] != "more" || colP2["items"].([]any)[0].(map[string]any)["id"] != "facet:developers:capcom" {
		t.Fatalf("facet collections page2 = %d %v", code, colP2)
	}
	// 5a. Collections tab query filter: diacritic match ("zoe" -> "Zoë Mode", "Zoë Interactive"), order preserved, cursor hash includes query.
	code, zoeP1 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&query=zoe&limit=1", nil)
	if code != 200 || zoeP1["continuation"] != "more" || exp.lastColQuery != "zoe" || zoeP1["items"].([]any)[0].(map[string]any)["id"] != "facet:developers:zoe-mode" {
		t.Fatalf("collections query=zoe page1 = %d %v", code, zoeP1)
	}
	zoeCursor := zoeP1["nextCursor"].(string)
	if badCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&query=cap&limit=1&cursor="+url.QueryEscape(zoeCursor), nil); badCode != 409 {
		t.Fatalf("collections cursor with altered query status=%d, want 409", badCode)
	}
	code, zoeP2 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&query=zoe&limit=1&cursor="+url.QueryEscape(zoeCursor), nil)
	if code != 200 || zoeP2["continuation"] != "exhausted" || zoeP2["items"].([]any)[0].(map[string]any)["id"] != "facet:developers:zoe-interactive" {
		t.Fatalf("collections query=zoe page2 = %d %v", code, zoeP2)
	}
	// Empty/whitespace query acts as no filter; >100 chars or search:false returns 400 unsupported_library_query.
	code, emptyQ := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&query=%20%20", nil)
	if code != 200 || len(emptyQ["items"].([]any)) != 4 {
		t.Fatalf("empty trimmed query should not filter, got %d %v", code, emptyQ)
	}
	code, searchFalse := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=publishers&query=snes", nil)
	if code != 400 || searchFalse["error"].(map[string]any)["code"] != "unsupported_library_query" {
		t.Fatalf("query on search:false tab = %d %v, want 400 unsupported_library_query", code, searchFalse)
	}
	if okCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=publishers", nil); okCode != 200 {
		t.Fatalf("search:false tab without query status=%d, want 200", okCode)
	}
	if longCode, longBody := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=developers&query="+strings.Repeat("a", 101), nil); longCode != 400 || longBody["error"].(map[string]any)["code"] != "unsupported_library_query" {
		t.Fatalf("query > 100 chars = %d %v, want 400 unsupported_library_query", longCode, longBody)
	}
	if catQCode, catQBody := doReq(http.MethodGet, "/vela/v0/libraries/23/collections?query=star", nil); catQCode != 400 || catQBody["error"].(map[string]any)["code"] != "unsupported_library_query" {
		t.Fatalf("catalogue collections query = %d %v, want 400 unsupported_library_query", catQCode, catQBody)
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=missing", nil); code != 404 {
		t.Fatalf("unknown collections tab status=%d, want 404", code)
	}

	// 5b. GET /vela/v0/libraries/{id}/collections?tab=cast: people tab lists 3 people alphabetically with counts,
	// batch-signs headshots in one call, and each person ID resolves on GET /vela/v0/people/{id} and /people/{id}/titles.
	exp.personImageBatches = 0
	code, castPage := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=cast", nil)
	if code != 200 || castPage["continuation"] != "exhausted" || exp.lastPeopleOrder != "name" {
		t.Fatalf("people tab = %d order=%q body=%v", code, exp.lastPeopleOrder, castPage)
	}
	if exp.personImageBatches != 1 {
		t.Fatalf("expected exactly 1 batch portrait signing call, got %d", exp.personImageBatches)
	}
	castItems := castPage["items"].([]any)
	if len(castItems) != 3 {
		t.Fatalf("expected 3 people on cast tab, got %d: %v", len(castItems), castItems)
	}
	wantCast := []struct {
		id       string
		name     string
		count    float64
		hasPhoto bool
	}{
		{ashleyID, "Ashley Johnson", 2, true},
		{lauraID, "Laura Bailey", 3, true},
		{troyID, "Troy Baker", 1, false},
	}
	for idx, w := range wantCast {
		entry := castItems[idx].(map[string]any)
		if entry["id"] != "person:"+w.id || entry["kind"] != "person" || entry["itemCount"] != w.count || entry["receiptToken"] != "" {
			t.Fatalf("cast[%d] envelope = %v, want id=person:%s kind=person itemCount=%v receiptToken=\"\"", idx, entry, w.id, w.count)
		}
		pWire := entry["person"].(map[string]any)
		if pWire["id"] != w.id || pWire["name"] != w.name {
			t.Fatalf("cast[%d] person = %v, want id=%s name=%s", idx, pWire, w.id, w.name)
		}
		pCode, personEndpoint := doReq(http.MethodGet, "/vela/v0/people/"+url.PathEscape(w.id), nil)
		if pCode != 200 {
			t.Fatalf("GET /vela/v0/people/%s status=%d body=%v", w.id, pCode, personEndpoint)
		}
		tCode, titlesEndpoint := doReq(http.MethodGet, "/vela/v0/people/"+url.PathEscape(w.id)+"/titles", nil)
		if tCode != 200 {
			t.Fatalf("GET /vela/v0/people/%s/titles status=%d body=%v", w.id, tCode, titlesEndpoint)
		}
		if w.hasPhoto {
			tabPhoto, ok := pWire["photo"].(map[string]any)
			if !ok || tabPhoto["kind"] != "person" || len(tabPhoto["sizes"].([]any)) != 1 {
				t.Fatalf("cast[%d] expected personPhotoWire with 1 size, got %v", idx, pWire["photo"])
			}
			epPhoto, ok := personEndpoint["photo"].(map[string]any)
			if !ok || tabPhoto["id"] != epPhoto["id"] || tabPhoto["kind"] != epPhoto["kind"] || len(tabPhoto["sizes"].([]any)) != len(epPhoto["sizes"].([]any)) {
				t.Fatalf("cast[%d] photo shape %v != /people/{id} photo shape %v", idx, tabPhoto, epPhoto)
			}
		} else if pWire["photo"] != nil {
			t.Fatalf("cast[%d] expected nil photo when person has no portrait, got %v", idx, pWire["photo"])
		}
	}

	// 5c. People tab query filter: diacritic match ("zoe" -> "Zoë Example", "Zoë Wanamaker"), order preserved, cursor paging.
	zoeExTMDB := core.PluginPersonID("Zoë Example")
	zoeExID := core.FacetID("person", fmt.Sprintf("%d", zoeExTMDB))
	zoeExDetail := frontend.PersonDetail{ID: zoeExID, TMDB: zoeExTMDB, Name: "Zoë Example", ArtworkItem: game2.Key}
	zoeWanTMDB := core.PluginPersonID("Zoë Wanamaker")
	zoeWanID := core.FacetID("person", fmt.Sprintf("%d", zoeWanTMDB))
	zoeWanDetail := frontend.PersonDetail{ID: zoeWanID, TMDB: zoeWanTMDB, Name: "Zoë Wanamaker", ArtworkItem: game1.Key}
	exp.peopleByID[zoeExID] = zoeExDetail
	exp.peopleByID[zoeWanID] = zoeWanDetail
	exp.peoplePages["voiceCast"] = frontend.FacetPersonPage{
		Items: append(exp.peoplePages["voiceCast"].Items,
			frontend.FacetPerson{Person: zoeWanDetail, ItemCount: 4},
			frontend.FacetPerson{Person: zoeExDetail, ItemCount: 2},
		),
	}
	code, castZoeP1 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=cast&query=zoe&limit=1", nil)
	if code != 200 || castZoeP1["continuation"] != "more" || exp.lastPeopleQuery != "zoe" || len(castZoeP1["items"].([]any)) != 1 {
		t.Fatalf("cast query=zoe page1 = %d %v", code, castZoeP1)
	}
	if castZoeP1["items"].([]any)[0].(map[string]any)["person"].(map[string]any)["name"] != "Zoë Example" {
		t.Fatalf("cast query=zoe page1 order = %v, want Zoë Example first", castZoeP1["items"])
	}
	castZoeCursor := castZoeP1["nextCursor"].(string)
	if badCastCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=cast&query=laura&limit=1&cursor="+url.QueryEscape(castZoeCursor), nil); badCastCode != 409 {
		t.Fatalf("cast cursor with altered query status=%d, want 409", badCastCode)
	}
	code, castZoeP2 := doReq(http.MethodGet, "/vela/v0/libraries/77/collections?tab=cast&query=zoe&limit=1&cursor="+url.QueryEscape(castZoeCursor), nil)
	if code != 200 || castZoeP2["continuation"] != "exhausted" || castZoeP2["items"].([]any)[0].(map[string]any)["person"].(map[string]any)["name"] != "Zoë Wanamaker" {
		t.Fatalf("cast query=zoe page2 = %d %v", code, castZoeP2)
	}

	// 6. GET /vela/v0/libraries/{id}/rows: plugin recommended rows & catalogue PlacementLibrary rows.
	code, pRows := doReq(http.MethodGet, "/vela/v0/libraries/77/rows", nil)
	if code != 200 || len(pRows["items"].([]any)) != 2 {
		t.Fatalf("plugin library rows = %d %v", code, pRows)
	}
	r0 := pRows["items"].([]any)[0].(map[string]any)
	r0Art := r0["items"].([]any)[0].(map[string]any)["title"].(map[string]any)["artwork"].([]any)
	if r0["name"] != "Recently Added" || len(r0Art) == 0 || r0Art[0].(map[string]any)["kind"] != "thumb" {
		t.Fatalf("unexpected plugin row wire: %v", r0)
	}

	code, cRows := doReq(http.MethodGet, "/vela/v0/libraries/23/rows", nil)
	if code != 200 || len(cRows["items"].([]any)) != 1 || cRows["items"].([]any)[0].(map[string]any)["id"] != "cat-lib-23" {
		t.Fatalf("catalogue library rows = %d %v", code, cRows)
	}
	if code, _ := doReq(http.MethodGet, "/vela/v0/libraries/hidden/rows", nil); code != 404 {
		t.Fatalf("hidden library rows status=%d, want 404", code)
	}

	// 7. POST /vela/v0/search with "library" for catalogue & plugin libraries, sort+direction+kind, and 400 invalid_library.
	code, sPlug := doReq(http.MethodPost, "/vela/v0/search", object{
		"query":      "chrono",
		"mode":       "fast",
		"library":    "77",
		"sort":       "index",
		"direction":  "asc",
		"generation": 1,
	})
	if code != 200 || exp.lastLibID != "77" || exp.lastQuery.Filter.Query != "chrono" || exp.lastQuery.Sort != frontend.SortIndex {
		t.Fatalf("plugin library search = %d lib=%s q=%+v body=%v", code, exp.lastLibID, exp.lastQuery, sPlug)
	}
	sPlugArt := sPlug["items"].([]any)[0].(map[string]any)["artwork"].([]any)
	if len(sPlugArt) == 0 || sPlugArt[0].(map[string]any)["kind"] != "thumb" {
		t.Fatalf("plugin library search missing landscape thumb art: %v", sPlugArt)
	}

	code, sCat := doReq(http.MethodPost, "/vela/v0/search", object{
		"query":      "arrival",
		"mode":       "fast",
		"library":    "23",
		"sort":       "released",
		"direction":  "desc",
		"kind":       "movie",
		"generation": 2,
	})
	if code != 200 || exp.lastLibID != "23" || exp.lastQuery.Filter.Query != "arrival" || !exp.lastQuery.Desc {
		t.Fatalf("catalogue library search = %d lib=%s q=%+v body=%v", code, exp.lastLibID, exp.lastQuery, sCat)
	}

	// Incompatible library + kind returns empty items without error.
	code, sMismatch := doReq(http.MethodPost, "/vela/v0/search", object{
		"query":      "arrival",
		"mode":       "fast",
		"library":    "23",
		"kind":       "show",
		"generation": 3,
	})
	if code != 200 || len(sMismatch["items"].([]any)) != 0 {
		t.Fatalf("mismatched kind+library search = %d %v", code, sMismatch)
	}

	// Unknown / not-visible library returns 400 invalid_library.
	code, sBad := doReq(http.MethodPost, "/vela/v0/search", object{
		"query":      "chrono",
		"mode":       "fast",
		"library":    "999",
		"generation": 4,
	})
	errObj, _ := sBad["error"].(map[string]any)
	errCode, _ := errObj["code"].(string)
	if code != 400 || !strings.Contains(errCode, "invalid_library") {
		t.Fatalf("invalid library search = %d %v, want 400 invalid_library", code, sBad)
	}

	// 8. GET /vela/v0/libraries/{id}/rows cursor pagination across 30+ plugin rows.
	manyRows := make([]frontend.Row, 0, 32)
	for n := 1; n <= 32; n++ {
		manyRows = append(manyRows, frontend.Row{
			ID:      fmt.Sprintf("plugin:77:shelf:topic-%02d", n),
			Title:   fmt.Sprintf("Topic %02d", n),
			Purpose: frontend.RowRecommendation,
			Kind:    "shelf",
			Style:   "shelf",
			Items:   []frontend.Title{game1},
		})
	}
	exp.pluginRows["77"] = manyRows

	var gotRowIDs []string
	cursor := ""
	pages := 0
	for {
		path := "/vela/v0/libraries/77/rows?limit=10"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		code, page := doReq(http.MethodGet, path, nil)
		if code != 200 {
			t.Fatalf("plugin rows page %d status=%d body=%v", pages+1, code, page)
		}
		pages++
		items := page["items"].([]any)
		for _, raw := range items {
			gotRowIDs = append(gotRowIDs, raw.(map[string]any)["id"].(string))
		}
		if pages == 1 {
			if page["continuation"] != "more" {
				t.Fatalf("page 1 continuation=%v, want more", page["continuation"])
			}
			firstCursor, _ := page["nextCursor"].(string)
			if firstCursor == "" {
				t.Fatalf("page 1 missing nextCursor: %v", page)
			}
			// Changing limit invalidates the cursor (409 cursor_expired).
			if badLimitCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/rows?limit=5&cursor="+url.QueryEscape(firstCursor), nil); badLimitCode != 409 {
				t.Fatalf("rows cursor with altered limit status=%d, want 409", badLimitCode)
			}
		}
		if page["continuation"] == "exhausted" {
			if _, hasCursor := page["nextCursor"]; hasCursor {
				t.Fatalf("exhausted rows page must omit nextCursor: %v", page)
			}
			break
		}
		if page["continuation"] != "more" {
			t.Fatalf("unexpected continuation %v on page %d", page["continuation"], pages)
		}
		cursor, _ = page["nextCursor"].(string)
		if cursor == "" || pages > 10 {
			t.Fatalf("bad pagination progress at page %d: cursor=%q", pages, cursor)
		}
	}
	if pages != 4 || len(gotRowIDs) != 32 || gotRowIDs[0] != "plugin:77:shelf:topic-01" || gotRowIDs[31] != "plugin:77:shelf:topic-32" {
		t.Fatalf("paged 32 rows: pages=%d count=%d first=%q last=%q", pages, len(gotRowIDs), gotRowIDs[0], gotRowIDs[len(gotRowIDs)-1])
	}
	if badCurCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/77/rows?cursor=not-a-valid-cursor", nil); badCurCode != 409 {
		t.Fatalf("bogus plugin rows cursor status=%d, want 409", badCurCode)
	}
	if catCurCode, _ := doReq(http.MethodGet, "/vela/v0/libraries/23/rows?cursor=anything", nil); catCurCode != 409 {
		t.Fatalf("catalogue rows cursor status=%d, want 409", catCurCode)
	}
}

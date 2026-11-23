package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type branchingTabRowsExp struct {
	pluginPresExperience
	tabRows         map[string][]frontend.Row
	childrenByQuery func(parent core.RatingKey, q frontend.ItemQuery) frontend.Page
	lastTabID       string
	lastChildQuery  frontend.ItemQuery
}

func (e *branchingTabRowsExp) PluginLibraryTabRows(_ context.Context, _ *core.Viewer, _ core.Device, viewID, tabID string) ([]frontend.Row, error) {
	e.lastTabID = tabID
	return e.tabRows[viewID+":"+tabID], nil
}

func (e *branchingTabRowsExp) Children(_ context.Context, _ *core.Viewer, _ core.Device, parent core.RatingKey, q frontend.ItemQuery) (frontend.Page, error) {
	e.lastChildQuery = q
	if e.childrenByQuery != nil {
		return e.childrenByQuery(parent, q), nil
	}
	return frontend.Page{}, nil
}

func TestVelaPluginLevelsCapabilitiesTabRowsCardChildCountsAndRoleFilter(t *testing.T) {
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

	artistKey := core.PluginKey(801)
	albumKey := core.PluginKey(802)
	concert1Key := core.PluginKey(803)
	concert2Key := core.PluginKey(804)

	artist := frontend.Title{
		Key:               artistKey,
		LibraryID:         88,
		Type:              core.TypeShow,
		Title:             "Miles Davis",
		KindLevel:         "artist",
		PrimaryChildLevel: "album",
		MediaClass:        core.MediaClassMusic,
		MediaRole:         core.MediaRoleArtist,
		ChildCount:        3,
		ChildCounts:       map[string]int{"album": 1, "concert": 2},
	}
	album := frontend.Title{
		Key:          albumKey,
		LibraryID:    88,
		Type:         core.TypeSeason,
		Title:        "Kind of Blue",
		KindLevel:    "album",
		MediaClass:   core.MediaClassMusic,
		MediaRole:    core.MediaRoleAlbum,
		ParentKey:    artistKey,
		ParentTitle:  "Miles Davis",
		MediaParents: []core.TitleParent{{Key: artistKey, Title: "Miles Davis", Role: core.MediaRoleArtist}},
	}
	concert1 := frontend.Title{
		Key:          concert1Key,
		LibraryID:    88,
		Type:         core.TypeMovie,
		Title:        "Live at Newport 1958",
		KindLevel:    "concert",
		MediaClass:   core.MediaClassVideo,
		MediaRole:    "concert",
		ParentKey:    artistKey,
		ParentTitle:  "Miles Davis",
		MediaParents: []core.TitleParent{{Key: artistKey, Title: "Miles Davis", Role: core.MediaRoleArtist}},
	}
	concert2 := frontend.Title{
		Key:          concert2Key,
		LibraryID:    88,
		Type:         core.TypeMovie,
		Title:        "Live in Paris 1960",
		KindLevel:    "concert",
		MediaClass:   core.MediaClassVideo,
		MediaRole:    "concert",
		ParentKey:    artistKey,
		ParentTitle:  "Miles Davis",
		MediaParents: []core.TitleParent{{Key: artistKey, Title: "Miles Davis", Role: core.MediaRoleArtist}},
	}

	views := []frontend.View{{
		ID:        "88",
		LibraryID: 88,
		Name:      "Music",
		Type:      core.TypeShow,
		PluginID:  "community.example.music",
		KindKey:   "music",
		Card:      "square",
		Media:     core.MediaClassMusic,
		Tabs: []frontend.ViewTab{
			{ID: "recommended", Label: "Recommended", Kind: "recommended"},
			{ID: "concerts", Label: "Concerts", Kind: "recommended", Card: "landscape"},
			{ID: "artists", Label: "Artists", Kind: "library", Level: "artist"},
		},
	}}

	exp := &branchingTabRowsExp{
		pluginPresExperience: pluginPresExperience{
			images: map[core.RatingKey][]frontend.Image{
				concert1Key: {
					{Kind: frontend.ImagePoster, Path: "con1-poster", Tag: "p1", Width: 600, Height: 900},
					{Kind: frontend.ImageBackdrop, Path: "con1-wide", Tag: "w1", Width: 1920, Height: 1080},
				},
			},
		},
		tabRows: map[string][]frontend.Row{
			"88:recommended": {{
				ID:      "plugin:88:shelf:recent-albums",
				Title:   "Recent albums",
				Purpose: frontend.RowRecentlyAdded,
				Style:   "shelf",
				Items:   []frontend.Title{album},
			}},
			"88:concerts": {
				{
					ID:      "plugin:88:tab:concerts:continue",
					Title:   "Continue Watching",
					Purpose: frontend.RowContinue,
					Style:   "shelf",
					Items:   []frontend.Title{concert1},
				},
				{
					ID:      "plugin:88:tab:concerts:shelf:recent-concerts",
					Title:   "Recent concerts",
					Purpose: frontend.RowRecentlyAdded,
					Style:   "shelf",
					Items:   []frontend.Title{concert1, concert2},
				},
			},
		},
	}
	exp.ViewsFunc = func(context.Context, *frontend.Viewer) ([]frontend.View, error) { return views, nil }
	exp.ItemFunc = func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		switch req.Key {
		case artistKey:
			return frontend.ItemDetail{Title: artist}, nil
		case concert1Key:
			return frontend.ItemDetail{Title: concert1}, nil
		default:
			return frontend.ItemDetail{}, core.ErrNotFound
		}
	}
	var artistChildQueries []frontend.ItemQuery
	exp.childrenByQuery = func(parent core.RatingKey, q frontend.ItemQuery) frontend.Page {
		if parent != artistKey {
			return frontend.Page{}
		}
		artistChildQueries = append(artistChildQueries, q)
		var pool []frontend.Title
		switch {
		case q.Role == "concert" || q.Level == "concert":
			pool = []frontend.Title{concert1, concert2}
		case q.Role == "album" || q.Level == "album":
			pool = []frontend.Title{album}
		default:
			pool = []frontend.Title{album, concert1, concert2}
		}
		start := min(q.Offset, len(pool))
		end := len(pool)
		if q.Limit > 0 {
			end = min(start+q.Limit, len(pool))
		}
		return frontend.Page{Items: pool[start:end], Total: len(pool), TotalExact: true}
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
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.19"},
		},
	}
	inst.routes()

	get := func(path string) (int, object) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Vela-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.serve(w, r)
		var out object
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	// 1. Capabilities advertises features.pluginLevels.
	status, caps := get("/vela/v0/capabilities")
	if status != http.StatusOK {
		t.Fatalf("capabilities status = %d %v", status, caps)
	}
	feats, _ := caps["features"].(map[string]any)
	pl, _ := feats["pluginLevels"].(map[string]any)
	if pl["available"] != true || pl["tabRows"] != true || pl["childRoles"] != true || pl["expandOnOpen"] != true {
		t.Fatalf("features.pluginLevels = %+v, want available/tabRows/childRoles/expandOnOpen all true", pl)
	}

	// 2. Libraries emits LibraryTab.card for recommended tabs.
	status, libs := get("/vela/v0/libraries")
	if status != http.StatusOK {
		t.Fatalf("libraries status = %d", status)
	}
	items := libs["items"].([]any)
	lib0 := items[0].(map[string]any)
	tabs := lib0["tabs"].([]any)
	concertsTab := tabs[1].(map[string]any)
	if concertsTab["id"] != "concerts" || concertsTab["card"] != "landscape" {
		t.Fatalf("concerts tab wire = %+v, want card=landscape", concertsTab)
	}

	// 3. /libraries/{id}/rows?tab=concerts returns tab-scoped rows, applies landscape card override,
	//    and binds tab into cursor hash (cross-tab replay returns 409).
	status, rowsResp := get("/vela/v0/libraries/88/rows?tab=concerts&limit=1")
	if status != http.StatusOK {
		t.Fatalf("rows?tab=concerts status = %d %v", status, rowsResp)
	}
	if exp.lastTabID != "concerts" {
		t.Fatalf("PluginLibraryTabRows tabID = %q, want concerts", exp.lastTabID)
	}
	rows := rowsResp["items"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows?tab=concerts limit=1 len = %d", len(rows))
	}
	row0 := rows[0].(map[string]any)
	if row0["id"] != "plugin:88:tab:concerts:continue" {
		t.Fatalf("concerts tab row0 = %+v, want id=plugin:88:tab:concerts:continue", row0)
	}
	// Tab card="landscape" overrides library card="square", so backdrop artwork is prioritized first.
	row0Items := row0["items"].([]any)
	row0Title := row0Items[0].(map[string]any)["title"].(map[string]any)
	row0Art := row0Title["artwork"].([]any)
	if len(row0Art) == 0 || row0Art[0].(map[string]any)["kind"] != "backdrop" {
		t.Fatalf("concerts tab card override failed: artwork[0] = %+v, want kind=backdrop", row0Art)
	}
	nextCur, _ := rowsResp["nextCursor"].(string)
	if nextCur == "" {
		t.Fatalf("expected nextCursor on rows?tab=concerts&limit=1")
	}
	// Replaying concerts cursor on recommended tab must return 409 Conflict.
	status, _ = get("/vela/v0/libraries/88/rows?tab=recommended&limit=1&cursor=" + nextCur)
	if status != http.StatusConflict {
		t.Fatalf("cross-tab cursor replay status = %d, want 409", status)
	}
	// Unknown tab -> 404; non-recommended tab -> 400.
	if status, _ = get("/vela/v0/libraries/88/rows?tab=nope"); status != http.StatusNotFound {
		t.Fatalf("unknown tab status = %d, want 404", status)
	}
	if status, _ = get("/vela/v0/libraries/88/rows?tab=artists"); status != http.StatusBadRequest {
		t.Fatalf("non-recommended tab status = %d, want 400", status)
	}

	// 4. Container title carries media.childCounts; video child carries kind="movie" + media.class="video" + media.role="concert" + parents.
	artistID := ids.RatingKeyGUID(artistKey)
	status, artistWire := get("/vela/v0/titles/" + artistID)
	if status != http.StatusOK {
		t.Fatalf("artist detail status = %d", status)
	}
	artistTitle, ok := artistWire["title"].(map[string]any)
	if !ok {
		t.Fatalf("artist detail missing title: %+v", artistWire)
	}
	artistMedia, ok := artistTitle["media"].(map[string]any)
	if !ok {
		t.Fatalf("artist title missing media: %+v", artistTitle)
	}
	childCounts := artistMedia["childCounts"].(map[string]any)
	if childCounts["album"] != float64(1) || childCounts["concert"] != float64(2) {
		t.Fatalf("artist media.childCounts = %+v, want {album:1, concert:2}", childCounts)
	}
	if artistMedia["childCount"] != float64(3) {
		t.Fatalf("artist media.childCount = %v, want 3 (direct children)", artistMedia["childCount"])
	}
	// Detail seasons must contain only the primary child level (albums), never mixing concerts.
	seasons, _ := artistWire["seasons"].([]any)
	if len(seasons) != 1 || seasons[0].(map[string]any)["name"] != "Kind of Blue" {
		t.Fatalf("artist detail seasons = %+v, want single album (concerts excluded)", seasons)
	}
	if len(artistChildQueries) == 0 || artistChildQueries[len(artistChildQueries)-1].Level != "album" {
		t.Fatalf("detailChildren artist queries = %+v, want Level=album", artistChildQueries)
	}

	// 4b. Unhydrated artist detail refreshes media.childCount / childCounts after detailChildren hydrates it.
	unhydratedKey := core.PluginKey(809)
	unhydrated := artist
	unhydrated.Key = unhydratedKey
	unhydrated.Title = "Hydrating Artist"
	unhydrated.PrimaryChildLevel = "" // also exercises filterPrimaryBranchItems fallback when PrimaryChildLevel is unset
	unhydrated.ChildCount = 12        // stale leaf count before hydration
	unhydrated.ChildCounts = nil
	hydrated := false
	origItemFunc := exp.ItemFunc
	exp.ItemFunc = func(c context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		if req.Key == unhydratedKey {
			if !hydrated {
				return frontend.ItemDetail{Title: unhydrated}, nil
			}
			done := unhydrated
			done.ChildCount = 3
			done.ChildCounts = map[string]int{"album": 1, "concert": 2}
			return frontend.ItemDetail{Title: done}, nil
		}
		return origItemFunc(c, req)
	}
	origChildren := exp.childrenByQuery
	exp.childrenByQuery = func(parent core.RatingKey, q frontend.ItemQuery) frontend.Page {
		if parent == unhydratedKey {
			hydrated = true
			// Return mixed levels to verify filterPrimaryBranchItems strips concerts from seasons.
			return frontend.Page{Items: []frontend.Title{album, concert1, concert2}, Total: 3, TotalExact: true}
		}
		return origChildren(parent, q)
	}
	status, hydWire := get("/vela/v0/titles/" + ids.RatingKeyGUID(unhydratedKey))
	if status != http.StatusOK {
		t.Fatalf("unhydrated artist detail status = %d", status)
	}
	hydMedia := hydWire["title"].(map[string]any)["media"].(map[string]any)
	hydCounts, _ := hydMedia["childCounts"].(map[string]any)
	if hydMedia["childCount"] != float64(3) || hydCounts["album"] != float64(1) || hydCounts["concert"] != float64(2) {
		t.Fatalf("hydrated detail media = %+v, want childCount=3 childCounts={album:1,concert:2}", hydMedia)
	}
	hydSeasons, _ := hydWire["seasons"].([]any)
	if len(hydSeasons) != 1 || hydSeasons[0].(map[string]any)["name"] != "Kind of Blue" {
		t.Fatalf("unhydrated detail seasons = %+v, want album only", hydSeasons)
	}

	// 5. /titles/{id}/children?role=concert pages within that role, rejects cross-role cursor replay (409),
	//    and ?role=album works for every childCounts key.
	status, chP1 := get("/vela/v0/titles/" + artistID + "/children?role=concert&limit=1")
	if status != http.StatusOK {
		t.Fatalf("children?role=concert p1 status = %d %v", status, chP1)
	}
	chItems := chP1["items"].([]any)
	if len(chItems) != 1 || chP1["continuation"] != "more" {
		t.Fatalf("children?role=concert p1 = %+v", chP1)
	}
	firstConcert := chItems[0].(map[string]any)
	firstMedia := firstConcert["media"].(map[string]any)
	if firstConcert["kind"] != "movie" || firstMedia["class"] != "video" || firstMedia["role"] != "concert" {
		t.Fatalf("concert wire item = %+v", firstConcert)
	}
	chCur, _ := chP1["nextCursor"].(string)
	if chCur == "" {
		t.Fatalf("expected nextCursor on children?role=concert&limit=1")
	}
	// Cross-role cursor replay must return 409.
	if status, _ = get("/vela/v0/titles/" + artistID + "/children?role=album&limit=1&cursor=" + chCur); status != http.StatusConflict {
		t.Fatalf("cross-role children cursor replay status = %d, want 409", status)
	}
	// Second page with matching role succeeds.
	status, chP2 := get("/vela/v0/titles/" + artistID + "/children?role=concert&limit=1&cursor=" + chCur)
	if status != http.StatusOK {
		t.Fatalf("children?role=concert p2 status = %d %v", status, chP2)
	}
	chItems2 := chP2["items"].([]any)
	if len(chItems2) != 1 || chItems2[0].(map[string]any)["name"] != "Live in Paris 1960" || chP2["continuation"] != "exhausted" {
		t.Fatalf("children?role=concert p2 items = %+v", chP2)
	}
	// Role-less childCounts key ("album") works via ?role=album.
	status, albCh := get("/vela/v0/titles/" + artistID + "/children?role=album&limit=10")
	albItems, _ := albCh["items"].([]any)
	if status != http.StatusOK || len(albItems) != 1 || albItems[0].(map[string]any)["name"] != "Kind of Blue" {
		t.Fatalf("children?role=album = status:%d %+v", status, albCh)
	}
	if exp.lastChildQuery.Role != "album" {
		t.Fatalf("lastChildQuery.Role = %q, want album", exp.lastChildQuery.Role)
	}
}

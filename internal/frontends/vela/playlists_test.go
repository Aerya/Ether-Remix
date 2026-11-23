package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	_ "github.com/braedonsaunders/ether/internal/notinterested"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	_ "github.com/braedonsaunders/ether/internal/watchlist"
)

type playlistTestIdentity struct {
	frontend.SurfaceIdentity
	viewers map[string]*frontend.Viewer
}

func (id playlistTestIdentity) ResolveDevice(_ context.Context, token string, d frontend.Device) (*frontend.Viewer, time.Time, error) {
	if d.ID == "" {
		return nil, time.Time{}, frontend.ErrUnauthorized
	}
	v, ok := id.viewers[token]
	if !ok {
		return nil, time.Time{}, frontend.ErrUnauthorized
	}
	cp := *v
	return &cp, time.Now().Add(time.Hour), nil
}

func (id playlistTestIdentity) GrantScope(_ context.Context, token string, d frontend.Device) (string, error) {
	if d.ID == "" {
		return "", frontend.ErrUnauthorized
	}
	v, ok := id.viewers[token]
	if !ok {
		return "", frontend.ErrUnauthorized
	}
	return "family-" + v.Username, nil
}

func (id playlistTestIdentity) GrantScopeCurrent(_ context.Context, scope string, d frontend.Device) (*frontend.Viewer, error) {
	if d.ID == "" {
		return nil, frontend.ErrUnauthorized
	}
	for _, v := range id.viewers {
		if "family-"+v.Username == scope {
			cp := *v
			return &cp, nil
		}
	}
	return nil, frontend.ErrUnauthorized
}

type playlistTestImageSigner struct {
	frontendtest.ImageGrants
}

func (playlistTestImageSigner) SignPage(_ context.Context, req frontend.ImagePageRequest) ([]frontend.ImageBatchItem, error) {
	out := make([]frontend.ImageBatchItem, 0, len(req.Titles))
	for _, t := range req.Titles {
		out = append(out, frontend.ImageBatchItem{
			Item:          t.Key,
			Authoritative: true,
			Images: []frontend.GrantedImage{{
				Image:  frontend.Image{Kind: frontend.ImagePoster, Path: "/art/" + t.Key.String(), Tag: "v1", Width: 600, Height: 900},
				Secret: "sec-" + t.Key.String(),
			}},
		})
	}
	return out, nil
}

type playlistHarness struct {
	t           *testing.T
	inst        *instance
	handler     http.Handler
	state       *state.State
	playlists   core.Playlists
	rawBase     core.Playlists
	exp         *experience.Service
	titles      map[core.RatingKey]core.Title
	children    map[core.RatingKey][]core.Title
	grandkids   map[core.RatingKey][]core.Title
	mu          sync.Mutex
	getCalls    int
	batchCalls  int
	removedKeys []core.RatingKey
	now         time.Time
}

func newPlaylistHarness(t *testing.T, withPlaylists bool) *playlistHarness {
	t.Helper()
	db := store.OpenTest(t)
	h := &playlistHarness{
		t:         t,
		titles:    map[core.RatingKey]core.Title{},
		children:  map[core.RatingKey][]core.Title{},
		grandkids: map[core.RatingKey][]core.Title{},
		now:       time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
	}
	st := state.New(db, state.Options{Now: func() time.Time {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.now = h.now.Add(time.Second)
		return h.now
	}})
	h.state = st
	h.rawBase = st.Playlists()

	viewers := map[string]*frontend.Viewer{
		"tok-1": {ID: 1, Username: "alice", IsOwner: true},
		"tok-2": {ID: 2, Username: "bob"},
	}
	activeUsers := map[int64]*core.User{
		1: {ID: 1, Username: "alice", IsOwner: true},
		2: {ID: 2, Username: "bob"},
	}
	var playlists core.Playlists
	if withPlaylists {
		favSvc, err := favorites.New(favorites.Options{
			DB:      db,
			Marked:  h.rawBase.(core.TransactionalMarkedPlaylists),
			Now:     func() time.Time { return h.now },
			Active:  func(_ context.Context, id int64) (*core.User, error) { return activeUsers[id], nil },
			Allowed: func(context.Context, *core.User, favorites.Subject) bool { return true },
			Changed: func(context.Context, int64, favorites.Subject) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := favorites.NewPlaylists(h.rawBase, favSvc)
		if err != nil {
			t.Fatal(err)
		}
		playlists = wrapped
		h.playlists = wrapped
	}

	sectionFor := func(typ core.MediaType) (int, bool) {
		switch typ {
		case core.TypeMovie:
			return 1, true
		case core.TypeShow, core.TypeSeason, core.TypeEpisode:
			return 2, true
		default:
			return 0, false
		}
	}
	getTitle := func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		h.mu.Lock()
		t, ok := h.titles[k]
		h.mu.Unlock()
		if !ok {
			return nil, core.ErrNotFound
		}
		return &t, nil
	}
	deps := experience.ServiceDeps{
		Protocol: "vela",
		Active: func(_ context.Context, id int64) (*core.User, error) {
			u, ok := activeUsers[id]
			if !ok {
				return nil, core.ErrNotFound
			}
			cp := *u
			return &cp, nil
		},
		Get: func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
			h.mu.Lock()
			h.getCalls++
			h.mu.Unlock()
			return getTitle(ctx, k)
		},
		CachedGetMany: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.batchCalls++
			out := make(map[core.RatingKey]core.Title, len(keys))
			for _, k := range keys {
				if title, ok := h.titles[k]; ok {
					out[k] = title
				}
			}
			return out, nil
		},
		Children: func(_ context.Context, _ *core.User, k core.RatingKey) ([]core.Title, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return append([]core.Title(nil), h.children[k]...), nil
		},
		Grandchildren: func(_ context.Context, _ *core.User, k core.RatingKey) ([]core.Title, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return append([]core.Title(nil), h.grandkids[k]...), nil
		},
		State: st,
		Rows:  &experience.RowsService{},
		Visibility: experience.Visibility{
			GetTitle:   getTitle,
			SectionFor: sectionFor,
		},
		Search: experience.Search{
			Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
				return nil, nil
			},
			Visibility: experience.Visibility{GetTitle: getTitle, SectionFor: sectionFor},
		},
		Playlists: playlists,
		HomeRevision: func(ctx context.Context, u *core.User) (frontend.HomeRevision, error) {
			return experience.ReadHomeRevision(ctx, db, u)
		},
		RemoveContinue: func(ctx context.Context, u *core.User, k core.RatingKey) error {
			h.mu.Lock()
			h.removedKeys = append(h.removedKeys, k)
			h.mu.Unlock()
			return st.HideContinue(ctx, u.ID, k)
		},
	}
	svc, err := experience.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	h.exp = svc

	f := Frontend{DB: db, ReplayKey: make([]byte, 32)}
	mounted, err := f.Mount(context.Background(), frontend.Host{
		Identity:    playlistTestIdentity{viewers: viewers},
		Experience:  svc,
		Sessions:    frontendtest.Sessions{},
		ImageGrants: playlistTestImageSigner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })
	h.inst = mounted.(*instance)
	h.handler = mounted.Handler()
	return h
}

func (h *playlistHarness) do(token, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	h.t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Vela-Device-Id", "dev-1")
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	var out map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

func (h *playlistHarness) seedCatalog() (movieA, movieB, hiddenAdult, showS, season1, ep1, ep2, epAdult, epS2E1 core.RatingKey) {
	movieA = core.MovieKey(101)
	movieB = core.MovieKey(102)
	hiddenAdult = core.MovieKey(103)
	showS = core.ShowKey(200)
	season1 = core.SeasonKey(200, 1)
	season2 := core.SeasonKey(200, 2)
	ep1 = core.EpisodeKey(200, 1, 1)
	ep2 = core.EpisodeKey(200, 1, 2)
	epAdult = core.EpisodeKey(200, 1, 3)
	epS2E1 = core.EpisodeKey(200, 2, 1)

	tMovieA := core.Title{Key: movieA, Type: core.TypeMovie, Title: "Arrival", Year: 2016}
	tMovieB := core.Title{Key: movieB, Type: core.TypeMovie, Title: "Dune", Year: 2021}
	tAdult := core.Title{Key: hiddenAdult, Type: core.TypeMovie, Title: "Hidden Film", Adult: true}
	tShow := core.Title{Key: showS, Type: core.TypeShow, Title: "Andor", Year: 2022}
	tSeason1 := core.Title{Key: season1, Type: core.TypeSeason, Title: "Season 1", ShowKey: showS, Season: 1}
	tSeason2 := core.Title{Key: season2, Type: core.TypeSeason, Title: "Season 2", ShowKey: showS, Season: 2}
	tEp1 := core.Title{Key: ep1, Type: core.TypeEpisode, Title: "Kassa", ShowKey: showS, Season: 1, Episode: 1}
	tEp2 := core.Title{Key: ep2, Type: core.TypeEpisode, Title: "That Would Be Me", ShowKey: showS, Season: 1, Episode: 2}
	tEpAdult := core.Title{Key: epAdult, Type: core.TypeEpisode, Title: "Restricted Ep", ShowKey: showS, Season: 1, Episode: 3, Adult: true}
	tEpS2E1 := core.Title{Key: epS2E1, Type: core.TypeEpisode, Title: "One Year Later", ShowKey: showS, Season: 2, Episode: 1}

	for _, title := range []core.Title{tMovieA, tMovieB, tAdult, tShow, tSeason1, tSeason2, tEp1, tEp2, tEpAdult, tEpS2E1} {
		h.titles[title.Key] = title
	}
	h.children[showS] = []core.Title{tSeason1, tSeason2}
	h.children[season1] = []core.Title{tEp1, tEp2, tEpAdult}
	h.children[season2] = []core.Title{tEpS2E1}
	h.grandkids[showS] = []core.Title{tEp1, tEp2, tEpAdult, tEpS2E1}
	return
}

func errorCode(doc map[string]any) string {
	errObj, _ := doc["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

func TestPlaylistCapabilitiesAndNegotiate(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		h := newPlaylistHarness(t, true)
		w, caps := h.do("tok-1", http.MethodGet, "/vela/v0/capabilities", "")
		if w.Code != 200 {
			t.Fatalf("capabilities status %d: %s", w.Code, w.Body.String())
		}
		features := caps["features"].(map[string]any)
		plCap := features["playlists"].(map[string]any)
		if plCap["available"] != true || plCap["maxEntries"] != float64(frontend.MaxPlaylistEntries) {
			t.Fatalf("capabilities.features.playlists = %#v", plCap)
		}
		rmCap := features["removeFromContinue"].(map[string]any)
		if rmCap["available"] != true {
			t.Fatalf("capabilities.features.removeFromContinue = %#v", rmCap)
		}

		negBody := `{"idempotencyKey":"neg-1","client":{"protocolVersions":[0],"device":{"id":"dev-1","appVersion":"0.2.0"}}}`
		w, neg := h.do("", http.MethodPost, "/vela/v0/negotiate", negBody)
		if w.Code != 200 {
			t.Fatalf("negotiate status %d: %s", w.Code, w.Body.String())
		}
		negFeatures := neg["capabilities"].(map[string]any)["features"].(map[string]any)
		negPl := negFeatures["playlists"].(map[string]any)
		if negPl["available"] != true || negPl["maxEntries"] != float64(frontend.MaxPlaylistEntries) {
			t.Fatalf("negotiate.features.playlists = %#v", negPl)
		}
		if negFeatures["removeFromContinue"].(map[string]any)["available"] != true {
			t.Fatalf("negotiate.features.removeFromContinue = %#v", negFeatures["removeFromContinue"])
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		h := newPlaylistHarness(t, false)
		w, caps := h.do("tok-1", http.MethodGet, "/vela/v0/capabilities", "")
		if w.Code != 200 {
			t.Fatalf("capabilities status %d", w.Code)
		}
		plCap := caps["features"].(map[string]any)["playlists"].(map[string]any)
		if plCap["available"] != false || plCap["maxEntries"] != float64(frontend.MaxPlaylistEntries) {
			t.Fatalf("unavailable playlists cap = %#v", plCap)
		}
		w, doc := h.do("tok-1", http.MethodGet, "/vela/v0/playlists", "")
		if w.Code != 403 || errorCode(doc) != "feature_unavailable" {
			t.Fatalf("GET /playlists when unavailable: %d %s", w.Code, w.Body.String())
		}
		w, doc = h.do("tok-1", http.MethodPost, "/vela/v0/playlists", `{"idempotencyKey":"c1","name":"Test"}`)
		if w.Code != 403 || errorCode(doc) != "feature_unavailable" {
			t.Fatalf("POST /playlists when unavailable: %d %s", w.Code, w.Body.String())
		}
	})
}

func TestPlaylistLifecycleShowSeasonExpansionAndSharing(t *testing.T) {
	h := newPlaylistHarness(t, true)
	movieA, movieB, hiddenAdult, showS, season1, ep1, ep2, _, epS2E1 := h.seedCatalog()

	// 1. Create an empty playlist.
	w, emptyCard := h.do("tok-1", http.MethodPost, "/vela/v0/playlists", `{"idempotencyKey":"create-empty","name":"Watch Later"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create empty: %d %s", w.Code, w.Body.String())
	}
	if emptyCard["kind"] != "playlist" || emptyCard["name"] != "Watch Later" || emptyCard["itemCount"] != float64(0) || emptyCard["smart"] != false || emptyCard["updatedAt"] == "" {
		t.Fatalf("unexpected empty card: %#v", emptyCard)
	}
	if _, ok := emptyCard["artwork"].([]any); !ok {
		t.Fatalf("artwork must be array: %#v", emptyCard["artwork"])
	}

	// 2. Create playlist with movie + show (show expands to visible episodes in order, skipping adult ep).
	createBody, _ := json.Marshal(map[string]any{
		"idempotencyKey": "create-main",
		"name":           "Sci-Fi Marathon",
		"titleIds":       []string{ids.RatingKeyGUID(movieA), ids.RatingKeyGUID(showS)},
	})
	w, mainCard := h.do("tok-1", http.MethodPost, "/vela/v0/playlists", string(createBody))
	if w.Code != http.StatusCreated {
		t.Fatalf("create main: %d %s", w.Code, w.Body.String())
	}
	plID := mainCard["id"].(string)
	if mainCard["itemCount"] != float64(4) {
		t.Fatalf("expected 4 items (movie + 3 visible episodes), got %v", mainCard["itemCount"])
	}
	if art := mainCard["artwork"].([]any); len(art) == 0 {
		t.Fatalf("expected signed playlist artwork on card, got %#v", mainCard["artwork"])
	}

	// Idempotency replay on create returns exact same 201 card without creating a duplicate.
	wReplay, replayCard := h.do("tok-1", http.MethodPost, "/vela/v0/playlists", string(createBody))
	if wReplay.Code != http.StatusCreated || replayCard["id"] != plID {
		t.Fatalf("create replay failed: %d %#v", wReplay.Code, replayCard)
	}

	// 3. Cross-app sharing: underlying state.Playlists() sees the exact same playlist & order.
	plKey, err := ids.ParseRatingKeyGUID(plID)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := h.rawBase.Get(context.Background(), 1, plKey)
	if err != nil {
		t.Fatalf("state.Playlists().Get failed: %v", err)
	}
	wantShared := []core.RatingKey{movieA, ep1, ep2, epS2E1}
	if shared.Title != "Sci-Fi Marathon" || len(shared.Items) != len(wantShared) {
		t.Fatalf("shared playlist = %+v, want %+v", shared, wantShared)
	}
	for idx, want := range wantShared {
		if shared.Items[idx] != want {
			t.Fatalf("shared.Items[%d] = %v, want %v", idx, shared.Items[idx], want)
		}
	}

	// A playlist created externally (e.g. Plex via state.Playlists()) is visible in Vela.
	plexPl, err := h.rawBase.Create(context.Background(), 1, "From Plex", []core.RatingKey{movieB})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Add a season (expands to visible episodes S1E1, S1E2, allowing duplicates).
	addBody, _ := json.Marshal(map[string]any{
		"idempotencyKey": "add-season",
		"titleIds":       []string{ids.RatingKeyGUID(season1)},
	})
	w, addResp := h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+plID+"/entries", string(addBody))
	if w.Code != 200 || addResp["entryCount"] != float64(6) {
		t.Fatalf("add season: %d %s", w.Code, w.Body.String())
	}

	// 5. Paginate GET /vela/v0/playlists with sealed cursors.
	w, page1 := h.do("tok-1", http.MethodGet, "/vela/v0/playlists?limit=2", "")
	if w.Code != 200 || page1["continuation"] != "more" || page1["nextCursor"] == "" {
		t.Fatalf("list page 1: %d %s", w.Code, w.Body.String())
	}
	cursor1 := page1["nextCursor"].(string)
	w, page2 := h.do("tok-1", http.MethodGet, "/vela/v0/playlists?limit=2&cursor="+url.QueryEscape(cursor1), "")
	if w.Code != 200 || page2["continuation"] != "exhausted" {
		t.Fatalf("list page 2: %d %s", w.Code, w.Body.String())
	}
	if _, hasNext := page2["nextCursor"]; hasNext {
		t.Fatalf("exhausted page must omit nextCursor: %#v", page2)
	}
	totalListed := len(page1["items"].([]any)) + len(page2["items"].([]any))
	if totalListed != 3 {
		t.Fatalf("expected 3 playlists across pages, got %d", totalListed)
	}
	// Tampered cursor -> 409 cursor_expired.
	w, badCur := h.do("tok-1", http.MethodGet, "/vela/v0/playlists?limit=2&cursor=tampered", "")
	if w.Code != 409 || errorCode(badCur) != "cursor_expired" {
		t.Fatalf("tampered cursor: %d %s", w.Code, w.Body.String())
	}

	// 6. Paginate GET /vela/v0/playlists/{id} and inspect CollectionContents + RowEntry shape.
	var allEntries []map[string]any
	nextCursor := ""
	for {
		u := "/vela/v0/playlists/" + plID + "?limit=2"
		if nextCursor != "" {
			u += "&cursor=" + url.QueryEscape(nextCursor)
		}
		w, detail := h.do("tok-1", http.MethodGet, u, "")
		if w.Code != 200 {
			t.Fatalf("detail %s: %d %s", u, w.Code, w.Body.String())
		}
		col := detail["collection"].(map[string]any)
		if col["id"] != plID || col["kind"] != "playlist" || col["name"] != "Sci-Fi Marathon" || col["smart"] != false || col["entryCount"] != float64(6) {
			t.Fatalf("unexpected collection header: %#v", col)
		}
		for _, raw := range detail["items"].([]any) {
			e := raw.(map[string]any)
			if e["kind"] != "title" || e["receiptToken"] != "" || e["entryId"] == "" {
				t.Fatalf("unexpected RowEntry: %#v", e)
			}
			titleObj := e["title"].(map[string]any)
			if e["id"] != titleObj["id"] {
				t.Fatalf("entry id %v != title id %v", e["id"], titleObj["id"])
			}
			allEntries = append(allEntries, e)
		}
		if detail["continuation"] == "exhausted" {
			break
		}
		nextCursor = detail["nextCursor"].(string)
	}
	if len(allEntries) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(allEntries))
	}
	// Verify duplicate episodes have distinct entryIds.
	entryIDs := make([]string, len(allEntries))
	seenEntryIDs := map[string]bool{}
	for idx, e := range allEntries {
		eid := e["entryId"].(string)
		if seenEntryIDs[eid] {
			t.Fatalf("duplicate entryId %q", eid)
		}
		seenEntryIDs[eid] = true
		entryIDs[idx] = eid
	}

	// 7. Move entries: move entry[2] (ep2) to front (afterEntryId: null).
	w, _ = h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+plID+"/entries/"+entryIDs[2]+"/move", `{"idempotencyKey":"mv-front","afterEntryId":null}`)
	if w.Code != 200 {
		t.Fatalf("move to front: %d %s", w.Code, w.Body.String())
	}
	// Stale list cursor from before mutation now returns 409 cursor_expired.
	w, staleDoc := h.do("tok-1", http.MethodGet, "/vela/v0/playlists?limit=2&cursor="+url.QueryEscape(cursor1), "")
	if w.Code != 409 || errorCode(staleDoc) != "cursor_expired" {
		t.Fatalf("stale list cursor: %d %s", w.Code, w.Body.String())
	}

	// Move entry[0] (movieA, now at index 1) after entry[3] (epS2E1).
	moveAfterBody, _ := json.Marshal(map[string]any{"idempotencyKey": "mv-after", "afterEntryId": entryIDs[3]})
	w, _ = h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+plID+"/entries/"+entryIDs[0]+"/move", string(moveAfterBody))
	if w.Code != 200 {
		t.Fatalf("move after: %d %s", w.Code, w.Body.String())
	}
	// Missing afterEntryId field -> 400 invalid_request.
	w, doc := h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+plID+"/entries/"+entryIDs[0]+"/move", `{"idempotencyKey":"mv-bad"}`)
	if w.Code != 400 || errorCode(doc) != "invalid_request" {
		t.Fatalf("move missing afterEntryId: %d %s", w.Code, w.Body.String())
	}
	// Verify order after both moves: [entry2, entry1, entry3, entry0, entry4, entry5].
	w, reordered := h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+plID, "")
	if w.Code != 200 {
		t.Fatalf("get after moves: %d %s", w.Code, w.Body.String())
	}
	wantOrder := []string{entryIDs[2], entryIDs[1], entryIDs[3], entryIDs[0], entryIDs[4], entryIDs[5]}
	gotItems := reordered["items"].([]any)
	for idx, wantID := range wantOrder {
		if gotItems[idx].(map[string]any)["entryId"] != wantID {
			t.Fatalf("reordered[%d] = %v, want %v", idx, gotItems[idx].(map[string]any)["entryId"], wantID)
		}
	}

	// 8. GET /vela/v0/titles/{id}/playlists returns every playlist with entryIds (including duplicates & empty).
	smart, err := h.rawBase.(core.SmartPlaylists).CreateSmartPlaylist(context.Background(), 1, "Recent Smart", "/library/sections/1/all")
	if err != nil {
		t.Fatal(err)
	}
	w, tp := h.do("tok-1", http.MethodGet, "/vela/v0/titles/"+ids.RatingKeyGUID(ep1)+"/playlists", "")
	if w.Code != 200 {
		t.Fatalf("title playlists: %d %s", w.Code, w.Body.String())
	}
	tpItems := tp["items"].([]any)
	if len(tpItems) != 4 {
		t.Fatalf("expected 4 viewer playlists in title lookup, got %d: %#v", len(tpItems), tpItems)
	}
	byID := map[string]map[string]any{}
	for _, raw := range tpItems {
		m := raw.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if eids := byID[plID]["entryIds"].([]any); len(eids) != 2 || eids[0] != entryIDs[1] || eids[1] != entryIDs[4] {
		t.Fatalf("main playlist entryIds for ep1 = %#v, want [%s, %s]", eids, entryIDs[1], entryIDs[4])
	}
	plexID := ids.RatingKeyGUID(plexPl.Key)
	if eids := byID[plexID]["entryIds"].([]any); len(eids) != 0 {
		t.Fatalf("plex playlist should have empty entryIds for ep1, got %#v", eids)
	}
	smartID := ids.RatingKeyGUID(smart.Key)
	if byID[smartID]["smart"] != true || len(byID[smartID]["entryIds"].([]any)) != 0 {
		t.Fatalf("smart playlist membership = %#v", byID[smartID])
	}
	// A show's membership is the entries of its episodes (a show is added as its episodes).
	w, tpShow := h.do("tok-1", http.MethodGet, "/vela/v0/titles/"+ids.RatingKeyGUID(showS)+"/playlists", "")
	if w.Code != 200 {
		t.Fatalf("show playlists: %d %s", w.Code, w.Body.String())
	}
	for _, raw := range tpShow["items"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == plID {
			if eids := m["entryIds"].([]any); len(eids) < 2 {
				t.Fatalf("show membership should include its episode entries, got %#v", eids)
			}
		}
	}
	// Hidden title lookup -> 404.
	w, _ = h.do("tok-1", http.MethodGet, "/vela/v0/titles/"+ids.RatingKeyGUID(hiddenAdult)+"/playlists", "")
	if w.Code != 404 {
		t.Fatalf("hidden title playlists: got %d, want 404", w.Code)
	}

	// 9. Remove one duplicate entry by entryId.
	w, _ = h.do("tok-1", http.MethodDelete, "/vela/v0/playlists/"+plID+"/entries/"+entryIDs[1], `{"idempotencyKey":"del-entry-1"}`)
	if w.Code != 200 {
		t.Fatalf("delete entry: %d %s", w.Code, w.Body.String())
	}
	// Removing same entryId again with new idempotencyKey -> 404.
	w, _ = h.do("tok-1", http.MethodDelete, "/vela/v0/playlists/"+plID+"/entries/"+entryIDs[1], `{"idempotencyKey":"del-entry-2"}`)
	if w.Code != 404 {
		t.Fatalf("second delete entry: got %d, want 404", w.Code)
	}

	// 10. Rename (PATCH) with idempotency replay and conflict.
	w, renamed := h.do("tok-1", http.MethodPatch, "/vela/v0/playlists/"+plID, `{"idempotencyKey":"ren-1","name":"Renamed Sci-Fi"}`)
	if w.Code != 200 || renamed["name"] != "Renamed Sci-Fi" || renamed["itemCount"] != float64(5) {
		t.Fatalf("PATCH rename: %d %s", w.Code, w.Body.String())
	}
	// Replay with same key & body -> 200.
	w, renamed2 := h.do("tok-1", http.MethodPatch, "/vela/v0/playlists/"+plID, `{"idempotencyKey":"ren-1","name":"Renamed Sci-Fi"}`)
	if w.Code != 200 || renamed2["name"] != "Renamed Sci-Fi" {
		t.Fatalf("PATCH replay: %d %s", w.Code, w.Body.String())
	}
	// Same key & different body -> 409 idempotency_conflict.
	w, conflict := h.do("tok-1", http.MethodPatch, "/vela/v0/playlists/"+plID, `{"idempotencyKey":"ren-1","name":"Different Name"}`)
	if w.Code != 409 || errorCode(conflict) != "idempotency_conflict" {
		t.Fatalf("PATCH conflict: %d %s", w.Code, w.Body.String())
	}

	// 11. Viewer isolation: viewer 2 cannot read or mutate viewer 1's playlist.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/vela/v0/playlists/" + plID, ""},
		{http.MethodPatch, "/vela/v0/playlists/" + plID, `{"idempotencyKey":"v2-ren","name":"Stolen"}`},
		{http.MethodDelete, "/vela/v0/playlists/" + plID, `{"idempotencyKey":"v2-del"}`},
		{http.MethodPost, "/vela/v0/playlists/" + plID + "/entries", `{"idempotencyKey":"v2-add","titleIds":["` + ids.RatingKeyGUID(movieA) + `"]}`},
		{http.MethodDelete, "/vela/v0/playlists/" + plID + "/entries/" + entryIDs[0], `{"idempotencyKey":"v2-rm"}`},
		{http.MethodPost, "/vela/v0/playlists/" + plID + "/entries/" + entryIDs[0] + "/move", `{"idempotencyKey":"v2-mv","afterEntryId":null}`},
	} {
		w, _ = h.do("tok-2", tc.method, tc.path, tc.body)
		if w.Code != 404 {
			t.Fatalf("viewer 2 %s %s: got %d, want 404", tc.method, tc.path, w.Code)
		}
	}

	// 12. Delete playlist + idempotency replay.
	w, _ = h.do("tok-1", http.MethodDelete, "/vela/v0/playlists/"+plID, `{"idempotencyKey":"del-pl"}`)
	if w.Code != 200 {
		t.Fatalf("DELETE playlist: %d %s", w.Code, w.Body.String())
	}
	w, _ = h.do("tok-1", http.MethodDelete, "/vela/v0/playlists/"+plID, `{"idempotencyKey":"del-pl"}`)
	if w.Code != 200 {
		t.Fatalf("DELETE playlist replay: %d %s", w.Code, w.Body.String())
	}
	w, _ = h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+plID, "")
	if w.Code != 404 {
		t.Fatalf("GET deleted playlist: got %d, want 404", w.Code)
	}
}

func TestPlaylistValidationSmartReadOnlyVisibilityAndLimits(t *testing.T) {
	h := newPlaylistHarness(t, true)
	movieA, movieB, hiddenAdult, _, _, _, _, _, _ := h.seedCatalog()

	// 1. Unsupported kinds -> 422 unsupported_kind.
	chGUID, _ := ids.StringGUID(ids.Channel, "ch-1")
	unsupported := []string{
		ids.RatingKeyGUID(core.CollectionKey(5)),
		ids.RatingKeyGUID(core.SmartKey(6)),
		ids.RatingKeyGUID(core.PlaylistKey(7)),
		ids.RatingKeyGUID(core.ExtraKey(8)),
		chGUID,
		"person:42",
	}
	for idx, rawID := range unsupported {
		body, _ := json.Marshal(map[string]any{
			"idempotencyKey": "bad-kind-" + rawID,
			"name":           "Bad",
			"titleIds":       []string{rawID},
		})
		w, doc := h.do("tok-1", http.MethodPost, "/vela/v0/playlists", string(body))
		if w.Code != 422 || errorCode(doc) != "unsupported_kind" {
			t.Fatalf("case %d (%s): got %d %s, want 422 unsupported_kind", idx, rawID, w.Code, w.Body.String())
		}
	}

	// 2. Smart playlist mutations all answer 409 playlist_read_only.
	smart, err := h.rawBase.(core.SmartPlaylists).CreateSmartPlaylist(context.Background(), 1, "Smart List", "/library/sections/1/all")
	if err != nil {
		t.Fatal(err)
	}
	smartID := ids.RatingKeyGUID(smart.Key)
	for idx, tc := range []struct{ method, path, body string }{
		{http.MethodPatch, "/vela/v0/playlists/" + smartID, `{"idempotencyKey":"sm-1","name":"Nope"}`},
		{http.MethodDelete, "/vela/v0/playlists/" + smartID, `{"idempotencyKey":"sm-2"}`},
		{http.MethodPost, "/vela/v0/playlists/" + smartID + "/entries", `{"idempotencyKey":"sm-3","titleIds":["` + ids.RatingKeyGUID(movieA) + `"]}`},
		{http.MethodDelete, "/vela/v0/playlists/" + smartID + "/entries/1", `{"idempotencyKey":"sm-4"}`},
		{http.MethodPost, "/vela/v0/playlists/" + smartID + "/entries/1/move", `{"idempotencyKey":"sm-5","afterEntryId":null}`},
	} {
		w, doc := h.do("tok-1", tc.method, tc.path, tc.body)
		if w.Code != 409 || errorCode(doc) != "playlist_read_only" {
			t.Fatalf("smart mutation %d (%s %s): got %d %s, want 409 playlist_read_only", idx, tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// 3. Too large (> 5000 entries) -> 422 playlist_too_large on create and add.
	bigShow := core.ShowKey(999)
	h.titles[bigShow] = core.Title{Key: bigShow, Type: core.TypeShow, Title: "Huge Show"}
	bigEps := make([]core.Title, frontend.MaxPlaylistEntries+1)
	for n := range bigEps {
		ek := core.EpisodeKey(999, 1, n+1)
		bigEps[n] = core.Title{Key: ek, Type: core.TypeEpisode, Title: "Ep", ShowKey: bigShow, Season: 1, Episode: n + 1}
	}
	h.grandkids[bigShow] = bigEps
	tooBigCreate, _ := json.Marshal(map[string]any{
		"idempotencyKey": "too-big-create",
		"name":           "Too Big",
		"titleIds":       []string{ids.RatingKeyGUID(bigShow)},
	})
	w, doc := h.do("tok-1", http.MethodPost, "/vela/v0/playlists", string(tooBigCreate))
	if w.Code != 422 || errorCode(doc) != "playlist_too_large" {
		t.Fatalf("too big create: %d %s", w.Code, w.Body.String())
	}

	// Create a playlist with 5000 entries directly and verify adding 1 more fails with 422 playlist_too_large.
	fullItems := make([]core.RatingKey, frontend.MaxPlaylistEntries)
	for n := range fullItems {
		fullItems[n] = movieA
	}
	fullPl, err := h.rawBase.Create(context.Background(), 1, "Full", fullItems)
	if err != nil {
		t.Fatal(err)
	}
	addOne, _ := json.Marshal(map[string]any{
		"idempotencyKey": "too-big-add",
		"titleIds":       []string{ids.RatingKeyGUID(movieB)},
	})
	w, doc = h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+ids.RatingKeyGUID(fullPl.Key)+"/entries", string(addOne))
	if w.Code != 422 || errorCode(doc) != "playlist_too_large" {
		t.Fatalf("too big add: %d %s", w.Code, w.Body.String())
	}

	// 4. Visibility filtering & hidden entry protection + batch reads (no per-entry Get calls).
	mixed, err := h.rawBase.Create(context.Background(), 1, "Mixed", []core.RatingKey{hiddenAdult, movieA, movieB})
	if err != nil {
		t.Fatal(err)
	}
	mixedID := ids.RatingKeyGUID(mixed.Key)
	hiddenEntryID := json.Number(strings.TrimSpace(string(mustMarshal(mixed.ItemIDs[0])))).String()

	h.mu.Lock()
	h.getCalls, h.batchCalls = 0, 0
	h.mu.Unlock()
	w, detail := h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+mixedID, "")
	if w.Code != 200 {
		t.Fatalf("mixed detail: %d %s", w.Code, w.Body.String())
	}
	h.mu.Lock()
	gets, batches := h.getCalls, h.batchCalls
	h.mu.Unlock()
	if gets != 0 || batches == 0 {
		t.Fatalf("expected batch hydration without per-entry Get calls: getCalls=%d batchCalls=%d", gets, batches)
	}
	col := detail["collection"].(map[string]any)
	items := detail["items"].([]any)
	if col["entryCount"] != float64(2) || len(items) != 2 {
		t.Fatalf("hidden entry leaked into detail: col=%#v len=%d", col, len(items))
	}
	// Hidden entryId cannot be removed or moved.
	w, _ = h.do("tok-1", http.MethodDelete, "/vela/v0/playlists/"+mixedID+"/entries/"+hiddenEntryID, `{"idempotencyKey":"rm-hidden"}`)
	if w.Code != 404 {
		t.Fatalf("removing hidden entryId: got %d, want 404", w.Code)
	}
	visEntry1 := items[0].(map[string]any)["entryId"].(string)
	visEntry2 := items[1].(map[string]any)["entryId"].(string)
	w, _ = h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+mixedID+"/entries/"+visEntry2+"/move", `{"idempotencyKey":"mv-vis-front","afterEntryId":null}`)
	if w.Code != 200 {
		t.Fatalf("moving visible entry past hidden entry: %d %s", w.Code, w.Body.String())
	}
	w, afterMove := h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+mixedID, "")
	if w.Code != 200 {
		t.Fatalf("afterMove detail: %d %s", w.Code, w.Body.String())
	}
	movedItems := afterMove["items"].([]any)
	if movedItems[0].(map[string]any)["entryId"] != visEntry2 || movedItems[1].(map[string]any)["entryId"] != visEntry1 {
		t.Fatalf("visible reorder failed around hidden entry: %#v", movedItems)
	}

	// Verify moving an entry backward past a hidden entry (from > rawAfter).
	movieC := core.MovieKey(104)
	h.titles[movieC] = core.Title{Key: movieC, Type: core.TypeMovie, Title: "Blade Runner 2049", Year: 2017}
	sandwich, err := h.rawBase.Create(context.Background(), 1, "Sandwich", []core.RatingKey{movieA, hiddenAdult, movieB, movieC})
	if err != nil {
		t.Fatal(err)
	}
	swID := ids.RatingKeyGUID(sandwich.Key)
	w, swDetail := h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+swID, "")
	if w.Code != 200 {
		t.Fatalf("sandwich detail: %d %s", w.Code, w.Body.String())
	}
	swItems := swDetail["items"].([]any)
	swA := swItems[0].(map[string]any)["entryId"].(string)
	swB := swItems[1].(map[string]any)["entryId"].(string)
	swC := swItems[2].(map[string]any)["entryId"].(string)
	// Move C immediately after A (from=3 > rawAfter=0 -> lands at 1, ahead of hidden & B).
	mvBack, _ := json.Marshal(map[string]any{"idempotencyKey": "mv-sw-back", "afterEntryId": swA})
	w, _ = h.do("tok-1", http.MethodPost, "/vela/v0/playlists/"+swID+"/entries/"+swC+"/move", string(mvBack))
	if w.Code != 200 {
		t.Fatalf("sandwich backward move: %d %s", w.Code, w.Body.String())
	}
	w, swAfter := h.do("tok-1", http.MethodGet, "/vela/v0/playlists/"+swID, "")
	if w.Code != 200 {
		t.Fatalf("swAfter detail: %d %s", w.Code, w.Body.String())
	}
	swAfterItems := swAfter["items"].([]any)
	if len(swAfterItems) != 3 ||
		swAfterItems[0].(map[string]any)["entryId"] != swA ||
		swAfterItems[1].(map[string]any)["entryId"] != swC ||
		swAfterItems[2].(map[string]any)["entryId"] != swB {
		t.Fatalf("sandwich backward move order = %#v, want [%s, %s, %s]", swAfterItems, swA, swC, swB)
	}
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestWatchContinueWatchingRemovalAndHomeRevisionBump(t *testing.T) {
	h := newPlaylistHarness(t, true)
	movieA, _, _, showS, _, ep1, ep2, _, _ := h.seedCatalog()
	ctx := context.Background()
	viewer := &core.User{ID: 1, Username: "alice", IsOwner: true}

	// Seed in-progress playback and watched state.
	if err := h.state.Progress(ctx, 1, movieA, 12*time.Minute, 2*time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}
	if err := h.state.Progress(ctx, 1, ep1, 8*time.Minute, 45*time.Minute, "paused"); err != nil {
		t.Fatal(err)
	}
	if err := h.state.Scrobble(ctx, 1, ep2); err != nil {
		t.Fatal(err)
	}

	// 1. Remove movie from Continue Watching.
	revBeforeMovie, ok, err := h.exp.HomeRevision(ctx, viewer)
	if err != nil || !ok {
		t.Fatalf("HomeRevision before movie: %v %v", ok, err)
	}
	w, doc := h.do("tok-1", http.MethodPost, "/vela/v0/titles/"+ids.RatingKeyGUID(movieA)+"/watch", `{"idempotencyKey":"cw-movie","continueWatching":false}`)
	if w.Code != 200 || doc["accepted"] != true {
		t.Fatalf("remove movie from continue watching: %d %s", w.Code, w.Body.String())
	}
	revAfterMovie, _, err := h.exp.HomeRevision(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if revAfterMovie.Viewer == revBeforeMovie.Viewer {
		t.Fatalf("HomeRevision did not bump on movie RemoveFromContinue: before=%v after=%v", revBeforeMovie, revAfterMovie)
	}
	wsMovie, err := h.state.Get(ctx, 1, movieA)
	if err != nil {
		t.Fatal(err)
	}
	if wsMovie.ViewOffset != 12*time.Minute || wsMovie.ViewCount != 0 {
		t.Fatalf("movie watch state modified: offset=%v count=%d", wsMovie.ViewOffset, wsMovie.ViewCount)
	}
	hidden, err := h.state.ContinueHidden(ctx, 1, []core.RatingKey{movieA})
	if err != nil || !hidden[movieA] {
		t.Fatalf("movie not marked continue-hidden: %v %v", hidden, err)
	}

	// 2. Remove episode from Continue Watching -> calls RemoveFromContinue for its show.
	revBeforeEp, _, err := h.exp.HomeRevision(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	w, doc = h.do("tok-1", http.MethodPost, "/vela/v0/titles/"+ids.RatingKeyGUID(ep1)+"/watch", `{"idempotencyKey":"cw-ep","continueWatching":false}`)
	if w.Code != 200 || doc["accepted"] != true {
		t.Fatalf("remove episode from continue watching: %d %s", w.Code, w.Body.String())
	}
	revAfterEp, _, err := h.exp.HomeRevision(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if revAfterEp.Viewer == revBeforeEp.Viewer {
		t.Fatalf("HomeRevision did not bump on episode RemoveFromContinue: before=%v after=%v", revBeforeEp, revAfterEp)
	}
	h.mu.Lock()
	gotRemoved := append([]core.RatingKey(nil), h.removedKeys...)
	h.mu.Unlock()
	if len(gotRemoved) != 2 || gotRemoved[0] != movieA || gotRemoved[1] != showS {
		t.Fatalf("RemoveFromContinue called with %v, want [%v, %v]", gotRemoved, movieA, showS)
	}
	wsEp1, err := h.state.Get(ctx, 1, ep1)
	if err != nil {
		t.Fatal(err)
	}
	wsEp2, err := h.state.Get(ctx, 1, ep2)
	if err != nil {
		t.Fatal(err)
	}
	if wsEp1.ViewOffset != 8*time.Minute || wsEp1.ViewCount != 0 || wsEp2.ViewCount != 1 {
		t.Fatalf("episode watch state modified: ep1=%+v ep2=%+v", wsEp1, wsEp2)
	}
	hiddenShow, err := h.state.ContinueHidden(ctx, 1, []core.RatingKey{showS})
	if err != nil || !hiddenShow[showS] {
		t.Fatalf("show not marked continue-hidden: %v %v", hiddenShow, err)
	}

	// 3. continueWatching: true is rejected with 400 invalid_request.
	w, doc = h.do("tok-1", http.MethodPost, "/vela/v0/titles/"+ids.RatingKeyGUID(movieA)+"/watch", `{"idempotencyKey":"cw-true","continueWatching":true}`)
	if w.Code != 400 || errorCode(doc) != "invalid_request" {
		t.Fatalf("continueWatching: true: %d %s", w.Code, w.Body.String())
	}
}

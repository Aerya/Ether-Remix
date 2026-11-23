package vela

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/screens"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

type testVelaScreensBridge struct {
	svc  *screens.Service
	favs *favorites.Service
}

func (b testVelaScreensBridge) Generation() uint64 { return b.svc.Generation() }

func (b testVelaScreensBridge) Navigation(ctx context.Context, viewer *frontend.Viewer) ([]frontend.NavigationScreen, error) {
	items, err := b.svc.Navigation(ctx, viewer, "vela")
	if err != nil {
		return nil, err
	}
	out := make([]frontend.NavigationScreen, len(items))
	for i, it := range items {
		out[i] = frontend.NavigationScreen{Kind: it.Kind, ID: it.ID, Title: it.Title, Icon: it.Icon}
	}
	return out, nil
}

func (b testVelaScreensBridge) ResolveScreen(ctx context.Context, viewer *frontend.Viewer, id, league, date string) (json.RawMessage, error) {
	res, err := b.svc.Resolve(ctx, viewer, screens.ResolveRequest{
		FrontendID: "vela",
		ScreenID:   id,
		League:     league,
		Date:       date,
	})
	if err != nil {
		if errors.Is(err, screens.ErrInvalidDate) {
			return nil, frontend.ErrInvalidScreenDate
		}
		return nil, err
	}
	raw, err := json.Marshal(res)
	return json.RawMessage(raw), err
}

func (b testVelaScreensBridge) QueryEntities(ctx context.Context, viewer *frontend.Viewer, feed, kind, league, q string) (json.RawMessage, error) {
	res, err := b.svc.QueryEntities(ctx, viewer, "vela", feed, kind, league, q)
	if err != nil {
		if errors.Is(err, screens.ErrFeedNotFound) {
			return nil, frontend.ErrNotFound
		}
		return nil, err
	}
	raw, err := json.Marshal(res)
	return json.RawMessage(raw), err
}

func (b testVelaScreensBridge) SetFavoriteEntity(ctx context.Context, viewer *frontend.Viewer, key string, on bool) error {
	if _, ok := favorites.ParseEntityKey(key); !ok {
		return frontend.ErrInvalidEntityKey
	}
	_, err := b.favs.Set(ctx, viewer, favorites.Subject{Kind: favorites.KindEntity, ID: key}, on)
	if errors.Is(err, favorites.ErrForbidden) {
		return frontend.ErrForbidden
	}
	return err
}

type testHub struct {
	events   []core.LiveEvent
	tables   []core.LiveEventTable
	entities []core.LiveEventEntity
}

func (h *testHub) Events(_ context.Context, _ *core.User, q screens.EventQuery) ([]core.LiveEvent, error) {
	var out []core.LiveEvent
	for _, ev := range h.events {
		if q.League != "" && ev.League.ID != q.League {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (h *testHub) Standings(_ context.Context, _ *core.User, _, _, _ string) ([]core.LiveEventTable, error) {
	return h.tables, nil
}

func (h *testHub) Entities(_ context.Context, _ *core.User, q screens.EntityQuery) ([]core.LiveEventEntity, error) {
	var out []core.LiveEventEntity
	for _, e := range h.entities {
		if q.League != "" && e.League != q.League {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

type favListAdapter struct{ svc *favorites.Service }

func (f favListAdapter) ListEntityKeys(ctx context.Context, v *core.User) ([]string, error) {
	entries, err := f.svc.List(ctx, v, favorites.KindEntity, 0)
	if errors.Is(err, favorites.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Subject.ID
	}
	return out, nil
}

type testWatchMatcher struct{}

func (testWatchMatcher) WatchTargets(_ context.Context, _ *core.User, ev core.LiveEvent) ([]core.LiveEventWatchTarget, error) {
	if ev.Status.State == core.LiveEventStateIn {
		return []core.LiveEventWatchTarget{{Kind: "channel", ChannelID: "ch-1", Name: "Arena HD", Number: "101", Live: true, Confidence: 0.9}}, nil
	}
	return nil, nil
}

type velaScreensEnv struct {
	handler   http.Handler
	registry  *screens.Registry
	pluginRec plugins.Record
	pluginID  string
	feed      string
	now       time.Time
}

func newVelaScreensEnv(t *testing.T) *velaScreensEnv {
	t.Helper()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	pluginID := "community.example.scores"
	feed := "scores"

	reg := screens.NewRegistry(screens.RegistryOptions{})
	favSvc, err := favorites.New(favorites.Options{
		DB:     db,
		Marked: state.New(db, state.Options{}).Playlists().(core.TransactionalMarkedPlaylists),
		Now:    func() time.Time { return now },
		Active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil },
		Allowed: func(ctx context.Context, viewer *core.User, sub favorites.Subject) bool {
			if sub.Kind == favorites.KindEntity {
				return reg.EntityAllowed(ctx, viewer, sub.ID)
			}
			return true
		},
		Changed: func(context.Context, int64, favorites.Subject) {},
	})
	if err != nil {
		t.Fatal(err)
	}

	rawMan, _ := json.Marshal(map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Scores",
		"ether": map[string]any{
			"contributes": map[string]any{
				"liveFeeds": []map[string]any{
					{"id": feed, "title": "Scores", "minIntervalSec": 15, "entityKinds": []string{"team"}},
				},
			},
		},
	})
	rec := plugins.Record{
		ID:          pluginID,
		Name:        "Scores",
		Version:     "1.0.0",
		Enabled:     true,
		ManifestRaw: rawMan,
		Manifest: plugins.Manifest{
			ID:      pluginID,
			Version: "1.0.0",
			Name:    "Scores",
			Ether: &plugins.EtherBlock{
				Protocol: 1,
				Kind:     plugins.KindExtension,
				Summary:  "Live scores feed",
				Runtime:  plugins.RuntimeDecl{Kind: "hosted", Entry: "dist/plugin.js"},
				Contributes: plugins.Contributes{
					Screens: []plugins.ScreenMeta{
						{
							ID:    "scores",
							Title: "Live scores",
							Icon:  "trophy",
							Feed:  feed,
							Sections: []plugins.ScreenSection{
								{ID: "hero", Type: "hero"},
								{ID: "leagues", Type: "leagues"},
								{ID: "live", Type: "events", Title: "Live now", Query: &plugins.ScreenEventQuery{State: []string{"in"}, Window: "today"}},
								{ID: "mine", Type: "events", Title: "Your teams", Query: &plugins.ScreenEventQuery{Favorites: true, Window: "week"}},
								{ID: "sched", Type: "schedule", Title: "Schedule"},
								{ID: "standings", Type: "table", Title: "Standings"},
								{ID: "teams", Type: "entities", Title: "Teams", EntityKind: "team"},
							},
						},
					},
				},
			},
		},
	}
	reg.Sync(rec, nil)

	hub := &testHub{
		events: []core.LiveEvent{
			{
				ID:     pluginID + ":" + feed + ":g1",
				Feed:   feed,
				League: core.LiveEventLeague{ID: "league-a", Name: "League A", Abbr: "LA"},
				Start:  now.Add(-30 * time.Minute).Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "Q4 2:00", Period: 4, Clock: "2:00", Close: true},
				Competitors: []core.LiveEventCompetitor{
					{ID: "t1", Name: "Alpha", Abbr: "ALP", Score: "21", EntityID: "alpha"},
					{ID: "t2", Name: "Beta", Abbr: "BET", Score: "20", EntityID: "beta", Home: true},
				},
				UpdatedAt: now.Format(time.RFC3339),
			},
			{
				ID:     pluginID + ":" + feed + ":g2",
				Feed:   feed,
				League: core.LiveEventLeague{ID: "league-b", Name: "League B", Abbr: "LB"},
				Start:  now.Add(-20 * time.Minute).Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "2nd 8:40", Period: 2, Clock: "8:40"},
				Competitors: []core.LiveEventCompetitor{
					{ID: "t3", Name: "Gamma", Abbr: "GAM", Score: "3", EntityID: "gamma"},
					{ID: "t4", Name: "Delta", Abbr: "DEL", Score: "1", EntityID: "delta", Home: true},
				},
				UpdatedAt: now.Format(time.RFC3339),
			},
		},
		tables: []core.LiveEventTable{
			{Title: "Division 1", Columns: []string{"Team", "W", "L"}, Rows: [][]string{{"Alpha", "10", "1"}}},
		},
		entities: []core.LiveEventEntity{
			{ID: "alpha", Kind: "team", Name: "Alpha", Abbr: "ALP", League: "league-a"},
			{ID: "gamma", Kind: "team", Name: "Gamma", Abbr: "GAM", League: "league-b"},
		},
	}

	screenSvc := screens.NewService(screens.ServiceOptions{
		Registry:  reg,
		Hub:       hub,
		Watch:     testWatchMatcher{},
		Favorites: favListAdapter{svc: favSvc},
		Now:       func() time.Time { return now },
	})
	bridge := testVelaScreensBridge{svc: screenSvc, favs: favSvc}

	mounted, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), frontend.Host{
		Identity:   identityFake{},
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
		Screens:    bridge,
		PageFreshness: func() frontend.PageFreshness {
			return frontend.PageFreshness{Screens: reg.Generation()}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })

	return &velaScreensEnv{
		handler:   mounted.Handler(),
		registry:  reg,
		pluginRec: rec,
		pluginID:  pluginID,
		feed:      feed,
		now:       now,
	}
}

func authVelaRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Vela-Device-Id", "dev-screens")
	return req
}

func TestVelaBootstrapNavigationAndScreensEndpoint(t *testing.T) {
	env := newVelaScreensEnv(t)

	// 1. Bootstrap navigation lists the published plugin screen in binding wire format.
	wBoot := httptest.NewRecorder()
	env.handler.ServeHTTP(wBoot, authVelaRequest(http.MethodGet, "/vela/v0/bootstrap"))
	if wBoot.Code != http.StatusOK {
		t.Fatalf("GET /bootstrap = %d: %s", wBoot.Code, wBoot.Body.String())
	}
	var boot struct {
		Navigation []map[string]any `json:"navigation"`
	}
	if err := json.Unmarshal(wBoot.Body.Bytes(), &boot); err != nil {
		t.Fatal(err)
	}
	if len(boot.Navigation) != 1 {
		t.Fatalf("bootstrap navigation = %+v, want 1 screen", boot.Navigation)
	}
	nav := boot.Navigation[0]
	if nav["kind"] != "screen" || nav["id"] != "scores" || nav["title"] != "Live scores" || nav["icon"] != "trophy" {
		t.Fatalf("navigation[0] = %+v", nav)
	}
	etag1 := wBoot.Header().Get("ETag")

	// 2. GET /vela/v0/screens/scores returns resolved generic sections with watch targets.
	wScreen := httptest.NewRecorder()
	env.handler.ServeHTTP(wScreen, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores"))
	if wScreen.Code != http.StatusOK {
		t.Fatalf("GET /screens/scores = %d: %s", wScreen.Code, wScreen.Body.String())
	}
	var doc struct {
		ID       string           `json:"id"`
		Title    string           `json:"title"`
		Sections []map[string]any `json:"sections"`
	}
	if err := json.Unmarshal(wScreen.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != "scores" || doc.Title != "Live scores" || len(doc.Sections) != 7 {
		t.Fatalf("screen doc = %+v", doc)
	}
	hero := doc.Sections[0]
	heroEv := hero["event"].(map[string]any)
	if heroEv["id"] != env.pluginID+":"+env.feed+":g1" {
		t.Fatalf("initial hero = %v, want close game g1", heroEv["id"])
	}
	if watch := heroEv["watch"].([]any); len(watch) != 1 {
		t.Fatalf("hero watch targets = %#v, want 1", watch)
	}
	if also := hero["also"].([]any); len(also) != 1 {
		t.Fatalf("hero also = %#v, want 1 companion event", also)
	}

	// Conditional ETag returns 304.
	req304 := authVelaRequest(http.MethodGet, "/vela/v0/screens/scores")
	req304.Header.Set("If-None-Match", wScreen.Header().Get("ETag"))
	w304 := httptest.NewRecorder()
	env.handler.ServeHTTP(w304, req304)
	if w304.Code != http.StatusNotModified {
		t.Fatalf("conditional GET /screens/scores = %d, want 304", w304.Code)
	}

	// 3. Invalid date -> 400; unknown query param -> 400; unknown screen -> 404; unauth -> 401.
	wBadDate := httptest.NewRecorder()
	env.handler.ServeHTTP(wBadDate, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores?date=not-a-date"))
	if wBadDate.Code != http.StatusBadRequest {
		t.Fatalf("bad date = %d, want 400", wBadDate.Code)
	}
	wBadParam := httptest.NewRecorder()
	env.handler.ServeHTTP(wBadParam, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores?foo=bar"))
	if wBadParam.Code != http.StatusBadRequest {
		t.Fatalf("bad query param = %d, want 400", wBadParam.Code)
	}
	wMissing := httptest.NewRecorder()
	env.handler.ServeHTTP(wMissing, authVelaRequest(http.MethodGet, "/vela/v0/screens/unknown"))
	if wMissing.Code != http.StatusNotFound {
		t.Fatalf("unknown screen = %d, want 404", wMissing.Code)
	}
	wUnauth := httptest.NewRecorder()
	env.handler.ServeHTTP(wUnauth, httptest.NewRequest(http.MethodGet, "/vela/v0/screens/scores", nil))
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth screen = %d, want 401", wUnauth.Code)
	}

	// 4. Disabling the plugin removes it from bootstrap navigation, advances bootstrap ETag, and hides /screens/scores.
	disabled := env.pluginRec
	disabled.Enabled = false
	env.registry.Sync(disabled, nil)

	reqBoot2 := authVelaRequest(http.MethodGet, "/vela/v0/bootstrap")
	reqBoot2.Header.Set("If-None-Match", etag1)
	wBoot2 := httptest.NewRecorder()
	env.handler.ServeHTTP(wBoot2, reqBoot2)
	if wBoot2.Code != http.StatusOK {
		t.Fatalf("bootstrap after disable = %d, want 200 (cache invalidated)", wBoot2.Code)
	}
	var boot2 struct {
		Navigation []map[string]any `json:"navigation"`
	}
	if err := json.Unmarshal(wBoot2.Body.Bytes(), &boot2); err != nil {
		t.Fatal(err)
	}
	if len(boot2.Navigation) != 0 {
		t.Fatalf("disabled plugin still in bootstrap navigation: %+v", boot2.Navigation)
	}
	wHidden := httptest.NewRecorder()
	env.handler.ServeHTTP(wHidden, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores"))
	if wHidden.Code != http.StatusNotFound {
		t.Fatalf("disabled screen = %d, want 404", wHidden.Code)
	}
}

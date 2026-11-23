package screens

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type fakeHub struct {
	mu           sync.Mutex
	events       []core.LiveEvent
	tables       []core.LiveEventTable
	entities     []core.LiveEventEntity
	eventCalls   int
	eventReqs    []EventQuery
	standingsReq []string
	entityReqs   []EntityQuery
}

func (f *fakeHub) Events(_ context.Context, _ *core.User, q EventQuery) ([]core.LiveEvent, error) {
	f.mu.Lock()
	f.eventCalls++
	f.eventReqs = append(f.eventReqs, q)
	f.mu.Unlock()
	var out []core.LiveEvent
	for _, ev := range f.events {
		if q.Feed != "" && ev.Feed != "" && ev.Feed != q.Feed {
			continue
		}
		if q.League != "" && ev.League.ID != q.League {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (f *fakeHub) Standings(_ context.Context, _ *core.User, _, _, league string) ([]core.LiveEventTable, error) {
	f.mu.Lock()
	f.standingsReq = append(f.standingsReq, league)
	f.mu.Unlock()
	return f.tables, nil
}

func (f *fakeHub) Entities(_ context.Context, _ *core.User, q EntityQuery) ([]core.LiveEventEntity, error) {
	f.mu.Lock()
	f.entityReqs = append(f.entityReqs, q)
	f.mu.Unlock()
	var idSet map[string]struct{}
	if len(q.IDs) > 0 {
		idSet = make(map[string]struct{}, len(q.IDs))
		for _, id := range q.IDs {
			idSet[id] = struct{}{}
		}
	}
	var out []core.LiveEventEntity
	for _, e := range f.entities {
		if q.League != "" && e.League != q.League {
			continue
		}
		if len(idSet) > 0 {
			if _, ok := idSet[e.ID]; !ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}

type fakeWatch struct {
	targets map[string][]core.LiveEventWatchTarget
	calls   int
}

func (w *fakeWatch) WatchTargets(_ context.Context, _ *core.User, ev core.LiveEvent) ([]core.LiveEventWatchTarget, error) {
	w.calls++
	return w.targets[ev.ID], nil
}

type fakeFavs struct {
	byViewer map[int64][]string
}

func (f fakeFavs) ListEntityKeys(_ context.Context, v *core.User) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	return f.byViewer[v.ID], nil
}

func testScreenRecord(pluginID, screenID, title, feed string, enabled bool) plugins.Record {
	man := plugins.Manifest{
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
						ID:    screenID,
						Title: title,
						Icon:  "trophy",
						Feed:  feed,
						Sections: []plugins.ScreenSection{
							{ID: "hero", Type: "hero"},
							{ID: "leagues", Type: "leagues"},
							{
								ID:    "live-now",
								Type:  "events",
								Title: "Live now",
								Query: &plugins.ScreenEventQuery{State: []string{"in"}, Window: "today"},
							},
							{
								ID:    "your-teams",
								Type:  "events",
								Title: "Your teams",
								Query: &plugins.ScreenEventQuery{Favorites: true, Window: "week"},
							},
							{
								ID:    "today",
								Type:  "events",
								Title: "Today",
								Query: &plugins.ScreenEventQuery{State: []string{"pre", "in", "post"}, Window: "today"},
							},
							{ID: "schedule", Type: "schedule", Title: "Schedule"},
							{ID: "standings", Type: "table", Title: "Standings"},
							{ID: "picker", Type: "entities", Title: "Follow teams", EntityKind: "team"},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(map[string]any{
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
	return plugins.Record{
		ID:          pluginID,
		Name:        "Scores",
		Version:     "1.0.0",
		Enabled:     enabled,
		Manifest:    man,
		ManifestRaw: raw,
	}
}

func TestResolveScreenSectionsAndWireShapes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	pluginID := "community.example.scores"
	feed := "scores"

	reg := NewRegistry(RegistryOptions{})
	reg.Sync(testScreenRecord(pluginID, "scores", "$t:screen_title", feed, true), func(key string) string {
		if key == "screen_title" {
			return "Live scores"
		}
		return ""
	})

	evFavLive := core.LiveEvent{
		ID:       pluginID + ":" + feed + ":ev-fav-live",
		Feed:     feed,
		Category: "football",
		League:   core.LiveEventLeague{ID: "league-a", Name: "League A", Abbr: "LA"},
		Start:    now.Add(-45 * time.Minute).Format(time.RFC3339),
		Status:   core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "Q3 5:10", Period: 3, Clock: "5:10"},
		Competitors: []core.LiveEventCompetitor{
			{ID: "t1", Name: "Alpha", Abbr: "ALP", Score: "14", EntityID: "alpha"},
			{ID: "t2", Name: "Beta", Abbr: "BET", Score: "10", EntityID: "beta", Home: true},
		},
		UpdatedAt: now.Format(time.RFC3339),
	}
	evCloseLive := core.LiveEvent{
		ID:       pluginID + ":" + feed + ":ev-close-live",
		Feed:     feed,
		Category: "hockey",
		League:   core.LiveEventLeague{ID: "league-b", Name: "League B", Abbr: "LB"},
		Start:    now.Add(-30 * time.Minute).Format(time.RFC3339),
		Status:   core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "3rd 1:05", Period: 3, Clock: "1:05", Close: true},
		Competitors: []core.LiveEventCompetitor{
			{ID: "t3", Name: "Gamma", Abbr: "GAM", Score: "2", EntityID: "gamma"},
			{ID: "t4", Name: "Delta", Abbr: "DEL", Score: "2", EntityID: "delta", Home: true},
		},
		UpdatedAt: now.Format(time.RFC3339),
	}
	evUpcoming := core.LiveEvent{
		ID:       pluginID + ":" + feed + ":ev-pre",
		Feed:     feed,
		Category: "basketball",
		League:   core.LiveEventLeague{ID: "league-b", Name: "League B", Abbr: "LB"},
		Start:    now.Add(2 * time.Hour).Format(time.RFC3339),
		Status:   core.LiveEventStatus{State: core.LiveEventStatePre, Detail: "8:00 PM"},
		Competitors: []core.LiveEventCompetitor{
			{ID: "t5", Name: "Epsilon", Abbr: "EPS", EntityID: "epsilon"},
			{ID: "t6", Name: "Zeta", Abbr: "ZET", EntityID: "zeta", Home: true},
		},
		UpdatedAt: now.Format(time.RFC3339),
	}
	evTomorrowFav := core.LiveEvent{
		ID:       pluginID + ":" + feed + ":ev-tomorrow-fav",
		Feed:     feed,
		Category: "racing",
		League:   core.LiveEventLeague{ID: "league-c", Name: "League C", Abbr: "LC"},
		Start:    now.Add(26 * time.Hour).Format(time.RFC3339),
		Status:   core.LiveEventStatus{State: core.LiveEventStatePre, Detail: "Tomorrow"},
		Ranking: []core.LiveEventRankRow{
			{Pos: 1, Name: "Racer One", Abbr: "R1", EntityID: "racer1"},
		},
		UpdatedAt: now.Format(time.RFC3339),
	}

	hub := &fakeHub{
		events: []core.LiveEvent{evCloseLive, evFavLive, evUpcoming, evTomorrowFav},
		tables: []core.LiveEventTable{
			{Title: "East", Columns: []string{"Team", "W", "L"}, Rows: [][]string{{"Alpha", "9", "2"}}},
		},
		entities: []core.LiveEventEntity{
			{ID: "alpha", Kind: "team", Name: "Alpha", Abbr: "ALP", League: "league-a"},
			{ID: "gamma", Kind: "team", Name: "Gamma", Abbr: "GAM", League: "league-b"},
		},
	}
	watch := &fakeWatch{
		targets: map[string][]core.LiveEventWatchTarget{
			evFavLive.ID: {
				{Kind: "channel", ChannelID: "ch-205", Name: "Sports One", Number: "205", Live: true, Confidence: 0.95},
			},
		},
	}
	favKeyAlpha := favorites.FormatEntityKey(pluginID, feed, "team", "alpha")
	favKeyRacer := favorites.FormatEntityKey(pluginID, feed, "team", "racer1")
	svc := NewService(ServiceOptions{
		Registry:  reg,
		Hub:       hub,
		Watch:     watch,
		Favorites: fakeFavs{byViewer: map[int64][]string{1: {favKeyAlpha, favKeyRacer}}},
		Now:       func() time.Time { return now },
	})

	viewer1 := &core.User{ID: 1}
	resolved, err := svc.Resolve(ctx, viewer1, ResolveRequest{FrontendID: "vela", ScreenID: "scores"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Title != "Live scores" {
		t.Fatalf("Title = %q, want localized Live scores", resolved.Title)
	}
	if len(resolved.Sections) != 8 {
		t.Fatalf("sections len = %d, want 8", len(resolved.Sections))
	}

	// Verify exact JSON wire shapes agreed with Vela.
	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wire struct {
		ID       string           `json:"id"`
		Title    string           `json:"title"`
		Sections []map[string]any `json:"sections"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	byID := map[string]map[string]any{}
	for _, s := range wire.Sections {
		byID[s["id"].(string)] = s
	}

	// 1. hero: {id, type, event, also?: [event]} — favorite live beats close non-favorite live.
	hero := byID["hero"]
	heroEvent := hero["event"].(map[string]any)
	if heroEvent["id"] != evFavLive.ID {
		t.Fatalf("hero event = %v, want favorite live %s", heroEvent["id"], evFavLive.ID)
	}
	if watchList, ok := heroEvent["watch"].([]any); !ok || len(watchList) != 1 {
		t.Fatalf("hero watch targets = %#v, want 1", heroEvent["watch"])
	}
	if also, ok := hero["also"].([]any); !ok || len(also) != 2 {
		t.Fatalf("hero also = %#v, want 2 companion events", hero["also"])
	}

	// 2. leagues: {leagues: [league], selected?}
	leaguesSec := byID["leagues"]
	if leagues, ok := leaguesSec["leagues"].([]any); !ok || len(leagues) != 3 {
		t.Fatalf("leagues = %#v, want 3", leaguesSec["leagues"])
	}
	if _, hasSelected := leaguesSec["selected"]; hasSelected {
		t.Fatalf("unfiltered leagues should omit selected, got %#v", leaguesSec["selected"])
	}

	// 3. events "live-now": {query?, events: [event]}
	liveNow := byID["live-now"]
	if liveNow["query"] == nil {
		t.Fatal("events section missing query")
	}
	if evs := liveNow["events"].([]any); len(evs) != 2 {
		t.Fatalf("live-now len = %d, want 2", len(evs))
	}

	// 4. events "your-teams": favorites filter matches both competitors[] and ranking[].
	yourTeams := byID["your-teams"]
	if evs := yourTeams["events"].([]any); len(evs) != 2 {
		t.Fatalf("your-teams len = %d, want 2 (fav live + tomorrow ranking fav)", len(evs))
	}

	// 5. schedule: {date, previous?, next?, days?: [...], events: [event]}
	sched := byID["schedule"]
	if sched["date"] != "2026-11-22" || sched["previous"] != "2026-11-21" || sched["next"] != "2026-11-23" {
		t.Fatalf("schedule paging = date=%v prev=%v next=%v", sched["date"], sched["previous"], sched["next"])
	}
	if days, ok := sched["days"].([]any); !ok || len(days) != 7 {
		t.Fatalf("schedule days = %#v, want 7-day strip", sched["days"])
	}
	if evs := sched["events"].([]any); len(evs) != 3 {
		t.Fatalf("schedule events len = %d, want 3 today events", len(evs))
	}

	// 6. table: {feed, league?, tables: [table]}
	tbl := byID["standings"]
	if tbl["feed"] != feed {
		t.Fatalf("table feed = %v, want %s", tbl["feed"], feed)
	}
	if tables := tbl["tables"].([]any); len(tables) != 1 {
		t.Fatalf("tables len = %d, want 1", len(tables))
	}

	// 7. entities: {feed, entityKind, entities: [LiveEntity]} with key + favorite.
	ents := byID["picker"]
	if ents["feed"] != feed || ents["entityKind"] != "team" {
		t.Fatalf("entities metadata = feed=%v entityKind=%v", ents["feed"], ents["entityKind"])
	}
	entList := ents["entities"].([]any)
	if len(entList) != 2 {
		t.Fatalf("entities len = %d, want 2", len(entList))
	}
	firstEnt := entList[0].(map[string]any)
	if firstEnt["key"] != favKeyAlpha || firstEnt["favorite"] != true {
		t.Fatalf("first entity = %#v, want key=%s favorite=true", firstEnt, favKeyAlpha)
	}
	secondEnt := entList[1].(map[string]any)
	if secondEnt["favorite"] != false {
		t.Fatalf("second entity = %#v, want favorite=false", secondEnt)
	}

	// Verify memoization: hero + leagues + live-now + today + schedule share the same
	// today window query, so only 2 distinct hub.Events calls run (today + week).
	if hub.eventCalls != 2 {
		t.Fatalf("hub.Events calls = %d, want 2 (today memoized across 5 sections + week)", hub.eventCalls)
	}
	// Watch targets memoized per unique event ID (4 unique events across all sections).
	if watch.calls != 4 {
		t.Fatalf("watch.WatchTargets calls = %d, want 4", watch.calls)
	}

	// Verify league filter keeps ALL league chips while setting selected and filtering events.
	filtered, err := svc.Resolve(ctx, viewer1, ResolveRequest{
		FrontendID: "vela",
		ScreenID:   "scores",
		League:     "league-b",
		Date:       "2026-11-23",
	})
	if err != nil {
		t.Fatalf("Resolve filtered: %v", err)
	}
	var filteredWire struct {
		Sections []map[string]any `json:"sections"`
	}
	b, _ := json.Marshal(filtered)
	_ = json.Unmarshal(b, &filteredWire)
	fByID := map[string]map[string]any{}
	for _, s := range filteredWire.Sections {
		fByID[s["id"].(string)] = s
	}
	if fByID["leagues"]["selected"] != "league-b" {
		t.Fatalf("leagues.selected = %v, want league-b", fByID["leagues"]["selected"])
	}
	if len(fByID["leagues"]["leagues"].([]any)) != 3 {
		t.Fatalf("filtered leagues chip count = %d, want all 3 chips preserved", len(fByID["leagues"]["leagues"].([]any)))
	}
	if heroEv := fByID["hero"]["event"].(map[string]any); heroEv["id"] != evCloseLive.ID {
		t.Fatalf("filtered hero = %v, want %s", heroEv["id"], evCloseLive.ID)
	}
	if fByID["schedule"]["date"] != "2026-11-23" {
		t.Fatalf("schedule date = %v, want 2026-11-23", fByID["schedule"]["date"])
	}

	// Invalid date fails with ErrInvalidDate.
	if _, err := svc.Resolve(ctx, viewer1, ResolveRequest{FrontendID: "vela", ScreenID: "scores", Date: "22-11-2026"}); !errors.Is(err, ErrInvalidDate) {
		t.Fatalf("invalid date err = %v, want ErrInvalidDate", err)
	}
}

func TestHeroSpoilerFreeDoesNotLeakCloseStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	pluginID := "community.example.scores"
	feed := "scores"

	reg := NewRegistry(RegistryOptions{})
	reg.Sync(testScreenRecord(pluginID, "scores", "Scores", feed, true), nil)

	evEarlier := core.LiveEvent{
		ID:     "ev-1",
		Feed:   feed,
		League: core.LiveEventLeague{ID: "l1", Name: "L1"},
		Start:  now.Add(-60 * time.Minute).Format(time.RFC3339),
		Status: core.LiveEventStatus{State: core.LiveEventStateIn, Close: false},
		// Spoiler-free viewer: both events carry spoiler: true.
		Spoiler: true,
	}
	evCloseLater := core.LiveEvent{
		ID:      "ev-2",
		Feed:    feed,
		League:  core.LiveEventLeague{ID: "l1", Name: "L1"},
		Start:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		Status:  core.LiveEventStatus{State: core.LiveEventStateIn, Close: true},
		Spoiler: true,
	}

	svc := NewService(ServiceOptions{
		Registry: reg,
		Hub:      &fakeHub{events: []core.LiveEvent{evCloseLater, evEarlier}},
		Now:      func() time.Time { return now },
	})

	// With spoilers hidden, Close must not promote ev-2 ahead of ev-1.
	res, err := svc.Resolve(ctx, &core.User{ID: 1}, ResolveRequest{FrontendID: "vela", ScreenID: "scores"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sections[0].Event == nil || res.Sections[0].Event.ID != "ev-1" {
		t.Fatalf("spoiler-free hero = %+v, want ev-1 (Close must not leak)", res.Sections[0].Event)
	}

	// With spoilers visible (Spoiler: false), Close promotes ev-2 as biggest live game.
	evEarlier.Spoiler, evCloseLater.Spoiler = false, false
	svc.SetHub(&fakeHub{events: []core.LiveEvent{evEarlier, evCloseLater}})
	res, err = svc.Resolve(ctx, &core.User{ID: 1}, ResolveRequest{FrontendID: "vela", ScreenID: "scores"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sections[0].Event == nil || res.Sections[0].Event.ID != "ev-2" {
		t.Fatalf("normal hero = %+v, want ev-2 (close live game)", res.Sections[0].Event)
	}
}

func TestRegistryPublishingRolesViewerAndCollisions(t *testing.T) {
	ctx := context.Background()
	published := true
	allowedViewerID := int64(1)
	reg := NewRegistry(RegistryOptions{
		IsPublished: func(_ context.Context, pluginID, screenID, frontendID string) bool {
			return published && frontendID == "vela"
		},
		IsViewerAllowed: func(_ context.Context, viewer *core.User, _, _ string) bool {
			return viewer.ID == allowedViewerID
		},
	})
	recA := testScreenRecord("community.a.scores", "scores", "Scores A", "scores", true)
	recB := testScreenRecord("community.b.scores", "scores", "Scores B", "scores", true)
	gen0 := reg.Generation()
	reg.Sync(recA, nil)
	gen1 := reg.Generation()
	if gen1 <= gen0 {
		t.Fatal("Sync did not bump Generation")
	}
	// Idempotent re-sync must not churn Generation.
	reg.Sync(recA, nil)
	if reg.Generation() != gen1 {
		t.Fatal("unchanged Sync bumped Generation")
	}
	reg.Sync(recB, nil)

	v1 := &core.User{ID: 1}
	v2 := &core.User{ID: 2}

	// Viewer 2 is denied both screens and entity checks.
	if list, _ := reg.ListScreens(ctx, v2, "vela"); len(list) != 0 {
		t.Fatalf("denied viewer saw %+v", list)
	}
	if reg.EntityAllowed(ctx, v2, "community.a.scores:scores:team:t1") {
		t.Fatal("denied viewer allowed entity")
	}

	// Viewer 1 sees both screens with collision disambiguation.
	list, err := reg.ListScreens(ctx, v1, "vela")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListScreens = %+v %v", list, err)
	}
	if list[0].ID != "scores" || list[1].ID != "community.b.scores:scores" {
		t.Fatalf("collision IDs = %q, %q", list[0].ID, list[1].ID)
	}

	// Switching off live_events role hides plugin A's screen and entities.
	recA.RolesOff = []plugins.Capability{liveEventsCapability}
	reg.Sync(recA, nil)
	list, _ = reg.ListScreens(ctx, v1, "vela")
	if len(list) != 1 || list[0].PluginID != "community.b.scores" || list[0].ID != "scores" {
		t.Fatalf("after RolesOff ListScreens = %+v", list)
	}
	if reg.EntityAllowed(ctx, v1, "community.a.scores:scores:team:t1") {
		t.Fatal("RolesOff plugin still allowed entity")
	}

	// Unpublishing hides all screens from Vela navigation and lookup.
	published = false
	if list, _ = reg.ListScreens(ctx, v1, "vela"); len(list) != 0 {
		t.Fatalf("unpublished screens leaked: %+v", list)
	}
}

func TestQueryEntitiesAnnotatesKeyAndFavorite(t *testing.T) {
	ctx := context.Background()
	pluginID := "community.example.scores"
	feed := "scores"
	reg := NewRegistry(RegistryOptions{})
	reg.Sync(testScreenRecord(pluginID, "scores", "Scores", feed, true), nil)

	hub := &fakeHub{
		entities: []core.LiveEventEntity{
			{ID: "alpha", Kind: "team", Name: "Alpha", Abbr: "ALP", League: "league-a", Color: "#112233"},
			{ID: "beta", Kind: "team", Name: "Beta", Abbr: "BET", League: "league-a", Color: "#445566"},
		},
	}
	favKey := favorites.FormatEntityKey(pluginID, feed, "team", "beta")
	svc := NewService(ServiceOptions{
		Registry:  reg,
		Hub:       hub,
		Favorites: fakeFavs{byViewer: map[int64][]string{1: {favKey}}},
	})

	res, err := svc.QueryEntities(ctx, &core.User{ID: 1}, "vela", "scores", "", "league-a", "be")
	if err != nil {
		t.Fatalf("QueryEntities: %v", err)
	}
	if res.Feed != feed || res.EntityKind != "team" || len(res.Entities) != 2 {
		t.Fatalf("QueryEntities result = %+v", res)
	}
	if res.Entities[0].Favorite || res.Entities[0].Key != favorites.FormatEntityKey(pluginID, feed, "team", "alpha") {
		t.Fatalf("entity[0] = %+v", res.Entities[0])
	}
	if !res.Entities[1].Favorite || res.Entities[1].Key != favKey {
		t.Fatalf("entity[1] = %+v, want favorite=true key=%s", res.Entities[1], favKey)
	}
	if len(hub.entityReqs) != 1 || hub.entityReqs[0].Query != "be" || hub.entityReqs[0].League != "league-a" {
		t.Fatalf("hub entity request = %+v", hub.entityReqs)
	}

	// Unknown feed fails with ErrFeedNotFound.
	if _, err := svc.QueryEntities(ctx, &core.User{ID: 1}, "vela", "missing", "", "", ""); !errors.Is(err, ErrFeedNotFound) {
		t.Fatalf("missing feed err = %v, want ErrFeedNotFound", err)
	}
}

type fakeBatchWatch struct {
	batchCalls  int
	singleCalls int
}

func (w *fakeBatchWatch) WatchTargets(_ context.Context, _ *core.User, _ core.LiveEvent) ([]core.LiveEventWatchTarget, error) {
	w.singleCalls++
	return []core.LiveEventWatchTarget{}, nil
}

func (w *fakeBatchWatch) WatchTargetsBatch(_ context.Context, _ *core.User, events []core.LiveEvent) (map[string][]core.LiveEventWatchTarget, error) {
	w.batchCalls++
	out := make(map[string][]core.LiveEventWatchTarget, len(events))
	for _, ev := range events {
		out[ev.ID] = []core.LiveEventWatchTarget{{Kind: core.LiveEventWatchChannel, ChannelID: "ch-1", Name: "FOX", Live: ev.Status.State == core.LiveEventStateIn}}
	}
	return out, nil
}

func TestResolveScreenScopesEntitiesSkipsEmptyFavWeekAndBatchesWatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	pluginID := "community.example.scores"
	feed := "scores"
	reg := NewRegistry(RegistryOptions{})
	reg.Sync(testScreenRecord(pluginID, "scores", "Scores", feed, true), nil)

	hub := &fakeHub{
		events: []core.LiveEvent{
			{
				ID:     pluginID + ":" + feed + ":rank-1",
				Feed:   feed,
				League: core.LiveEventLeague{ID: "pga", Name: "PGA Tour", Abbr: "PGA"},
				Title:  "The RSM Classic",
				Start:  now.Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStateIn},
				Ranking: []core.LiveEventRankRow{
					{Pos: 1, Name: "Leader", Detail: "-10", Secondary: "thru 14"},
				},
			},
			{
				ID:     pluginID + ":" + feed + ":g1",
				Feed:   feed,
				League: core.LiveEventLeague{ID: "league-a", Name: "League A", Abbr: "LA"},
				Start:  now.Add(time.Hour).Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStatePre},
				Competitors: []core.LiveEventCompetitor{
					{ID: "a1", Name: "Alpha", Abbr: "ALP", EntityID: "a1"},
					{ID: "a2", Name: "Bravo", Abbr: "BRV", EntityID: "a2", Home: true},
				},
			},
			{
				ID:     pluginID + ":" + feed + ":g2",
				Feed:   feed,
				League: core.LiveEventLeague{ID: "league-b", Name: "League B", Abbr: "LB"},
				Start:  now.Add(2 * time.Hour).Format(time.RFC3339),
				Status: core.LiveEventStatus{State: core.LiveEventStatePre},
				Competitors: []core.LiveEventCompetitor{
					{ID: "b1", Name: "Gamma", Abbr: "GAM", EntityID: "b1"},
					{ID: "b2", Name: "Delta", Abbr: "DLT", EntityID: "b2", Home: true},
				},
			},
		},
		entities: []core.LiveEventEntity{
			{ID: "a1", Kind: "team", Name: "Alpha", League: "league-a"},
			{ID: "a2", Kind: "team", Name: "Bravo", League: "league-a"},
			{ID: "b1", Kind: "team", Name: "Gamma", League: "league-b"},
			{ID: "c1", Kind: "team", Name: "Omega", League: "league-c"},
		},
	}

	batchWatch := &fakeBatchWatch{}
	svc := NewService(ServiceOptions{
		Registry:  reg,
		Hub:       hub,
		Watch:     batchWatch,
		Favorites: fakeFavs{}, // zero favourites
		Now:       func() time.Time { return now },
	})

	// 1. Viewer with ZERO favourites:
	//    - your-teams must NOT trigger a 7-day Events query
	//    - entities must NOT inline all 4 leagues' teams (falls back from teamless pga to league-a: 2 teams only)
	//    - ranking event must serialize competitors: []
	//    - watch matcher runs ONCE in batch mode
	res, err := svc.Resolve(ctx, &core.User{ID: 99}, ResolveRequest{FrontendID: "vela", ScreenID: "scores"})
	if err != nil {
		t.Fatalf("Resolve (no favs): %v", err)
	}
	if batchWatch.batchCalls != 1 || batchWatch.singleCalls != 0 {
		t.Fatalf("watch calls = (batch=%d, single=%d), want (1, 0)", batchWatch.batchCalls, batchWatch.singleCalls)
	}
	for _, eq := range hub.eventReqs {
		if eq.To.Sub(eq.From) > 24*time.Hour {
			t.Fatalf("unexpected multi-day Events query when viewer has zero favourites: %+v", eq)
		}
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal screen: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal screen: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, s := range doc["sections"].([]any) {
		m := s.(map[string]any)
		byID[m["id"].(string)] = m
	}
	ents := byID["picker"]["entities"].([]any)
	if len(ents) != 2 {
		t.Fatalf("entities len = %d (%v), want 2 (league-a only, not full catalogue)", len(ents), ents)
	}
	heroEv := byID["hero"]["event"].(map[string]any)
	if comps, ok := heroEv["competitors"].([]any); !ok || len(comps) != 0 {
		t.Fatalf("ranking hero event competitors = %#v, want []", heroEv["competitors"])
	}

	// 2. Viewer with favourite in league-c and ?league=league-b selected:
	//    must return league-c favourite + league-b teams (2 total), NOT league-a!
	favC1 := favorites.FormatEntityKey(pluginID, feed, "team", "c1")
	svcFav := NewService(ServiceOptions{
		Registry:  reg,
		Hub:       hub,
		Watch:     &fakeBatchWatch{},
		Favorites: fakeFavs{byViewer: map[int64][]string{7: {favC1}}},
		Now:       func() time.Time { return now },
	})
	resFav, err := svcFav.Resolve(ctx, &core.User{ID: 7}, ResolveRequest{FrontendID: "vela", ScreenID: "scores", League: "league-b"})
	if err != nil {
		t.Fatalf("Resolve (with fav + league): %v", err)
	}
	var picker ResolvedSection
	for _, sec := range resFav.Sections {
		if sec.ID == "picker" {
			picker = sec
			break
		}
	}
	if len(picker.Entities) != 2 {
		t.Fatalf("entities len = %d (%+v), want 2 (fav c1 + league-b b1)", len(picker.Entities), picker.Entities)
	}
	if picker.Entities[0].ID != "c1" || !picker.Entities[0].Favorite || picker.Entities[1].ID != "b1" || picker.Entities[1].Favorite {
		t.Fatalf("unexpected scoped entities = %+v", picker.Entities)
	}
}

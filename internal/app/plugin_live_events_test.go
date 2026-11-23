package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeLiveEventsRuntime struct {
	calls   atomic.Int64
	failing atomic.Bool
}

func (f *fakeLiveEventsRuntime) Call(_ context.Context, req plugins.Request) (plugins.Response, error) {
	f.calls.Add(1)
	if req.Path == "/ether/v1/live/main/events" {
		if f.failing.Load() {
			return plugins.Response{Status: http.StatusServiceUnavailable, Body: json.RawMessage(`{"error":"upstream timeout"}`)}, nil
		}
		body, _ := json.Marshal(core.LiveEventsResponse{
			NextPollSec: 30,
			Events: []core.LiveEvent{
				{
					ID:     "ev-live",
					Feed:   "main",
					League: core.LiveEventLeague{ID: "alpha", Name: "Alpha League", Abbr: "ALP"},
					Title:  "North vs South",
					Start:  "2026-11-22T18:00:00Z",
					Status: core.LiveEventStatus{State: core.LiveEventStateIn, Detail: "Period 2 08:15", Close: true},
					Competitors: []core.LiveEventCompetitor{
						{ID: "n", Name: "North", Score: "14", EntityID: "north"},
						{ID: "s", Name: "South", Score: "13", EntityID: "south"},
					},
					Broadcasts: []core.LiveEventBroadcast{{Name: "NET1", Kind: "tv"}},
					MatchHints: &core.LiveEventMatchHints{Terms: []string{"North", "South"}, Callsigns: []string{"NET1"}},
				},
				{
					ID:     "ev-upcoming",
					Feed:   "main",
					League: core.LiveEventLeague{ID: "beta", Name: "Beta Cup", Abbr: "BET"},
					Title:  "East vs West",
					Start:  "2026-11-22T21:00:00Z",
					Status: core.LiveEventStatus{State: core.LiveEventStatePre, Detail: "21:00 UTC"},
					Competitors: []core.LiveEventCompetitor{
						{ID: "e", Name: "East", EntityID: "east"},
						{ID: "w", Name: "West", EntityID: "west"},
					},
				},
				{
					ID:     "ev-final",
					Feed:   "main",
					League: core.LiveEventLeague{ID: "alpha", Name: "Alpha League", Abbr: "ALP"},
					Title:  "Old Match",
					Start:  "2026-11-22T14:00:00Z",
					Status: core.LiveEventStatus{State: core.LiveEventStatePost, Detail: "Final"},
				},
			},
		})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	}
	if req.Path == "/ether/health" {
		return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"status":"ok"}`)}, nil
	}
	return plugins.Response{Status: http.StatusNotFound, Body: json.RawMessage(`{}`)}, nil
}

func (f *fakeLiveEventsRuntime) Close() error { return nil }

func TestPluginLiveEventsConsoleEndpoint(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 18, 30, 0, 0, time.UTC)
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfgStore, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatalf("config.Open: %v", err)
	}
	if err := cfgStore.Update(func(c *config.Config) error {
		c.Frontends = map[string]config.Frontend{"vela": {Enabled: true}}
		return nil
	}); err != nil {
		t.Fatalf("config update: %v", err)
	}

	rt := &fakeLiveEventsRuntime{}
	mgr, err := plugins.New(plugins.Deps{
		DB:     db,
		Logger: log,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("plugins.New: %v", err)
	}
	mgr.RegisterRuntime("hosted", func(plugins.Installed) (plugins.Runtime, error) {
		return rt, nil
	})
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("mgr.Start: %v", err)
	}

	manifestJSON := []byte(`{
		"id": "community.example.live",
		"version": "1.0.0",
		"name": "Example Live",
		"description": "Generic live events and screen plugin.",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"capabilities": ["live_events"],
			"summary": "$t:summary",
			"runtime": {"kind": "hosted", "entry": "plugin.js"},
			"strings": {"en": "i18n/en.json"},
			"contributes": {
				"liveFeeds": [{"id": "main", "title": "$t:feed_title", "minIntervalSec": 15, "entityKinds": ["entrant"]}],
				"screens": [{
					"id": "live-screen",
					"title": "$t:screen_title",
					"icon": "radio",
					"feed": "main",
					"sections": [{"id": "live", "type": "events", "title": "$t:section_live", "feed": "main"}]
				}]
			}
		}
	}`)
	stringsJSON := []byte(`{"summary":"Live updates","feed_title":"Main Feed","screen_title":"Events Hub","section_live":"Live now"}`)

	preview, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manifestJSON,
		Files: map[string][]byte{
			"plugin.js":    []byte(`export default function(){}`),
			"i18n/en.json": stringsJSON,
		},
	})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	if _, err := mgr.Install(ctx, preview, plugins.InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Add one matching Live TV channel + programme so watchCount > 0 is exercised.
	ltv, err := livetv.New(livetv.Options{DB: db, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("livetv.New: %v", err)
	}
	defer ltv.Close()
	ch := livetv.Channel{
		ID:        "ch-net1",
		Number:    "10",
		Name:      "NET1 HD",
		Kind:      "iptv",
		Enabled:   true,
		SourceID:  "src-1",
		GuideID:   "net1.guide",
		StreamURL: "https://example.invalid/net1.m3u8",
	}
	chBytes, _ := json.Marshal(ch)
	if _, err := db.ExecContext(ctx, `INSERT INTO livetv_channels(id, data) VALUES(?, ?)`, ch.ID, string(chBytes)); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	prog := livetv.Programme{
		ID:        "net1-prog",
		ChannelID: "net1.guide",
		Start:     time.Date(2026, 11, 22, 17, 45, 0, 0, time.UTC),
		End:       time.Date(2026, 11, 22, 20, 30, 0, 0, time.UTC),
		Item:      livetv.Item{Title: "North vs South"},
	}
	progBytes, _ := json.Marshal(prog)
	if _, err := db.ExecContext(ctx, `INSERT INTO livetv_programmes(source_id, channel_id, start_ms, end_ms, data) VALUES(?, ?, ?, ?, ?)`,
		"src-1", prog.ChannelID, prog.Start.UnixMilli(), prog.End.UnixMilli(), string(progBytes)); err != nil {
		t.Fatalf("insert programme: %v", err)
	}

	a := &App{db: db, cfg: cfgStore, log: log, livetv: ltv}
	a.pluginsRT = &pluginRuntime{mgr: mgr, screens: a.buildScreensService(mgr)}
	a.buildLiveEvents()
	defer a.liveEvents.Close()

	handlers := map[string]http.Handler{}
	a.pluginHandlers(handlers)
	h := handlers["plugins"]

	// 1. Verify GET /{id} resolves $t: titles on liveFeeds and screens.
	detailRec := httptest.NewRecorder()
	h.ServeHTTP(detailRec, httptest.NewRequest(http.MethodGet, "/community.example.live", nil))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET detail = %d: %s", detailRec.Code, detailRec.Body.String())
	}
	var detailBody struct {
		Manifest plugins.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if got := detailBody.Manifest.Ether.Contributes.LiveFeeds[0].Title; got != "Main Feed" {
		t.Fatalf("detail liveFeed title = %q, want Main Feed", got)
	}
	if got := detailBody.Manifest.Ether.Contributes.Screens[0].Title; got != "Events Hub" {
		t.Fatalf("detail screen title = %q, want Events Hub", got)
	}

	// 2. Cold GET /{id}/live-events reads cache without calling the plugin.
	coldRec := httptest.NewRecorder()
	h.ServeHTTP(coldRec, httptest.NewRequest(http.MethodGet, "/community.example.live/live-events?limit=5", nil))
	if coldRec.Code != http.StatusOK {
		t.Fatalf("cold GET live-events = %d: %s", coldRec.Code, coldRec.Body.String())
	}
	if rt.calls.Load() != 0 {
		t.Fatalf("cold GET live-events called plugin %d times, want 0", rt.calls.Load())
	}

	// 3. Brief SSE subscription populates hub cache via normal hub subscriber rules.
	sseCtx, sseCancel := context.WithTimeout(ctx, 5*time.Second)
	defer sseCancel()
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, srv.URL+"/community.example.live/live-events?limit=5&stream=1", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	reader := bufio.NewReader(resp.Body)
	var populated pluginLiveEventsOverview
	for i := 0; i < 10; i++ {
		line, rerr := reader.ReadString('\n')
		if rerr != nil {
			t.Fatalf("read SSE: %v", rerr)
		}
		if payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &populated); err != nil {
				t.Fatalf("decode SSE overview: %v", err)
			}
			if len(populated.Events) == 2 {
				break
			}
		}
	}
	sseCancel()
	_ = resp.Body.Close()

	if len(populated.Events) != 2 || populated.LiveCount != 1 || populated.UpcomingCount != 1 || populated.TotalEvents != 3 {
		t.Fatalf("populated overview = events:%d live:%d upcoming:%d total:%d, want 2/1/1/3",
			len(populated.Events), populated.LiveCount, populated.UpcomingCount, populated.TotalEvents)
	}
	if populated.Events[0].ID != "community.example.live:main:ev-live" || populated.Events[0].WatchCount != 1 {
		t.Fatalf("first event = %+v, want ev-live with watchCount=1", populated.Events[0])
	}
	if len(populated.Feeds) != 1 || len(populated.Feeds[0].Leagues) != 2 || populated.Feeds[0].EntityTotal != 4 {
		t.Fatalf("feed summary = %+v, want 2 leagues and 4 entities", populated.Feeds)
	}
	if len(populated.Screens) != 1 || populated.Screens[0].Title != "Events Hub" || !populated.Screens[0].Frontends[0].Published {
		t.Fatalf("screens = %+v, want published Events Hub on Vela", populated.Screens)
	}

	// 4. Subsequent JSON GET with limit=1 returns cached live event without extra plugin calls.
	callsBefore := rt.calls.Load()
	warmRec := httptest.NewRecorder()
	h.ServeHTTP(warmRec, httptest.NewRequest(http.MethodGet, "/community.example.live/live-events?limit=1", nil))
	if warmRec.Code != http.StatusOK {
		t.Fatalf("warm GET = %d", warmRec.Code)
	}
	if rt.calls.Load() != callsBefore {
		t.Fatalf("warm GET triggered extra plugin calls: %d -> %d", callsBefore, rt.calls.Load())
	}
	var limited pluginLiveEventsOverview
	if err := json.Unmarshal(warmRec.Body.Bytes(), &limited); err != nil {
		t.Fatalf("decode limited: %v", err)
	}
	if len(limited.Events) != 1 || limited.Events[0].Status.State != core.LiveEventStateIn {
		t.Fatalf("limited events = %+v, want 1 live event", limited.Events)
	}

	// 5. Turning Vela off updates screen publish state to frontend_disabled.
	if err := cfgStore.Update(func(c *config.Config) error {
		c.Frontends["vela"] = config.Frontend{Enabled: false}
		return nil
	}); err != nil {
		t.Fatalf("disable vela: %v", err)
	}
	offRec := httptest.NewRecorder()
	h.ServeHTTP(offRec, httptest.NewRequest(http.MethodGet, "/community.example.live/live-events", nil))
	var offOverview pluginLiveEventsOverview
	if err := json.Unmarshal(offRec.Body.Bytes(), &offOverview); err != nil {
		t.Fatalf("decode off overview: %v", err)
	}
	if fe := offOverview.Screens[0].Frontends[0]; fe.Published || fe.Reason != "frontend_disabled" {
		t.Fatalf("screen frontend when Vela off = %+v, want unpublished frontend_disabled", fe)
	}
}

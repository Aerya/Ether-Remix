package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/screens"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

type sportsTestIdentity struct {
	frontend.SurfaceIdentity
	viewer *frontend.Viewer
}

func (i sportsTestIdentity) ResolveDevice(_ context.Context, token string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
	if token == "token" && i.viewer != nil {
		return i.viewer, time.Now().Add(time.Hour), nil
	}
	return nil, time.Time{}, frontend.ErrUnauthorized
}

func (i sportsTestIdentity) Resolve(_ context.Context, token string) (*frontend.Viewer, error) {
	if token == "token" && i.viewer != nil {
		return i.viewer, nil
	}
	return nil, frontend.ErrUnauthorized
}

type sportsFixtureTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t sportsFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.base.RoundTrip(clone)
}

func TestSportsIntegrationEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Fake ESPN fixture HTTP server backed by the recorded Sports plugin fixtures.
	fixturesDir := filepath.Join("..", "..", "sdk", "examples", "sports", "tests", "fixtures")
	readFix := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(fixturesDir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return b
	}
	fixtureBySuffix := map[string][]byte{
		"football/nfl/scoreboard":              readFix("scoreboard-nfl.json"),
		"football/nfl/summary?event=401873066": readFix("summary-nfl.json"),
		"football/nfl/teams":                   readFix("teams-nfl.json"),
		"football/nfl/standings":               readFix("standings-nfl.json"),
	}
	espnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if ev := r.URL.Query().Get("event"); ev != "" {
			key += "?event=" + ev
		}
		for suffix, body := range fixtureBySuffix {
			if strings.HasSuffix(key, suffix) || strings.HasSuffix(r.URL.Path, suffix) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer espnSrv.Close()

	targetURL, err := url.Parse(espnSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fetchClient := &http.Client{
		Transport: sportsFixtureTransport{target: targetURL, base: espnSrv.Client().Transport},
	}

	// 2. Pack and install the real Sports example plugin into plugins.Manager.
	now := time.Date(2026, 11, 22, 18, 15, 0, 0, time.UTC)
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	mgr, err := plugins.New(plugins.Deps{
		DB:          db,
		Logger:      log,
		FetchClient: fetchClient,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("plugins.New: %v", err)
	}
	eng := wasm.NewEngine(log)
	defer eng.Close()
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("mgr.Start: %v", err)
	}

	sportsDir := filepath.Join("..", "..", "sdk", "examples", "sports")
	pkg, err := kit.LoadDir(sportsDir)
	if err != nil {
		t.Fatalf("kit.LoadDir(sports): %v", err)
	}
	pkgBytes, err := kit.Pack(pkg)
	if err != nil {
		t.Fatalf("kit.Pack(sports): %v", err)
	}
	preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: pkgBytes})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	if _, err := mgr.Install(ctx, preview, plugins.InstallOptions{
		Settings: map[string]any{
			"leagues": []string{"nfl"},
			"region":  "us",
		},
	}); err != nil {
		t.Fatalf("Install sports plugin: %v", err)
	}

	// 3. Stand up Live TV with a CBS channel airing Dolphins at Bills (401873070).
	ltv, err := livetv.New(livetv.Options{DB: db, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("livetv.New: %v", err)
	}
	defer ltv.Close()
	ch := livetv.Channel{
		ID:        "ch-cbs-1",
		Number:    "4",
		Name:      "CBS Sports HD",
		Kind:      "iptv",
		Enabled:   true,
		SourceID:  "src-1",
		GuideID:   "cbs.1",
		StreamURL: "https://example.invalid/cbs.m3u8",
	}
	cb, _ := json.Marshal(ch)
	if _, err := db.ExecContext(ctx, `INSERT INTO livetv_channels(id, data) VALUES(?, ?)`, ch.ID, string(cb)); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	prog := livetv.Programme{
		ID:        "cbs.1-nfl",
		ChannelID: "cbs.1",
		Start:     time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 11, 22, 21, 30, 0, 0, time.UTC),
		Item: livetv.Item{
			Title:  "Miami Dolphins at Buffalo Bills",
			Genres: []string{"Sports", "Football"},
		},
	}
	pb, _ := json.Marshal(prog)
	if _, err := db.ExecContext(ctx, `INSERT INTO livetv_programmes(source_id, channel_id, start_ms, end_ms, data) VALUES(?,?,?,?,?)`,
		"src-1", "cbs.1", prog.Start.UnixMilli(), prog.End.UnixMilli(), string(pb)); err != nil {
		t.Fatalf("insert programme: %v", err)
	}

	// 4. Wire Favorites, Screens, LiveEvents Hub, WatchMatcher, Alerts, and Vela.
	a := &App{db: db, livetv: ltv, log: log}
	viewer := &core.User{ID: 7}
	favSvc, err := favorites.New(favorites.Options{
		DB:     db,
		Marked: state.New(db, state.Options{}).Playlists().(core.TransactionalMarkedPlaylists),
		Now:    func() time.Time { return now },
		Active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil },
		Allowed: func(ctx context.Context, u *core.User, sub favorites.Subject) bool {
			if sub.Kind == favorites.KindEntity {
				ok, _ := a.favoriteEntityAllowed(ctx, u, sub.ID)
				return ok
			}
			return true
		},
		Changed: func(ctx context.Context, id int64, sub favorites.Subject) {
			a.favoriteChanged(ctx, id, sub)
		},
	})
	if err != nil {
		t.Fatalf("favorites.New: %v", err)
	}
	a.favorites = favSvc

	screenReg := screens.NewRegistry(screens.RegistryOptions{})
	mgr.OnSync(func(_ context.Context, rec plugins.Record) {
		screenReg.Sync(rec, nil)
	})
	mgr.OnRemove(func(_ context.Context, id string) {
		screenReg.Remove(id)
	})
	a.pluginsRT = &pluginRuntime{
		mgr: mgr,
		screens: screens.NewService(screens.ServiceOptions{
			Registry:  screenReg,
			Favorites: appFavoriteEntities{app: a},
			Now:       func() time.Time { return now },
		}),
	}
	a.buildLiveEvents()
	defer a.liveEvents.Close()

	mounted, err := (vela.Frontend{
		AssetsDir: t.TempDir(),
		DB:        db,
		ReplayKey: make([]byte, 32),
	}).Mount(ctx, frontend.Host{
		Identity:        sportsTestIdentity{viewer: viewer},
		Experience:      frontendtest.Experience{},
		Sessions:        frontendtest.Sessions{},
		LiveEvents:      surfaceLiveEvents{app: a},
		LiveEventLink:   surfaceLiveEventLink{app: a},
		LiveEventTicker: surfaceLiveEventTicker{app: a},
		Screens:         surfaceScreens{app: a, surface: "vela"},
	})
	if err != nil {
		t.Fatalf("vela.Mount: %v", err)
	}
	defer mounted.Close(ctx)

	velaSrv := httptest.NewServer(mounted.Handler())
	defer velaSrv.Close()

	doVela := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, velaSrv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Vela-Device-Id", "dev-e2e")
		resp, err := velaSrv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// 5. List entities and favourite Buffalo Bills via Vela API.
	entResp := doVela(http.MethodGet, "/vela/v0/live/entities?feed=org.ether.sports:sports&kind=team&league=nfl")
	defer entResp.Body.Close()
	if entResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(entResp.Body)
		t.Fatalf("GET /live/entities = %d: %s", entResp.StatusCode, string(b))
	}
	var entDoc struct {
		Entities []struct {
			ID       string `json:"id"`
			Key      string `json:"key"`
			Name     string `json:"name"`
			Favorite bool   `json:"favorite"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(entResp.Body).Decode(&entDoc); err != nil {
		t.Fatalf("decode entities: %v", err)
	}
	billsKey := ""
	for _, e := range entDoc.Entities {
		if strings.Contains(e.Name, "Buffalo Bills") {
			billsKey = e.Key
			break
		}
	}
	if billsKey == "" {
		t.Fatalf("Buffalo Bills not found in entities: %+v", entDoc.Entities)
	}

	favResp := doVela(http.MethodPut, "/vela/v0/favorites/entities/"+url.PathEscape(billsKey))
	favResp.Body.Close()
	if favResp.StatusCode != http.StatusOK && favResp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /favorites/entities = %d", favResp.StatusCode)
	}

	// 6. Resolve /vela/v0/screens/sports and verify events, watch targets, favourites, and standings.
	screenResp := doVela(http.MethodGet, "/vela/v0/screens/sports")
	defer screenResp.Body.Close()
	if screenResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(screenResp.Body)
		t.Fatalf("GET /screens/sports = %d: %s", screenResp.StatusCode, string(b))
	}
	var screenDoc struct {
		ID       string `json:"id"`
		Sections []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Events []struct {
				ID    string                      `json:"id"`
				Watch []core.LiveEventWatchTarget `json:"watch"`
			} `json:"events"`
			Tables   []core.LiveEventTable `json:"tables"`
			Entities []struct {
				Key      string `json:"key"`
				Favorite bool   `json:"favorite"`
			} `json:"entities"`
		} `json:"sections"`
	}
	if err := json.NewDecoder(screenResp.Body).Decode(&screenDoc); err != nil {
		t.Fatalf("decode screen: %v", err)
	}
	if screenDoc.ID != "sports" {
		t.Fatalf("screen id = %q, want sports", screenDoc.ID)
	}

	foundFavEventWithWatch := false
	foundFavEntity := false
	foundStandings := false
	for _, sec := range screenDoc.Sections {
		switch sec.ID {
		case "your-teams":
			for _, ev := range sec.Events {
				if ev.ID == "org.ether.sports:sports:nfl-401873070" {
					for _, w := range ev.Watch {
						if w.Kind == core.LiveEventWatchChannel && w.ChannelID == "ch-cbs-1" {
							foundFavEventWithWatch = true
						}
					}
				}
			}
		case "teams":
			for _, ent := range sec.Entities {
				if ent.Key == billsKey && ent.Favorite {
					foundFavEntity = true
				}
			}
		case "standings":
			if len(sec.Tables) > 0 {
				foundStandings = true
			}
		}
	}
	if !foundFavEventWithWatch {
		t.Fatalf("your-teams section missing Dolphins@Bills event with ch-cbs-1 watch target: %+v", screenDoc.Sections)
	}
	if !foundFavEntity {
		t.Fatalf("teams section did not mark %s as favorite", billsKey)
	}
	if !foundStandings {
		t.Fatalf("standings section returned no tables")
	}

	// 7. Fetch event detail and verify tables, numeric play periods, and probabilitySeries.
	detResp := doVela(http.MethodGet, "/vela/v0/live/events/"+url.PathEscape("org.ether.sports:sports:nfl-401873066"))
	defer detResp.Body.Close()
	if detResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(detResp.Body)
		t.Fatalf("GET /live/events/detail = %d: %s", detResp.StatusCode, string(b))
	}
	var detailDoc core.LiveEventDetail
	if err := json.NewDecoder(detResp.Body).Decode(&detailDoc); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detailDoc.Tables) < 3 || len(detailDoc.Plays) == 0 || len(detailDoc.ProbabilitySeries) < 10 {
		t.Fatalf("incomplete detail: tables=%d plays=%d prob=%d", len(detailDoc.Tables), len(detailDoc.Plays), len(detailDoc.ProbabilitySeries))
	}

	// 8. Subscribe to /vela/v0/live/stream?screens=sports&alerts=1 and verify both
	//    initial event snapshot (with watch targets) and unified AlertHub delivery.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	sReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, velaSrv.URL+"/vela/v0/live/stream?screens=sports&alerts=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	sReq.Header.Set("Authorization", "Bearer token")
	sReq.Header.Set("X-Vela-Device-Id", "dev-e2e")
	sResp, err := velaSrv.Client().Do(sReq)
	if err != nil {
		t.Fatalf("GET /live/stream: %v", err)
	}
	defer sResp.Body.Close()
	if sResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(sResp.Body)
		t.Fatalf("GET /live/stream = %d: %s", sResp.StatusCode, string(b))
	}

	type sseFrame struct {
		event string
		data  string
	}
	frames := make(chan sseFrame, 8)
	go func() {
		sc := bufio.NewScanner(sResp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if cur.event != "" || cur.data != "" {
					frames <- cur
					cur = sseFrame{}
				}
				continue
			}
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				cur.event = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				cur.data = v
			}
		}
	}()

	// Expect initial update frame carrying Dolphins@Bills with ch-cbs-1 watch target.
	select {
	case f := <-frames:
		if f.event != "update" {
			t.Fatalf("first SSE event = %q, want update", f.event)
		}
		if !strings.Contains(f.data, "org.ether.sports:sports:nfl-401873070") || !strings.Contains(f.data, "ch-cbs-1") {
			t.Fatalf("initial SSE update missing Dolphins@Bills or ch-cbs-1 watch target: %s", f.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial SSE update frame")
	}

	// Publish an alert via Hub's unified AlertHub and verify SSE delivers event: alert.
	a.LiveEventsHub().PublishAlert(ctx, viewer.ID, core.LiveEventAlert{
		ID:        "started:org.ether.sports:sports:nfl-401873070:" + billsKey,
		Kind:      core.LiveEventAlertStarted,
		EventID:   "org.ether.sports:sports:nfl-401873070",
		EntityKey: billsKey,
		Title:     "NFL · Dolphins at Bills",
		Body:      "Live now",
	})

	select {
	case f := <-frames:
		if f.event != "alert" {
			t.Fatalf("second SSE event = %q, want alert", f.event)
		}
		if !strings.Contains(f.data, billsKey) || !strings.Contains(f.data, "Live now") {
			t.Fatalf("SSE alert frame missing expected fields: %s", f.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSE alert frame")
	}
}

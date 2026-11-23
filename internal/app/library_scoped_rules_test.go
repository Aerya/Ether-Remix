package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

// TestLibraryScopedHideRulesAcrossFrontends verifies the core #719 invariant:
// a hide rule with scope="library" on built-in TV Shows hides matching anime
// titles from TV Shows' browse, sections, allLeaves, facets, export and
// section-scoped search across every frontend (Plex, Jellyfin/Emby, Vela,
// Silo, Stremio, Xtream), while keeping them visible in a custom Anime library
// built from tmdb-tv, server-wide search, direct lookup and playback.
func TestLibraryScopedHideRulesAcrossFrontends(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true, Settings: map[string]any{"discovery": false}},
			"emby":     {Enabled: true, Settings: map[string]any{"discovery": false}},
			"vela":     {Enabled: true},
			"silo":     {Enabled: true, Settings: map[string]any{"discovery": false}},
			"stremio":  {Enabled: true},
			"xtream":   {Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, Options{
		DataDir:    dir,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		plextvHTTP: &http.Client{Transport: fakePlexTVTransport{}},
		diskUsage:  func() (int64, int64, error) { return 1 << 40, 1 << 39, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rel := time.Date(2015, 3, 1, 0, 0, 0, 0, time.UTC)
	bbKey := core.ShowKey(1396)
	narutoKey := core.ShowKey(46260)
	aotKey := core.ShowKey(1429)
	bbEp := core.EpisodeKey(1396, 1, 1)
	narutoEp := core.EpisodeKey(46260, 1, 1)

	for _, title := range []core.Title{
		{Key: bbKey, Type: core.TypeShow, IDs: core.IDs{TMDB: 1396}, Title: "Breaking Bad", Year: 2008, Genres: []string{"Drama"}, Countries: []string{"US"}, Keywords: []string{"crime"}, Released: rel, Popularity: 90, VoteCount: 10000},
		{Key: narutoKey, Type: core.TypeShow, IDs: core.IDs{TMDB: 46260}, Title: "Naruto", Year: 2002, Genres: []string{"Animation", "Action"}, Countries: []string{"JP"}, Keywords: []string{"anime"}, Released: rel, Popularity: 85, VoteCount: 9000},
		{Key: aotKey, Type: core.TypeShow, IDs: core.IDs{TMDB: 1429}, Title: "Attack on Titan", Year: 2013, Genres: []string{"Animation", "Drama"}, Countries: []string{"JP"}, Keywords: []string{"anime"}, Released: rel, Popularity: 88, VoteCount: 9500},
		{Key: bbEp, Type: core.TypeEpisode, IDs: core.IDs{TMDB: 1396}, Title: "Pilot", ShowKey: bbKey, ShowTitle: "Breaking Bad", Season: 1, Episode: 1, Released: rel},
		{Key: narutoEp, Type: core.TypeEpisode, IDs: core.IDs{TMDB: 46260}, Title: "Enter Naruto Uzumaki", ShowKey: narutoKey, ShowTitle: "Naruto", Season: 1, Episode: 1, Released: rel},
	} {
		if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}

	// Put a library-scoped anime exclusion on TV Shows, preceded by a show override for Attack on Titan.
	showRules := []core.LibraryExclusion{
		{
			Name: "Keep AoT", Action: "show", MediaType: "show", Match: "all",
			Rules: []core.CollectionRule{{Field: "title", Op: "is", Value: json.RawMessage(`"` + aotKey.String() + `"`)}},
		},
		{
			Name: "Hide Anime From TV Shows", Action: "hide", MediaType: "show", Match: "all", Scope: core.VisibilityLibrary,
			Rules: []core.CollectionRule{{Field: "keyword", Op: "in", Value: json.RawMessage(`["anime"]`)}},
		},
	}
	if err := a.libraries.SetRules(ctx, core.LibraryShows, showRules, "show"); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshLibraryPolicies(ctx); err != nil {
		t.Fatal(err)
	}

	// Create a custom Anime library built from tmdb-tv and publish it to frontends.
	animeRules := []core.LibraryExclusion{{
		Name: "Anime Only", Action: "show", MediaType: "show", Match: "all",
		Rules: []core.CollectionRule{{Field: "keyword", Op: "in", Value: json.RawMessage(`["anime"]`)}},
	}}
	animeLib, err := a.libraries.Create(ctx, "Anime", core.TypeShow, animeRules, "hide", core.Presentations{
		Plex:         &core.PlexPresentation{Base: "show"},
		MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "tvshows"},
		Vela:         &core.VelaPresentation{Type: "shows"},
		Silo:         &core.SiloPresentation{Type: "tvshows"},
		Stremio:      &core.StremioPresentation{Type: "series"},
		Xtream:       &core.XtreamPresentation{Category: "series"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.libraries.SetSources(ctx, animeLib.ID, []string{"tmdb-tv"}); err != nil {
		t.Fatal(err)
	}
	a.publishLibrarySections()

	// 1) Shared experience checks: TV Shows browse & count vs Anime library vs global search, direct lookup, export & playback access.
	exp, err := a.newSurfaceExperience(core.Protocol("jellyfin"))
	if err != nil {
		t.Fatal(err)
	}
	tvPage, err := exp.LibraryItems(ctx, owner, core.Device{}, "2", frontend.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if tvPage.Total != 2 || len(tvPage.Items) != 2 {
		t.Fatalf("experience TV Shows items total=%d len=%d, want 2 (Breaking Bad + Attack on Titan)", tvPage.Total, len(tvPage.Items))
	}
	for _, it := range tvPage.Items {
		if it.Key == narutoKey {
			t.Fatal("Naruto leaked into built-in TV Shows browse")
		}
	}
	if ic, ok := exp.(interface {
		IndexedLibraryCount(context.Context, *core.Viewer, string) (int, error)
	}); ok {
		if n, err := ic.IndexedLibraryCount(ctx, owner, "2"); err != nil || n != 2 {
			t.Fatalf("IndexedLibraryCount(TV Shows) = %d, %v; want 2", n, err)
		}
	}

	animePage, err := exp.LibraryItems(ctx, owner, core.Device{}, strconv.Itoa(int(animeLib.ID)), frontend.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if animePage.Total != 2 || len(animePage.Items) != 2 {
		t.Fatalf("Anime custom library total=%d len=%d, want 2 (Naruto + Attack on Titan)", animePage.Total, len(animePage.Items))
	}

	// Server-wide search still finds Naruto!
	globalSearch, err := exp.Search(ctx, owner, core.Device{}, "Naruto", frontend.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	foundNaruto := false
	for _, it := range globalSearch.Page.Items {
		if it.Key == narutoKey {
			foundNaruto = true
		}
	}
	if !foundNaruto {
		t.Fatalf("server-wide Search hid Naruto: %+v", globalSearch.Page.Items)
	}

	// Direct lookup and playback access still allow Naruto!
	if _, err := exp.Item(ctx, frontend.ItemRequest{Viewer: owner, Key: narutoKey}); err != nil {
		t.Fatalf("direct Item(Naruto) failed: %v", err)
	}
	tvStored, err := a.libraries.Get(ctx, core.LibraryShows)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.catalog.TitleInFilter(core.WithCatalogViewer(ctx, owner), libraryAccessFilter(tvStored), narutoKey); err != nil || !ok {
		t.Fatalf("libraryAccessFilter(TV Shows) must keep Naruto playable, got ok=%v err=%v", ok, err)
	}

	// Xtream export (ExportLibraryPage & ExportPolicy) excludes Naruto from TV Shows and includes it in Anime.
	xtExp, err := a.newSurfaceExperience(core.Protocol("xtream"))
	if err != nil {
		t.Fatal(err)
	}
	exporter, ok := xtExp.(frontend.LibraryExporter)
	if !ok {
		t.Fatal("xtream experience does not implement LibraryExporter")
	}
	tvExport, err := exporter.ExportLibraryPage(ctx, owner, core.Device{}, "2", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range tvExport.Items {
		if it.Key == narutoKey {
			t.Fatal("Naruto leaked into Xtream TV Shows export")
		}
	}
	if len(tvExport.Items) != 2 {
		t.Fatalf("Xtream TV Shows export count = %d, want 2", len(tvExport.Items))
	}
	animeExport, err := exporter.ExportLibraryPage(ctx, owner, core.Device{}, strconv.Itoa(int(animeLib.ID)), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(animeExport.Items) != 2 {
		t.Fatalf("Xtream Anime export count = %d, want 2", len(animeExport.Items))
	}
	expPol, ok := xtExp.(frontend.LibraryExportPolicy)
	if !ok {
		t.Fatal("xtream experience does not implement LibraryExportPolicy")
	}
	polWithRule, err := expPol.ExportPolicy(ctx, owner, core.Device{}, "2")
	if err != nil || !polWithRule.TitleJudged {
		t.Fatalf("Xtream ExportPolicy = %+v, %v; want TitleJudged=true", polWithRule, err)
	}

	// 2) Live frontend HTTP tests: Plex, Jellyfin, Vela, Silo, Stremio.
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	a.frontends.start(ctx, srv.Listener.Addr())
	t.Cleanup(a.frontends.stop)

	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok := func(proto string) string {
		s, err := grants.Issue(ctx, owner.ID, nil, proto, core.Device{ID: "dev-" + proto})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	do := func(method, path, body string, headers map[string]string) string {
		t.Helper()
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rBody)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s -> %d: %s", method, path, resp.StatusCode, string(b))
		}
		return string(b)
	}

	plexH := map[string]string{"X-Plex-Token": "test-owner", "Accept": "application/json"}
	// Plex TV Shows /all, /allLeaves, /genre, /search, /hubs/search?sectionId=2 exclude Naruto.
	body := do("GET", "/library/sections/2/all", "", plexH)
	if strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Breaking Bad"`) || !strings.Contains(body, `"Attack on Titan"`) {
		t.Fatalf("Plex TV Shows /all unexpected: %s", body)
	}
	body = do("GET", "/library/sections/2/allLeaves", "", plexH)
	if strings.Contains(body, `"Enter Naruto Uzumaki"`) || !strings.Contains(body, `"Pilot"`) {
		t.Fatalf("Plex TV Shows /allLeaves unexpected: %s", body)
	}
	body = do("GET", "/library/sections/2/genre", "", plexH)
	if strings.Contains(body, `"Action"`) {
		t.Fatalf("Plex TV Shows /genre should omit Naruto-only genre Action: %s", body)
	}
	body = do("GET", "/library/sections/2/search?query=Naruto", "", plexH)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Plex TV Shows section search leaked Naruto: %s", body)
	}
	body = do("GET", "/hubs/search?query=Naruto&sectionId=2", "", plexH)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Plex hub search with sectionId=2 leaked Naruto: %s", body)
	}
	// Plex global hub search (no sectionId) STILL finds Naruto!
	body = do("GET", "/hubs/search?query=Naruto", "", plexH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Plex global hub search hid Naruto: %s", body)
	}
	// Plex custom Anime section lists Naruto!
	body = do("GET", fmt.Sprintf("/library/sections/%d/all", animeLib.ID), "", plexH)
	if !strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Attack on Titan"`) {
		t.Fatalf("Plex Anime section missing anime: %s", body)
	}

	// Jellyfin: TV Shows view browse + view-scoped SearchTerm + Search/Hints exclude Naruto; global SearchTerm keeps Naruto.
	jfTok := tok("jellyfin")
	jfH := map[string]string{"Authorization": `MediaBrowser Client="TV", Device="Test", DeviceId="dev-jellyfin", Version="1", Token="` + jfTok + `"`}
	tvGUID, _ := ids.GUID(ids.View, 2)
	animeGUID, _ := ids.GUID(ids.View, int64(animeLib.ID))
	body = do("GET", "/jellyfin/Items?ParentId="+tvGUID, "", jfH)
	if strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Breaking Bad"`) {
		t.Fatalf("Jellyfin TV Shows browse unexpected: %s", body)
	}
	body = do("GET", "/jellyfin/Items?ParentId="+tvGUID+"&SearchTerm=Naruto", "", jfH)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Jellyfin TV Shows scoped search leaked Naruto: %s", body)
	}
	body = do("GET", "/jellyfin/Search/Hints?parentId="+tvGUID+"&searchTerm=Naruto", "", jfH)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Jellyfin Search/Hints with parentId=TV Shows leaked Naruto: %s", body)
	}
	body = do("GET", "/jellyfin/Items?SearchTerm=Naruto", "", jfH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Jellyfin global search hid Naruto: %s", body)
	}
	body = do("GET", "/jellyfin/Items?ParentId="+animeGUID, "", jfH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Jellyfin Anime library missing Naruto: %s", body)
	}

	// Stremio: library catalog search excludes Naruto from TV Shows while global series search keeps Naruto.
	stTok := tok("stremio")
	body = do("GET", fmt.Sprintf("/stremio/%s/catalog/series/ether-lib-2.json", stTok), "", nil)
	if strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Breaking Bad"`) {
		t.Fatalf("Stremio TV Shows catalog unexpected: %s", body)
	}
	body = do("GET", fmt.Sprintf("/stremio/%s/catalog/series/ether-lib-2/search=Naruto.json", stTok), "", nil)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Stremio TV Shows scoped search leaked Naruto: %s", body)
	}
	body = do("GET", fmt.Sprintf("/stremio/%s/catalog/series/ether-series/search=Naruto.json", stTok), "", nil)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Stremio global series search hid Naruto: %s", body)
	}
	body = do("GET", fmt.Sprintf("/stremio/%s/catalog/series/ether-lib-%d.json", stTok, animeLib.ID), "", nil)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Stremio Anime catalog missing Naruto: %s", body)
	}

	// Vela: TV Shows items & library-scoped search exclude Naruto; global search & Anime library keep Naruto.
	velaTok := tok("vela")
	if _, err := grants.IssueRefresh(ctx, "vela", velaTok, core.Device{ID: "dev-vela"}); err != nil {
		t.Fatal(err)
	}
	velaH := map[string]string{
		"Authorization":    "Bearer " + velaTok,
		"X-Vela-Device-Id": "dev-vela",
		"Content-Type":     "application/json",
	}
	body = do("GET", "/vela/v0/libraries/2/items", "", velaH)
	if strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Breaking Bad"`) {
		t.Fatalf("Vela TV Shows items unexpected: %s", body)
	}
	body = do("POST", "/vela/v0/search", `{"idempotencyKey":"idem-tv-naruto","query":"Naruto","mode":"fast","library":"2","limit":10}`, velaH)
	if !strings.Contains(body, `"items":[]`) {
		t.Fatalf("Vela TV Shows scoped search leaked Naruto: %s", body)
	}
	body = do("POST", "/vela/v0/search", `{"idempotencyKey":"idem-global-naruto","query":"Naruto","mode":"fast","limit":10}`, velaH)
	if strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"name":"Naruto"`) {
		t.Fatalf("Vela global search hid Naruto: %s", body)
	}
	body = do("GET", fmt.Sprintf("/vela/v0/libraries/%d/items", animeLib.ID), "", velaH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Vela Anime library missing Naruto: %s", body)
	}

	// Silo: TV Shows browse & scoped search exclude Naruto; global search & Anime library keep Naruto.
	siloTok := tok("silo")
	siloH := map[string]string{"Authorization": "Bearer " + siloTok, "X-Silo-Device-Id": "dev-silo", "X-Silo-Client": "tv"}
	var siloLibs struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(do("GET", "/silo/api/v2/user/libraries", "", siloH)), &siloLibs); err != nil {
		t.Fatal(err)
	}
	var siloTVID, siloAnimeID string
	for _, l := range siloLibs.Items {
		switch l.Name {
		case "TV Shows":
			siloTVID = l.ID
		case "Anime":
			siloAnimeID = l.ID
		}
	}
	if siloTVID == "" || siloAnimeID == "" {
		t.Fatalf("Silo libraries missing TV Shows/Anime: %+v", siloLibs.Items)
	}
	body = do("GET", "/silo/api/v2/catalog?library_id="+siloTVID, "", siloH)
	if strings.Contains(body, `"Naruto"`) || !strings.Contains(body, `"Breaking Bad"`) {
		t.Fatalf("Silo TV Shows browse unexpected: %s", body)
	}
	body = do("GET", "/silo/api/v2/catalog?library_id="+siloTVID+"&q=Naruto", "", siloH)
	if strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Silo TV Shows scoped search leaked Naruto: %s", body)
	}
	body = do("GET", "/silo/api/v2/catalog?q=Naruto", "", siloH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Silo global search hid Naruto: %s", body)
	}
	body = do("GET", "/silo/api/v2/catalog?library_id="+siloAnimeID, "", siloH)
	if !strings.Contains(body, `"Naruto"`) {
		t.Fatalf("Silo Anime library missing Naruto: %s", body)
	}
}

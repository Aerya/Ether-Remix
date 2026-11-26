package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestHomeRowsBoundedAcrossRowGrowth verifies that Home row queries stay
// bounded per page rather than scaling with promoted collection count,
// playlist count, or item count across Vela, Jellyfin, and Plex.
func TestHomeRowsBoundedAcrossRowGrowth(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true},
			"vela":     {Enabled: true},
		}
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Server.BindAddress = "127.0.0.1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	scale := perfharness.Scale{
		Movies:              300,
		Shows:               60,
		SeasonsPerShow:      4,
		EpisodesPerSeason:   6,
		Viewers:             2,
		HistoryPerViewer:    120,
		InProgressPerViewer: 12,
	}
	if err := perfharness.Generate(ctx, db, scale, time.Now()); err != nil {
		t.Fatal(err)
	}
	viewers, err := perfharness.MintCredentials(ctx, db, perfharness.RestrictedViewer+1)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	var appLog bytes.Buffer
	a, err := New(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(&appLog, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.catalog.RebuildRank(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.catalog.ReplacePopularityIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	users, err := a.users.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var movieKeys, showKeys []string
	for i := 1; i <= 120; i++ {
		movieKeys = append(movieKeys, core.MovieKey(int64(i)).String())
	}
	for i := 1; i <= 60; i++ {
		showKeys = append(showKeys, core.ShowKey(int64(i)).String())
	}
	mixedKeys := make([]string, 0, 100)
	for i := 0; i < 50; i++ {
		mixedKeys = append(mixedKeys, movieKeys[i], showKeys[i])
	}
	seedCollections := func(specs []core.CollectionSpec) {
		t.Helper()
		for _, sp := range specs {
			if _, err := a.collections.CreateSpec(ctx, sp); err != nil {
				t.Fatalf("create collection %q: %v", sp.Name, err)
			}
		}
	}
	seedCollections([]core.CollectionSpec{
		{Name: "Home Movies 1", Enabled: true, MediaType: "movie", Sources: []core.CollectionSource{{Kind: "titles", Keys: movieKeys}}, BrowseInApps: true},
		{Name: "Home Shows 1", Enabled: true, MediaType: "show", Sources: []core.CollectionSource{{Kind: "titles", Keys: showKeys}}, BrowseInApps: true},
		{Name: "Home Mixed 1", Enabled: true, MediaType: "mixed", Sources: []core.CollectionSource{{Kind: "titles", Keys: mixedKeys}}, BrowseInApps: true},
		{Name: "Home Movies 2", Enabled: true, MediaType: "movie", Sources: []core.CollectionSource{{Kind: "titles", Keys: movieKeys[20:]}}, BrowseInApps: true},
	})
	for i := range users {
		if err := a.personalize.Rebuild(ctx, &users[i]); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(a.Handler())
	defer server.Close()
	a.frontends.start(ctx, server.Listener.Addr())
	h := a.Handler()
	if _, ok := a.frontends.registry.Instance("jellyfin"); !ok {
		if _, err := a.frontends.registry.Mount(ctx, core.Protocol("jellyfin")); err != nil {
			t.Fatal(err)
		}
	}

	velaSignIn := func(u, pw, dev string) string {
		req := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/sign-in", strings.NewReader(fmt.Sprintf(
			`{"idempotencyKey":"bound-%s","username":%q,"password":%q,"device":{"id":%q,"name":"bound","platform":"web"}}`,
			dev, u, pw, dev,
		)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("vela sign-in %s: %d %s", u, rec.Code, rec.Body.String())
		}
		var resp struct {
			AccessToken string `json:"accessToken"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.AccessToken
	}

	bustCatalogCache := func(n int64) {
		_ = a.catalog.UpsertTitle(ctx, core.Title{
			Key: core.MovieKey(9999900 + n), Type: core.TypeMovie, Title: "CacheBuster",
			Year: 2020, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		}, false)
	}

	measureQueries := func(label string, handler http.Handler, req *http.Request) int64 {
		t.Helper()
		mctx, stats := store.WithQueryStats(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(mctx))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", label, rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: empty body", label)
		}
		return stats.Queries()
	}

	// Small Home: 4 promoted collections, seeded playlists.
	tokSmall := velaSignIn(viewers[0].Username, perfharness.Password, "bound-dev-small")
	bustCatalogCache(1)
	reqSmallCold := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=home", nil)
	reqSmallCold.Header.Set("Authorization", "Bearer "+tokSmall)
	reqSmallCold.Header.Set("X-Vela-Device-Id", "bound-dev-small")
	velaSmallCold := measureQueries("vela small cold", h, reqSmallCold)

	reqSmallWarm := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=home", nil)
	reqSmallWarm.Header.Set("Authorization", "Bearer "+tokSmall)
	reqSmallWarm.Header.Set("X-Vela-Device-Id", "bound-dev-small")
	velaSmallWarm := measureQueries("vela small warm", h, reqSmallWarm)

	// Grow promoted collections (4 -> 12) and playlists (3 -> 11).
	extraSpecs := make([]core.CollectionSpec, 0, 8)
	for i := 0; i < 8; i++ {
		keys := movieKeys[i*5:]
		mt := "movie"
		if i%2 == 1 {
			keys = showKeys[i*3:]
			mt = "show"
		}
		extraSpecs = append(extraSpecs, core.CollectionSpec{
			Name:    fmt.Sprintf("Home Extra %d", i+1),
			Enabled: true, MediaType: mt,
			Sources:      []core.CollectionSource{{Kind: "titles", Keys: keys}},
			BrowseInApps: true,
		})
	}
	seedCollections(extraSpecs)
	for i := 1; i <= 8; i++ {
		if _, err := a.playlists.Create(ctx, users[0].ID, fmt.Sprintf("Extra Playlist %d", i), []core.RatingKey{core.MovieKey(int64(i)), core.MovieKey(int64(i + 10))}); err != nil {
			t.Fatalf("create playlist %d: %v", i, err)
		}
	}

	// Fresh device forces a cold manifest build with 3x collections and ~3.7x playlists.
	tokLarge := velaSignIn(viewers[0].Username, perfharness.Password, "bound-dev-large")
	bustCatalogCache(2)
	reqLargeCold := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=home", nil)
	reqLargeCold.Header.Set("Authorization", "Bearer "+tokLarge)
	reqLargeCold.Header.Set("X-Vela-Device-Id", "bound-dev-large")
	velaLargeCold := measureQueries("vela large cold", h, reqLargeCold)

	reqLargeWarm := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=home", nil)
	reqLargeWarm.Header.Set("Authorization", "Bearer "+tokLarge)
	reqLargeWarm.Header.Set("X-Vela-Device-Id", "bound-dev-large")
	velaLargeWarm := measureQueries("vela large warm", h, reqLargeWarm)

	// Restricted viewer cold Home must also stay bounded (no per-episode parent-show N+1).
	tokRestricted := velaSignIn(viewers[perfharness.RestrictedViewer].Username, perfharness.Password, "bound-dev-restricted")
	bustCatalogCache(3)
	reqRestricted := httptest.NewRequest(http.MethodGet, "/vela/v0/rows?placement=home", nil)
	reqRestricted.Header.Set("Authorization", "Bearer "+tokRestricted)
	reqRestricted.Header.Set("X-Vela-Device-Id", "bound-dev-restricted")
	velaRestrictedCold := measureQueries("vela restricted cold", h, reqRestricted)

	// Jellyfin & Plex Home share the same row/catalog/collection/playlist layers.
	jfInst, _ := a.frontends.registry.Instance("jellyfin")
	jfLoginRec := httptest.NewRecorder()
	jfLoginReq := httptest.NewRequest(http.MethodPost, "/Users/AuthenticateByName", strings.NewReader(fmt.Sprintf(`{"Username":%q,"Pw":%q}`, viewers[0].Username, perfharness.Password)))
	jfLoginReq.Header.Set("Authorization", `MediaBrowser Client="Web", DeviceId="bound-jf"`)
	jfInst.Handler().ServeHTTP(jfLoginRec, jfLoginReq)
	var jfAuth struct {
		AccessToken string
		User        struct{ Id string }
	}
	if err := json.Unmarshal(jfLoginRec.Body.Bytes(), &jfAuth); err != nil || jfAuth.AccessToken == "" {
		t.Fatalf("jellyfin login failed: %d %s", jfLoginRec.Code, jfLoginRec.Body.String())
	}
	bustCatalogCache(4)
	reqJF := httptest.NewRequest(http.MethodGet, "/Movies/Recommendations?userId="+jfAuth.User.Id+"&categoryLimit=10&itemLimit=12", nil)
	reqJF.Header.Set("X-Emby-Token", jfAuth.AccessToken)
	jfCold := measureQueries("jellyfin home cold", jfInst.Handler(), reqJF)

	bustCatalogCache(5)
	reqPlex := httptest.NewRequest(http.MethodGet, "/hubs?count=12", nil)
	reqPlex.Header.Set("X-Plex-Token", viewers[0].PlexToken)
	reqPlex.Header.Set("X-Plex-Client-Identifier", "bound-plex")
	reqPlex.Header.Set("Accept", "application/json")
	plexCold := measureQueries("plex hubs cold", h, reqPlex)

	t.Logf("queries: vela_cold=%d->%d vela_warm=%d->%d vela_restricted=%d jf_cold=%d plex_cold=%d",
		velaSmallCold, velaLargeCold, velaSmallWarm, velaLargeWarm, velaRestrictedCold, jfCold, plexCold)

	// Generous ceilings plus scaling invariance: adding 8 collections and 8 playlists
	// must not cause linear per-row or per-playlist query growth.
	if velaLargeCold > 220 {
		t.Errorf("vela cold home queries = %d, want <= 220", velaLargeCold)
	}
	if velaLargeCold-velaSmallCold > 25 {
		t.Errorf("vela cold home scaled with collections/playlists: small=%d large=%d (delta=%d)", velaSmallCold, velaLargeCold, velaLargeCold-velaSmallCold)
	}
	if velaLargeWarm > 80 {
		t.Errorf("vela warm home queries = %d, want <= 80", velaLargeWarm)
	}
	if velaLargeWarm-velaSmallWarm > 10 {
		t.Errorf("vela warm home scaled with collections/playlists: small=%d large=%d (delta=%d)", velaSmallWarm, velaLargeWarm, velaLargeWarm-velaSmallWarm)
	}
	if velaRestrictedCold > 220 {
		t.Errorf("vela restricted cold home queries = %d, want <= 220", velaRestrictedCold)
	}
	if jfCold > 150 {
		t.Errorf("jellyfin cold home queries = %d, want <= 150", jfCold)
	}
	if plexCold > 260 {
		t.Errorf("plex cold hubs queries = %d, want <= 260", plexCold)
	}
}

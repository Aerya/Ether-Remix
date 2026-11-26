package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/resolver"
)

func setupTestAppOwner(t *testing.T) *App {
	t.Helper()
	a := setupTestApp(t, true)
	if err := a.users.SetOwner(context.Background(), plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	return a
}

func doAdminAPI(t *testing.T, a *App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "http://localhost/ether"+path, reader)
	req.RemoteAddr = "127.0.0.1:1000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Plex-Token", "test-owner")
	req.Header.Set("X-Ether-Request", "1")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}

func TestQualityDevicesSummaryAndExplainAPI(t *testing.T) {
	ctx := context.Background()
	a := setupTestAppOwner(t)
	localID, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	viewerID, err := localID.CreateViewer(ctx, "chris", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}

	animeLib, err := a.libraries.Create(ctx, "Anime", core.TypeShow, libraryRules(core.Filter{Type: core.TypeShow, Genres: []string{"Animation"}}), "hide", core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	show := core.Title{Key: core.ShowKey(910), Type: core.TypeShow, Title: "Anime Test", Released: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Animation"}}
	ep := core.Title{Key: core.EpisodeKey(910, 1, 1), Type: core.TypeEpisode, Title: "Ep 1", ShowKey: show.Key, Season: 1, Episode: 1}
	if err := a.catalog.UpsertTitle(ctx, show, true); err != nil {
		t.Fatal(err)
	}
	if err := a.catalog.UpsertTitle(ctx, ep, true); err != nil {
		t.Fatal(err)
	}

	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality = config.Quality{
			MigratedRules: 1,
			Profiles: []core.QualityProfile{
				{Name: "4K", MaxHeight: 2160, AllowHDR: true},
				{Name: "Anime", MaxHeight: 1080},
				{Name: "Away", MaxHeight: 720},
			},
			Default:     "4K",
			DefaultAway: "Away",
			Rules: []config.QualityRule{
				{ID: "r_viewer_cap", Viewers: []int64{viewerID}, MaxBitrateKbps: 5000},
				{ID: "r_anime", Libraries: []int64{int64(animeLib.ID)}, Profile: "Anime"},
				{ID: "r_infuse_stereo", Devices: []config.DeviceMatch{{App: "jellyfin:Infuse"}}, StereoAudio: true},
			},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 1. GET /api/quality/devices returns groups, types, apps, and clients.
	w := doAdminAPI(t, a, http.MethodGet, "/api/quality/devices", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/quality/devices = %d: %s", w.Code, w.Body.String())
	}
	var devs qualityDevicesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &devs); err != nil {
		t.Fatal(err)
	}
	if len(devs.Groups) == 0 || len(devs.Types) == 0 || len(devs.Apps) == 0 {
		t.Fatalf("expected non-empty groups/types/apps: %+v", devs)
	}

	// 2. GET /api/quality/summary unfiltered lists exceptions and library rows.
	w = doAdminAPI(t, a, http.MethodGet, "/api/quality/summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/quality/summary = %d: %s", w.Code, w.Body.String())
	}
	var sum resolver.QualitySummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	var animeRow *resolver.QualitySummaryRow
	for i := range sum.Rows {
		if sum.Rows[i].LibraryID == int64(animeLib.ID) {
			animeRow = &sum.Rows[i]
		}
	}
	if animeRow == nil || len(animeRow.Cells) != 1 || animeRow.Cells[0].Home.Profile != "Anime" {
		t.Fatalf("anime summary row = %+v", animeRow)
	}
	if len(animeRow.Exceptions) < 2 {
		t.Fatalf("expected viewer & device exceptions on unfiltered summary: %+v", animeRow.Exceptions)
	}

	// 3. GET /api/quality/summary filtered by viewer + app evaluates limits inline.
	w = doAdminAPI(t, a, http.MethodGet, "/api/quality/summary?viewer="+strconv.FormatInt(viewerID, 10)+"&device=app:jellyfin:Infuse", "")
	if w.Code != http.StatusOK {
		t.Fatalf("filtered summary = %d: %s", w.Code, w.Body.String())
	}
	var fsum resolver.QualitySummary
	if err := json.Unmarshal(w.Body.Bytes(), &fsum); err != nil {
		t.Fatal(err)
	}
	for _, r := range fsum.Rows {
		if r.LibraryID == int64(animeLib.ID) {
			if r.Cells[0].Home.Profile != "Anime" || r.Cells[0].Home.MaxBitrateKbps != 5000 || !r.Cells[0].Home.Stereo {
				t.Fatalf("filtered anime cell = %+v", r.Cells[0].Home)
			}
		}
	}

	// Invalid summary query params return 400.
	if bad := doAdminAPI(t, a, http.MethodGet, "/api/quality/summary?viewer=abc", ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad viewer status = %d", bad.Code)
	}
	if bad := doAdminAPI(t, a, http.MethodGet, "/api/quality/summary?device=bogus", ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad device status = %d", bad.Code)
	}

	// 4. POST /api/quality/explain with libraryId + viewer + app device token.
	explainLibBody := `{"userId":` + strconv.FormatInt(viewerID, 10) + `,"clientId":"app:jellyfin:Infuse","libraryId":` + strconv.FormatInt(int64(animeLib.ID), 10) + `}`
	w = doAdminAPI(t, a, http.MethodPost, "/api/quality/explain", explainLibBody)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/quality/explain (libraryId) = %d: %s", w.Code, w.Body.String())
	}
	var ans resolver.QualityAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if ans.Profile != "Anime" || ans.ProfileRuleID != "r_anime" || ans.BitrateKbps != 5000 || !ans.Stereo {
		t.Fatalf("explain (libraryId) answer = %+v", ans)
	}

	// 5. POST /api/quality/explain with titleKey for a cached movie.
	movie := core.Title{Key: core.MovieKey(911), Type: core.TypeMovie, Title: "Explain Film", Year: 2024, Released: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := a.catalog.UpsertTitle(ctx, movie, true); err != nil {
		t.Fatal(err)
	}
	explainTitleBody := `{"userId":` + strconv.FormatInt(viewerID, 10) + `,"clientId":"type:Apple TV","titleKey":"` + movie.Key.String() + `","remote":true}`
	w = doAdminAPI(t, a, http.MethodPost, "/api/quality/explain", explainTitleBody)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/quality/explain (titleKey) = %d: %s", w.Code, w.Body.String())
	}
	var ans2 resolver.QualityAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &ans2); err != nil {
		t.Fatal(err)
	}
	if ans2.Profile != "Away" || ans2.BitrateKbps != 5000 || ans2.Kind != config.KindMovie {
		t.Fatalf("explain (titleKey) answer = %+v", ans2)
	}
}

func TestQualityConfigPutValidationAndServerRuleIDs(t *testing.T) {
	a := setupTestAppOwner(t)
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.MigratedRules = 1
		c.Quality.RulesMigrationNote = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Valid PUT with empty rule ID, migratedRules: 0 (must stay monotonic at 1),
	// and rulesMigrationNote: false (dismisses banner).
	validBody := `{
		"profiles": [
			{"name": "4K", "maxHeight": 2160},
			{"name": "1080p", "maxHeight": 1080}
		],
		"default": "4K",
		"defaultAway": "1080p",
		"migratedRules": 0,
		"rulesMigrationNote": false,
		"rules": [
			{"where": "away", "maxBitrateKbps": 8000}
		]
	}`
	w := doAdminAPI(t, a, http.MethodPut, "/api/config/quality", validBody)
	if w.Code != http.StatusOK {
		t.Fatalf("valid PUT /api/config/quality = %d: %s", w.Code, w.Body.String())
	}
	q := a.cfg.Get().Quality
	if q.MigratedRules != 1 {
		t.Fatalf("MigratedRules lowered to %d, want monotonic 1", q.MigratedRules)
	}
	if q.RulesMigrationNote {
		t.Fatalf("RulesMigrationNote was not dismissed")
	}
	if len(q.Rules) != 1 || q.Rules[0].ID == "" {
		t.Fatalf("expected server-assigned rule ID, got %+v", q.Rules)
	}

	// Reject unknown profile reference.
	badProfile := `{
		"profiles": [{"name": "4K", "maxHeight": 2160}],
		"default": "4K",
		"rules": [{"where": "away", "profile": "Missing"}]
	}`
	if w := doAdminAPI(t, a, http.MethodPut, "/api/config/quality", badProfile); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown profile PUT = %d, want 400 (%s)", w.Code, w.Body.String())
	}

	// Reject unknown library ID.
	badLib := `{
		"profiles": [{"name": "4K", "maxHeight": 2160}],
		"default": "4K",
		"rules": [{"libraries": [999999], "profile": "4K"}]
	}`
	if w := doAdminAPI(t, a, http.MethodPut, "/api/config/quality", badLib); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown library PUT = %d, want 400 (%s)", w.Code, w.Body.String())
	}

	// Reject unknown viewer ID.
	badViewer := `{
		"profiles": [{"name": "4K", "maxHeight": 2160}],
		"default": "4K",
		"rules": [{"viewers": [999999], "maxBitrateKbps": 4000}]
	}`
	if w := doAdminAPI(t, a, http.MethodPut, "/api/config/quality", badViewer); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown viewer PUT = %d, want 400 (%s)", w.Code, w.Body.String())
	}
}

func TestLibraryAndViewerDeletionCleanup(t *testing.T) {
	ctx := context.Background()
	a := setupTestAppOwner(t)
	localID, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := localID.CreateViewer(ctx, "viewer1", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := localID.CreateViewer(ctx, "viewer2", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	v3, err := localID.CreateViewer(ctx, "viewer3", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}

	libA, err := a.libraries.Create(ctx, "Lib A", core.TypeMovie, libraryRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Action"}}), "hide", core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	libB, err := a.libraries.Create(ctx, "Lib B", core.TypeMovie, libraryRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Comedy"}}), "hide", core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules = []config.QualityRule{
			{ID: "r_lib_only_a", Libraries: []int64{int64(libA.ID)}, Profile: "1080p"},
			{ID: "r_lib_ab", Libraries: []int64{int64(libA.ID), int64(libB.ID)}, Profile: "1080p"},
			{ID: "r_v1_only", Viewers: []int64{v1}, MaxBitrateKbps: 3000},
			{ID: "r_v1_v2", Viewers: []int64{v1, v2}, MaxBitrateKbps: 4000},
			{ID: "r_v2_v3", Viewers: []int64{v2, v3}, MaxBitrateKbps: 5000},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Delete Lib A: r_lib_only_a must be dropped, r_lib_ab narrowed to [libB].
	if w := doAdminAPI(t, a, http.MethodDelete, "/api/libraries/"+strconv.FormatInt(int64(libA.ID), 10), ""); w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Fatalf("DELETE /api/libraries/%d = %d: %s", libA.ID, w.Code, w.Body.String())
	}
	findRule := func(id string) *config.QualityRule {
		for _, r := range a.cfg.Get().Quality.Rules {
			if r.ID == id {
				cp := r
				return &cp
			}
		}
		return nil
	}
	if r := findRule("r_lib_only_a"); r != nil {
		t.Fatalf("r_lib_only_a should have been dropped: %+v", r)
	}
	if r := findRule("r_lib_ab"); r == nil || len(r.Libraries) != 1 || r.Libraries[0] != int64(libB.ID) {
		t.Fatalf("r_lib_ab should have been narrowed to [%d], got %+v", libB.ID, r)
	}

	// 2. Remove viewer v1: r_v1_only dropped, r_v1_v2 narrowed to [v2].
	if w := doAdminAPI(t, a, http.MethodDelete, "/api/users/"+strconv.FormatInt(v1, 10), ""); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/users/%d = %d: %s", v1, w.Code, w.Body.String())
	}
	if r := findRule("r_v1_only"); r != nil {
		t.Fatalf("r_v1_only should have been dropped: %+v", r)
	}
	if r := findRule("r_v1_v2"); r == nil || len(r.Viewers) != 1 || r.Viewers[0] != v2 {
		t.Fatalf("r_v1_v2 should have been narrowed to [%d], got %+v", v2, r)
	}

	// 3. Merge viewer v2 into v3: r_v1_v2 becomes [v3], r_v2_v3 deduplicates to [v3].
	mergeBody := `{"into":` + strconv.FormatInt(v3, 10) + `}`
	if w := doAdminAPI(t, a, http.MethodPost, "/api/users/"+strconv.FormatInt(v2, 10)+"/merge", mergeBody); w.Code != http.StatusOK {
		t.Fatalf("POST /api/users/%d/merge = %d: %s", v2, w.Code, w.Body.String())
	}
	if r := findRule("r_v1_v2"); r == nil || len(r.Viewers) != 1 || r.Viewers[0] != v3 {
		t.Fatalf("r_v1_v2 after merge = %+v, want [%d]", r, v3)
	}
	if r := findRule("r_v2_v3"); r == nil || len(r.Viewers) != 1 || r.Viewers[0] != v3 {
		t.Fatalf("r_v2_v3 after merge should dedupe to [%d], got %+v", v3, r)
	}
}

// TestLibraryProfilesInLibraryRetryOnErrorAndPluginFastPath verifies:
//  1. A cancelled/failing membership lookup returns false WITHOUT poisoning the
//     membership bitset cache, so an immediate retry with a live context returns
//     true and caches it.
//  2. Plugin titles carrying LibraryID resolve membership directly without SQL
//     or kindCatalog.Item calls (succeeding even on a cancelled context).
func TestLibraryProfilesInLibraryRetryOnErrorAndPluginFastPath(t *testing.T) {
	ctx := context.Background()
	a := setupTestApp(t, true)

	movie := core.Title{
		Key:      core.MovieKey(950),
		Type:     core.TypeMovie,
		Title:    "Retry Film",
		Year:     2024,
		Released: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Genres:   []string{"Animation"},
	}
	if err := a.catalog.UpsertTitle(ctx, movie, true); err != nil {
		t.Fatal(err)
	}
	lib, err := a.libraries.Create(ctx, "Animated Films", core.TypeMovie, libraryRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Animation"}}), "hide", core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules = []config.QualityRule{{ID: "r_anim", Libraries: []int64{int64(lib.ID)}, Profile: "1080p"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	lp := newLibraryProfiles(a)

	// Cancelled context causes TitleInFilter to error: must return false AND
	// NOT record known non-membership in the cache.
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if got := lp.InLibrary(cancelledCtx, &movie, lib.ID); got {
		t.Fatalf("cancelled lookup returned true")
	}
	lp.mu.Lock()
	bits, hasEntry := lp.cache.Peek(movie.Key)
	pos, hasPos := lp.refIndex[lib.ID]
	lp.mu.Unlock()
	if hasEntry && hasPos && bits.has(pos) {
		t.Fatalf("cancelled lookup poisoned membership cache for lib %d", lib.ID)
	}

	// Retrying with a live context must now query TitleInFilter, return true,
	// and store known membership in the cache.
	if got := lp.InLibrary(ctx, &movie, lib.ID); !got {
		t.Fatalf("retry with live context returned false, want true")
	}
	lp.mu.Lock()
	bits, hasEntry = lp.cache.Peek(movie.Key)
	pos, hasPos = lp.refIndex[lib.ID]
	lp.mu.Unlock()
	if !hasEntry || !hasPos || !bits.has(pos) || !bits.is(pos) {
		t.Fatalf("expected live lookup to cache membership: entry=%v pos=%d(%v) bits=%+v", hasEntry, pos, hasPos, bits)
	}

	// Plugin title with LibraryID set resolves immediately without touching SQL
	// or kindCatalog.Item, even with a cancelled context.
	pluginTitle := &core.Title{
		Key:       core.PluginKey(42),
		Type:      core.TypeMovie,
		Title:     "Plugin Video",
		LibraryID: core.LibraryID(99),
	}
	if !lp.InLibrary(cancelledCtx, pluginTitle, core.LibraryID(99)) {
		t.Fatalf("plugin title fast path failed for matching LibraryID")
	}
	if lp.InLibrary(cancelledCtx, pluginTitle, core.LibraryID(100)) {
		t.Fatalf("plugin title fast path matched wrong LibraryID")
	}
}

func TestQualityPUTPreservesMigrationMetadataAndReplacesFallbackLimits(t *testing.T) {
	a := setupTestAppOwner(t)
	at := time.Date(2026, 11, 24, 1, 2, 3, 0, time.UTC)
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.MigrationNote = &config.QualityMigrationNote{At: at}
		c.Quality.Rules = []config.QualityRule{{
			ID: "r_migrated", MaxBitrateKbps: 8000,
			From: &config.QualityRuleOrigin{Kind: "awayLimit"},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	put := func(body string, status int) {
		t.Helper()
		w := doAdminAPI(t, a, http.MethodPut, "/api/config/quality", body)
		if w.Code != status {
			t.Fatalf("quality PUT = %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	put(`{
		"defaultLimits":{"maxHeight":720,"stereoAudio":true},
		"defaultAwayLimits":{"maxBitrateKbps":6000},
		"rules":[
			{"id":"r_migrated","maxBitrateKbps":7000,"from":{"kind":"tvDefault"}},
			{"id":"r_new","maxBitrateKbps":9000,"from":{"kind":"tvDefault"}}
		]
	}`, http.StatusOK)
	q := a.cfg.Get().Quality
	if q.DefaultLimits != (config.QualityLimits{MaxHeight: 720, StereoAudio: true}) ||
		q.DefaultAwayLimits != (config.QualityLimits{MaxBitrateKbps: 6000}) {
		t.Fatalf("fallback limits = %+v home, %+v away", q.DefaultLimits, q.DefaultAwayLimits)
	}
	if q.Rules[0].From == nil || q.Rules[0].From.Kind != "awayLimit" || q.Rules[1].From != nil {
		t.Fatalf("incorrect stored provenance: %+v", q.Rules)
	}
	put(`{"defaultLimits":{"maxHeight":1000},"defaultAwayLimits":{"maxBitrateKbps":-1}}`, http.StatusBadRequest)
	put(`{"defaultLimits":{},"migrationNote":{"at":"2026-11-24T01:02:03Z","dismissed":true}}`, http.StatusOK)
	put(`{"migrationNote":{"at":"2026-11-24T01:02:03Z","dismissed":false}}`, http.StatusOK)
	q = a.cfg.Get().Quality
	if q.DefaultLimits.HasLimits() || q.DefaultAwayLimits.MaxBitrateKbps != 6000 {
		t.Fatalf("clearing home limits changed the wrong rows: %+v", q)
	}
	if q.MigrationNote == nil || !q.MigrationNote.Dismissed || !q.MigrationNote.At.Equal(at) {
		t.Fatalf("migration dismissal = %+v", q.MigrationNote)
	}
}

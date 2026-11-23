package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/store"
)

// fakeCatalogAddonServer simulates a Stremio catalogue add-on over real HTTP
// with configurable manifests per secret config prefix, paged catalogs, and
// optional HTTP 429 rate-limit injection.
type fakeCatalogAddonServer struct {
	mu          sync.Mutex
	manifests   map[string]string // prefix (e.g. "/cfg-adults") -> manifest JSON
	catalogs    map[string]string // full path -> response JSON
	requests    []string
	rateLimitOn bool
	retryAfter  string
}

func (f *fakeCatalogAddonServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/manifest.json") {
			prefix := strings.TrimSuffix(r.URL.Path, "/manifest.json")
			body, ok := f.manifests[prefix]
			f.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
			return
		}
		rl, retry := f.rateLimitOn, f.retryAfter
		body, ok := f.catalogs[r.URL.Path]
		f.mu.Unlock()
		if rl {
			if retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

func (f *fakeCatalogAddonServer) countPath(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.requests {
		if strings.Contains(p, substr) {
			n++
		}
	}
	return n
}

// httpAddonRuntime forwards installed add-on routes to the add-on's sealed
// base URL through the test HTTP client, just like the production remote
// runtime does for Stremio add-ons.
type httpAddonRuntime struct {
	base   string
	client *http.Client
}

func (r *httpAddonRuntime) Call(ctx context.Context, req plugins.Request) (plugins.Response, error) {
	url := strings.TrimRight(r.base, "/") + req.Path
	hreq, err := http.NewRequestWithContext(ctx, req.Method, url, nil)
	if err != nil {
		return plugins.Response{}, err
	}
	resp, err := r.client.Do(hreq)
	if err != nil {
		return plugins.Response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return plugins.Response{}, err
	}
	headers := map[string]string{}
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}
	return plugins.Response{Status: resp.StatusCode, Headers: headers, Body: body}, nil
}

func (r *httpAddonRuntime) Close() error { return nil }

type importTestHarness struct {
	cols     *collections.Service
	mgr      *plugins.Manager
	importer *adapters.CatalogImporter
	srv      *httptest.Server
	fake     *fakeCatalogAddonServer
	adminH   *admin.Handler
}

func newImportTestHarness(t *testing.T, colOpts ...collections.Option) *importTestHarness {
	t.Helper()
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	catCfg := config.Catalog{Language: "en-US", Region: "US", MinPopularity: 1, MinVotes: 1}
	cat, err := catalog.New(db, nil, catCfg, log)
	if err != nil {
		t.Fatal(err)
	}
	seed := []core.Title{
		{Key: core.MovieKey(101), Type: core.TypeMovie, IDs: core.IDs{TMDB: 101, IMDB: "tt0133093"}, Title: "The Matrix", Year: 1999, Released: time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC), Rating: 8.7, Popularity: 90, VoteCount: 10000},
		{Key: core.MovieKey(102), Type: core.TypeMovie, IDs: core.IDs{TMDB: 102, IMDB: "tt0234215"}, Title: "The Matrix Reloaded", Year: 2003, Released: time.Date(2003, 5, 15, 0, 0, 0, 0, time.UTC), Rating: 7.2, Popularity: 75, VoteCount: 6000},
		{Key: core.MovieKey(103), Type: core.TypeMovie, IDs: core.IDs{TMDB: 103, IMDB: "tt0242653"}, Title: "The Matrix Revolutions", Year: 2003, Released: time.Date(2003, 11, 5, 0, 0, 0, 0, time.UTC), Rating: 6.7, Popularity: 70, VoteCount: 5500},
		{Key: core.ShowKey(201), Type: core.TypeShow, IDs: core.IDs{TMDB: 201, IMDB: "tt0903747", TVDB: 81189}, Title: "Breaking Bad", Year: 2008, Released: time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC), Rating: 9.5, Popularity: 99, VoteCount: 15000},
		{Key: core.ShowKey(202), Type: core.TypeShow, IDs: core.IDs{TMDB: 202, IMDB: "tt3032476", TVDB: 273181}, Title: "Better Call Saul", Year: 2015, Released: time.Date(2015, 2, 8, 0, 0, 0, 0, time.UTC), Rating: 9.0, Popularity: 88, VoteCount: 12000},
	}
	for _, title := range seed {
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}

	opts := append([]collections.Option{
		collections.WithIndexTTL(time.Hour),
		collections.WithLiveTTL(time.Hour),
	}, colOpts...)
	cols, err := collections.New(db, cat, log, opts...)
	if err != nil {
		t.Fatal(err)
	}
	pers, err := personalize.New(personalize.Options{
		DB: db, Catalog: cat, Collections: cols, Logger: log, RefreshEvery: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pers.Close() })

	fake := &fakeCatalogAddonServer{
		manifests: map[string]string{},
		catalogs:  map[string]string{},
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	reg := extend.New()
	mgr, err := plugins.New(plugins.Deps{
		DB:          db,
		Registry:    reg,
		Tools:       tools.NewCatalogue(),
		Logger:      log,
		FetchClient: srv.Client(),
		IsLeader:    func() bool { return true },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("remote", func(in plugins.Installed) (plugins.Runtime, error) {
		return &httpAddonRuntime{base: in.SourceURL, client: srv.Client()}, nil
	})
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	adapters.Attach(mgr, cols, pers, nil)
	importer := adapters.AttachCatalogImporter(db, mgr, cols, log)

	cfgStore, err := config.Open(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	adminH := admin.NewHandler(admin.Deps{
		Config:              cfgStore,
		Collections:         cols,
		AddonCatalogImports: importer,
		Personalization:     pers,
		IsOwnerToken:        func(_ context.Context, tok string) bool { return tok == "owner-tok" },
		Logger:              log,
	})

	return &importTestHarness{
		cols:     cols,
		mgr:      mgr,
		importer: importer,
		srv:      srv,
		fake:     fake,
		adminH:   adminH,
	}
}

func TestStremioAddonImportPagingIDMappingAndManifestSync(t *testing.T) {
	h := newImportTestHarness(t)
	ctx := context.Background()

	const secretAdults = "SECRET_ADULTS_TOKEN_99"
	const secretKids = "SECRET_KIDS_TOKEN_77"
	adultsPrefix := "/" + secretAdults
	kidsPrefix := "/" + secretKids

	// Initial adults manifest: duplicate "Popular" (movie & series), IMDb Top,
	// required-genre catalog, required-search catalog, and an unchecked extra catalog.
	h.fake.manifests[adultsPrefix] = `{
		"id": "org.aiometadata.curated",
		"version": "1.0.0",
		"name": "Curated Meta",
		"resources": ["catalog"],
		"types": ["movie", "series"],
		"catalogs": [
			{"type": "movie",  "id": "popular",  "name": "Popular",      "extra": [{"name": "skip"}]},
			{"type": "series", "id": "popular",  "name": "Popular",      "extra": [{"name": "skip"}]},
			{"type": "movie",  "id": "imdb-top", "name": "IMDb Top 250", "extra": [{"name": "skip"}]},
			{"type": "movie",  "id": "by-genre", "name": "By Genre",     "extraRequired": ["genre"], "genres": ["Action", "Drama"]},
			{"type": "movie",  "id": "search",   "name": "Search Only",  "extra": [{"name": "search", "isRequired": true}]},
			{"type": "movie",  "id": "skip-me",  "name": "Skip Me"}
		]
	}`
	// Kids config on the same add-on host uses a different secret URL.
	h.fake.manifests[kidsPrefix] = `{
		"id": "org.aiometadata.curated",
		"version": "1.0.0",
		"name": "Curated Meta Kids",
		"resources": ["catalog"],
		"types": ["movie"],
		"catalogs": [
			{"type": "movie", "id": "popular", "name": "Family Picks"}
		]
	}`

	// Paged movie catalog (IMDb + TMDB + moviedb_id across two skip pages).
	h.fake.catalogs[adultsPrefix+"/catalog/movie/popular.json"] = `{"metas":[
		{"id":"tt0133093","type":"movie","name":"The Matrix"},
		{"id":"tmdb:102","type":"movie","name":"The Matrix Reloaded"}
	]}`
	h.fake.catalogs[adultsPrefix+"/catalog/movie/popular/skip=2.json"] = `{"metas":[
		{"id":"custom:3","moviedb_id":103,"type":"movie","name":"The Matrix Revolutions"}
	]}`
	h.fake.catalogs[adultsPrefix+"/catalog/movie/popular/skip=3.json"] = `{"metas":[]}`

	// Paged series catalog using TVDB IDs (tvdb:81189 and tvdbId: 273181).
	h.fake.catalogs[adultsPrefix+"/catalog/series/popular.json"] = `{"metas":[
		{"id":"tvdb:81189","type":"series","name":"Breaking Bad"},
		{"id":"custom:bcs","tvdbId":273181,"type":"series","name":"Better Call Saul"}
	]}`
	h.fake.catalogs[adultsPrefix+"/catalog/series/popular/skip=2.json"] = `{"metas":[]}`

	// IMDb Top & Genre-pinned catalogs.
	h.fake.catalogs[adultsPrefix+"/catalog/movie/imdb-top.json"] = `{"metas":[{"id":"tt0133093","type":"movie"}]}`
	h.fake.catalogs[adultsPrefix+"/catalog/movie/imdb-top/skip=1.json"] = `{"metas":[]}`
	h.fake.catalogs[adultsPrefix+"/catalog/movie/by-genre/genre=Action.json"] = `{"metas":[{"id":"tmdb:101","type":"movie"}]}`
	h.fake.catalogs[kidsPrefix+"/catalog/movie/popular.json"] = `{"metas":[{"id":"tt0133093","type":"movie"}]}`

	adultsURL := h.srv.URL + adultsPrefix + "/manifest.json"
	kidsURL := h.srv.URL + kidsPrefix + "/manifest.json"

	// 1. Preview via admin API never installs yet and never leaks the secret URL.
	prevBody := mustAdminJSON(t, h.adminH, http.MethodPost, "/ether/api/collections/addon-imports/preview",
		map[string]any{"url": adultsURL})
	if strings.Contains(string(prevBody), secretAdults) {
		t.Fatalf("preview response leaked secret token: %s", prevBody)
	}
	var prev core.AddonCatalogPreview
	if err := json.Unmarshal(prevBody, &prev); err != nil {
		t.Fatal(err)
	}
	if prev.Installed || len(prev.Catalogs) != 6 {
		t.Fatalf("preview = installed:%v catalogs:%d", prev.Installed, len(prev.Catalogs))
	}
	if !prev.Catalogs[0].HasDuplicateName || !prev.Catalogs[1].HasDuplicateName {
		t.Fatalf("expected duplicate name flag on Popular movie/series: %+v %+v", prev.Catalogs[0], prev.Catalogs[1])
	}
	if prev.Catalogs[3].Standalone || !slices.Equal(prev.Catalogs[3].RequiredExtras, []string{"genre"}) || len(prev.Catalogs[3].Extras[0].Options) != 2 {
		t.Fatalf("legacy genre extra not parsed in preview: %+v", prev.Catalogs[3])
	}

	// 2. Import 4 selected catalogues (+ intentionally send Search Only without required extra
	//    to verify it skips with a clear reason, and leave "Skip Me" unselected).
	popMovieKey := prev.Catalogs[0].Key
	popSeriesKey := prev.Catalogs[1].Key
	imdbTopKey := prev.Catalogs[2].Key
	byGenreKey := prev.Catalogs[3].Key
	searchKey := prev.Catalogs[4].Key

	applyBody := mustAdminJSON(t, h.adminH, http.MethodPost, "/ether/api/collections/addon-imports",
		core.AddonCatalogImportRequest{
			URL:            adultsURL,
			Folder:         "AIOMetadata",
			NameMode:       core.AddonImportNameAutoType,
			FollowManifest: true,
			Refresh:        collections.RefreshDaily,
			MaxItems:       250,
			Enabled:        true,
			Targets: core.CollectionTargets{
				CollectionsTab: true,
				Users:          core.UserTargets{IDs: []int64{1}},
			},
			Items: []core.AddonCatalogImportItem{
				{Key: popMovieKey},
				{Key: popSeriesKey},
				{Key: imdbTopKey},
				{Key: byGenreKey, Params: map[string]string{"genre": "Action"}},
				{Key: searchKey}, // missing required "search" extra -> skipped cleanly
			},
		})
	if strings.Contains(string(applyBody), secretAdults) {
		t.Fatalf("apply response leaked secret token: %s", applyBody)
	}
	var applied core.AddonCatalogImportResult
	if err := json.Unmarshal(applyBody, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Created != 4 || len(applied.Skipped) != 1 || applied.Skipped[0].Key != searchKey {
		t.Fatalf("apply result = %+v", applied)
	}

	// Verify auto-disambiguation, folder, manifest order, targets.users, and rebuild paging + ID mapping.
	specs, err := h.cols.ListSpecs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var managed []core.CollectionSpec
	for _, sp := range specs {
		if sp.AddonSync != nil && sp.AddonSync.AddonID == applied.AddonID {
			managed = append(managed, sp)
		}
	}
	if len(managed) != 4 {
		t.Fatalf("managed count = %d, want 4", len(managed))
	}
	wantNames := []string{"Popular (Movies)", "Popular (Series)", "IMDb Top 250", "By Genre"}
	for i, want := range wantNames {
		if managed[i].Name != want || managed[i].Folder != "AIOMetadata" || managed[i].SortOrder != i+1 {
			t.Fatalf("managed[%d] = name:%q folder:%q order:%d, want %q / AIOMetadata / %d",
				i, managed[i].Name, managed[i].Folder, managed[i].SortOrder, want, i+1)
		}
		if !slices.Equal(managed[i].Targets.Users.IDs, []int64{1}) {
			t.Fatalf("managed[%d] users = %+v, want [1]", i, managed[i].Targets.Users)
		}
	}

	// Rebuild Popular (Movies) and Popular (Series) to verify skip paging + IMDb/TMDB/TVDB ID mapping!
	builtMovies, err := h.cols.Rebuild(ctx, managed[0].ID)
	if err != nil || builtMovies.ItemCount != 3 {
		t.Fatalf("Popular (Movies) build = %+v err=%v, want 3 items across 2 skip pages", builtMovies, err)
	}
	builtSeries, err := h.cols.Rebuild(ctx, managed[1].ID)
	if err != nil || builtSeries.ItemCount != 2 {
		t.Fatalf("Popular (Series) build = %+v err=%v, want 2 TVDB-mapped shows", builtSeries, err)
	}
	builtGenre, err := h.cols.Rebuild(ctx, managed[3].ID)
	if err != nil || builtGenre.ItemCount != 1 {
		t.Fatalf("By Genre build = %+v err=%v, want 1 item", builtGenre, err)
	}

	// 3. Second config of the SAME add-on for kids (targets.users = [2]) stays completely independent.
	kidsPrev, err := h.importer.Preview(ctx, core.AddonCatalogPreviewRequest{URL: kidsURL})
	if err != nil || kidsPrev.AddonID == applied.AddonID {
		t.Fatalf("kids preview should yield distinct addon ID: %+v err=%v", kidsPrev, err)
	}
	kidsApplied, err := h.importer.Apply(ctx, core.AddonCatalogImportRequest{
		URL:            kidsURL,
		Folder:         "Kids",
		FollowManifest: false,
		Refresh:        collections.RefreshDaily,
		Enabled:        true,
		Targets:        core.CollectionTargets{CollectionsTab: true, Users: core.UserTargets{IDs: []int64{2}}},
		Items:          []core.AddonCatalogImportItem{{Key: kidsPrev.Catalogs[0].Key}},
	})
	if err != nil || kidsApplied.Created != 1 {
		t.Fatalf("kids apply = %+v err=%v", kidsApplied, err)
	}

	// Owner customizes "Popular (Series)" name AND changes targets.users on "IMDb Top 250" to [1, 3].
	customSeries := managed[1]
	customSeries.Name = "My Favourite Shows"
	if _, err := h.cols.UpdateSpec(ctx, customSeries.ID, customSeries); err != nil {
		t.Fatal(err)
	}
	customIMDb := managed[2]
	customIMDb.Targets.Users = core.UserTargets{IDs: []int64{1, 3}}
	if _, err := h.cols.UpdateSpec(ctx, customIMDb.ID, customIMDb); err != nil {
		t.Fatal(err)
	}

	// 4. Mutate the adults manifest:
	//    - Reorder IMDb Top 250 first AND rename it to "IMDb Classics"
	//    - Rename Popular (Series) to "Trending Shows" (must NOT clobber owner's "My Favourite Shows"!)
	//    - Remove Popular (Movie) (must flag Removed=true and keep its 3 built items!)
	//    - Add a brand-new standalone catalog "Hidden Gems" (auto-created!)
	//    - Add a new required-extra catalog "By Director" (skipped with reason!)
	//    - Keep "Skip Me" present (must NOT be auto-added because owner left it unchecked!)
	h.fake.mu.Lock()
	h.fake.manifests[adultsPrefix] = `{
		"id": "org.aiometadata.curated",
		"version": "1.1.0",
		"name": "Curated Meta",
		"resources": ["catalog"],
		"types": ["movie", "series"],
		"catalogs": [
			{"type": "movie",  "id": "imdb-top", "name": "IMDb Classics", "extra": [{"name": "skip"}]},
			{"type": "series", "id": "popular",  "name": "Trending Shows","extra": [{"name": "skip"}]},
			{"type": "movie",  "id": "by-genre", "name": "By Genre",      "extraRequired": ["genre"], "genres": ["Action", "Drama"]},
			{"type": "movie",  "id": "hidden",   "name": "Hidden Gems"},
			{"type": "movie",  "id": "director", "name": "By Director",   "extra": [{"name": "director", "isRequired": true}]},
			{"type": "movie",  "id": "skip-me",  "name": "Skip Me"}
		]
	}`
	h.fake.catalogs[adultsPrefix+"/catalog/movie/hidden.json"] = `{"metas":[{"id":"tt0133093","type":"movie"}]}`
	h.fake.mu.Unlock()

	// Trigger manual manifest sync via admin API.
	syncBody := mustAdminJSON(t, h.adminH, http.MethodPost, "/ether/api/collections/addon-imports/"+applied.AddonID+"/sync", nil)
	if strings.Contains(string(syncBody), secretAdults) {
		t.Fatalf("sync response leaked secret: %s", syncBody)
	}
	var synced core.AddonCatalogImportResult
	if err := json.Unmarshal(syncBody, &synced); err != nil {
		t.Fatal(err)
	}
	if !synced.ManifestChanged {
		t.Fatal("expected ManifestChanged=true after manifest update")
	}

	// Re-read managed collections and verify EVERY sync invariant!
	afterSpecs, err := h.cols.ListSpecs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.CollectionSpec{}
	var adultsAfter []core.CollectionSpec
	for _, sp := range afterSpecs {
		byID[sp.ID] = sp
		if sp.AddonSync != nil && sp.AddonSync.AddonID == applied.AddonID {
			adultsAfter = append(adultsAfter, sp)
		}
	}
	// Expect 5 collections: IMDb Classics, My Favourite Shows, By Genre, Hidden Gems (new), Popular (Movies) (flagged removed).
	// "Skip Me" was NOT resurrected and "By Director" was skipped.
	if len(adultsAfter) != 5 {
		t.Fatalf("after sync managed count = %d, want 5: %+v", len(adultsAfter), adultsAfter)
	}
	imdbAfter := byID[managed[2].ID]
	if imdbAfter.Name != "IMDb Classics" || imdbAfter.SortOrder != 1 {
		t.Fatalf("renamed/reordered IMDb = name:%q order:%d", imdbAfter.Name, imdbAfter.SortOrder)
	}
	if !slices.Equal(imdbAfter.Targets.Users.IDs, []int64{1, 3}) {
		t.Fatalf("owner targets.users clobbered on sync: %+v", imdbAfter.Targets.Users)
	}
	seriesAfter := byID[managed[1].ID]
	if seriesAfter.Name != "My Favourite Shows" || seriesAfter.SortOrder != 2 {
		t.Fatalf("owner-renamed series clobbered: name:%q order:%d", seriesAfter.Name, seriesAfter.SortOrder)
	}
	removedMovie := byID[managed[0].ID]
	if removedMovie.AddonSync == nil || !removedMovie.AddonSync.Removed {
		t.Fatalf("removed catalog was not flagged: %+v", removedMovie.AddonSync)
	}
	if removedMovie.ItemCount != 3 || len(removedMovie.Warnings) == 0 {
		t.Fatalf("removed catalog lost built items or warning: count=%d warnings=%v", removedMovie.ItemCount, removedMovie.Warnings)
	}
	// Owner can still unpublish a removed collection without validation rejecting the missing source.
	unpub := removedMovie
	unpub.Enabled = false
	if _, err := h.cols.UpdateSpec(ctx, unpub.ID, unpub); err != nil {
		t.Fatalf("unpublishing removed collection failed: %v", err)
	}
	// Frontend List order (shared by Plex/Jellyfin/Vela/Stremio/Xtream) follows manifest order:
	// IMDb Classics (order 1) comes before By Genre (order 3) for viewer 1!
	if _, err := h.cols.Rebuild(ctx, imdbAfter.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := h.cols.ListOwned(ctx, &core.User{ID: 1, IsOwner: true}, core.TypeMovie)
	if err != nil {
		t.Fatal(err)
	}
	imdbPos, genrePos := -1, -1
	for i, c := range listed {
		switch c.Title {
		case "IMDb Classics":
			imdbPos = i
		case "By Genre":
			genrePos = i
		}
	}
	if imdbPos < 0 || genrePos < 0 || imdbPos > genrePos {
		t.Fatalf("frontend collection order did not follow manifest reorder: imdbPos=%d genrePos=%d listed=%+v", imdbPos, genrePos, listed)
	}

	// 5. Restore Popular (Movie) in the manifest -> sync clears Removed and warning!
	h.fake.mu.Lock()
	h.fake.manifests[adultsPrefix] = `{
		"id": "org.aiometadata.curated",
		"version": "1.2.0",
		"name": "Curated Meta",
		"resources": ["catalog"],
		"types": ["movie", "series"],
		"catalogs": [
			{"type": "movie",  "id": "popular",  "name": "Popular",       "extra": [{"name": "skip"}]},
			{"type": "movie",  "id": "imdb-top", "name": "IMDb Classics", "extra": [{"name": "skip"}]},
			{"type": "series", "id": "popular",  "name": "Trending Shows","extra": [{"name": "skip"}]},
			{"type": "movie",  "id": "by-genre", "name": "By Genre",      "extraRequired": ["genre"], "genres": ["Action", "Drama"]},
			{"type": "movie",  "id": "hidden",   "name": "Hidden Gems"}
		]
	}`
	h.fake.mu.Unlock()
	if _, err := h.importer.SyncAddon(ctx, applied.AddonID); err != nil {
		t.Fatal(err)
	}
	restored, err := h.cols.GetSpec(ctx, managed[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.AddonSync == nil || restored.AddonSync.Removed || len(restored.Warnings) != 0 {
		t.Fatalf("restored catalog still flagged: sync=%+v warnings=%v", restored.AddonSync, restored.Warnings)
	}

	// 6. Rate-limit backoff: HTTP 429 pauses subsequent background builds for that add-on.
	h.fake.mu.Lock()
	h.fake.rateLimitOn = true
	h.fake.retryAfter = "120"
	h.fake.mu.Unlock()
	before429 := h.fake.countPath("/catalog/")
	if _, err := h.cols.Rebuild(ctx, imdbAfter.ID); err == nil {
		t.Fatal("expected error on 429")
	}
	afterFirst := h.fake.countPath("/catalog/")
	if _, err := h.cols.Rebuild(ctx, managed[3].ID); err == nil {
		t.Fatal("expected cooldown error on second catalog while paused")
	}
	afterSecond := h.fake.countPath("/catalog/")
	if afterFirst != before429+1 || afterSecond != afterFirst {
		t.Fatalf("rate-limited add-on was still hit during cooldown: before=%d first=%d second=%d", before429, afterFirst, afterSecond)
	}

	// 7. Unlink subscription via admin API leaves existing collections intact.
	req := httptest.NewRequest(http.MethodDelete, "/ether/api/collections/addon-imports/"+applied.AddonID, nil)
	req.Header.Set("Authorization", "Bearer owner-tok")
	req.Header.Set("X-Ether-Request", "1")
	rec := httptest.NewRecorder()
	h.adminH.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE subscription status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := h.cols.GetSpec(ctx, imdbAfter.ID); err != nil {
		t.Fatalf("unlinking subscription deleted managed collection: %v", err)
	}
}

func TestScheduledRunDueRespectsPerAddonBuildBudget(t *testing.T) {
	h := newImportTestHarness(t, collections.WithMaxPluginBuildsPerTick(2))
	ctx := context.Background()

	h.fake.manifests["/bulk"] = `{
		"id": "org.example.bulk",
		"version": "1.0.0",
		"name": "Bulk Catalogs",
		"resources": ["catalog"],
		"types": ["movie"],
		"catalogs": [
			{"type": "movie", "id": "c1", "name": "Cat 1"},
			{"type": "movie", "id": "c2", "name": "Cat 2"},
			{"type": "movie", "id": "c3", "name": "Cat 3"},
			{"type": "movie", "id": "c4", "name": "Cat 4"},
			{"type": "movie", "id": "c5", "name": "Cat 5"},
			{"type": "movie", "id": "c6", "name": "Cat 6"}
		]
	}`
	for i := 1; i <= 6; i++ {
		h.fake.catalogs[fmt.Sprintf("/bulk/catalog/movie/c%d.json", i)] = `{"metas":[{"id":"tt0133093","type":"movie"}]}`
	}

	prev, err := h.importer.Preview(ctx, core.AddonCatalogPreviewRequest{URL: h.srv.URL + "/bulk/manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]core.AddonCatalogImportItem, 0, len(prev.Catalogs))
	for _, c := range prev.Catalogs {
		items = append(items, core.AddonCatalogImportItem{Key: c.Key})
	}
	res, err := h.importer.Apply(ctx, core.AddonCatalogImportRequest{
		URL:     h.srv.URL + "/bulk/manifest.json",
		Folder:  "Bulk",
		Refresh: collections.RefreshDaily,
		Enabled: true,
		Targets: core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
		Items:   items,
	})
	if err != nil || res.Created != 6 {
		t.Fatalf("bulk apply = %+v err=%v", res, err)
	}
	// Bulk import (>5) deferred initial builds: zero catalog requests yet.
	if hits := h.fake.countPath("/catalog/"); hits != 0 {
		t.Fatalf("bulk import eagerly hammered add-on with %d catalog requests", hits)
	}

	// Each RunDue tick builds at most 2 catalogs from this add-on.
	for tick := 1; tick <= 3; tick++ {
		h.cols.RunDue(ctx)
		wantHits := tick * 2
		if got := h.fake.countPath("/catalog/"); got != wantHits {
			t.Fatalf("tick %d: catalog hits = %d, want %d", tick, got, wantHits)
		}
	}
}

func mustAdminJSON(t *testing.T, h http.Handler, method, path string, payload any) []byte {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer owner-tok")
	req.Header.Set("X-Ether-Request", "1")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("%s %s -> %d: %s", method, path, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

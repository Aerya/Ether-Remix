package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
)

type demandCatalogueKind struct {
	catID   string
	catType string
	tmdbID  int64
	calls   int
	fail    bool
	errMsg  string
	name    string
	hook    func(context.Context)
}

func (k *demandCatalogueKind) ID() string {
	catType, catID := k.catType, k.catID
	if catID == "" {
		catID = k.name
	}
	if catType == "" {
		catType = "movie"
	}
	if catID == "" {
		catID = "test"
	}
	return extend.PluginID("catalogue-test", plugins.StremioCatalogKey(plugins.StremioCat{Type: catType, ID: catID}))
}
func (*demandCatalogueKind) Describe() collections.KindDescribe                                   { return collections.KindDescribe{} }
func (*demandCatalogueKind) Validate(*collections.ValidationError, string, core.CollectionSource) {}
func (k *demandCatalogueKind) Gather(g *collections.Gather) {
	k.calls++
	if k.hook != nil {
		k.hook(g.Ctx)
	}
	if k.fail {
		msg := k.errMsg
		if msg == "" {
			msg = "upstream unavailable"
		}
		g.Fail(errors.New(msg))
		return
	}
	tmdbID := k.tmdbID
	if tmdbID == 0 {
		tmdbID = 101
	}
	mediaType := k.catType
	if mediaType == "" || mediaType == "movie" {
		mediaType = "movie"
	} else {
		mediaType = "show"
	}
	g.ResolveExternal("Test", []collections.ExternalRef{{TMDB: tmdbID, Type: mediaType}, {TMDB: tmdbID, Type: mediaType}}, 0)
}

func TestCatalogueMaterializationDemandCacheAndFailure(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(101), Type: core.TypeMovie, IDs: core.IDs{TMDB: 101}, Title: "Test", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err = cat.UpsertDetailedTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	kind := &demandCatalogueKind{}
	if err = cols.RegisterSourceKind(kind); err != nil {
		t.Fatal(err)
	}
	second := &demandCatalogueKind{name: "second"}
	if err = cols.RegisterSourceKind(second); err != nil {
		t.Fatal(err)
	}
	mgr, err := plugins.New(plugins.Deps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"catalogue-test","name":"Test","version":"1.0.0","resources":["catalog"],"types":["movie"],"catalogs":[{"type":"movie","id":"test"},{"type":"movie","id":"second"}]}`
	if _, err = db.Exec(`INSERT INTO plugins_installed(id,name,version,manifest,installed_at,updated_at) VALUES('catalogue-test','Test','1.0.0',?,0,0)`, manifest); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols, pluginsRT: &pluginRuntime{mgr: mgr}}
	a.wireCatalogueSources()
	if kind.calls != 0 {
		t.Fatal("eager source work")
	}
	id := "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: "test"})
	query := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, LocalOnly: true, CatalogueSources: []string{id}}, Limit: 10}
	for i := 0; i < 2; i++ {
		page, err := cat.Browse(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Total != 1 {
			t.Fatalf("materialized page: %+v", page)
		}
	}
	if kind.calls != 1 {
		t.Fatalf("cached source calls=%d", kind.calls)
	}
	kind.fail = true
	if _, err = db.Exec(`UPDATE libraries_catalogue_state SET refreshed=0 WHERE source_id=?`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = cat.Browse(ctx, query); err == nil {
			t.Fatal("upstream failure hidden")
		}
	}
	if kind.calls != 2 {
		t.Fatalf("failure retry was not throttled: %d", kind.calls)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM libraries_catalogue_members WHERE source_id=?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("old membership lost: %d %v", count, err)
	}
	// A manual retry bypasses the failure throttle but preserves the committed
	// state when fetching fails; later sources are still attempted.
	_, err = db.Exec(`UPDATE libraries_catalogue_state SET refreshed=123 WHERE source_id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	forced := withForceCatalogueSync(ctx)
	if err = cat.BrowseSourceLibrary(forced, []string{id, "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: "second"})}); err != nil {
		t.Fatalf("healthy sibling catalogue should remain available: %v", err)
	}
	var refreshed int64
	var savedCount int
	if err = db.QueryRow(`SELECT refreshed,item_count FROM libraries_catalogue_state WHERE source_id=?`, id).Scan(&refreshed, &savedCount); err != nil || refreshed != 0 || savedCount != 1 || kind.calls != 3 || second.calls != 1 {
		t.Fatalf("manual retry lost state or skipped later source: refreshed=%d count=%d calls=%d err=%v", refreshed, savedCount, kind.calls, err)
	}
	kind.fail = false
	if err = cat.BrowseSourceLibrary(forced, []string{id}); err != nil {
		t.Fatal(err)
	}

	// The HTTP request returns immediately; canceling it cannot kill the job.
	entered, release := make(chan struct{}), make(chan struct{})
	kind.hook = func(ctx context.Context) {
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Errorf("job inherited canceled request: %v", ctx.Err())
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 2*time.Minute {
			t.Error("old 30-second budget still applies")
		}
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(requestCtx)
	req.SetPathValue("source", id)
	recorder := httptest.NewRecorder()
	console := &librariesConsole{app: a}
	console.handleCatalogueSourceSync(recorder, req)
	cancelRequest()
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	<-entered
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for a.catalogueSync.status(id).Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := a.catalogueSync.status(id); status.Running || status.Error != "" {
		t.Fatalf("background sync=%+v", status)
	}

}

func TestCatalogueMaterializationIsolatesFailingSourceAndPersistsError(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	show := core.Title{Key: core.ShowKey(201), Type: core.TypeShow, IDs: core.IDs{TMDB: 201}, Title: "Good Show", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err = cat.UpsertDetailedTitle(ctx, show); err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	nextUp := &demandCatalogueKind{catType: "series", catID: "next-up", fail: true, errMsg: "Next Up gateway timeout"}
	topShows := &demandCatalogueKind{catType: "series", catID: "top-shows", tmdbID: 201}
	if err = cols.RegisterSourceKind(nextUp); err != nil {
		t.Fatal(err)
	}
	if err = cols.RegisterSourceKind(topShows); err != nil {
		t.Fatal(err)
	}
	mgr, err := plugins.New(plugins.Deps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"catalogue-test","name":"MDBList","version":"1.0.0","resources":["catalog"],"types":["series"],"catalogs":[{"type":"series","id":"next-up","name":"Next Up"},{"type":"series","id":"top-shows","name":"Top Shows"}]}`
	if _, err = db.Exec(`INSERT INTO plugins_installed(id,name,version,manifest,installed_at,updated_at) VALUES('catalogue-test','MDBList','1.0.0',?,0,0)`, manifest); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols, pluginsRT: &pluginRuntime{mgr: mgr}}
	a.wireCatalogueSources()

	nextUpID := "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "series", ID: "next-up"})
	topShowsID := "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "series", ID: "top-shows"})

	page, err := cat.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{Type: core.TypeShow, LocalOnly: true, CatalogueSources: []string{nextUpID, topShowsID}},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("failing Next Up list blocked sibling list import: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Key != core.ShowKey(201) {
		t.Fatalf("expected sibling list items, got %+v", page)
	}

	var nextUpErr string
	if err = db.QueryRow(`SELECT last_error FROM libraries_catalogue_state WHERE source_id=?`, nextUpID).Scan(&nextUpErr); err != nil {
		t.Fatalf("missing Next Up state row: %v", err)
	}
	if !strings.Contains(nextUpErr, "Next Up gateway timeout") {
		t.Fatalf("expected per-list error persisted, got %q", nextUpErr)
	}
	var topCount int
	var topErr string
	if err = db.QueryRow(`SELECT item_count, last_error FROM libraries_catalogue_state WHERE source_id=?`, topShowsID).Scan(&topCount, &topErr); err != nil {
		t.Fatalf("missing Top Shows state row: %v", err)
	}
	if topCount != 1 || topErr != "" {
		t.Fatalf("unexpected Top Shows state: count=%d err=%q", topCount, topErr)
	}

	// Manual force sync retries even within the 60s failure throttle window.
	nextUp.fail = false
	nextUp.tmdbID = 201
	if _, err = db.Exec(`UPDATE libraries_catalogue_state SET refreshed=0 WHERE source_id=?`, nextUpID); err != nil {
		t.Fatal(err)
	}
	if err = cat.BrowseSourceLibrary(withForceCatalogueSync(ctx), []string{nextUpID}); err != nil {
		t.Fatalf("manual force sync failed: %v", err)
	}
	if err = db.QueryRow(`SELECT item_count, last_error FROM libraries_catalogue_state WHERE source_id=?`, nextUpID).Scan(&topCount, &nextUpErr); err != nil {
		t.Fatal(err)
	}
	if topCount != 1 || nextUpErr != "" {
		t.Fatalf("expected recovered Next Up state count=1 err='', got count=%d err=%q", topCount, nextUpErr)
	}
}

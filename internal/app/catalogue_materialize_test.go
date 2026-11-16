package app

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type demandCatalogueKind struct {
	name  string
	calls int
	fail  bool
	hook  func(context.Context)
}

func (k *demandCatalogueKind) ID() string {
	name := k.name
	if name == "" {
		name = "test"
	}
	return extend.PluginID("catalogue-test", plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: name}))
}
func (*demandCatalogueKind) Describe() collections.KindDescribe                                   { return collections.KindDescribe{} }
func (*demandCatalogueKind) Validate(*collections.ValidationError, string, core.CollectionSource) {}
func (k *demandCatalogueKind) Gather(g *collections.Gather) {
	k.calls++
	if k.hook != nil {
		k.hook(g.Ctx)
	}
	if k.fail {
		g.Fail(errors.New("upstream unavailable"))
		return
	}
	g.ResolveExternal("Test", []collections.ExternalRef{{TMDB: 101, Type: "movie"}, {TMDB: 101, Type: "movie"}}, 0)
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
	forced := context.WithValue(ctx, catalogueForceKey{}, true)
	if err = cat.BrowseSourceLibrary(forced, []string{id, "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: "second"})}); err == nil {
		t.Fatal("expected failure")
	}
	var refreshed int64
	var savedCount int
	if err = db.QueryRow(`SELECT refreshed,item_count FROM libraries_catalogue_state WHERE source_id=?`, id).Scan(&refreshed, &savedCount); err != nil || refreshed != 123 || savedCount != 1 || kind.calls != 3 || second.calls != 1 {
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

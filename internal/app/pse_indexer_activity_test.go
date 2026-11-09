package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPSEIndexActivityEndpointsAndCacheInvalidation(t *testing.T) {
	a := &App{}
	t.Cleanup(func() { pseRuntimes.Delete(a) })
	rt := pseRuntimeOf(a)
	ctx := context.Background()
	db := store.OpenTest(t)
	deps := releasedb.Deps{DB: db, Config: func() config.Config { return config.Config{} }, Settings: releasedb.Settings{}}
	request := func(handler http.Handler, method string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/", nil))
		return w
	}
	live := a.pseIndexActivityHandler(false)
	if w := request(live, "POST"); w.Code != 405 {
		t.Fatal(w.Code)
	}
	w := request(live, "GET")
	var activity releasedb.Activity
	if err := json.Unmarshal(w.Body.Bytes(), &activity); err != nil || activity.State != "disabled" {
		t.Fatalf("%s %v", w.Body, err)
	}
	rt.indexer = releasedb.New(deps)
	sources := a.pseIndexActivityHandler(true)
	w = request(sources, "GET")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// New queries are hidden by the 30s snapshot, then visible immediately
	// when the indexer is replaced after a settings change.
	_, err := db.ExecContext(ctx, "INSERT INTO pse_index_budget(day,source_id,hits) VALUES(?,?,?)", time.Now().UTC().Format("2006-01-02"), "test", 3)
	if err != nil {
		t.Fatal(err)
	}
	w = request(sources, "GET")
	var overview releasedb.SourceOverview
	if err = json.Unmarshal(w.Body.Bytes(), &overview); err != nil || len(overview.Sources) != 0 {
		t.Fatalf("%s %v", w.Body, err)
	}
	rt.indexer = releasedb.New(deps)
	w = request(sources, "GET")
	if err = json.Unmarshal(w.Body.Bytes(), &overview); err != nil || len(overview.Sources) != 1 || overview.Sources[0].Hits != 3 {
		t.Fatalf("%s %v", w.Body, err)
	}
}

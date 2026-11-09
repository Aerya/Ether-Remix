package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/statistics"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPSEHistoryEndpointValidationAndRefresh(t *testing.T) {
	a := &App{db: store.OpenTest(t)}
	t.Cleanup(func() { pseRuntimes.Delete(a) })
	rt := pseRuntimeOf(a)
	if err := rt.statistics.Load(t.Context(), a.db); err != nil {
		t.Fatal(err)
	}
	rt.statistics.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
	h := a.pseIndexHistoryHandler()
	call := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := call("POST", "/"); w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/?period=invalid"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/?source="+strings.Repeat("x", 129)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := call("GET", "/?period=year")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	var d statistics.Dashboard
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || len(d.History) != 10 {
		t.Fatal(err, d)
	}
	if _, err := a.db.Exec("INSERT INTO pse_index_source_history(day,source_id,media_type,searches) VALUES('2026-11-09','a','movie',2)"); err != nil {
		t.Fatal(err)
	}
	w = call("GET", "/?period=year&refresh=1")
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.History[9].MovieSearches != 2 {
		t.Fatal(err, d)
	}
}

package releasedb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestActivityObservesRunningSearchAndSanitizesErrors(t *testing.T) {
	e := newFreshEnv(t)
	e.s.d.Search = func(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
		a := e.s.Activity()
		if a.State != "searching" || len(a.Active) != 1 || a.Active[0].Title != "Film" {
			t.Fatalf("%+v", a)
		}
		return nil, errors.New("https://private.example/secret-token")
	}
	e.index(t, 1)
	a := e.s.Activity()
	if len(a.Active) != 0 || len(a.Recent) != 2 || a.Recent[0].Status != "error" {
		t.Fatalf("%+v", a)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "secret-token") {
		t.Fatal("upstream error exposed")
	}
	a.Recent[0].Title = "mutated"
	if e.s.Activity().Recent[0].Title == "mutated" {
		t.Fatal("shared snapshot")
	}
	overview, err := e.s.SourceOverview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Sources) != 2 || overview.Sources[0].Errors != 1 || overview.Sources[0].Searches != 1 {
		t.Fatalf("%+v", overview)
	}
	if len(overview.History) != 14 {
		t.Fatalf("history: %d", len(overview.History))
	}
}

func TestSourceStatsSeparateRequestsOutcomesAndInventory(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A")}
	e.stub.errs["a"] = errors.New("partial")
	e.stub.errs["b"] = core.ErrBudgetExhausted
	e.index(t, 1)
	if _, err := e.s.st.spend(e.ctx, "a", 3, 100, e.now); err != nil {
		t.Fatal(err)
	}
	overview, err := e.s.SourceOverview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, b := overview.Sources[0], overview.Sources[1]
	if a.Partial != 1 || a.Errors != 0 || a.Results != 1 || a.Releases != 1 || a.Titles != 1 || a.Hits != 3 || a.Searches != 1 || b.Denied != 1 {
		t.Fatalf("%+v", overview)
	}
	// Restarting clears the session journal but retains daily counters.
	restarted := New(e.s.d)
	next, err := restarted.SourceOverview(e.ctx)
	if err != nil || next.Sources[0].Searches != 1 || len(restarted.Activity().Recent) != 0 {
		t.Fatalf("%+v %v", next, err)
	}
	e.cfg.Indexers[0].Enabled = false
	next, err = e.s.SourceOverview(e.ctx)
	if err != nil || next.Sources[0].Selected || next.Sources[0].Releases != 1 {
		t.Fatalf("%+v %v", next, err)
	}
}

func TestActivityBoundedAndCancellation(t *testing.T) {
	e := newFreshEnv(t)
	for i := 0; i < 55; i++ {
		event := e.s.beginSearch(core.Title{Title: "Film"}, source{id: "a", name: "A"})
		e.s.finishSearch(e.ctx, event, 0, nil)
	}
	if len(e.s.Activity().Recent) != 50 {
		t.Fatal("unbounded journal")
	}
	event := e.s.beginSearch(core.Title{Title: "Film"}, source{id: "a", name: "A"})
	e.s.finishSearch(e.ctx, event, 0, context.Canceled)
	overview, err := e.s.SourceOverview(e.ctx)
	if err != nil || overview.Sources[0].Searches != 55 {
		t.Fatalf("%+v %v", overview, err)
	}
	e.now = e.now.Add(9 * 24 * time.Hour)
	e.s.maintain(e.ctx)
	var n int
	if err = e.s.st.db.QueryRow("SELECT COUNT(*) FROM pse_index_activity").Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

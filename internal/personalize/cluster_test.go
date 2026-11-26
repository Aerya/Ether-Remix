package personalize

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/core"
)

// A home layout saved on one node is served by another after invalidation.
func TestHomeLayoutChangeReachesOtherNode(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e2, err := New(Options{DB: h.db, Catalog: h.cat, Logger: quietLogger(), Now: h.clock.Now, Location: time.UTC, RefreshEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	hub := clusterbus.NewHub()
	h.e.SetBus(hub.Node("a"))
	e2.SetBus(hub.Node("b"))

	p := rpage(core.PageHome, prow(KindTrending, core.PlacementAuto, 0))
	viewerOverride(&p, 1, p.Rows[0].ID, core.PlacementPinned, 2)
	mustSavePage(t, h.e, p)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := e2.Page(ctx, core.PageHome)
		if len(got.Variants) == 1 && got.Variants[0].Viewer == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node B still serves %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

package experience

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	_ "github.com/braedonsaunders/ether/internal/favorites"
	_ "github.com/braedonsaunders/ether/internal/notinterested"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	_ "github.com/braedonsaunders/ether/internal/watchlist"
)

// A catalogue preference change is a hard Home input for exactly one viewer,
// even when that viewer has no safety restrictions, and also fences row views.
func TestHomeRevisionViewerCatalogueIsolation(t *testing.T) {
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := &core.User{ID: 1}, &core.User{ID: 2}
	read := func(u *core.User) HomeRevision {
		t.Helper()
		r, err := ReadHomeRevision(t.Context(), db, u)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	beforeA, beforeB := read(a), read(b)
	if err := c.PutViewerRules(t.Context(), a.ID, catalog.ViewerRules{Mode: "custom"}); err != nil {
		t.Fatal(err)
	}
	afterA, afterB := read(a), read(b)
	if afterA.Viewer == beforeA.Viewer || afterB != beforeB || afterA.Policy != beforeA.Policy {
		t.Fatalf("revision isolation: a %v→%v b %v→%v", beforeA, afterA, beforeB, afterB)
	}
}

func TestHomeRevisionBumpsOnHideContinue(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	st := state.New(db, state.Options{Now: func() time.Time { return now }})
	a, b := &core.User{ID: 1}, &core.User{ID: 2}
	movie := core.MovieKey(55)
	if err := st.Progress(t.Context(), a.ID, movie, 10*time.Minute, time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}
	beforeA, err := ReadHomeRevision(t.Context(), db, a)
	if err != nil {
		t.Fatal(err)
	}
	beforeB, err := ReadHomeRevision(t.Context(), db, b)
	if err != nil {
		t.Fatal(err)
	}
	// Even without advancing the clock, hiding from Continue Watching bumps viewer A's revision only.
	if err := st.HideContinue(t.Context(), a.ID, movie); err != nil {
		t.Fatal(err)
	}
	afterA, err := ReadHomeRevision(t.Context(), db, a)
	if err != nil {
		t.Fatal(err)
	}
	afterB, err := ReadHomeRevision(t.Context(), db, b)
	if err != nil {
		t.Fatal(err)
	}
	if afterA.Viewer == beforeA.Viewer || afterB != beforeB {
		t.Fatalf("hide continue revision: a %v→%v b %v→%v", beforeA, afterA, beforeB, afterB)
	}
}

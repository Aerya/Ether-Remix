package releasedb

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"testing"
)

func TestQualityChangeRecountsWithoutLosingSearchResults(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-GOOD"), frRelease("Film.FRENCH.1080p-BAD")}
	e.index(t, 1)
	q, err := quality.New(e.ctx, e.s.Store().db)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err = q.Save(e.ctx, quality.Config{Profiles: []quality.Profile{{ID: "global", Name: "Exclude BAD", Scope: "global", Rules: []core.ReleaseRule{{ID: "bad", Name: "BAD", Action: "avoid", Match: "any", Conditions: []core.ReleaseCondition{{Field: "name", Operator: "regex", Value: "BAD"}}}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Store().SyncPlayable(e.ctx, Playable()); err != nil {
		t.Fatal(err)
	}
	if n := e.playable(t, 1); n != 1 {
		t.Fatalf("filtered count = %d", n)
	}
	if len(e.names(t, 1)) != 2 {
		t.Fatal("filter discarded raw results")
	}
	if err = q.Save(e.ctx, quality.Config{Profiles: []quality.Profile{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Store().SyncPlayable(e.ctx, Playable()); err != nil {
		t.Fatal(err)
	}
	if n := e.playable(t, 1); n != 2 {
		t.Fatalf("restored count = %d", n)
	}
}

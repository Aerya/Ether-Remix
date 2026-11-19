package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func torrentReleases(h *harness, ids ...string) {
	for i := range h.resolver.candidates {
		for _, id := range ids {
			if h.resolver.candidates[i].ID == id {
				h.resolver.candidates[i].Protocol = core.ProtocolTorrent
			}
		}
	}
}

func failingMKVs(err error, ids ...string) []*fakeRelease {
	var out []*fakeRelease
	for _, id := range ids {
		r := mkv(id, 100_000, time.Minute)
		r.OpenErr = err
		out = append(out, r)
	}
	return out
}

func TestFailoverAttemptsCapOpensAtMostFour(t *testing.T) {
	ids := []string{"A", "B", "C", "D", "E", "F"}
	h := newHarness(t, failingMKVs(errors.New("broken"), ids...), ids)
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c1")); err == nil {
		t.Fatal("prepare succeeded with every release broken")
	}
	if got := h.engine.openCount(); got != 4 {
		t.Fatalf("%d releases opened, want the default cap of 4", got)
	}

	h = newHarness(t, failingMKVs(errors.New("broken"), ids...), ids, func(h *harness, _ *Options) { h.cfg.Resolver.FailoverAttempts = 2 })
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c1")); err == nil {
		t.Fatal("prepare succeeded with every release broken")
	}
	if got := h.engine.openCount(); got != 2 {
		t.Fatalf("%d releases opened, want the configured 2", got)
	}
}

func TestDebridQuotaStopsDebridAttempts(t *testing.T) {
	quota := fmt.Errorf("alldebrid: HTTP 502: daily limit: %w", core.ErrDebridQuota)
	ids := []string{"A", "B", "C", "D"}
	h := newHarness(t, failingMKVs(quota, ids...), ids)
	torrentReleases(h, ids...)
	_, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c1"))
	if !errors.Is(err, core.ErrDebridQuota) || !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("error does not name the quota: %v", err)
	}
	if got := h.engine.openCount(); got != 1 {
		t.Fatalf("%d releases opened after the account quota error, want 1", got)
	}
}

func TestDebridQuotaStillTriesUsenet(t *testing.T) {
	quota := fmt.Errorf("alldebrid: daily limit: %w", core.ErrDebridQuota)
	a := failingMKVs(quota, "A", "B")
	rels := append(a, mkv("C", 100_000, time.Minute))
	h := newHarness(t, rels, []string{"A", "B", "C"})
	torrentReleases(h, "A", "B")
	ps, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c1"))
	if err != nil || ps.Resolution.Release.ID != "C" {
		t.Fatalf("usenet release not reached: %v %+v", err, ps)
	}
	for _, id := range h.engine.opens {
		if id == "B" {
			t.Fatal("a second debrid release was opened after the quota error")
		}
	}
}

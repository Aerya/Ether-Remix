package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestFailoverAttemptsCap(t *testing.T) {
	h := newHarness(t, nil, nil)
	if got := h.m.failoverAttempts(); got != 4 {
		t.Fatalf("défaut = %d, attendu 4", got)
	}
	cfg := h.cfg
	cfg.Resolver.FailoverAttempts = 2
	h.m.SetConfig(cfg)
	if got := h.m.failoverAttempts(); got != 2 {
		t.Fatalf("configuré = %d, attendu 2", got)
	}
	cfg.Resolver.FailoverAttempts = 10
	h.m.SetConfig(cfg)
	if got := h.m.failoverAttempts(); got != 10 {
		t.Fatalf("réglage 10 appliqué tel quel: %d", got)
	}
}

func TestOpenInitialStopsAtAttemptCap(t *testing.T) {
	var rels []*fakeRelease
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for _, id := range ids {
		rels = append(rels, &fakeRelease{ID: id, Name: id + ".mkv", Size: 1 << 20, Duration: time.Minute, OpenErr: fmt.Errorf("boom %s: %w", id, core.ErrSourceUnavailable)})
	}
	h := newHarness(t, rels, ids)
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c")); err == nil {
		t.Fatal("attendu un échec")
	}
	if n := h.engine.openCount(); n != 4 {
		t.Fatalf("%d releases essayées, plafond 4", n)
	}
}

func TestAccountErrorStopsDebridReleases(t *testing.T) {
	quota := fmt.Errorf("alldebrid: daily limit: %w", core.ErrDebridQuota)
	rels := []*fakeRelease{
		{ID: "t1", Name: "t1.mkv", Size: 1 << 20, Duration: time.Minute, OpenErr: quota},
		{ID: "t2", Name: "t2.mkv", Size: 1 << 20, Duration: time.Minute},
		{ID: "t3", Name: "t3.mkv", Size: 1 << 20, Duration: time.Minute},
	}
	h := newHarness(t, rels, []string{"t1", "t2", "t3"})
	for i := range h.resolver.candidates {
		h.resolver.candidates[i].Protocol = core.ProtocolTorrent
	}
	_, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c"))
	if !errors.Is(err, core.ErrDebridQuota) {
		t.Fatalf("err = %v, attendu ErrDebridQuota", err)
	}
	if n := h.engine.openCount(); n != 1 {
		t.Fatalf("%d releases ouvertes après une erreur de quota, attendu 1", n)
	}
}

func TestAccountErrorKeepsUsenetReleases(t *testing.T) {
	quota := fmt.Errorf("alldebrid: %w", core.ErrDebridQuota)
	rels := []*fakeRelease{
		{ID: "t1", Name: "t1.mkv", Size: 1 << 20, Duration: time.Minute, OpenErr: quota},
		{ID: "u1", Name: "u1.mkv", Size: 1 << 20, Duration: time.Minute},
	}
	h := newHarness(t, rels, []string{"t1", "u1"})
	h.resolver.candidates[0].Protocol = core.ProtocolTorrent
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c")); err != nil {
		t.Fatalf("la release Usenet doit passer: %v", err)
	}
}

func TestDependsOnDebrid(t *testing.T) {
	if dependsOnDebrid(core.Release{}) || !dependsOnDebrid(core.Release{Protocol: core.ProtocolTorrent}) ||
		!dependsOnDebrid(core.Release{Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid"}) || dependsOnDebrid(core.Release{Protocol: core.ProtocolHTTP}) {
		t.Fatal("classement des releases dépendant du debrid")
	}
	if got := withoutDebrid([]core.Release{{ID: "a"}, {ID: "b", Protocol: core.ProtocolTorrent}}); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("withoutDebrid = %+v", got)
	}
}

func TestSkippedDebridReleasesDoNotSpendAttempts(t *testing.T) {
	quota := fmt.Errorf("alldebrid: %w", core.ErrDebridQuota)
	rels := []*fakeRelease{
		{ID: "t1", Name: "t1.mkv", Size: 1 << 20, Duration: time.Minute, OpenErr: quota},
		{ID: "t2", Name: "t2.mkv", Size: 1 << 20, Duration: time.Minute},
		{ID: "t3", Name: "t3.mkv", Size: 1 << 20, Duration: time.Minute},
		{ID: "t4", Name: "t4.mkv", Size: 1 << 20, Duration: time.Minute},
		{ID: "u1", Name: "u1.mkv", Size: 1 << 20, Duration: time.Minute},
	}
	h := newHarness(t, rels, []string{"t1", "t2", "t3", "t4", "u1"})
	for i := range h.resolver.candidates {
		if h.resolver.candidates[i].ID != "u1" {
			h.resolver.candidates[i].Protocol = core.ProtocolTorrent
		}
	}
	ps, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c"))
	if err != nil || ps.Resolution.Release.ID != "u1" {
		t.Fatalf("la release Usenet doit être atteinte: %v %+v", err, ps)
	}
	if n := h.engine.openCount(); n != 2 {
		t.Fatalf("%d ouvertures, attendu 2 (t1 puis u1)", n)
	}
}

// With verifiedFiles "first", a warm-up or resume on an add-on stream gives
// way to a fresh ranking even without a picked version.

func TestRaceSkippedDebridDoesNotSpendAttempts(t *testing.T) {
	h := newHarness(t, nil, nil)
	cfg := h.cfg
	cfg.Resolver.FailoverAttempts = 2
	h.m.SetConfig(cfg)
	s := &session{title: h.catalog.titles[movieKey], caps: defaultCaps("race-cap")}
	var fetched []string
	h.m.deps.FetchNZB = func(_ context.Context, r core.Release) ([]byte, error) {
		fetched = append(fetched, r.ID)
		if r.Protocol == core.ProtocolTorrent {
			return nil, core.ErrDebridQuota
		}
		return nil, core.ErrSourceUnavailable
	}
	alts := []core.Release{
		{ID: "t1", Protocol: core.ProtocolTorrent},
		{ID: "t2", Protocol: core.ProtocolTorrent},
		{ID: "t3", Protocol: core.ProtocolTorrent},
		{ID: "u1", Protocol: core.ProtocolUsenet},
		{ID: "u2", Protocol: core.ProtocolUsenet},
	}
	_, _, _ = h.m.raceAlternates(t.Context(), s, nil, alts, nil, 1, nil, nil)
	if len(fetched) != 2 || fetched[0] != "t1" || fetched[1] != "u1" {
		t.Fatalf("expected t1 then u1 within two actual attempts, got %v", fetched)
	}
}

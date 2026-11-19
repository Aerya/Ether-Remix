package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRepeatedFailedPrepareIsGrouped(t *testing.T) {
	h := newHarness(t, nil, nil)
	for range 3 {
		if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c")); !errors.Is(err, core.ErrNotAvailable) {
			t.Fatalf("err = %v", err)
		}
	}
	if n := h.resolver.callCount(); n != 1 {
		t.Fatalf("%d résolutions pour 3 demandes rapprochées, attendu 1", n)
	}
	// Another client is another request.
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("other")); err == nil {
		t.Fatal("attendu un échec")
	}
	if n := h.resolver.callCount(); n != 2 {
		t.Fatalf("autre client: %d résolutions", n)
	}
	// After the window the title is resolved again.
	h.clock.Advance(retryFailureWindow + time.Second)
	_, _ = h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c"))
	if n := h.resolver.callCount(); n != 3 {
		t.Fatalf("après la fenêtre: %d résolutions", n)
	}
	// A settings change forgets the memory.
	_, _ = h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c"))
	h.m.SetConfig(h.cfg)
	_, _ = h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c"))
	if n := h.resolver.callCount(); n != 4 {
		t.Fatalf("après changement de réglages: %d résolutions", n)
	}
}

func TestSessionFailoverFailureWindow(t *testing.T) {
	s := &session{}
	g, other := &generation{}, &generation{}
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	boom := fmt.Errorf("x: %w", core.ErrNotAvailable)
	s.noteFailoverFailure(g, boom, now)
	if s.recentFailoverFailure(g, now.Add(time.Second)) == nil {
		t.Fatal("échec récent non rappelé")
	}
	if s.recentFailoverFailure(other, now.Add(time.Second)) != nil {
		t.Fatal("autre génération concernée")
	}
	if s.recentFailoverFailure(g, now.Add(retryFailureWindow)) != nil {
		t.Fatal("fenêtre non expirée")
	}
	s.noteFailoverFailure(g, context.Canceled, now)
	if retryableByWindow(context.Canceled) {
		t.Fatal("annulation mémorisée")
	}
}

func TestRepeatedPlayRequestsShareTheRecentFailure(t *testing.T) {
	ids := []string{"A", "B"}
	h := newHarness(t, failingMKVs(errors.New("broken"), ids...), ids)
	caps := defaultCaps("c1")
	_, err1 := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if !errors.Is(err1, core.ErrNotAvailable) {
		t.Fatalf("first prepare: %v", err1)
	}
	calls, opens := h.resolver.callCount(), h.engine.openCount()
	for range 5 {
		if _, err := h.m.Prepare(context.Background(), owner, movieKey, caps); !errors.Is(err, core.ErrNotAvailable) {
			t.Fatalf("repeat: %v", err)
		}
	}
	if h.resolver.callCount() != calls || h.engine.openCount() != opens {
		t.Fatalf("repeats resolved again: calls %d->%d opens %d->%d", calls, h.resolver.callCount(), opens, h.engine.openCount())
	}
	// Another client is a different request.
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c2")); err == nil || h.resolver.callCount() == calls {
		t.Fatalf("other client was answered from the first one's failure")
	}
	// After the window the title is resolved again.
	h.clock.Advance(retryFailureWindow + time.Second)
	calls = h.resolver.callCount()
	_, _ = h.m.Prepare(context.Background(), owner, movieKey, caps)
	if h.resolver.callCount() == calls {
		t.Fatal("expired failure still answers")
	}
}

func TestRepeatedFailoverSharesTheRecentFailure(t *testing.T) {
	a, b := mkv("A", 100_000, time.Minute), mkv("B", 100_000, time.Minute)
	b.OpenErr = errors.New("broken")
	h := newHarness(t, []*fakeRelease{a, b}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "c1")
	if _, err := h.m.Failover(context.Background(), ps.ID, time.Second); err == nil {
		t.Fatal("failover found a release")
	}
	calls, opens := h.resolver.callCount(), h.engine.openCount()
	for range 4 {
		if _, err := h.m.Failover(context.Background(), ps.ID, time.Second); err == nil {
			t.Fatal("repeated failover found a release")
		}
	}
	if h.resolver.callCount() != calls || h.engine.openCount() != opens {
		t.Fatalf("repeats searched again: calls %d->%d opens %d->%d", calls, h.resolver.callCount(), opens, h.engine.openCount())
	}
}

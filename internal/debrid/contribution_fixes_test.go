package debrid

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type quotaLinkProbe struct {
	*fakeProvider
	entry *entry
	now   func() time.Time
	t     *testing.T
}

func (p *quotaLinkProbe) Link(ctx context.Context, h Handle) (DirectLink, error) {
	// Acquire succeeded, but the actual download unlock has not returned yet.
	if err := p.entry.blocked(p.now()); !errors.Is(err, ErrQuotaExceeded) {
		p.t.Errorf("quota probe released before link unlock: %v", err)
	}
	return p.fakeProvider.Link(ctx, h)
}

func TestQuotaProbeHeldUntilLinkUnlock(t *testing.T) {
	clk := newClock()
	s, provs := quotaService(t, clk, "alldebrid")
	e := s.active()[0]
	e.p = &quotaLinkProbe{provs["alldebrid-0"], e, clk.now, t}
	s.observe(e, ErrQuotaExceeded)
	clk.add(quotaPause + time.Second)
	if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
		t.Fatal(err)
	}
	if err := e.blocked(clk.now()); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledRelinkDoesNotReserveFutureSlot(t *testing.T) {
	r := newReplayRig(t, 512<<10, "realdebrid")
	p, err := DecodePayload(r.pl)
	if err != nil {
		t.Fatal(err)
	}
	f := &remoteFile{e: r.eng, pl: p, size: p.Size}
	due := time.Now().Add(time.Minute)
	f.relinkAt = due
	before := totalProviderCalls(r)
	for range 3 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := f.link(ctx, true, 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	}
	if !f.relinkAt.Equal(due) {
		t.Fatalf("cancelled readers postponed renewal: %v", f.relinkAt)
	}
	if totalProviderCalls(r) != before {
		t.Fatal("cancelled reader called provider")
	}
	// A reader of an older generation reuses the current link without delay.
	if _, _, err := f.link(context.Background(), true, -1); err != nil {
		t.Fatal(err)
	}
}

func TestAllDebridSharedMissRequiresFreshAddition(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			f := &fakeAD{ready: false}
			srv := httptest.NewServer(f.handler(t))
			defer srv.Close()
			p := newAllDebrid(testKey, fastOpts(srv.URL))
			req := acquireReq(true)
			if existing {
				req.TorrentID = "555"
			}
			_, err := p.Acquire(t.Context(), req)
			if errors.Is(err, errSharedCacheMiss) == existing {
				t.Fatalf("existing=%v shared miss=%v: %v", existing, errors.Is(err, errSharedCacheMiss), err)
			}
		})
	}
}

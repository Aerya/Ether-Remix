package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// providerURL is the signed provider link a fake delivery source hands out.
// Its shape mirrors what internal/debrid reports for a TorBox route.
const providerURL = "https://nexus-001.example/dld/token123"

// deliverySource is a fake source that also names the provider's own URL for
// the same bytes, the way a debrid http source does (core.DirectURLSource). It
// embeds the concrete *fakeSource, not core.MediaSource, so every other source
// interface the playback layer asserts on is still promoted.
type deliverySource struct {
	*fakeSource
}

func (s *deliverySource) DirectURL() (core.DirectSourceLink, bool) {
	return core.DirectSourceLink{URL: providerURL, Provider: "TorBox", Edge: "nexus-001.example"}, true
}

// deliveryEngine opens every release with a deliverySource.
type deliveryEngine struct{ *fakeEngine }

func (e *deliveryEngine) Open(ctx context.Context, releaseID string, nzb []byte) (core.MediaSource, error) {
	src, err := e.fakeEngine.Open(ctx, releaseID, nzb)
	if err != nil {
		return nil, err
	}
	fs, ok := src.(*fakeSource)
	if !ok {
		return src, nil
	}
	return &deliverySource{fakeSource: fs}, nil
}

// deliveryHarness wires the playback fakes so that every source they open can
// be redirected to, which is the precondition the gate is asked about.
func deliveryHarness(t *testing.T, rels []*fakeRelease, ids []string) *harness {
	t.Helper()
	return newHarness(t, rels, ids, func(h *harness, _ *Options) {
		h.testEngine = &deliveryEngine{fakeEngine: h.engine}
	})
}

// tsRelease is a fake MPEG-TS file: the fake probe reports container "mpegts"
// for a name ending in .ts, which is what makes a swap onto it spliceable.
func tsRelease(id string, size int64, dur time.Duration) *fakeRelease {
	return &fakeRelease{ID: id, Name: id + ".ts", Size: size, Duration: dur}
}

// relayKeepsServing reports whether the relay would still answer the client's
// original byte coordinates for this session (the 206 path in serve.go).
func relayKeepsServing(sess *session) bool {
	_, ok := sess.directRangeGeneration()
	return ok
}

// A redirected read never passes a byte through Ether, so the answer must also
// carry the two marks a relayed read makes: the direct-play decision and the
// media touch. A session on its adopted generation whose source has a live
// provider link is exactly the session that may be redirected.
func TestDirectDeliveryAnswersAdoptedGeneration(t *testing.T) {
	h := deliveryHarness(t, []*fakeRelease{mkv("A", 100_000, 100*time.Second), mkv("B", 100_000, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "c1")

	// The playback layer records no decision of its own during Prepare: the
	// frontend's SetDecision does. So the direct-play decision below can only
	// have come from DirectDelivery's own mark.
	before, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("prepared session missing")
	}
	if before.Decision == core.DecisionDirectPlay {
		t.Fatalf("test premise: decision was already %q before the redirect", before.Decision)
	}
	if before.RequiresRemux {
		t.Fatal("test premise: a fresh session owes the client a remux")
	}
	// The harness clock is frozen, so advancing it makes the touch observable.
	h.clock.Advance(30 * time.Second)

	link, ok := h.m.DirectDelivery(ps.ID)
	if !ok {
		t.Fatal("DirectDelivery refused a session still on its adopted generation")
	}
	if link.URL != providerURL || link.Provider != "TorBox" || link.Edge != "nexus-001.example" {
		t.Fatalf("link = %+v, want the provider URL, provider and edge", link)
	}

	snap, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("session vanished after the redirect")
	}
	if snap.Decision != core.DecisionDirectPlay {
		t.Fatalf("decision after redirect = %q, want %q: a redirected read is a direct play", snap.Decision, core.DecisionDirectPlay)
	}
	if !snap.LastActive.Equal(h.clock.Now()) {
		t.Fatalf("last active = %v, want the touched %v: a redirected session must not age out", snap.LastActive, h.clock.Now())
	}
	if sess := h.m.lookup(ps.ID); !relayKeepsServing(sess) {
		t.Fatal("the same session cannot even be relayed: the fixture, not the gate, is at fault")
	}
}

// B: a read that names no live session has nothing to redirect.
func TestDirectDeliveryUnknownSession(t *testing.T) {
	h := deliveryHarness(t, []*fakeRelease{mkv("A", 100_000, 100*time.Second)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "c1")

	for _, id := range []string{"", "nope", ps.ID + "-x"} {
		if link, ok := h.m.DirectDelivery(id); ok || link.URL != "" {
			t.Fatalf("DirectDelivery(%q) = %+v, %v; want the zero link and !ok", id, link, ok)
		}
	}
}

// C: a source with no provider URL of its own — Usenet, an addon that needs
// its own request headers — has nothing to redirect to, yet relays fine.
func TestDirectDeliveryNeedsDirectURLSource(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, 100*time.Second), mkv("B", 100_000, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "c1")

	if _, isDirect := ps.Source.(core.DirectURLSource); isDirect {
		t.Fatal("test premise: the unwrapped fixture source answers DirectURL")
	}
	if link, ok := h.m.DirectDelivery(ps.ID); ok || link.URL != "" {
		t.Fatalf("DirectDelivery = %+v, %v; want the zero link and !ok", link, ok)
	}
	if !relayKeepsServing(h.m.lookup(ps.ID)) {
		t.Fatal("the relay must still serve a source it cannot redirect")
	}
}

// D: THE TS CASE. A spliceable MPEG-TS release swap keeps the relay happy: the
// client keeps its original byte coordinates and serve.go's toSource maps them
// into the new file. A 307 cannot — it hands the client's Range to the
// provider's file as that file stands, so offsets in the replacement's
// coordinates would be answered in the original's. The redirect must therefore
// refuse the swap even though the relay accepts it, and even though the new
// file has a perfectly good provider link.
func TestDirectDeliveryRefusedAfterTSSwap(t *testing.T) {
	const size = 100_000
	a := tsRelease("A", size, 100*time.Second)
	b := tsRelease("B", size, 100*time.Second) // a same-size repost: spliceable
	h := deliveryHarness(t, []*fakeRelease{a, b}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "c1")
	sess := h.m.lookup(ps.ID)

	// The baseline: on the adopted .ts generation the redirect is answered, so
	// a later refusal cannot be blamed on the container or on a missing link.
	if _, ok := h.m.DirectDelivery(ps.ID); !ok {
		t.Fatal("DirectDelivery refused the original .ts generation")
	}

	if _, err := h.m.Failover(context.Background(), ps.ID, 12*time.Second); err != nil {
		t.Fatalf("failover onto a same-size .ts release: %v", err)
	}
	swapped, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("session vanished after the failover")
	}
	if swapped.Resolution == nil || swapped.Resolution.Release.ID != "B" {
		t.Fatalf("release after failover = %+v, want B", swapped.Resolution)
	}
	if swapped.RequiresRemux {
		t.Fatal("the spliceable swap owes the client a remux, so the refusal would not prove the generation gate")
	}

	// Both halves of the contract, in one place: the relay is still satisfied
	// by the client's original coordinates...
	if !relayKeepsServing(sess) {
		t.Fatal("the relay stopped serving after a spliceable .ts swap")
	}
	// ...while the redirect is not, even though the new source has a live link.
	if src, direct := swapped.Source.(core.DirectURLSource); !direct {
		t.Fatal("test premise: the swapped-in .ts source has no provider URL")
	} else if _, ok := src.DirectURL(); !ok {
		t.Fatal("test premise: the swapped-in .ts source's provider URL is dead")
	}
	if sess := h.m.lookup(ps.ID); sess.cur == sess.virtualGen {
		t.Fatal("test premise: the session is still on its adopted generation")
	}
	if link, ok := h.m.DirectDelivery(ps.ID); ok {
		t.Fatalf("DirectDelivery answered with %+v after a spliceable .ts swap: a 307 cannot remap the client's offsets", link)
	}
	// The relay still answers the same request the redirect had to refuse.
	if !relayKeepsServing(h.m.lookup(ps.ID)) {
		t.Fatal("the relay must stay happy where the redirect refuses")
	}
}

// E: belt and braces. A Matroska failover is not spliceable at all, so the
// relay's own gate (serve.go directRangeGeneration) fails it too: the client
// gets a 409 and a new decision rather than somebody else's bytes. The
// redirect must refuse for the same reason it refuses the TS swap — the file
// behind the client's byte coordinates has changed — and the original .mkv
// generation must have answered, so the container is not the cause.
func TestDirectDeliveryRefusedAfterMatroskaFailover(t *testing.T) {
	const size = 100_000
	h := deliveryHarness(t, []*fakeRelease{mkv("A", size, 100*time.Second), mkv("B", size, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "c1")

	if _, ok := h.m.DirectDelivery(ps.ID); !ok {
		t.Fatal("DirectDelivery refused the adopted .mkv generation")
	}
	if _, err := h.m.Failover(context.Background(), ps.ID, 12*time.Second); err != nil {
		t.Fatalf("failover onto a same-size .mkv release: %v", err)
	}
	swapped, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("session vanished after the failover")
	}
	if swapped.Resolution == nil || swapped.Resolution.Release.ID != "B" {
		t.Fatalf("release after failover = %+v, want B", swapped.Resolution)
	}
	if link, ok := h.m.DirectDelivery(ps.ID); ok {
		t.Fatalf("DirectDelivery answered with %+v after a Matroska failover", link)
	}
	// A Matroska direct-play handoff cannot continue the client's byte stream,
	// so this failover is also a remux switch: the refusal is doubly earned, and
	// RequiresRemux alone would explain it. That is why the spliceable .ts case
	// above is the one that isolates the generation gate.
	if !swapped.RequiresRemux {
		t.Fatal("premise: a Matroska direct-play failover should owe the client a remux")
	}
	if relayKeepsServing(h.m.lookup(ps.ID)) {
		t.Fatal("premise: Matroska is not spliceable, so the relay must refuse this session too")
	}
}

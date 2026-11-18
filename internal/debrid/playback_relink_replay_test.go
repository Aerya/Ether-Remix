package debrid

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// Tests of debrid.playbackRelink = "replay": a launched file reads to the end
// on its link; a media server failure never asks the provider API for a link
// or for another provider. The default mode ("refresh") keeps the existing
// behaviour, covered by the other tests of this package.

func setPlaybackRelink(r *engineRig, mode string) {
	cfg := r.svc.Config()
	cfg.PlaybackRelink = mode
	r.svc.UpdateConfig(cfg)
}

func newReplayRig(t testing.TB, size int, kinds ...string) *engineRig {
	h := newHarness(t, size, kinds...)
	h.cacheEverywhere(testHash)
	r := &engineRig{harness: h, cache: newMemCache(), inner: &fakeInner{}, rel: torrentRelease("rel-replay")}
	r.eng = NewEngine(h.svc, EngineOptions{Inner: r.inner, Cache: r.cache, HedgeDelay: -1, BlockSize: testBlock, Logger: h.svc.log})
	t.Cleanup(func() { r.eng.Close() })
	var err error
	if r.pl, err = h.svc.Acquire(context.Background(), r.rel); err != nil {
		t.Fatal(err)
	}
	setPlaybackRelink(r, config.PlaybackRelinkReplay)
	return r
}

func totalProviderCalls(r *engineRig) int {
	n := 0
	for _, p := range r.provs {
		n += p.totalCalls()
	}
	return n
}

func (m *mediaServer) forceStatusOnAllTokens(code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tok := range m.valid {
		m.status[tok] = code
	}
}

// A media host that answers 502 over and over never makes Ether ask the
// provider for a link or another provider; the read resumes on the same
// signed link once the host answers again.
func TestReplayMode502DoesNotAskProviderForLink(t *testing.T) {
	r := newReplayRig(t, 512<<10, "realdebrid")
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	tok := payloadToken(t, r.pl)
	before := totalProviderCalls(r)
	linkBefore := r.eng.Stats().LinkRefreshes

	r.media.mu.Lock()
	r.media.status[tok] = http.StatusBadGateway
	r.media.mu.Unlock()
	for i := 0; i < 3; i++ {
		if _, err := src.ReadAt(make([]byte, 10), int64(i)*testBlock); err == nil {
			t.Fatal("read succeeded while the media host answered 502")
		}
	}
	if got := totalProviderCalls(r); got != before {
		t.Fatalf("provider API called %d times during 502s, want 0", got-before)
	}
	r.media.mu.Lock()
	delete(r.media.status, tok)
	r.media.mu.Unlock()
	if got := readAll(t, src); !bytes.Equal(got, r.media.data) {
		t.Fatal("content after the host recovered")
	}
	if got := totalProviderCalls(r); got != before {
		t.Fatalf("provider API called %d times across the outage, want 0", got-before)
	}
	if r.eng.Stats().LinkRefreshes != linkBefore {
		t.Fatal("a link refresh was counted for a media host failure")
	}
}

// The mode is read at each decision: switching it on a running service
// applies to the files already open.
func TestReplayModeAppliesToOpenFiles(t *testing.T) {
	r := newReplayRig(t, 512<<10, "realdebrid")
	setPlaybackRelink(r, "")
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	setPlaybackRelink(r, config.PlaybackRelinkReplay)
	tok := payloadToken(t, r.pl)
	before := totalProviderCalls(r)
	r.media.mu.Lock()
	r.media.status[tok] = http.StatusBadGateway
	r.media.mu.Unlock()
	_, _ = src.ReadAt(make([]byte, 10), 0)
	if got := totalProviderCalls(r); got != before {
		t.Fatalf("provider API called %d times after switching to replay, want 0", got-before)
	}
}

// A 429 on the media host neither asks for a link nor moves the file to
// another provider.
func TestReplayMode429KeepsProvider(t *testing.T) {
	r := newReplayRig(t, 512<<10, "realdebrid", "torbox")
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	p0, _ := src.(*httpSource).f.payload()
	tok := payloadToken(t, r.pl)
	before := totalProviderCalls(r)
	r.media.mu.Lock()
	r.media.status[tok] = http.StatusTooManyRequests
	r.media.mu.Unlock()
	_, _ = src.ReadAt(make([]byte, 10), 512<<10-10) // past any block Open fetched
	p1, _ := src.(*httpSource).f.payload()
	if p1.Provider != p0.Provider || p1.URL != p0.URL {
		t.Fatalf("429 moved the file: %s -> %s", p0.Provider, p1.Provider)
	}
	if got := totalProviderCalls(r); got != before {
		t.Fatalf("provider API called %d times on a 429, want 0", got-before)
	}
}

// A link the host reports gone (410) is still renewed in replay mode.
func TestReplayModeGoneLinkIsRenewed(t *testing.T) {
	r := newReplayRig(t, 256<<10, "realdebrid")
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	tok := payloadToken(t, r.pl)
	r.media.mu.Lock()
	r.media.status[tok] = http.StatusGone
	r.media.mu.Unlock()
	links := r.provs["realdebrid-0"].callCount("link")
	if got := readAll(t, src); !bytes.Equal(got, r.media.data) {
		t.Fatal("content after renewal")
	}
	if r.provs["realdebrid-0"].callCount("link") <= links {
		t.Fatal("expired link was not renewed")
	}
}

// A fresh link that is refused again inside relinkFloor waits for the slot
// and is then renewed: it is not one more dead link, so the read ends well.
func TestReplayModeRelinkFloorWaitsThenRenews(t *testing.T) {
	old := relinkFloor
	relinkFloor = 400 * time.Millisecond
	t.Cleanup(func() { relinkFloor = old })
	r := newReplayRig(t, 512<<10, "realdebrid")
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	prov := r.provs["realdebrid-0"]
	base := prov.callCount("link")
	prov.mu.Lock()
	next := prov.tokens + 1
	prov.mu.Unlock()
	r.media.mu.Lock()
	r.media.status[payloadToken(t, r.pl)] = http.StatusGone                   // the launched link is dead
	r.media.status[fmt.Sprintf("realdebrid-%d", next)] = http.StatusForbidden // its replacement is refused once
	r.media.mu.Unlock()
	start := time.Now()
	buf := make([]byte, 10)
	if _, err := src.ReadAt(buf, 0); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !bytes.Equal(buf, r.media.data[:10]) {
		t.Fatal("wrong content")
	}
	if got := prov.callCount("link") - base; got != 2 {
		t.Fatalf("%d fresh links, want 2", got)
	}
	if d := time.Since(start); d < relinkFloor-50*time.Millisecond {
		t.Fatalf("second renewal after %v, want at least the floor", d)
	}
}

// relinkWait books slots relinkFloor apart.
func TestRelinkWaitSpacesSlots(t *testing.T) {
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	f := &remoteFile{e: &Engine{now: func() time.Time { return now }}}
	if w := f.relinkWait(); w != 0 {
		t.Fatalf("first wait %v, want 0", w)
	}
	now = now.Add(10 * time.Second)
	if w := f.relinkWait(); w != relinkFloor-10*time.Second {
		t.Fatalf("second wait %v", w)
	}
	now = now.Add(relinkFloor * 2)
	if w := f.relinkWait(); w != 0 {
		t.Fatalf("wait after the floor %v, want 0", w)
	}
}

// debrid.playbackRelink ignores case and spaces.
func TestReplayModeValueIsNormalised(t *testing.T) {
	for v, want := range map[string]bool{"replay": true, "Replay": true, " REPLAY ": true, "": false, "refresh": false, "bogus": false} {
		if got := config.PlaybackRelinkIsReplay(v); got != want {
			t.Fatalf("%q: got %v want %v", v, got, want)
		}
	}
}

// The default mode keeps the existing behaviour: a 429 on an urgent read
// asks the provider API for a fresh link (control for
// TestReplayMode429KeepsProvider, which expects no call at all).
func TestRefreshMode429AsksProviderForLink(t *testing.T) {
	r := newReplayRig(t, 512<<10, "realdebrid", "torbox")
	setPlaybackRelink(r, config.PlaybackRelinkRefresh)
	src, err := r.eng.Open(context.Background(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	before := totalProviderCalls(r)
	r.media.mu.Lock()
	r.media.status[payloadToken(t, r.pl)] = http.StatusTooManyRequests
	r.media.mu.Unlock()
	// Read the last block: Open may already have fetched the first one,
	// and a read served from the cache never meets the 429.
	_, _ = src.ReadAt(make([]byte, 10), 512<<10-10)
	if totalProviderCalls(r) == before {
		t.Fatal("refresh mode did not ask the provider API on a 429")
	}
}

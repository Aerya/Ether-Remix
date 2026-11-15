package playback

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestTorrentProbeRenewalIdentity(t *testing.T) {
	ctx := context.Background()
	var probes atomic.Int32
	h := newHarness(t, []*fakeRelease{mkv("rel1", 10000, time.Minute)}, []string{"rel1"}, withNativeProbe(func(ctx context.Context, src core.MediaSource) (*core.MediaInfo, error) {
		probes.Add(1)
		select {
		case <-time.After(1211 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nativeInfo(src), nil
	}, nil))
	p := map[string]any{"kind": "torbox", "provider": "tb", "infoHash": strings.Repeat("a", 40), "path": "The Mummy (1999).mkv", "size": 10000, "url": "https://cdn.example/old", "expires": "2026-11-14T00:00:00Z"}
	encode := func() []byte { b, _ := json.Marshal(p); return append([]byte(debridPayloadPrefix), b...) }
	old := encode()
	info := *nativeInfo(&fakeSource{rel: h.engine.releases["rel1"]})
	if err := h.m.resume.saveProbe(ctx, owner.ID, movieKey, "rel1", old, info, time.Now()); err != nil {
		t.Fatal(err)
	}
	p["url"], p["expires"] = "https://cdn.example/renewed", "2026-11-15T00:00:00Z"
	renewed := encode()
	if nzbHash(old) == nzbHash(renewed) {
		t.Fatal("payload revisions collapsed")
	}
	if got := h.m.persistedProbeHash(ctx, owner.ID, movieKey, "rel1", nzbHash(renewed)); got != nil {
		t.Fatal("raw hash unexpectedly hit")
	}
	// Replay the logged 1.211 s native-probe stage through the real generation
	// pipeline; only the source and native probe are fakes. These timings are
	// offline stage measurements, not a claim about live cold TTFF.
	measure := func(facts *probeCache) time.Duration {
		s := newSession("renewal", identity{userID: owner.ID, key: movieKey}, owner, defaultCaps("renewal"), h.catalog.titles[movieKey], "", h.clock.Now())
		res := &core.Resolution{Release: core.Release{ID: "rel1"}, NZB: renewed}
		started := time.Now()
		g, err := h.m.probeGeneration(ctx, s, res, &fakeSource{rel: h.engine.releases["rel1"]}, func(error) {}, func(int64) core.MediaInfo { return info }, facts)
		elapsed := time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		h.m.dropGeneration(g)
		return elapsed
	}
	before := measure(nil)
	started := time.Now()
	facts := h.m.persistedProbe(ctx, owner.ID, movieKey, "rel1", renewed)
	lookup := time.Since(started)
	if facts == nil || facts.nzbHash != nzbHash(renewed) || !facts.needsAugment {
		t.Fatalf("renewal cache miss: %+v", facts)
	}
	after := measure(facts)
	if probes.Load() != 1 {
		t.Fatalf("native probes %d", probes.Load())
	}
	t.Logf("offline recorded probe stage: before=%s after=%s persisted_lookup=%s native_probes=1→0", before, after, lookup)
	for _, field := range []string{"infoHash", "path", "size", "provider", "kind"} {
		saved := p[field]
		if field == "size" {
			p[field] = 10001
		} else {
			p[field] = "different"
		}
		if got := h.m.persistedProbe(ctx, owner.ID, movieKey, "rel1", encode()); got != nil {
			t.Errorf("changed %s reused probe", field)
		}
		p[field] = saved
	}
	if got := h.m.persistedProbe(ctx, friend.ID, movieKey, "rel1", renewed); got != nil {
		t.Fatal("cross-viewer probe")
	}
	if got := h.m.persistedProbe(ctx, owner.ID, movie2, "rel1", renewed); got != nil {
		t.Fatal("cross-title probe")
	}
	if got := h.m.persistedProbe(ctx, owner.ID, movieKey, "other-release", renewed); got != nil {
		t.Fatal("cross-release probe")
	}
}

package playback

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet"
)

type kindCandidateSpec struct {
	fake     *fakeRelease
	protocol string
}

func kindCandidateInput(titleKey core.RatingKey, specs []kindCandidateSpec, seenExcludes *[][]string) core.KindInput {
	var mu sync.Mutex
	title := &core.Title{Key: titleKey, Type: core.TypeMovie, Title: "Plugin Kind Movie", Year: 2025, Runtime: specs[0].fake.Duration}
	return core.KindInput{
		Title:    title,
		SourceID: "kind-leaf",
		OpenCandidate: func(_ context.Context, exclude []string) (core.MediaSource, core.Release, error) {
			if seenExcludes != nil {
				mu.Lock()
				*seenExcludes = append(*seenExcludes, append([]string(nil), exclude...))
				mu.Unlock()
			}
			for _, sp := range specs {
				if slices.Contains(exclude, sp.fake.ID) {
					continue
				}
				if sp.fake.OpenErr != nil {
					return nil, core.Release{}, sp.fake.OpenErr
				}
				return &fakeSource{rel: sp.fake}, core.Release{
					ID:       sp.fake.ID,
					Name:     sp.fake.Name,
					Protocol: sp.protocol,
					Size:     sp.fake.Size,
				}, nil
			}
			return nil, core.Release{}, core.ErrNotAvailable
		},
	}
}

// TestKindDirectPlayFailoverNonSpliceableSetsRequiresRemuxAnd409 verifies that
// when a plugin kind item direct-playing a non-spliceable MKV Usenet release
// fails mid-stream (e.g. NNTP 481 provider backoff after the stream hold bound),
// playback swaps to the TorBox/debrid candidate from the same kind resolve,
// flags RequiresRemux, answers stale range retries with 409 Conflict ("source
// changed; restart playback"), and clears RequiresRemux once remux/HLS starts.
func TestKindDirectPlayFailoverNonSpliceableSetsRequiresRemuxAnd409(t *testing.T) {
	usenetRel := mkv("usenet-1", 200_000, 100*time.Second)
	torboxRel := mkv("torbox-2", 260_000, 100*time.Second)
	h := newHarness(t, []*fakeRelease{usenetRel, torboxRel}, nil)

	var seenExcludes [][]string
	in := kindCandidateInput(core.PluginKey(101), []kindCandidateSpec{
		{fake: usenetRel, protocol: core.ProtocolUsenet},
		{fake: torboxRel, protocol: core.ProtocolTorrent},
	}, &seenExcludes)

	ps, err := h.m.PrepareKind(context.Background(), owner, in, defaultCaps("vela-kind-dp"))
	if err != nil {
		t.Fatalf("PrepareKind: %v", err)
	}
	if ps.Resolution == nil || ps.Resolution.Release.ID != "usenet-1" {
		t.Fatalf("initial release = %+v, want usenet-1", ps.Resolution)
	}

	<-h.m.lookup(ps.ID).current().indexReady
	usenetRel.setFail(120_000, 130_000, usenet.ErrProviderBackoff)

	srv := directPlayServer(t, h.m, ps.ID)
	resp, body, err := get(t, srv.URL, nil)
	if resp.StatusCode != http.StatusOK || err == nil {
		t.Fatalf("want truncated 200 on non-spliceable MKV failover, got status=%d err=%v len=%d", resp.StatusCode, err, len(body))
	}
	if int64(len(body)) != usenetRel.FailFrom || !bytes.Equal(body, expected("usenet-1", 0, usenetRel.FailFrom)) {
		t.Fatalf("prefix before failover len=%d, want %d intact bytes from usenet-1", len(body), usenetRel.FailFrom)
	}

	st, ok := h.m.Session(ps.ID)
	if !ok || st.Resolution == nil || st.Resolution.Release.ID != "torbox-2" || st.Failovers != 1 {
		t.Fatalf("after failover session = %+v, want torbox-2 and Failovers=1", st)
	}
	if !st.RequiresRemux || !h.m.RequiresRemux(ps.ID) {
		t.Fatalf("RequiresRemux = (%v, %v), want (true, true)", st.RequiresRemux, h.m.RequiresRemux(ps.ID))
	}
	if len(seenExcludes) < 2 || !slices.Contains(seenExcludes[len(seenExcludes)-1], "usenet-1") {
		t.Fatalf("OpenCandidate excludes = %v, want usenet-1 excluded on failover", seenExcludes)
	}

	// Stale direct-play byte range on the old validator/representation gets 409 Conflict
	// with "source changed; restart playback" so the client re-plans onto remux/HLS.
	retryResp, retryBody, err := get(t, srv.URL, map[string]string{"Range": "bytes=120000-120999"})
	if err != nil || retryResp.StatusCode != http.StatusConflict || !strings.Contains(string(retryBody), "source changed; restart playback") {
		t.Fatalf("range retry after failover = status %d body %q err %v, want 409 source changed", retryResp.StatusCode, retryBody, err)
	}

	// Re-planning onto remux clears RequiresRemux while keeping the old direct URL fenced.
	h.m.SetDecision(ps.ID, core.Decision{Kind: core.DecisionRemux})
	if h.m.RequiresRemux(ps.ID) {
		t.Fatal("RequiresRemux remained true after SetDecision(DecisionRemux)")
	}
	retryAfterRemux, _, err := get(t, srv.URL, map[string]string{"Range": "bytes=0-999"})
	if err != nil || retryAfterRemux.StatusCode != http.StatusConflict {
		t.Fatalf("old direct URL after remux decision = %d %v, want 409", retryAfterRemux.StatusCode, err)
	}
}

func TestKindFailoverSeamlessTSSpliceHLSInputSwapAndPinnedNoReplace(t *testing.T) {
	t.Run("seamless splice on MPEG-TS kind direct play", func(t *testing.T) {
		const sizeA, sizeB = 188 * 1000, 188 * 2000
		a := &fakeRelease{
			ID:       "usenet-ts",
			Name:     "stream-a.ts",
			Size:     sizeA,
			Duration: 50 * time.Second,
		}
		b := &fakeRelease{
			ID:       "torbox-ts",
			Name:     "stream-b.ts",
			Size:     sizeB,
			Duration: 50 * time.Second,
		}
		h := newHarness(t, []*fakeRelease{a, b}, nil)
		in := kindCandidateInput(core.PluginKey(102), []kindCandidateSpec{
			{fake: a, protocol: core.ProtocolUsenet},
			{fake: b, protocol: core.ProtocolTorrent},
		}, nil)

		ps, err := h.m.PrepareKind(context.Background(), owner, in, defaultCaps("kind-ts"))
		if err != nil {
			t.Fatalf("PrepareKind(.ts): %v", err)
		}
		<-h.m.lookup(ps.ID).current().indexReady
		failAt := int64(188*500 + 17)
		a.setFail(failAt, 188*600, usenet.ErrProviderBackoff)

		srv := directPlayServer(t, h.m, ps.ID)
		resp, body, err := get(t, srv.URL, nil)
		if err != nil || resp.StatusCode != http.StatusOK || len(body) != sizeA {
			t.Fatalf("TS splice = status %d err %v len %d, want complete 200 of %d bytes", resp.StatusCode, err, len(body), sizeA)
		}
		if !bytes.Equal(body[:failAt], expected("usenet-ts", 0, failAt)) {
			t.Fatal("TS prefix mismatch")
		}
		newOff := alignPacket(failAt*sizeB/sizeA, tsPacket)
		if !bytes.Equal(body[failAt:], expected("torbox-ts", newOff, sizeA-failAt)) {
			t.Fatal("TS suffix not spliced from torbox-ts at aligned offset")
		}
		if h.m.RequiresRemux(ps.ID) {
			t.Fatal("TS seamless splice unexpectedly flagged RequiresRemux")
		}
	})

	t.Run("HLS input swap on kind transcode/remux session", func(t *testing.T) {
		a := mkv("usenet-hls", 200_000, 100*time.Second)
		b := mkv("torbox-hls", 300_000, 100*time.Second)
		h := newHarness(t, []*fakeRelease{a, b}, nil)
		in := kindCandidateInput(core.PluginKey(103), []kindCandidateSpec{
			{fake: a, protocol: core.ProtocolUsenet},
			{fake: b, protocol: core.ProtocolTorrent},
		}, nil)

		ps, err := h.m.PrepareKind(context.Background(), owner, in, defaultCaps("kind-hls"))
		if err != nil {
			t.Fatalf("PrepareKind(HLS): %v", err)
		}
		sess := h.m.lookup(ps.ID)
		<-sess.current().indexReady
		hls := h.tr.addHLS(ps.ID)
		h.m.SetDecision(ps.ID, core.Decision{Kind: core.DecisionTranscode})

		a.setFail(100_000, 120_000, usenet.ErrProviderBackoff)
		resp, _, err := get(t, h.m.InputURL(ps.ID), map[string]string{"Range": "bytes=80000-"})
		if resp.StatusCode != http.StatusPartialContent || err == nil {
			t.Fatalf("want truncated 206 on loopback read failure, got %d %v", resp.StatusCode, err)
		}

		select {
		case sw := <-hls.swapC:
			if sw.Info.Size != b.Size {
				t.Fatalf("SwapInput Info.Size = %d, want %d", sw.Info.Size, b.Size)
			}
		case <-t.Context().Done():
			t.Fatal("timed out waiting for HLS SwapInput on kind failover")
		}
		if err := lock(t.Context(), sess.failSem); err != nil {
			t.Fatal(err)
		}
		unlock(sess.failSem)
		st, _ := h.m.Session(ps.ID)
		if st.Resolution.Release.ID != "torbox-hls" || st.Failovers != 1 {
			t.Fatalf("HLS session after swap = %+v, want torbox-hls", st)
		}
	})

	t.Run("viewer-pinned kind source revision is not replaced", func(t *testing.T) {
		a := mkv("usenet-pinned", 200_000, 100*time.Second)
		b := mkv("torbox-unused", 200_000, 100*time.Second)
		h := newHarness(t, []*fakeRelease{a, b}, nil)
		var excludes [][]string
		in := kindCandidateInput(core.PluginKey(104), []kindCandidateSpec{
			{fake: a, protocol: core.ProtocolUsenet},
			{fake: b, protocol: core.ProtocolTorrent},
		}, &excludes)

		ps, err := h.m.PrepareKind(context.Background(), owner, in, defaultCaps("kind-pinned"))
		if err != nil {
			t.Fatalf("PrepareKind(pinned): %v", err)
		}
		<-h.m.lookup(ps.ID).current().indexReady
		rev := core.SourceRevision(ps)
		if err := h.m.PinSourceRevision(context.Background(), ps.ID, rev); err != nil {
			t.Fatalf("PinSourceRevision: %v", err)
		}

		a.setFail(50_000, 60_000, core.ErrArticleMissing)
		srv := directPlayServer(t, h.m, ps.ID)
		resp, _, _ := get(t, srv.URL, map[string]string{"Range": "bytes=50000-51000"})
		if resp != nil && resp.StatusCode == http.StatusPartialContent {
			t.Fatal("expected pinned broken range to fail")
		}
		st, _ := h.m.Session(ps.ID)
		if st.Resolution.Release.ID != "usenet-pinned" || st.Failovers != 0 {
			t.Fatalf("pinned session replaced release: %+v", st)
		}
		if len(excludes) != 1 {
			t.Fatalf("OpenCandidate called %d times on pinned session, want 1 (initial open only)", len(excludes))
		}
	})
}

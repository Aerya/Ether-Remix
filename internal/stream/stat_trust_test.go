package stream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

// End-to-end stream health check over real NNTP when a provider answers 223 to
// STAT and then fails BODY (451 takedown or mid-article corruption).
func TestEngineHealthDistrustsLyingStatProvider(t *testing.T) {
	srv := nntptest.Start(t, nntptest.Options{})

	// Release 1 establishes that srv lies on STAT (STAT 223, BODY 451 on mid segments).
	rel1, err := srv.Post(map[string][]byte{"Gone.2026.mkv": mkvBytes(1, 120_000)}, 10_000, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Release 2 is a 2-segment file whose second segment fails BODY behind STAT 223.
	// Note: when STAT is trusted, bodyProbeSlots(2) is empty (only head/plan + STAT),
	// so catching segment 2 requires untrusted STAT verification.
	rel2, err := srv.Post(map[string][]byte{"TwoPart.2026.mkv": mkvBytes(2, 40_000)}, 25_000, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Release 3 has 200 KiB segments (> 128 KiB header drain threshold) where the
	// sampled full-body slot (index 6/3 = 2, i.e. segment 3) has a valid =ybegin/=ypart
	// header but corrupt payload/CRC at the end of the body.
	rel3Data := mkvBytes(3, 1_200_000)
	rel3, err := srv.Post(map[string][]byte{"TailCorrupt.2026.mkv": rel3Data}, 200_000, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	corruptSeg := rel3.Files[0].Segments[2]
	srv.AddArticle(corruptSeg.MessageID, []byte(
		"=ybegin part=3 total=6 line=128 size=1200000 name=TailCorrupt.2026.mkv\r\n"+
			"=ypart begin=400001 end=600000\r\n"+
			"corrupted-mid-body-payload\r\n"+
			"=yend size=200000 part=3 pcrc32=deadbeef\r\n",
	))

	deadIDs := map[string]bool{
		strings.Trim(rel2.Files[0].Segments[1].MessageID, "<>"): true,
	}
	for _, seg := range rel1.Files[0].Segments[1:] {
		deadIDs[strings.Trim(seg.MessageID, "<>")] = true
	}
	srv.SetFault(func(cmd, arg string) nntptest.Fault {
		if cmd == "BODY" && deadIDs[strings.Trim(arg, "<>")] {
			return nntptest.Fault{Code: 451, Text: "taken down"}
		}
		return nntptest.Fault{}
	})

	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := usenet.New([]config.UsenetProvider{srv.Provider("lying", 4)}, usenet.Options{
		Logger:  quietLog,
		MinIdle: -1,
		Retries: 0,
	})
	t.Cleanup(func() { _ = fetcher.Close() })

	e := newTestEngine(t, fetcher, nil, Options{HealthSamples: 6})
	ctx := context.Background()

	// Check + failed reads on rel1 teach the pool that STAT 223 fails BODY.
	if ok, err := e.Check(ctx, rel1.NZB); ok || !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("rel1 Check = %v, %v; want missing", ok, err)
	}
	for _, seg := range rel1.Files[0].Segments[1:4] {
		_, _ = fetcher.Stat(ctx, seg.MessageID)
		_ = fetcher.CheckBody(ctx, seg.MessageID, nil, core.PriorityLive)
	}
	if !fetcher.StatUntrusted() {
		t.Fatal("expected fetcher STAT to be untrusted after repeated STAT-223 -> BODY-451")
	}

	// 2-segment release with segment 2 behind STAT-223 + BODY-451 must now fail Check.
	if ok, err := e.Check(ctx, rel2.NZB); ok || !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("rel2 Check with untrusted STAT = %v, %v; want missing", ok, err)
	}

	// Large-segment release with valid yEnc header but corrupt body/CRC on sampled segment must fail Check.
	if ok, err := e.Check(ctx, rel3.NZB); ok || err == nil {
		t.Fatalf("rel3 Check with mid-article CRC corruption = %v, %v; want unhealthy", ok, err)
	}

	// A genuinely intact release on the same provider still passes Check.
	relOK, err := srv.Post(map[string][]byte{"Intact.2026.mkv": mkvBytes(4, 180_000)}, 30_000, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.Check(ctx, relOK.NZB); !ok || err != nil {
		t.Fatalf("intact release Check = %v, %v; want healthy", ok, err)
	}
}

type lyingStatPlainFetcher struct {
	*fakeFetcher
	lieMissing map[string]bool
}

func (f *lyingStatPlainFetcher) Stat(ctx context.Context, id string) (bool, error) {
	if f.lieMissing[id] {
		return true, nil
	}
	return f.fakeFetcher.Stat(ctx, id)
}

func TestPlainFetcherFallbackStatTrust(t *testing.T) {
	base := newFakeFetcher()
	doc1 := post(t, base, 20_000, postFile{name: "trip.mkv", data: mkvBytes(1, 120_000)})
	doc2 := post(t, base, 25_000, postFile{name: "twopart.mkv", data: mkvBytes(2, 40_000)})

	lie := map[string]bool{
		postID("trip.mkv", 2):    true,
		postID("trip.mkv", 3):    true,
		postID("trip.mkv", 4):    true,
		postID("twopart.mkv", 2): true,
	}
	for id := range lie {
		base.setMissing(id)
	}
	lf := &lyingStatPlainFetcher{fakeFetcher: base, lieMissing: lie}
	e := newTestEngine(t, lf, nil, Options{HealthSamples: 6, Retries: 0})
	ctx := context.Background()

	// Correlate 3 STAT-present -> BodyPart-missing outcomes on the plain ArticleFetcher.
	for _, part := range []int{2, 3, 4} {
		id := postID("trip.mkv", part)
		if ok, err := lf.Stat(ctx, id); err != nil || !ok {
			t.Fatalf("Stat(%s) = %v, %v", id, ok, err)
		}
		e.noteStatPresent(id)
		_, _, err := lf.BodyPart(ctx, id, nil, core.PriorityLive)
		e.noteBodyOutcome(id, err, ctx)
	}
	_ = doc1
	if !e.statUntrusted() {
		t.Fatal("expected engine fallback STAT tracker to mark plain fetcher untrusted")
	}

	// 2-part release where part 2 lies on Stat(true) and fails BodyPart must now fail Check.
	if ok, err := e.Check(ctx, doc2); ok || !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("Check(doc2) = %v, %v; want missing", ok, err)
	}
}

package livetv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type anchorIndex struct {
	transcode.SegmentIndex
	segments []transcode.Segment
}

func (i *anchorIndex) Latest() (transcode.Segment, bool) { return i.segments[len(i.segments)-1], true }
func (i *anchorIndex) Window(from, until time.Time) []transcode.Segment {
	var out []transcode.Segment
	for _, s := range i.segments {
		if s.Start.Before(until) && s.Start.Add(s.Duration).After(from) {
			out = append(out, s)
		}
	}
	return out
}

// The wire anchor must describe actual segments across slides, short opening
// GOPs, fractional EXTINF durations and PDT resynchronization at a splice.
func TestLiveAnchorMatchesPublishedWindow(t *testing.T) {
	ctx := context.Background()
	origin := time.Unix(1700000000, 0)
	durations := []time.Duration{500 * time.Millisecond, 500 * time.Millisecond, time.Second, 1999999999, 333333333, 2 * time.Second}
	check := func(plan, playlist frontend.LiveWindow, segments []renderedSeg, starts map[int64]time.Duration) {
		t.Helper()
		if plan != playlist || len(segments) == 0 {
			t.Fatalf("plan=%+v playlist=%+v segments=%v", plan, playlist, segments)
		}
		if plan.Anchor.MediaSequence != segments[0].seq {
			t.Fatal("anchor sequence does not match playlist")
		}
		w := httptest.NewRecorder()
		writeLiveAnchor(w, playlist)
		if w.Header().Get("X-Ether-Live-Media-Sequence") != strconv.FormatInt(segments[0].seq, 10) {
			t.Fatal("header sequence does not match playlist")
		}
		at, err := strconv.ParseFloat(w.Header().Get("X-Ether-Live-Window-Start"), 64)
		if err != nil {
			t.Fatal(err)
		}
		for _, seg := range segments {
			want := starts[seg.seq]
			if d := time.Duration(at*float64(time.Second)) - want; d < -time.Nanosecond || d > time.Nanosecond {
				t.Fatalf("sequence %d: start %.9f want %s", seg.seq, at, want)
			}
			at += seg.dur
		}
		if d := at - plan.LiveEdgeSeconds; d < -1e-8 || d > 1e-8 {
			t.Fatalf("edge=%f want %f", at, plan.LiveEdgeSeconds)
		}
	}
	t.Run("owned", func(t *testing.T) {
		tl := newOwnedTimeline("anchor", slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now, 0, nil)
		tl.target = 2 * time.Second
		tl.windowSpan = 6 * time.Second
		tl.lastPDT = origin
		service := &Service{sessions: map[string]*session{"x": {owned: &ownedLease{tl: tl}}}}
		starts := map[int64]time.Duration{}
		var elapsed time.Duration
		for n := range 50 {
			dur := durations[n%len(durations)]
			starts[int64(n)] = elapsed
			// A discontinuity may move PDT by hours without moving session seconds.
			tl.appendLocked(&ownedSegment{dur: dur, disc: n == 20, ext: "ts"}, origin.Add(elapsed+dur+time.Hour))
			elapsed += dur
			plan, err := service.LiveWindow(ctx, "x")
			if err != nil {
				t.Fatal(err)
			}
			body, snapshot := tl.renderSnapshot(func(n int64, _ string) string { return fmt.Sprintf("%d.ts", n) }, func(s string) string { return s }, true)
			_, segments := parseOwned(t, body)
			check(plan, snapshot, segments, starts)
		}
	})
	t.Run("broadcast", func(t *testing.T) {
		index := &anchorIndex{}
		done := make(chan struct{})
		close(done)
		rendition := &broadcastRendition{id: "base", encoder: &broadcastFakeEncoder{index: index}, origin: origin, originDone: done}
		b := &broadcast{renditions: map[string]*broadcastRendition{"base": rendition}}
		lease := &broadcastLease{broadcast: b, rendition: "base"}
		service := &Service{sessions: map[string]*session{"x": {broadcast: lease, caps: core.ClientCaps{Protocol: "vela"}}}}
		starts := map[int64]time.Duration{}
		var elapsed time.Duration
		for n := range 50 {
			dur := durations[n%len(durations)]
			starts[int64(n)] = elapsed
			index.segments = append(index.segments, transcode.Segment{Seq: int64(n), Start: origin.Add(elapsed), Duration: dur})
			elapsed += dur
			plan, err := service.LiveWindow(ctx, "x")
			if err != nil {
				t.Fatal(err)
			}
			window := index.Window(origin.Add(elapsed-service.latencyPolicy().PlaylistWindow()), origin.Add(elapsed))
			snapshot, err := broadcastLiveWindow(ctx, lease, index, window)
			if err != nil {
				t.Fatal(err)
			}
			var segments []renderedSeg
			for _, seg := range window {
				segments = append(segments, renderedSeg{seq: seg.Seq, dur: seg.Duration.Seconds()})
			}
			check(plan, snapshot, segments, starts)
		}
	})
}

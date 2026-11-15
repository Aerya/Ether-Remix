package livetv

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Model a cold title master and a programme resolving in 1.5s, independently
// of encoder throughput. The same continuous encoder must deliver real media
// lead, preserve every PCM tick, and splice without a restart.
type tuneStartupPlanner struct {
	titleStartupPlanner
	segment time.Duration
}

func (p tuneStartupPlanner) segmentDuration() time.Duration { return p.segment }
func (p tuneStartupPlanner) startupLead() time.Duration     { return 3 * p.segment }
func (p tuneStartupPlanner) introPlan(time.Time) (broadcastPlan, time.Duration) {
	return p.card, p.segment
}

type tuneStartupRunner struct {
	broadcastFakeRunner
	encoder *tuneStartupEncoder
	segment time.Duration
}

func (r *tuneStartupRunner) StartEncoder(context.Context, transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	r.encoder = &tuneStartupEncoder{segment: r.segment}
	r.encoder.index = tuneStartupIndex{e: r.encoder}
	return r.encoder, nil
}

type tuneStartupEncoder struct {
	broadcastFakeEncoder
	segment time.Duration
}

func (e *tuneStartupEncoder) Write(ctx context.Context, f transcode.RawFrame) error {
	select {
	case <-time.After(4 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	return e.broadcastFakeEncoder.Write(ctx, f)
}

type tuneStartupIndex struct {
	fakeBroadcastIndex
	e *tuneStartupEncoder
}

func (s tuneStartupIndex) Since(seq int64, limit int) []transcode.Segment {
	frames := s.e.snapshot()
	g := int(s.e.segment * 30 / time.Second)
	starts := []int{0, g / 4, g / 2, g}
	var result []transcode.Segment
	for n := 0; ; n++ {
		start, end := 0, 0
		if n < 3 {
			start, end = starts[n], starts[n+1]
		} else {
			start = g + (n-3)*g
			end = start + g
		}
		if end > len(frames) {
			break
		}
		if int64(n) > seq {
			result = append(result, transcode.Segment{Seq: int64(n), Start: frames[0].OutputTime.Add(time.Duration(start) * time.Second / 30), Duration: time.Duration(end-start) * time.Second / 30})
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result
}
func (s tuneStartupIndex) Latest() (transcode.Segment, bool) {
	a := s.Since(-1, 0)
	if len(a) == 0 {
		return transcode.Segment{}, false
	}
	return a[len(a)-1], true
}
func (s tuneStartupIndex) Wait(ctx context.Context, seq int64) (transcode.Segment, error) {
	for {
		if a := s.Since(seq, 1); len(a) > 0 {
			return a[0], nil
		}
		select {
		case <-ctx.Done():
			return transcode.Segment{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
func TestLiveTuneStartup(t *testing.T) {
	for _, optimized := range []bool{false, true} {
		name := "exclusive-title-4s"
		segment := 4 * time.Second
		if optimized {
			name = "interruptible-title-2s"
			segment = 2 * time.Second
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				origin := time.Now()
				f := smallBroadcastFormat()
				programme := broadcastPlan{id: "programme", programme: true, start: origin.Add(-time.Minute), end: origin.Add(time.Hour), open: coldHoldOpen(f, 90, 1500*time.Millisecond)}
				card := broadcastPlan{id: "title", element: "tonight", fill: optimized, open: func(context.Context) (transcode.Decoder, func(), error) {
					return &coldTitleDecoder{delayedMomentDecoder: &delayedMomentDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, value: 30, closed: make(chan struct{})}, ready: origin.Add(100 * time.Millisecond)}, masterAt: origin.Add(20 * time.Second)}, nil, nil
				}}
				p := tuneStartupPlanner{titleStartupPlanner: titleStartupPlanner{coldHoldPlanner{elementBroadcastPlanner: elementBroadcastPlanner{programme, broadcastPlan{id: "after", start: programme.end}}, card: card}}, segment: segment}
				runner := &tuneStartupRunner{segment: segment}
				b, err := newBroadcast(t.Context(), runner, p, f)
				if err != nil {
					t.Fatal(err)
				}
				defer b.close()
				if _, err = b.addRendition(t.Context(), "main", core.Decision{}); err != nil {
					t.Fatal(err)
				}
				b.start()
				var tuneReady time.Duration
				ready := make(chan time.Duration, 1)
				if optimized {
					go func() {
						awaitBroadcastCushion(t.Context(), &broadcastLease{broadcast: b, rendition: "main"}, false)
						ready <- time.Since(origin)
					}()
				}
				var first, third, cushion, picture time.Duration
				// Observe beyond the producer's bounded startup deadline. Its
				// startupUntil belongs to the frame goroutine; inspecting it
				// here races with that goroutine clearing it.
				for time.Since(origin) < 22*time.Second {
					time.Sleep(10 * time.Millisecond)
					synctest.Wait()
					frames := runner.encoder.snapshot()
					segments := runner.encoder.index.Since(-1, 0)
					elapsed := time.Since(origin)
					if len(segments) > 0 && first == 0 {
						first = elapsed
					}
					if len(segments) >= 3 && third == 0 {
						third = elapsed
					}
					if len(segments) > 0 && segments[len(segments)-1].Start.Add(segments[len(segments)-1].Duration).Sub(origin) >= 3*segment && cushion == 0 {
						cushion = elapsed
					}
					for _, frame := range frames {
						if frame.Video[0] == 90 && picture == 0 {
							picture = elapsed
						}
					}
				}
				if optimized {
					tuneReady = <-ready
				}
				b.close()
				frames := runner.encoder.snapshot()
				for n, frame := range frames {
					if frame.OutputTime != origin.Add(time.Duration(n)*time.Second/30) || len(frame.PCM) != 6400 {
						t.Fatalf("discontinuous tick %d", n)
					}
				}
				t.Logf("first media=%s three segments=%s 3x target buffer=%s programme picture=%s final lead=%s tune ready=%s", first, third, cushion, picture, b.lead, tuneReady)
				if optimized && (first == 0 || first >= segment || third == 0 || third > 2*time.Second || cushion == 0 || cushion > 4*time.Second || picture == 0 || picture > 2*time.Second || b.lead < 5*time.Second || tuneReady < cushion-10*time.Millisecond || tuneReady > 3*time.Second) {
					t.Fatal("startup failed to build playable lead promptly")
				}
			})
		})
	}
}

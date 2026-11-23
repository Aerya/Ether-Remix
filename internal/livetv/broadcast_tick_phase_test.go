package livetv

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// tickCheckEncoder enforces the real continuous encoder's per-tick contract
// (transcode continuousEncoder.WriteShared): its own tick k must carry exactly
// tickSamples(k) of PCM, counted from the first frame it receives.
type tickCheckEncoder struct {
	format transcode.RawFormat
	mu     sync.Mutex
	tick   int64
	err    error
}

func (e *tickCheckEncoder) Write(_ context.Context, f transcode.RawFrame) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if want := tickSamples(e.format, e.tick) * e.format.Channels * 2; len(f.PCM) != want || len(f.Video) != e.format.Width*e.format.Height*3/2 {
		e.err = fmt.Errorf("tick %d: pcm %d bytes, want %d: %w", e.tick, len(f.PCM), want, errTickSizeMismatch)
		return e.err
	}
	e.tick++
	return nil
}
func (e *tickCheckEncoder) Segments() transcode.SegmentIndex { return fakeBroadcastIndex{} }
func (e *tickCheckEncoder) Close() error                     { return nil }
func (e *tickCheckEncoder) state() (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tick, e.err
}

var errTickSizeMismatch = errors.New("raw tick size mismatch")

type tickCheckRunner struct {
	broadcastFakeRunner
	mu       sync.Mutex
	encoders []*tickCheckEncoder
}

func (r *tickCheckRunner) StartEncoder(_ context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := &tickCheckEncoder{format: req.Source}
	r.encoders = append(r.encoders, e)
	return e, nil
}

func TestTickCycle(t *testing.T) {
	for _, c := range []struct {
		num, den int
		want     int64
	}{{24, 1, 1}, {25, 1, 1}, {30, 1, 1}, {50, 1, 1}, {60, 1, 1}, {24000, 1001, 1}, {30000, 1001, 5}, {60000, 1001, 5}} {
		f := transcode.RawFormat{FPSNum: c.num, FPSDen: c.den, SampleRate: 48000, Channels: 2}
		got := tickCycle(f)
		if got != c.want {
			t.Errorf("%d/%d: cycle %d, want %d", c.num, c.den, got, c.want)
		}
		for n := int64(0); n < 200; n++ {
			if tickSamples(f, n+got) != tickSamples(f, n) {
				t.Fatalf("%d/%d: tickSamples(%d) != tickSamples(%d)", c.num, c.den, n+got, n)
			}
		}
	}
}

// A viewer can join an output that is already running (a second rendition,
// a retained session rejoining, or admission handing the output a primed first
// picture before the encoder starts). Its encoder must never receive a tick
// whose PCM size is out of phase with the encoder's own tick zero.
func TestBroadcastLateRenditionKeepsPCMTickPhase(t *testing.T) {
	for _, rate := range [][2]int{{30, 1}, {50, 1}, {30000, 1001}, {60000, 1001}} {
		var joined []int64
		for wait := 1; wait <= 8; wait++ {
			t.Run(fmt.Sprintf("%d_%d/wait%d", rate[0], rate[1], wait), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					format := smallBroadcastFormat()
					format.FPSNum, format.FPSDen = rate[0], rate[1]
					runner := &tickCheckRunner{}
					plan := broadcastPlan{id: "programme", open: func(context.Context) (transcode.Decoder, func(), error) {
						return &broadcastFakeDecoder{origin: time.Now(), format: format, closed: make(chan struct{})}, nil, nil
					}}
					b, err := newBroadcast(context.Background(), runner, broadcastFakePlanner{plan: plan, screenPlan: plan}, format)
					if err != nil {
						t.Fatal(err)
					}
					defer b.close()
					// Drive startup ticks synchronously so decoder startup cannot
					// turn a late-join case into a tick-zero join. The production
					// run loop takes over after the rendition is attached.
					deadline := time.Now().Add(10 * time.Second)
					for b.tick < int64(wait*7) {
						if !time.Now().Before(deadline) {
							t.Fatalf("output never reached a late join tick: %d", b.tick)
						}
						if b.step(b.origin.Add(time.Duration(b.tick) * frameDurationOf(format))) {
							b.tick++
						}
						time.Sleep(frameDurationOf(format))
						synctest.Wait()
					}
					at := b.tick
					joined = append(joined, at)
					if _, err := b.addRendition(context.Background(), "late", core.Decision{MaxHeight: 720}); err != nil {
						t.Fatal(err)
					}
					b.start()
					time.Sleep(3 * time.Second)
					synctest.Wait()
					b.mu.Lock()
					_, attached := b.renditions["late"]
					b.mu.Unlock()
					b.close()
					if len(runner.encoders) != 1 {
						t.Fatalf("encoders=%d, want 1", len(runner.encoders))
					}
					ticks, werr := runner.encoders[0].state()
					if werr != nil {
						t.Fatalf("joined at output tick %d: %v", at, werr)
					}
					if !attached {
						t.Fatalf("joined at output tick %d: rendition dropped", at)
					}
					if ticks < 60 {
						t.Fatalf("joined at output tick %d: received %d ticks in 3s", at, ticks)
					}
				})
			})
		}
		// The case only proves something if some join was out of phase.
		cycle := tickCycle(transcode.RawFormat{FPSNum: rate[0], FPSDen: rate[1], SampleRate: 48000})
		offPhase := false
		for _, at := range joined {
			offPhase = offPhase || at%cycle != 0
		}
		if cycle > 1 && len(joined) > 0 && !offPhase {
			t.Fatalf("%d/%d: every join landed in phase (ticks %v); the test did not exercise the bug", rate[0], rate[1], joined)
		}
	}
}

func frameDurationOf(f transcode.RawFormat) time.Duration {
	return time.Second * time.Duration(f.FPSDen) / time.Duration(f.FPSNum)
}

package livetv

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

type changingLiveDecoder struct {
	picture bool
}

func (d *changingLiveDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if d.picture {
		return transcode.RawFrame{Video: []byte{1}, SourcePTS: time.Second, SourcePTSValid: true}, nil
	}
	return transcode.RawFrame{}, transcode.ErrInputChanged
}

func (d *changingLiveDecoder) Close() error { return nil }

// Recovery stays inside the same output-facing decoder until distinct failed
// decoders prove the input cannot air. Neither duplicate observations nor old
// changes may turn an otherwise recoverable input into a storm.
func TestLiveChangeStormFailsOver(t *testing.T) {
	t0 := time.Unix(1000, 0)
	now := t0
	l := newSteadyLadder()
	p := &providerBroadcastPlanner{ladder: l, s: &Service{o: Options{Now: func() time.Time { return now }}}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first := &changingLiveDecoder{}
	d := &providerBroadcastDecoder{Decoder: first, planner: p, sourceID: "primary", life: ctx, frame: time.Second / 30}
	defer d.Close()
	opens := 1
	d.reopen = func() (transcode.Decoder, error) {
		opens++
		now = t0.Add(time.Duration(opens-1) * 7 * time.Second)
		return &changingLiveDecoder{}, nil
	}
	sources := []string{"primary", "backup"}
	l.opened("primary", t0)
	_, err := d.Next(ctx)
	if !errors.Is(err, errSteadyChangeStorm) || opens != steadyChangeLimit {
		t.Fatalf("opens=%d error=%v, want three distinct failed decoders then change storm", opens, err)
	}
	if d.Decoder != first || d.aired.Load() != 0 {
		t.Fatal("failed splices replaced the current decoder or reported playback")
	}
	at := l.failed("primary", err, false, false, sources, now)
	id, edge, ok, _ := l.choose(sources, at)
	if !at.Equal(now) || !ok || id != "backup" || edge {
		t.Fatalf("storm chose %q edge=%v ok=%v at=%v, want backup immediately", id, edge, ok, at)
	}
	// Even after the backup ends, the primary must remain parked.
	l.opened("backup", now)
	l.failed("backup", errors.New("backup ended"), false, true, sources, now.Add(time.Second))
	id, _, _, _ = l.choose(sources, now.Add(steadyParked-time.Second))
	if id == "primary" {
		t.Fatal("storm source retried during cooldown")
	}
	l.next = ""
	if id, _, ok, _ := l.choose(sources, now.Add(steadyParked)); !ok || id != "primary" {
		t.Fatal("storm source did not become eligible after the ordinary cooldown")
	}

	// Three widely separated changes, or changes separated by productive
	// playback, must not be classified as an unproductive twenty-second storm.
	for _, productive := range []bool{false, true} {
		ladder := newSteadyLadder()
		for i := range 3 {
			step := 11 * time.Second
			if productive {
				step = time.Second
			}
			if ladder.changed("input", productive, t0.Add(time.Duration(i)*step)) {
				t.Fatal("old or productive changes counted as a storm")
			}
		}
	}
	// One change with a good successor splices a picture through the same
	// wrapper, even before the first picture and without a starvation interface.
	p.ladder = newSteadyLadder()
	healed := &providerBroadcastDecoder{Decoder: &changingLiveDecoder{}, planner: p, sourceID: "healed", life: ctx, frame: time.Second / 30, ready: func() {}}
	defer healed.Close()
	healed.reopen = func() (transcode.Decoder, error) { return &changingLiveDecoder{picture: true}, nil }
	healed.cur = startPump(ctx, healed.Decoder)
	oldPump := healed.cur
	frame, err := healed.Next(ctx)
	if err != nil || !frame.SourcePTSValid || !healed.passed {
		t.Fatalf("in-place recovery returned frame=%+v err=%v", frame, err)
	}
	// Seeing that same change again must not consume another storm slot.
	before := p.ladder.state("healed").changes
	firstStorm := healed.noteChange(oldPump)
	secondStorm := healed.noteChange(oldPump)
	if firstStorm || secondStorm || before != p.ladder.state("healed").changes {
		t.Fatal("duplicate change signature counted as a storm")
	}
}

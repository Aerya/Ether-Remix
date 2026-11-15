package livetv

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

// A codec or stream-set change on a live input starves one elementary
// stream while the other keeps flowing. Waiting for the decoder to give up
// (ErrInputChanged) and only then reopening costs about a second of held
// picture. Instead, once one stream has starved for spliceOpenAfter, a
// candidate decoder opens on the same input at its live edge; the clock
// switches to it as soon as it has a picture while the old one is still
// starved (or reported the change). If the old decoder recovers first, the
// candidate closes again. The provider connection is the same shared tap.
//
// With decoder errors during the wait (a codec change floods them) the
// splice is quick; without, it waits out interleave jitter (a long-GOP
// stream's big keyframes can hold pictures back half a second).
const (
	spliceOpenAfter   = 200 * time.Millisecond
	spliceSwitchAfter = 250 * time.Millisecond
	quietOpenAfter    = 500 * time.Millisecond
	quietSwitchAfter  = 700 * time.Millisecond
	spliceRecovered   = time.Second
	spliceRetryAfter  = 2 * time.Second
	splicePoll        = 40 * time.Millisecond
)

type pumped struct {
	frame transcode.RawFrame
	err   error
}

type decoderPump struct {
	dec         transcode.Decoder
	ch          chan pumped
	cancel      context.CancelFunc
	held        *pumped // a candidate's first picture, kept until the switch
	opened      time.Time
	changeNoted bool // debounce this decoder's selected-stream starvation signature
}

func startPump(parent context.Context, dec transcode.Decoder) *decoderPump {
	ctx, cancel := context.WithCancel(parent)
	p := &decoderPump{dec: dec, ch: make(chan pumped), cancel: cancel, opened: time.Now()}
	go func() {
		for {
			frame, err := dec.Next(ctx)
			select {
			case p.ch <- pumped{frame, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

func (p *decoderPump) close() {
	if p != nil {
		p.cancel()
		go p.dec.Close()
	}
}

type starving interface {
	StarvingErrors() (time.Duration, bool)
}

// pull is the next picture of the current decoder, splicing a live-edge
// candidate in when the current one starves.
func (d *providerBroadcastDecoder) pull(ctx context.Context) (transcode.RawFrame, error) {
	if d.reopen == nil {
		return d.Decoder.Next(ctx)
	}
	if d.cur == nil {
		d.cur = startPump(d.life, d.Decoder)
	}
	tick := time.NewTicker(splicePoll)
	defer tick.Stop()
	failed := d.cur.ch == nil
	for {
		var candidate chan pumped
		if d.cand != nil && d.cand.held == nil {
			candidate = d.cand.ch
		}
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case r := <-d.cur.ch:
			if r.err == nil {
				return r.frame, nil
			}
			if !errors.Is(r.err, transcode.ErrInputChanged) {
				return r.frame, r.err
			}
			if d.noteChange(d.cur) {
				return transcode.RawFrame{}, errSteadyChangeStorm
			}
			// Even an early change belongs to this splice, not to a new
			// planner attempt. Keep the output clock and source grant alive.
			failed = true
			d.cur.ch = nil
		case r := <-candidate:
			if r.err != nil {
				storm := errors.Is(r.err, transcode.ErrInputChanged) && d.noteChange(d.cand)
				d.cand.close()
				d.cand = nil
				if storm {
					return transcode.RawFrame{}, errSteadyChangeStorm
				}
				if failed && !errors.Is(r.err, transcode.ErrInputChanged) {
					return transcode.RawFrame{}, r.err
				}
				continue
			}
			if !d.replayed(r.frame) {
				d.cand.held = &r
			}
		case <-tick.C:
		}
		var starved time.Duration
		var errored bool
		if source, ok := d.Decoder.(starving); ok {
			starved, errored = source.StarvingErrors()
		}
		open, switchAfter := quietOpenAfter, quietSwitchAfter
		if errored {
			open, switchAfter = spliceOpenAfter, spliceSwitchAfter
		}
		switch {
		case d.cand != nil && d.cand.held != nil && (failed || starved >= switchAfter):
			if d.noteChange(d.cur) {
				return transcode.RawFrame{}, errSteadyChangeStorm
			}
			frame := d.cand.held.frame
			d.cand.held = nil
			d.cur.close()
			d.Decoder, d.cur, d.cand = d.cand.dec, d.cand, nil
			d.planner.logger().Info("live input spliced to a new decoder", "input", d.sourceID, "reason", "stream change", "starved", starved, "confirmed", failed)
			return frame, nil
		case d.cand != nil && time.Since(d.cand.opened) >= steadyFirstFrameRejoin:
			d.cand.close()
			d.cand = nil
			if failed {
				return transcode.RawFrame{}, errSteadyStarved
			}
		case d.cand == nil && (failed || starved >= open) && time.Since(d.lastCandidate) >= spliceRetryAfter:
			d.lastCandidate = time.Now()
			if dec, err := d.reopen(); err == nil {
				d.cand = startPump(d.life, dec)
			} else if failed {
				return transcode.RawFrame{}, err
			}
		case d.cand != nil && !failed && starved == 0 && time.Since(d.cand.opened) >= spliceRecovered:
			d.cand.close()
			d.cand = nil
		}
	}
}

// noteChange debounces repeated observations of the same failed selected
// streams on one decoder. Extra PIDs and PMT versions never enter this path:
// only starvation of the mapped video/audio or ErrInputChanged does. A new
// decoder failing again is a new event, even with the same codec signature.
func (d *providerBroadcastDecoder) noteChange(pump *decoderPump) bool {
	if pump.changeNoted {
		return false
	}
	pump.changeNoted = true
	aired := d.aired.Load()
	productive := time.Duration(aired-d.changeAired)*d.frame >= steadyDelivered
	// Accumulate delivered media across short splices too: several changes
	// with moving pictures between them must not look like no playback.
	if productive {
		d.changeAired = aired
	}
	p := d.planner
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ladder.changed(d.sourceID, productive, p.s.o.Now())
}

// replayed reports a candidate picture this source already aired.
func (d *providerBroadcastDecoder) replayed(frame transcode.RawFrame) bool {
	if !frame.SourcePTSValid {
		return true
	}
	d.planner.mu.Lock()
	floor, seen := d.planner.lastAired[d.sourceID]
	d.planner.mu.Unlock()
	return seen && frame.SourcePTS <= floor && floor-frame.SourcePTS < steadyClockReset
}

// Close stops the current decoder and any candidate.
func (d *providerBroadcastDecoder) Close() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.cand.close()
	if d.cur != nil {
		d.cur.cancel()
	}
	return d.Decoder.Close()
}

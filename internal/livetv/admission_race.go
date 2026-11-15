package livetv

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// raceSteadyInputs gives each input a useful probe window rather than dividing
// the deadline by the channel's input count. Only three probes can be alive.
// Capped accounts are deliberately serial here; reservation and login leasing
// remain the authority for capacity already held by other channels/nodes.
func (s *Service) raceSteadyInputs(ctx context.Context, c Channel, candidates []StreamSource) (*steadyAdmission, []TuneInputFailure, bool, bool, error) {
	type result struct {
		src     StreamSource
		account string
		link    *steadyLink
		info    *core.MediaInfo
		err     error
	}
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 3)
	pending := append([]StreamSource(nil), candidates...)
	active := map[string]int{}
	running := 0
	var winner *steadyAdmission
	var failures []TuneInputFailure
	var last error = core.ErrNotAvailable
	silent, radio := false, false
	for len(pending) > 0 || running > 0 {
		for winner == nil && life.Err() == nil && running < 3 {
			if deadline, ok := life.Deadline(); ok && time.Until(deadline) < 2500*time.Millisecond {
				pending = nil
				break
			}
			index := -1
			account := ""
			for j, src := range pending {
				account = src.AuthSourceID
				if account == "" {
					account = c.SourceID
				}
				entry, err := s.sourceEntry(life, account)
				if err == nil && entry.limit > 0 && active[account] > 0 {
					continue
				}
				index = j
				break
			}
			if index < 0 {
				break
			}
			src := pending[index]
			pending = append(pending[:index], pending[index+1:]...)
			active[account]++
			running++
			go func(src StreamSource, account string) {
				// Up to three inputs keep the full admission window. Larger
				// lists get two waves with 3.5 seconds per candidate.
				attempt := life
				stop := func() {}
				if len(candidates) > 3 {
					attempt, stop = context.WithTimeout(life, 3500*time.Millisecond)
				}
				defer stop()
				started := time.Now()
				link, err := s.linkSteadySource(attempt, c, src, "broadcast-"+c.ID+"-admit-"+randomID(), s.latencyPolicy().TargetLatency()+s.latencyPolicy().SegmentDuration())
				sourceOpenElapsed := time.Since(started)
				var probeElapsed, pictureElapsed time.Duration
				var info *core.MediaInfo
				if err == nil {
					s.setInputAdmission(link.ss, true)
					probeStarted := time.Now()
					info, err = s.probeSteadyLink(attempt, c, link)
					probeElapsed = time.Since(probeStarted)
					if err == nil {
						pictureStarted := time.Now()
						err = s.primeAdmissionPicture(attempt, c, link, info)
						pictureElapsed = time.Since(pictureStarted)
					}
				}
				s.logger().Debug("live admission input finished", "channel", c.ID, "input", src.ID, "elapsed", time.Since(started), "source_open_elapsed", sourceOpenElapsed, "probe_elapsed", probeElapsed, "first_picture_elapsed", pictureElapsed, "success", err == nil)
				results <- result{src: src, account: account, link: link, info: info, err: err}
			}(src, account)
		}
		if running == 0 {
			break
		}
		// Always drain the bounded workers before returning: no orphaned probes,
		// grants, login leases or reservations survive a losing attempt.
		r := <-results
		running--
		active[r.account]--
		if winner != nil || ctx.Err() != nil {
			r.link.close(s)
			pending = nil
			continue
		}
		if r.err == nil && r.info != nil && r.info.Video() != nil && liveInputHasAudio(r.info) {
			s.setInputAdmission(r.link.ss, false)
			r.link.format = steadyInputFormat(r.info)
			winner = &steadyAdmission{shape: steadyShapeFor(r.info), link: r.link, info: r.info}
			s.recordSourceHealth(c.ID, r.src, nil, false)
			cancel()
			pending = nil
			continue
		}
		r.link.close(s)
		if r.err == nil && r.info != nil && len(r.info.Streams) > 0 {
			if r.info.Video() == nil {
				radio = true
			} else {
				silent = true
			}
			continue
		}
		if r.err == nil {
			r.err = core.ErrNotAvailable
		}
		last = r.err
		failures = append(failures, inputFailure(r.src.ID, r.err))
		if !errors.Is(r.err, ErrProviderBusy) && !errors.Is(r.err, ErrProviderPaced) && life.Err() == nil {
			s.recordSourceHealth(c.ID, r.src, r.err, false)
		}
	}
	if winner == nil && ctx.Err() != nil {
		last = ctx.Err()
	}
	return winner, failures, silent, radio, last
}

// admissionDecoder transfers exactly one already decoded frame, then resumes
// the same decoder without another upstream open or keyframe wait.
type admissionDecoder struct {
	transcode.Decoder
	first *transcode.RawFrame
}

func (d *admissionDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if err := ctx.Err(); err != nil {
		return transcode.RawFrame{}, err
	}
	if d.first != nil {
		f := *d.first
		d.first = nil
		return f, nil
	}
	return d.Decoder.Next(ctx)
}

func (s *Service) setInputAdmission(ss *session, admitting bool) {
	s.inputRelay.mu.Lock()
	g := s.inputRelay.bySess[ss]
	s.inputRelay.mu.Unlock()
	if g != nil {
		g.admitting.Store(admitting)
	}
}

// primeAdmissionPicture runs only while a viewer is tuning. The winning
// decoder outlives the attempt context; losers are closed before admission
// returns. Its first frame is retained rather than opening the provider again.
func (s *Service) primeAdmissionPicture(ctx context.Context, c Channel, link *steadyLink, info *core.MediaInfo) error {
	if info == nil || info.Video() == nil || !liveInputHasAudio(info) {
		return nil
	}
	if code, _ := steadyRefusal(info); code != "" {
		return nil
	}
	runner, ok := s.transcoder().(transcode.ContinuousRunner)
	if !ok {
		return nil
	}
	input, err := s.relayInput(link.ss)
	if err != nil {
		return err
	}
	reserve := s.stabilityReserve(c) > 0
	d, err := runner.StartDecoder(link.ss.ctx, transcode.DecoderRequest{SessionID: link.ss.info.ID, InputURL: input, Format: steadyShapeFor(info).format, Live: true, PrimeLive: !reserve, ReserveStart: reserve, InputFormat: steadyInputFormat(info)})
	if err != nil {
		return err
	}
	first, err := d.Next(ctx)
	if err != nil {
		_ = d.Close()
		return err
	}
	link.primedDecoder = &admissionDecoder{Decoder: d, first: &first}
	return nil
}

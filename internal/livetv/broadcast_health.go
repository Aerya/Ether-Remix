package livetv

import (
	"regexp"
	"time"
)

var diagnosticURL = regexp.MustCompile(`[a-z]+://[^\s'"]+`)

// sanitizeDiagnostics drops URLs (relay grants, provider tokens) from decoder
// diagnostics and bounds their length for the log.
func sanitizeDiagnostics(s string) string {
	s = diagnosticURL.ReplaceAllString(s, "[url]")
	if len(s) > 900 {
		s = s[:900] + "…"
	}
	return s
}

// liveHealthEvery spaces the frame clock's periodic health line for a live
// provider output. One line per channel every half minute names everything a
// visible glitch depends on: the reserve behind the decoder, the provider's
// cadence, the output's lead over the wall clock and the published edge.
const liveHealthEvery = 30 * time.Second

// logHealth records the output's live state. reason is "periodic", "stall"
// (the decoder stopped delivering pictures) or "resume" (it delivered again).
// It runs on the frame clock and takes no lock the clock does not already
// allow: the planner reads an atomic health snapshot.
func (b *broadcast) logHealth(reason string, now, scheduled time.Time) {
	b.healthAt = now
	attrs := []any{"reason", reason, "lead", b.lead.Round(time.Millisecond)}
	if !scheduled.IsZero() {
		attrs = append(attrs, "clockLate", time.Since(scheduled.Add(-b.lead)).Round(time.Millisecond))
	}
	if b.pacer != nil {
		attrs = append(attrs, "playRate", b.pacer.lastRate)
	}
	if b.current != nil {
		attrs = append(attrs, "decodedAhead", len(b.current.frames))
		if !b.current.lastFrameAt.IsZero() {
			attrs = append(attrs, "sincePicture", now.Sub(b.current.lastFrameAt).Round(time.Millisecond))
		}
	} else {
		attrs = append(attrs, "input", "none")
	}
	if !b.stallSince.IsZero() && reason == "resume" {
		attrs = append(attrs, "stalledFor", now.Sub(b.stallSince).Round(time.Millisecond))
	}
	if b.holdMode != "" {
		attrs = append(attrs, "hold", b.holdMode, "holdFor", now.Sub(b.holdSince).Round(time.Millisecond))
	}
	if b.current != nil && reason != "periodic" {
		d := b.current.decoder
		if wrapped, ok := d.(*overlayDecoder); ok {
			d = wrapped.Decoder
		}
		if source, ok := d.(interface{ Diagnostics() string }); ok {
			attrs = append(attrs, "decoder", sanitizeDiagnostics(source.Diagnostics()))
		}
	}
	if source, ok := b.planner.(interface{ healthAttrs() []any }); ok {
		attrs = append(attrs, source.healthAttrs()...)
	}
	b.mu.Lock()
	for id, r := range b.renditions {
		attrs = append(attrs, "rendition", id, "queued", r.queue.pending())
		if index := r.encoder.Segments(); index != nil {
			if latest, ok := index.Latest(); ok {
				attrs = append(attrs, "publishedLead", time.Until(latest.Start.Add(latest.Duration)).Round(time.Millisecond))
			}
		}
	}
	b.mu.Unlock()
	b.logger.Info("live steady health", attrs...)
}

// healthAttrs describes the current input's provider transport for
// broadcast.logHealth.
func (p *providerBroadcastPlanner) healthAttrs() []any {
	p.mu.Lock()
	a := p.attempt
	if a == nil {
		a = p.stalledAttempt
	}
	p.mu.Unlock()
	if a == nil || a.link == nil {
		return []any{"source", "none"}
	}
	h, ok := p.s.inputHealth(a.link.ss)
	if !ok {
		return []any{"source", a.src.ID, "transport", "unknown", "grant", p.s.healthGrant(a.link.ss) != nil}
	}
	behind := any("unknown")
	p.mu.Lock()
	aired, seen := p.lastAired[a.src.ID]
	p.mu.Unlock()
	if seen && h.EdgePTS >= 0 {
		// Media between the newest picture the provider delivered and the
		// picture on air: the real cushion, wherever it is buffered.
		edge := time.Duration(h.EdgePTS) * time.Second / tsClockHz
		wrap := time.Duration(tsWrap) * time.Second / tsClockHz
		d := (edge - aired%wrap + wrap) % wrap
		if d > wrap/2 {
			d -= wrap
		}
		behind = d.Round(time.Millisecond)
	}
	return []any{"source", a.src.ID, "transport", h.Kind, "behindLive", behind,
		"reserve", h.ReserveDepth.Round(time.Millisecond), "reserveTarget", h.Target, "paceAhead", h.PaceAhead.Round(time.Millisecond), "readers", h.Readers, "readerSpread", h.Spread.Round(time.Millisecond),
		"dataAge", h.DataAge.Round(time.Millisecond), "stallAfter", h.StallAfter.Round(time.Millisecond),
		"liveSocket", h.LiveSocket, "providerStalled", h.Stalled, "failed", h.Failed,
		"reconnects", h.Reconnects, "kbps", h.Bitrate / 1000}
}

func (q *broadcastFrameQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.frames) - q.head
}

// refillInput lets the current input's paced reader run a pace lead past
// where it is now (see broadcast.step).
func (p *providerBroadcastPlanner) refillInput() {
	p.mu.Lock()
	a := p.attempt
	if a == nil {
		a = p.stalledAttempt
	}
	p.mu.Unlock()
	if a != nil && a.link != nil {
		p.s.refillSteadyInput(a.link.ss)
	}
}

// growLead reports that the frame clock may tick a little early to raise a
// provider output's published lead toward its goal (the latency target): the
// lead is the client's cushion against a late segment, a slow download or a
// stalled encoder, and the startup burst could only spend the backlog the
// provider sent at connect. It grows only while fresh pictures flow, nothing
// holds, the encoders keep up and the input reserve exceeds its target by
// what the growth spends, so it spends surplus reserve, never a picture.
func (b *broadcast) growLead() bool {
	if !b.burstNoWait || b.bursting || !b.startupUntil.IsZero() || !b.lastFresh || !b.holdSince.IsZero() || b.current == nil {
		return false
	}
	goal := time.Duration(0)
	if source, ok := b.planner.(interface{ leadGoal() time.Duration }); ok {
		goal = source.leadGoal()
	}
	if b.lead >= goal {
		return false
	}
	fps := b.format.FPSNum / max(1, b.format.FPSDen)
	if len(b.current.frames) < fps/2 {
		return false
	}
	source, ok := b.planner.(reservePaceSource)
	if !ok {
		return false
	}
	// Spend only reserve above the preset's target (plus a second): growing
	// the lead must never leave the input short of what the preset promises.
	// Each early tick spends one picture of that surplus, so the lead grows
	// as far as the surplus reaches and resumes when the reserve refills;
	// requiring the whole remaining growth up front kept a reserve that sat
	// a little above target from ever growing the lead.
	if depth, target, ok := source.reserveTarget(); !ok || target <= 0 || depth < target+time.Second {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.renditions {
		if r.queue.pending() > fps/4 {
			return false
		}
	}
	return true
}

// leadGoal is the published lead a provider output grows toward: the owner's
// latency target, so a viewer starts and stays that far behind the encoder.
func (p *providerBroadcastPlanner) leadGoal() time.Duration {
	return p.s.latencyPolicy().TargetLatency()
}

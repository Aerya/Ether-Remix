package resolver

import (
	"context"
	"time"
)

const readySourceBudget = 1250 * time.Millisecond

// readyPeer keeps the owner's quality, language, edition, policy and score
// floor. Cache availability alone is never proof that a source can be played.
func (s *Service) readyPeer(cands []candidate) int {
	if len(cands) < 2 || cands[0].fallback || cands[0].rel.OpensOnDemand() {
		return -1
	}
	margin := s.resolverCfg().SourceScoreMargin
	if margin <= 0 {
		margin = defaultSourceScoreMargin
	}
	lead := cands[0]
	for i := 1; i < len(cands); i++ {
		c := cands[i]
		if s.knownCached(c.rel) && sameQuality(lead, c) && c.bandwidth >= lead.bandwidth && c.rel.Info.Edition == lead.rel.Info.Edition && c.rel.Score >= lead.rel.Score-margin {
			return i
		}
	}
	return -1
}

// validateReady races just the preferred release and its best cached peer.
// Equal-ranked ready copies get a brief scheduling grace; a better rank or an
// explicit routing decision gets one short, absolute budget including discovery.
// Existing checks are reused exactly.
func (s *Service) validateReady(ctx context.Context, cands []candidate, tr *Trace, peer int) (int, []byte, error) {
	var owned candidateChecks
	checks := [2]*candidateCheck{cands[0].prepared, cands[peer].prepared}
	indexes := [2]int{0, peer}
	for j, i := range indexes {
		if checks[j] == nil {
			checks[j] = owned.start(ctx, s, cands[i].rel)
		}
		checks[j].retained = true // this call now owns cancellation and joining
		cands[i].prepared = checks[j]
		tr.Candidates[cands[i].trace].Attempted = true
	}
	win := func(j int) (int, []byte, error) {
		v, i := checks[j].result, indexes[j]
		if v.rel.ID != "" {
			cands[i].rel = v.rel
		}
		if v.healthChecked && !v.healthCached {
			s.markVerifiedKeys(ctx, s.allVerificationKeys(cands[i].rel, v.extraContentKeys...))
			s.noteStartup(cands[i].rel, max(v.fetchDuration+v.healthDuration, time.Microsecond))
		}
		s.recordReputation(ctx, cands[i].rel.Indexer, true)
		return i, v.nzb, nil
	}
	keepFallback := false
	defer func() {
		cleanup := func() {
			for j, f := range checks {
				if !keepFallback {
					f.cancel()
				}
				<-f.done
				v := f.result
				rel := cands[indexes[j]].rel
				if v.rel.ID != "" {
					rel = v.rel
				}
				noteValidation(ctx, rel, v)
				if keepFallback && ((!v.healthy && v.err == nil) || permanentFailure(v.err) || (!v.healthChecked && permanentGrabFailure(v.err))) {
					s.recordFailedValidation(ctx, rel, v)
				}
			}
		}
		s.bgMu.Lock()
		if keepFallback && !s.bgClosed {
			s.bgWG.Add(1)
			s.bgMu.Unlock()
			go func() { defer s.bgWG.Done(); cleanup() }()
		} else {
			s.bgMu.Unlock()
			keepFallback = false
			cleanup()
		}
	}()
	start := checks[0].started
	if gs := cands[0].graceStart; !gs.IsZero() && gs.Before(start) {
		start = gs
	}
	budget := readySourceBudget
	if cands[peer].rel.Score >= cands[0].rel.Score &&
		!compareCandidates(cands[0], cands[peer]) && !cands[0].sourcePreferred {
		// Give an equally ranked preferred check a brief scheduling grace. Otherwise
		// a sub-millisecond cached answer can win before it even starts.
		// Real network checks still let an equal peer win as soon as ready.
		budget = 10 * time.Millisecond
	}
	timer := time.NewTimer(max(time.Until(start.Add(budget)), 0))
	defer timer.Stop()
	expired := false
	done := [2]bool{}
	record := func(j int) {
		done[j] = true
		v := checks[j].result
		if v.rel.ID != "" {
			cands[indexes[j]].rel = v.rel
		}
		ct := &tr.Candidates[cands[indexes[j]].trace]
		ct.FetchDuration, ct.HealthDuration = v.fetchDuration, v.healthDuration
		ct.HealthChecked, ct.Healthy = v.healthChecked, v.healthy && v.err == nil
		if !ct.Healthy {
			ct.Rejected, ct.err = true, v.err
			ct.Reason = "health: unhealthy"
			if v.err != nil {
				ct.Reason = v.err.Error()
			}
			if v.err == nil || permanentFailure(v.err) || (!v.healthChecked && permanentGrabFailure(v.err)) {
				s.recordFailedValidation(ctx, cands[indexes[j]].rel, v)
			}
		}
	}
	for {
		for j, f := range checks {
			if !done[j] {
				select {
				case <-f.done:
					record(j)
				default:
				}
			}
		}
		if ctx.Err() != nil {
			return -1, nil, ctx.Err()
		}
		if done[0] && checks[0].result.healthy && checks[0].result.err == nil {
			return win(0)
		}
		if done[1] && checks[1].result.healthy && checks[1].result.err == nil &&
			(done[0] || expired) {
			tr.Candidates[cands[peer].trace].Route = "verified cached peer ready; preferred source not ready within quality budget"
			keepFallback = true
			return win(1)
		}
		if done[0] && (!checks[0].result.healthy || checks[0].result.err != nil) && len(cands[0].fallbacks) > 0 {
			// Leader failed and has quiet fallbacks inside its repost group:
			// hand off to ranked validation so the group's fallbacks run before
			// falling back to another release.
			return s.validateRanked(ctx, cands, tr)
		}
		if done[1] && (!checks[1].result.healthy || checks[1].result.err != nil) {
			// The cached answer was stale. Normal ranked validation handles errors,
			// retries and the rest of the candidates using these exact checks.
			return s.validateRanked(ctx, cands, tr)
		}
		var a, b <-chan struct{}
		if !done[0] {
			a = checks[0].done
		}
		if !done[1] {
			b = checks[1].done
		}
		select {
		case <-a:
		case <-b:
		case <-timer.C:
			expired = true
		case <-ctx.Done():
			return -1, nil, ctx.Err()
		}
	}
}

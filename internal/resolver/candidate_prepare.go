package resolver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/vault"
)

type observationTitleKey struct{}
type speculativeChecksKey struct{}
type healthAttemptsKey struct{}
type singleGroupHedgeKey struct{}

// One allowance follows the whole resolve through discovery, preparation and
// ranked validation. Candidate positions cannot enforce this limit because
// later search results can reorder releases already checked during discovery.
func reserveSpeculativeCheck(ctx context.Context) bool {
	if !core.IsSpeculative(ctx) {
		return true
	}
	budget, _ := ctx.Value(speculativeChecksKey{}).(*atomic.Int64)
	return budget == nil || budget.Add(1) <= speculativeHealthAttempts
}

func claimHealthAttempt(ctx context.Context) bool {
	budget, _ := ctx.Value(healthAttemptsKey{}).(*atomic.Int64)
	if budget == nil {
		return true
	}
	limit := int64(maxHealthAttempts)
	if core.IsSpeculative(ctx) && int64(speculativeHealthAttempts) < limit {
		limit = int64(speculativeHealthAttempts)
	}
	return budget.Add(1) <= limit
}

// launchValidation runs one candidate check in its own goroutine: a
// per-candidate timeout over workCtx, reusing a prepared check when present,
// trying any quiet repost-group fallbacks if the leader fails, tagging the
// result with its candidate index and sending it on results.
// The caller owns workCtx cancellation and joining wg; results buffering is
// the caller's choice and is unchanged by this extraction.
func (s *Service) launchValidation(wg *sync.WaitGroup, workCtx context.Context, cand candidate, i int, finalWave bool, results chan<- validation) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		results <- s.runValidation(workCtx, cand, i, finalWave)
	}()
}

func (s *Service) runValidation(workCtx context.Context, cand candidate, i int, finalWave bool) validation {
	if !claimHealthAttempt(workCtx) {
		return validation{index: i, err: errHealthBudget, budgetExhausted: true}
	}
	runMemberCtx := func(parent context.Context, m candidate, final bool) validation {
		checkCtx, cancel := context.WithTimeout(parent, checkBudgetFor(parent, m.rel, final))
		defer cancel()
		var mv validation
		if m.prepared != nil {
			mv = m.prepared.wait(checkCtx)
		} else {
			mv = s.validateOne(checkCtx, m.rel)
		}
		// Retire the losing open before publishing its result: a timeout must
		// not let the next on-demand candidate start beside it.
		cancel()
		return mv
	}

	var v validation
	var tried []fallbackValidation
	budgetExhausted := false
	hedgedIdx := -1

	allowHedge, _ := workCtx.Value(singleGroupHedgeKey{}).(bool)
	hedgeCandidateIdx := -1
	if allowHedge && len(cand.fallbacks) > 0 && spendsGrab(cand.rel) {
		leadCID := cand.rel.EffectiveContentID()
		for idx, fb := range cand.fallbacks {
			if fb.rel.OpensOnDemand() || s.isBadRelease(fb.rel) || s.recentlyFailedRelease(fb.rel) {
				continue
			}
			if leadCID != "" && fb.rel.EffectiveContentID() == leadCID {
				continue
			}
			hedgeCandidateIdx = idx
			break
		}
	}

	if hedgeCandidateIdx >= 0 {
		leadCtx, cancelLead := context.WithCancel(workCtx)
		hedgeCtx, cancelHedge := context.WithCancel(workCtx)
		var subWG sync.WaitGroup
		leadCh := make(chan validation, 1)
		var hedgeCh chan validation
		hedgeTimer := time.NewTimer(s.healthHedgeDelay(cand.rel))
		graceTimer := time.NewTimer(rankedSourceGrace)
		cleanup := func() {
			hedgeTimer.Stop()
			graceTimer.Stop()
			cancelLead()
			cancelHedge()
			subWG.Wait()
		}

		subWG.Add(1)
		go func() {
			defer subWG.Done()
			leadCh <- runMemberCtx(leadCtx, cand, false)
		}()

		select {
		case lv := <-leadCh:
			v = lv
		case <-workCtx.Done():
			cleanup()
			return validation{index: i, err: workCtx.Err()}
		case <-hedgeTimer.C:
			if workCtx.Err() == nil && claimHealthAttempt(workCtx) {
				hedgedIdx = hedgeCandidateIdx
				fb := cand.fallbacks[hedgedIdx]
				hedgeCh = make(chan validation, 1)
				subWG.Add(1)
				go func() {
					defer subWG.Done()
					hedgeCh <- runMemberCtx(hedgeCtx, fb, finalWave && hedgedIdx == len(cand.fallbacks)-1)
				}()
			} else if workCtx.Err() == nil {
				budgetExhausted = true
			}
			var leadVal, hedgeVal *validation
			graceExpired := false
			graceTick := graceTimer.C
			for {
				if leadVal != nil && leadVal.healthy && leadVal.err == nil {
					v = *leadVal
					break
				}
				if hedgeVal != nil && hedgeVal.healthy && hedgeVal.err == nil && (leadVal != nil || graceExpired) {
					fb := cand.fallbacks[hedgedIdx]
					fbRel := fb.rel
					if hedgeVal.rel.ID != "" {
						fbRel = hedgeVal.rel
					}
					tried = append(tried, fallbackValidation{trace: fb.trace, rel: fbRel, val: *hedgeVal})
					promoted := fb
					promoted.rel = fbRel
					fv := *hedgeVal
					fv.promoted = &promoted
					fv.fallbacks = tried
					v = fv
					break
				}
				if leadVal != nil && (hedgeVal != nil || hedgeCh == nil) {
					v = *leadVal
					if hedgeVal != nil {
						fb := cand.fallbacks[hedgedIdx]
						fbRel := fb.rel
						if hedgeVal.rel.ID != "" {
							fbRel = hedgeVal.rel
						}
						if errors.Is(hedgeVal.err, errHealthBudget) {
							budgetExhausted = true
						} else {
							tried = append(tried, fallbackValidation{trace: fb.trace, rel: fbRel, val: *hedgeVal})
							if workCtx.Value(speculativeHealthKey{}) == nil && (hedgeVal.err == nil || permanentFailure(hedgeVal.err) || (!hedgeVal.healthChecked && permanentGrabFailure(hedgeVal.err))) {
								s.recordFailedValidation(workCtx, fbRel, *hedgeVal)
							}
						}
					}
					break
				}
				select {
				case lv := <-leadCh:
					leadVal = &lv
					leadCh = nil
				case hv := <-hedgeCh:
					hedgeVal = &hv
					hedgeCh = nil
				case <-graceTick:
					graceExpired = true
					graceTick = nil
				case <-workCtx.Done():
					cleanup()
					return validation{index: i, err: workCtx.Err()}
				}
			}
		}
		cleanup()
	} else {
		v = runMemberCtx(workCtx, cand, finalWave && len(cand.fallbacks) == 0)
	}

	if v.promoted == nil && (!v.healthy || v.err != nil) && len(cand.fallbacks) > 0 && workCtx.Err() == nil {
		if workCtx.Value(speculativeHealthKey{}) == nil && (v.err == nil || permanentFailure(v.err) || (!v.healthChecked && permanentGrabFailure(v.err))) {
			s.recordFailedValidation(workCtx, cand.rel, v)
		}
		if errors.Is(v.err, errHealthBudget) {
			budgetExhausted = true
		}
		for fbi, fb := range cand.fallbacks {
			if fbi == hedgedIdx || budgetExhausted {
				continue
			}
			if workCtx.Err() != nil {
				break
			}
			if pastPlayDeadline(workCtx) && (inconclusiveCheck(v) || hasUnverified(workCtx)) {
				break
			}
			if fb.rel.OpensOnDemand() && !core.OnDemandAllowedAt(workCtx, fb.rel, i) {
				continue
			}
			// Skip quiet fallbacks whose cross-indexer content identity is
			// already known dead (e.g. the leader's failed grab/check).
			if s.isBadRelease(fb.rel) || s.recentlyFailedRelease(fb.rel) {
				continue
			}
			if !claimHealthAttempt(workCtx) {
				budgetExhausted = true
				break
			}
			fv := runMemberCtx(workCtx, fb, finalWave && fbi == len(cand.fallbacks)-1)
			if errors.Is(fv.err, errHealthBudget) {
				budgetExhausted = true
				break
			}
			fbRel := fb.rel
			if fv.rel.ID != "" {
				fbRel = fv.rel
			}
			tried = append(tried, fallbackValidation{trace: fb.trace, rel: fbRel, val: fv})
			if fv.healthy && fv.err == nil {
				promoted := fb
				promoted.rel = fbRel
				fv.promoted = &promoted
				fv.fallbacks = tried
				v = fv
				budgetExhausted = false
				break
			}
			if workCtx.Value(speculativeHealthKey{}) == nil && (fv.err == nil || permanentFailure(fv.err) || (!fv.healthChecked && permanentGrabFailure(fv.err))) {
				s.recordFailedValidation(workCtx, fbRel, fv)
			}
			if pastPlayDeadline(workCtx) && (inconclusiveCheck(fv) || hasUnverified(workCtx)) {
				break
			}
		}
		if v.promoted == nil {
			v.fallbacks = tried
			v.budgetExhausted = budgetExhausted
		}
	}
	v.index = i
	return v
}

func (s *Service) validateOne(ctx context.Context, rel core.Release) validation {
	if !reserveSpeculativeCheck(ctx) {
		return validation{err: errHealthBudget, budgetExhausted: true}
	}
	if key := vault.KeyOf(rel); key != "" && s.vault != nil {
		if err := s.verifyVault(ctx, key); err != nil {
			return validation{err: err}
		}
	}
	if protocolOf(rel) == core.ProtocolUsenet && s.usenetVerdicts != nil {
		if urlKey := s.usenetVerdictURLKey(rel); urlKey != "" {
			if verdict, ok := s.usenetVerdicts.Lookup(s.now(), urlKey, "", ""); ok && !verdict.Available {
				return validation{rel: rel, err: core.ErrArticleMissing}
			}
		}
	}
	v := validation{rel: rel}
	at := time.Now()
	v.nzb, v.err = s.indexer.FetchNZB(ctx, rel)
	v.fetchDuration = time.Since(at)
	if v.err == nil {
		if len(v.nzb) > 0 {
			rel.SourceHash = fmt.Sprintf("%x", sha256.Sum256(v.nzb))
		}
		var extraKeys []string
		rel, extraKeys = s.learnReleaseContent(ctx, rel, v.nzb)
		v.rel = rel
		v.extraContentKeys = extraKeys
		if title, ok := ctx.Value(observationTitleKey{}).(*core.Title); ok && title != nil && s.rejectedFor(title.Key, rel) {
			v.err = core.ErrReleaseRejected
			return v
		}
		if s.isBadRelease(rel, extraKeys...) {
			v.err = core.ErrReleaseRejected
			return v
		}
		s.con.mu.Lock()
		if recent, ok := s.con.recent[rel.ID]; ok {
			recent.rel.SourceHash = rel.SourceHash
			if rel.ContentID != "" {
				recent.rel.ContentID = rel.ContentID
			}
			s.con.recent[rel.ID] = recent
		}
		s.con.mu.Unlock()
		verifyKeys := s.allVerificationKeys(rel, extraKeys...)
		if !s.recentlyVerifiedKeys(ctx, verifyKeys) && s.recentlyFailedRelease(rel, extraKeys...) {
			v.err = core.ErrArticleMissing
			return v
		}
		v.healthy = true
		if s.health != nil {
			v.healthChecked = true
			at = time.Now()
			reusedVerdict := false
			if protocolOf(rel) == core.ProtocolUsenet && s.usenetVerdicts != nil {
				urlKey, nzbHash, contentID := s.usenetVerdictKeys(rel, v.nzb)
				if verdict, ok := s.usenetVerdicts.Lookup(s.now(), urlKey, nzbHash, contentID); ok {
					if !verdict.Available {
						v.healthy = false
						v.err = core.ErrArticleMissing
						return v
					}
					if s.healthTTL > 0 && !s.usenetBackingOff() {
						v.healthy = true
						v.healthCached = true
						reusedVerdict = true
					}
				}
			}
			if !reusedVerdict {
				healthCtx, cancelHealth := withForegroundHealthDeadline(ctx)
				if s.usenetBackingOff() && protocolOf(rel) == core.ProtocolUsenet {
					v.healthy, v.err = core.CheckReleaseHealth(healthCtx, s.health, rel.ID, v.nzb)
					if v.healthy && v.err == nil && ctx.Value(speculativeHealthKey{}) == nil {
						s.markVerifiedKeys(ctx, verifyKeys)
					}
				} else {
					v.healthy, v.healthCached, v.err = s.checkHealthCachedKeys(healthCtx, rel.ID, verifyKeys, v.nzb)
				}
				cancelHealth()
				if !v.healthCached && ctx.Value(speculativeHealthKey{}) == nil {
					if v.healthy && v.err == nil {
						s.recordUsenetVerdict(rel, v.nzb, true, "")
					} else if (!v.healthy && v.err == nil) || permanentFailure(v.err) {
						reason := "health_unhealthy"
						if v.err != nil {
							reason = v.err.Error()
						}
						s.recordUsenetVerdict(rel, v.nzb, false, reason)
					}
				}
			}
			if v.healthy && v.err == nil && !v.healthCached && s.observed != nil && vault.KeyOf(rel) == "" {
				if title, ok := ctx.Value(observationTitleKey{}).(*core.Title); ok {
					s.persistBookkeeping(ctx, func(ctx context.Context) { s.observed(ctx, title, rel) })
				}
			}
			v.healthDuration = time.Since(at)
		}
	}
	return v
}

type candidateCheck struct {
	retained         bool      // ownership transferred to ready-source validation
	started          time.Time // includes time spent checking during source discovery
	done             chan struct{}
	cancel           context.CancelFunc
	result           validation                       // immutable after done closes
	retrySpeculative func(context.Context) validation // foreground may admit work speculation refused
}

func (c *candidateCheck) wait(ctx context.Context) validation {
	select {
	case <-c.done:
		if c.retrySpeculative != nil && (errors.Is(c.result.err, core.ErrNotCached) || errors.Is(c.result.err, core.ErrBudgetExhausted) || errors.Is(c.result.err, core.ErrForegroundOnly) || (!core.IsSpeculative(ctx) && errors.Is(c.result.err, errHealthBudget))) {
			return c.retrySpeculative(ctx)
		}
		return c.result
	case <-ctx.Done():
		// Discovery owns this check for the same Resolve. Cancel and join it
		// before failover can launch another open; never reopen a live check.
		c.cancel()
		<-c.done
		res := c.result
		if res.healthy && res.err == nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return res
			}
			return validation{err: ctx.Err()}
		}
		// Preserve any already-fetched NZB and healthChecked state so
		// lastResort can play an inconclusive Usenet check after a timeout,
		// while reporting ctx.Err() when c.cancel() stopped the check.
		if res.err == nil || errors.Is(res.err, context.Canceled) {
			res.err = ctx.Err()
		}
		return res
	}
}

// pendingPrep is one descriptor-preparation call running alongside the
// speculative checks. done is buffered so the goroutine can always finish;
// consumed marks that the result was already taken (or drained by close).
type pendingPrep struct {
	cancel   context.CancelFunc
	done     chan []core.Release
	consumed bool
}

// Owned by one Resolve call. Its deferred close cancels and joins speculative
// checks unless ready-source validation explicitly takes ownership.
type candidateChecks struct {
	all   []*candidateCheck
	preps []*pendingPrep
}

func (c *candidateChecks) start(ctx context.Context, s *Service, rel core.Release) *candidateCheck {
	budget := candidateBudget
	if !core.IsSpeculative(ctx) && !core.IsBackgroundCheck(ctx) {
		budget = finalCandidateBudget
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	stop := context.AfterFunc(s.bgCtx, cancel)
	f := &candidateCheck{started: time.Now(), done: make(chan struct{}), cancel: cancel}
	c.all = append(c.all, f)
	go func() { defer close(f.done); defer cancel(); defer stop(); f.result = s.validateOne(ctx, rel) }()
	return f
}

func (c *candidateChecks) close() {
	for _, f := range c.all {
		if f.retained {
			continue
		}
		f.cancel()
	}
	for _, p := range c.preps {
		p.cancel()
	}
	for _, f := range c.all {
		if f.retained {
			continue
		}
		<-f.done
	}
	for _, p := range c.preps {
		if !p.consumed {
			<-p.done
			p.consumed = true
		}
	}
}

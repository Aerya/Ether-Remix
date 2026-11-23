package resolver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// LaunchAction instructs RunRankedWindow how to handle candidate next during
// a fill pass.
type LaunchAction int

const (
	// LaunchAllow permits launching candidate next (subject to window capacity
	// and the grab-hedge delay).
	LaunchAllow LaunchAction = iota
	// LaunchHold stops filling for now; if holdUntil is non-zero the window
	// wakes at holdUntil to re-check.
	LaunchHold
	// LaunchSkip marks candidate next skipped without launching it and advances
	// to the following candidate.
	LaunchSkip
)

// RankedWindowOutcome describes how RunRankedWindow finished.
type RankedWindowOutcome[R any] struct {
	// Winner is the winning candidate's index, or -1 if no candidate won.
	Winner int
	// Lead is the lead candidate's index when Winner won (Winner != Lead means
	// an equivalent or immediate-overtake backup won while Lead was still open).
	Lead int
	// Result holds the winning result when Winner >= 0.
	Result R
	// Err is non-nil when ctx was cancelled before any candidate won.
	Err error
}

// RankedWindowConfig configures RunRankedWindow.
type RankedWindowConfig[R any] struct {
	// Count is the number of candidates in rank order.
	Count int
	// Width is the steady-state concurrency (defaults to 2).
	Width int
	// WideWidth is the widened concurrency for equivalent candidates after two
	// confirmed failures (defaults to 4).
	WideWidth int
	// Grace is how long a lead may remain in flight before an equivalent ready
	// backup overtakes it or a stalled lead yields an extra slot (defaults to
	// RankedSourceGrace).
	Grace time.Duration

	// Run executes candidate i in its own goroutine. finalWave is true when
	// i >= Count-Width.
	Run func(ctx context.Context, i int, finalWave bool) R
	// Succeeded reports whether result r is a winning result.
	Succeeded func(r R) bool
	// ConfirmedFailure reports whether a failed result counts toward the
	// two-confirmed-failure widening threshold (permanent failures; never
	// timeouts or transient provider errors).
	ConfirmedFailure func(r R) bool
	// IgnoreFailure, when non-nil and returning true for a failed result,
	// marks the candidate done without counting it as a failure or confirmed
	// failure.
	IgnoreFailure func(r R) bool
	// Equivalent reports whether candidate other may overtake lead after Grace
	// or occupy a stalled/widened window slot beside lead. When nil, all
	// candidates are considered equivalent.
	Equivalent func(lead, other int) bool
	// ImmediateOvertake, when non-nil and returning true, allows a ready
	// candidate other to overtake an in-flight lead without waiting for Grace.
	ImmediateOvertake func(lead, other int) bool
	// SpendsGrab reports whether candidate i consumes an indexer grab and
	// should therefore join a single healthy-so-far in-flight open only as a
	// hedge after HedgeDelay.
	SpendsGrab func(i int) bool
	// HedgeDelay returns the delay before a grab-spending candidate joins a
	// single in-flight open (defaults to DefaultHealthHedgeDelay).
	HedgeDelay func(i int) time.Duration
	// AdjustStart, when non-nil, may backdate candidate i's start time (for
	// example when descriptor preparation already consumed part of its grace).
	AdjustStart func(i int, now time.Time) time.Time
	// BeforeLaunch, when non-nil, runs in the coordinator goroutine before
	// capacity and grab-hedge checks for candidate next.
	BeforeLaunch func(lead, next, inflight int, leadStarted time.Time, leadDone bool) (action LaunchAction, holdUntil time.Time)
	// OnLaunch, when non-nil, runs in the coordinator goroutine immediately
	// before starting candidate i.
	OnLaunch func(i int)
	// OnResult, when non-nil, runs in the coordinator goroutine when candidate
	// i finishes while ctx is still live.
	OnResult func(i int, r R)
	// OnDrop, when non-nil, runs during teardown (after cancelling in-flight
	// workers and joining all worker goroutines) once for every completed
	// non-winning result. recorded is true if OnResult was already called for
	// that result, and false if the result finished during cancellation or win
	// teardown. Callers must release any resources held by r (such as closing
	// a losing ByteSource).
	OnDrop func(i int, r R, recorded bool)
}

type rankedItem[R any] struct {
	index int
	res   R
}

// RunRankedWindow executes ranked candidates with a sliding window: two checks
// stay in flight in rank order, and when one fails the next starts immediately.
// Until a check fails, a candidate that spends an indexer grab joins a single
// in-flight check only as a hedge after HedgeDelay. The best-ranked candidate
// (the "lead") wins when it succeeds; a later equivalent ready candidate may
// overtake the lead after Grace. A lead still unanswered after Grace stops
// holding a slot for one equivalent backup, and after two confirmed failures
// the window widens to WideWidth for candidates equivalent to the lead. All
// losing workers are cancelled, joined, and passed to OnDrop before returning.
func RunRankedWindow[R any](ctx context.Context, cfg RankedWindowConfig[R]) RankedWindowOutcome[R] {
	limit := cfg.Count
	if limit <= 0 {
		return RankedWindowOutcome[R]{Winner: -1}
	}
	if err := ctx.Err(); err != nil {
		return RankedWindowOutcome[R]{Winner: -1, Err: err}
	}
	width := cfg.Width
	if width <= 0 {
		width = 2
	}
	wideWidth := cfg.WideWidth
	if wideWidth <= 0 {
		wideWidth = 4
	}
	grace := cfg.Grace
	if grace <= 0 {
		grace = rankedSourceGrace
	}

	workCtx, cancelWork := context.WithCancel(ctx)
	var wg sync.WaitGroup
	results := make(chan rankedItem[R], limit)
	startedAt := make([]time.Time, limit)
	done := make([]*R, limit)
	skipped := make([]bool, limit)
	var dropped []rankedItem[R]
	winner := -1

	defer func() {
		cancelWork()
		wg.Wait()
		if cfg.OnDrop == nil {
			return
		}
		for _, item := range dropped {
			if item.index != winner {
				cfg.OnDrop(item.index, item.res, false)
			}
		}
		for {
			select {
			case item := <-results:
				if item.index != winner {
					cfg.OnDrop(item.index, item.res, false)
				}
			default:
				goto doneDrain
			}
		}
	doneDrain:
		for i, r := range done {
			if r != nil && i != winner {
				cfg.OnDrop(i, *r, true)
			}
		}
	}()

	next, inflight, confirmed, failed := 0, 0, 0, 0
	equiv := func(lead, other int) bool {
		return cfg.Equivalent == nil || cfg.Equivalent(lead, other)
	}
	launch := func() {
		i := next
		next++
		inflight++
		now := time.Now()
		if cfg.AdjustStart != nil {
			now = cfg.AdjustStart(i, now)
		}
		startedAt[i] = now
		if cfg.OnLaunch != nil {
			cfg.OnLaunch(i)
		}
		finalWave := i >= limit-width
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- rankedItem[R]{index: i, res: cfg.Run(workCtx, i, finalWave)}
		}()
	}
	// stalled reports whether the lead is still running past its grace, so that
	// a healthy equivalent backup would already be allowed to win.
	stalled := func(lead int) bool {
		return lead < next && !skipped[lead] && done[lead] == nil && time.Since(startedAt[lead]) >= grace
	}
	// capacity is how many checks may run before candidate next starts. Extra
	// slots only ever go to a candidate that could overtake the lead: one while
	// the lead is stalled, up to wideWidth once confirmed failures show leading
	// candidates are dead.
	capacity := func(lead int) int {
		switch {
		case lead >= limit || !equiv(lead, next):
			return width
		case confirmed >= 2:
			return wideWidth
		case stalled(lead):
			return width + 1
		}
		return width
	}
	// hedgeAt is when a held-back candidate may start; zero when none is waiting.
	var hedgeAt time.Time
	hedgeDue := func(i int) bool {
		if inflight != 1 || failed > 0 || cfg.SpendsGrab == nil || !cfg.SpendsGrab(i) {
			return true
		}
		var youngest time.Time
		for j := 0; j < next; j++ {
			if !skipped[j] && done[j] == nil && startedAt[j].After(youngest) {
				youngest = startedAt[j]
			}
		}
		delay := defaultHealthHedgeDelay
		if cfg.HedgeDelay != nil {
			if d := cfg.HedgeDelay(i); d > 0 {
				delay = d
			}
		}
		at := youngest.Add(delay)
		if time.Now().Before(at) {
			if hedgeAt.IsZero() || at.Before(hedgeAt) {
				hedgeAt = at
			}
			return false
		}
		return true
	}
	fill := func(lead *int) {
		hedgeAt = time.Time{}
		// Once a healthy backup is ready, wait for the lead or its grace period
		// instead of spending more calls.
		for i := *lead; i < next; i++ {
			if r := done[i]; r != nil && cfg.Succeeded(*r) {
				return
			}
		}
		for next < limit {
			if cfg.BeforeLaunch != nil {
				leadStarted := time.Time{}
				leadDone := true
				if *lead < next {
					leadStarted = startedAt[*lead]
					leadDone = skipped[*lead] || done[*lead] != nil
				}
				act, holdUntil := cfg.BeforeLaunch(*lead, next, inflight, leadStarted, leadDone)
				switch act {
				case LaunchSkip:
					skipped[next] = true
					next++
					for *lead < next && skipped[*lead] {
						*lead++
					}
					continue
				case LaunchHold:
					if !holdUntil.IsZero() && (hedgeAt.IsZero() || holdUntil.Before(hedgeAt)) {
						hedgeAt = holdUntil
					}
					return
				}
			}
			if inflight >= capacity(*lead) {
				return
			}
			if !hedgeDue(next) {
				return
			}
			launch()
		}
	}
	record := func(item rankedItem[R]) {
		inflight--
		res := item.res
		done[item.index] = &res
		if cfg.OnResult != nil {
			cfg.OnResult(item.index, res)
		}
		if cfg.Succeeded(res) {
			return
		}
		if cfg.IgnoreFailure != nil && cfg.IgnoreFailure(res) {
			return
		}
		failed++
		if cfg.ConfirmedFailure != nil && cfg.ConfirmedFailure(res) {
			confirmed++
		}
	}

	lead := 0
	fill(&lead)
	if lead >= limit {
		return RankedWindowOutcome[R]{Winner: -1, Lead: lead}
	}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		// Consume completed checks before choosing an alternative, so a
		// buffered preferred result wins even when the grace timer also fires.
	drain:
		for {
			select {
			case item := <-results:
				if ctx.Err() != nil {
					dropped = append(dropped, item)
					return RankedWindowOutcome[R]{Winner: -1, Lead: lead, Err: ctx.Err()}
				}
				record(item)
			default:
				break drain
			}
		}
		if ctx.Err() != nil {
			return RankedWindowOutcome[R]{Winner: -1, Lead: lead, Err: ctx.Err()}
		}
		// Settle everything already known, in rank order.
		for lead < limit && (skipped[lead] || done[lead] != nil) {
			if !skipped[lead] && cfg.Succeeded(*done[lead]) {
				winner = lead
				return RankedWindowOutcome[R]{Winner: lead, Lead: lead, Result: *done[lead]}
			}
			lead++
		}
		if lead >= limit {
			break
		}
		fill(&lead)
		if lead >= limit {
			break
		}
		if inflight == 0 && lead >= next {
			break
		}
		// A healthy later candidate may overtake a lead still running.
		age := time.Since(startedAt[lead])
		var wait time.Duration = -1
		for k := lead + 1; k < limit; k++ {
			r := done[k]
			if r == nil || !cfg.Succeeded(*r) {
				continue
			}
			switch {
			case cfg.ImmediateOvertake != nil && cfg.ImmediateOvertake(lead, k):
				winner = k
				return RankedWindowOutcome[R]{Winner: k, Lead: lead, Result: *r}
			case equiv(lead, k):
				if age >= grace {
					winner = k
					return RankedWindowOutcome[R]{Winner: k, Lead: lead, Result: *r}
				}
				if left := grace - age; wait < 0 || left < wait {
					wait = left
				}
			}
		}
		// Wake when the lead's grace ends if an equivalent backup could then
		// take its slot.
		if lead < next && !skipped[lead] && done[lead] == nil && next < limit && inflight == width && equiv(lead, next) {
			if left := grace - age; left > 0 && (wait < 0 || left < wait) {
				wait = left
			}
		}
		if !hedgeAt.IsZero() {
			if left := max(time.Until(hedgeAt), time.Millisecond); wait < 0 || left < wait {
				wait = left
			}
		}
		var tick <-chan time.Time
		if wait >= 0 {
			timer.Reset(wait)
			tick = timer.C
		}
		select {
		case item := <-results:
			if ctx.Err() != nil {
				dropped = append(dropped, item)
				return RankedWindowOutcome[R]{Winner: -1, Lead: lead, Err: ctx.Err()}
			}
			record(item)
		case <-tick:
		case <-ctx.Done():
			return RankedWindowOutcome[R]{Winner: -1, Lead: lead, Err: ctx.Err()}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	return RankedWindowOutcome[R]{Winner: -1, Lead: lead}
}

// validateRanked validates candidates in rank order with a sliding window
// powered by RunRankedWindow.
func (s *Service) validateRanked(ctx context.Context, cands []candidate, tr *Trace) (int, []byte, error) {
	ctx, cancelBudget := context.WithTimeout(ctx, validationBudget)
	defer cancelBudget()
	attempts := &atomic.Int64{}
	ctx = context.WithValue(ctx, healthAttemptsKey{}, attempts)
	if len(cands) == 1 && len(cands[0].fallbacks) > 0 && !cands[0].verified && !cands[0].rel.OpensOnDemand() {
		ctx = context.WithValue(ctx, singleGroupHedgeKey{}, true)
	}
	margin := s.resolverCfg().SourceScoreMargin
	if margin <= 0 {
		margin = defaultSourceScoreMargin
	}
	const width, wideWidth = 2, 4
	limit := min(len(cands), maxHealthAttempts)
	budgetHit := false
	var transient error
	// An unattended search stops at the first on-demand candidate.
	foregroundOnly := false

	// addonHedge reports whether cands[next] may start beside a lead add-on
	// stream still being checked: Play is waiting, the lead is the only check
	// running and its add-on marks it debrid-cached, it has run
	// onDemandHedgeDelay, and next is native or another cached add-on stream.
	addonHedge := func(lead, next, inflight int, leadStarted time.Time, leadDone bool) (bool, time.Time) {
		if lead >= next || leadDone || inflight != 1 || !core.OnDemandStreamsAllowed(ctx) || !core.OnDemandCached(cands[lead].rel) {
			return false, time.Time{}
		}
		if c := cands[next].rel; c.OpensOnDemand() && !core.OnDemandCached(c) {
			return false, time.Time{}
		}
		if at := leadStarted.Add(onDemandHedgeDelay); time.Now().Before(at) {
			return false, at
		}
		return true, time.Time{}
	}

	isBudgetSkip := func(v validation) bool {
		return errors.Is(v.err, errHealthBudget) && !v.healthChecked && len(v.nzb) == 0 && v.promoted == nil && len(v.fallbacks) == 0
	}

	outcome := RunRankedWindow(ctx, RankedWindowConfig[validation]{
		Count:     limit,
		Width:     width,
		WideWidth: wideWidth,
		Grace:     rankedSourceGrace,
		Run: func(workCtx context.Context, i int, finalWave bool) validation {
			return s.runValidation(workCtx, cands[i], i, finalWave)
		},
		Succeeded: func(v validation) bool {
			return v.healthy && v.err == nil
		},
		IgnoreFailure: isBudgetSkip,
		ConfirmedFailure: func(v validation) bool {
			return v.err == nil || permanentFailure(v.err)
		},
		Equivalent: func(lead, other int) bool {
			return equivalentHedge(cands[lead], cands[other], margin)
		},
		ImmediateOvertake: func(lead, other int) bool {
			leadUncachedTorrent := cands[lead].keys.addonOrder == 0 && protocolOf(cands[lead].rel) == core.ProtocolTorrent && !s.knownCached(cands[lead].rel)
			return leadUncachedTorrent &&
				cands[other].keys.sourceBand == cands[lead].keys.sourceBand &&
				s.knownCached(cands[other].rel) &&
				cands[other].policy.Fallback <= cands[lead].policy.Fallback
		},
		SpendsGrab: func(i int) bool {
			return spendsGrab(cands[i].rel)
		},
		HedgeDelay: func(i int) time.Duration {
			return s.healthHedgeDelay(cands[i].rel)
		},
		AdjustStart: func(i int, now time.Time) time.Time {
			if prepared := cands[i].prepared; prepared != nil && !prepared.started.IsZero() {
				// Discovery already spent part (or all) of this source's grace.
				// Reusing the check must not restart that grace period.
				now = prepared.started
			}
			if gs := cands[i].graceStart; !gs.IsZero() && gs.Before(now) {
				// Descriptor discovery for this source overran the grace while
				// an equivalent was already healthy; that time counts too.
				now = gs
			}
			return now
		},
		BeforeLaunch: func(lead, next, inflight int, leadStarted time.Time, leadDone bool) (LaunchAction, time.Time) {
			// A verified or on-demand lead runs alone: reusing a known working
			// release must not start speculative downloads, and each on-demand
			// open makes an add-on fetch a release.
			if next > lead && lead < limit && (cands[lead].verified || cands[lead].rel.OpensOnDemand()) && !leadDone {
				if ok, at := addonHedge(lead, next, inflight, leadStarted, leadDone); !ok {
					return LaunchHold, at
				}
			}
			if cands[next].rel.OpensOnDemand() {
				if !core.OnDemandAllowedAt(ctx, cands[next].rel, next) {
					foregroundOnly = true
					return LaunchHold, time.Time{}
				}
				if inflight > 0 {
					if ok, at := addonHedge(lead, next, inflight, leadStarted, leadDone); !ok {
						return LaunchHold, at
					}
				}
			}
			if core.IsSpeculative(ctx) && next >= speculativeHealthAttempts {
				budgetHit = true
				return LaunchHold, time.Time{}
			}
			if pastPlayDeadlineWithUnverified(ctx) {
				return LaunchHold, time.Time{}
			}
			if attempts.Load() >= int64(maxHealthAttempts) {
				budgetHit = true
				return LaunchHold, time.Time{}
			}
			return LaunchAllow, time.Time{}
		},
		OnResult: func(i int, v validation) {
			if v.budgetExhausted {
				budgetHit = true
			}
			origLeader := cands[i]
			if len(v.fallbacks) > 0 {
				if fterr := s.applyFallbackValidations(ctx, tr, v.fallbacks); fterr != nil && v.promoted == nil {
					transient = errors.Join(transient, fterr)
				}
			}
			if v.promoted != nil {
				lct := &tr.Candidates[origLeader.trace]
				lct.Attempted = true
				lct.Rejected = true
				if lct.Reason == "" {
					lct.Reason = "health: unhealthy"
				}
				cands[i] = *v.promoted
			} else if v.rel.ID != "" {
				cands[i].rel = v.rel
			}
			if v.promoted == nil {
				noteValidation(ctx, cands[i].rel, v)
			}
			if isBudgetSkip(v) {
				budgetHit = true
				return
			}
			ct := &tr.Candidates[cands[i].trace]
			ct.Attempted = true
			ct.FetchDuration = v.fetchDuration
			ct.HealthDuration = v.healthDuration
			ct.HealthChecked = v.healthChecked
			ct.Healthy = v.healthy && v.err == nil
			if ct.Healthy {
				return
			}
			ct.Rejected = true
			ct.err = v.err
			if v.err != nil {
				ct.Reason = candidateErrorReason(v.err)
			} else {
				ct.Reason = "health: unhealthy"
			}
			if v.err != nil && !permanentFailure(v.err) {
				if !errors.Is(v.err, errHealthBudget) {
					transient = errors.Join(transient, v.err)
				}
			} else {
				s.recordFailedValidation(ctx, origLeader.rel, v)
				s.recordReputation(ctx, origLeader.rel.Indexer, false)
			}
		},
		OnDrop: func(_ int, v validation, recorded bool) {
			if !recorded {
				// Checks cut off by the budget or cancellation may have fetched
				// their NZB; record their outcomes without double-noting checks
				// already passed to OnResult.
				noteDroppedValidation(ctx, cands, v)
			}
		},
	})

	if outcome.Err != nil {
		return -1, nil, outcome.Err
	}
	if outcome.Winner >= 0 {
		v := outcome.Result
		if v.index != outcome.Lead {
			ct := &tr.Candidates[cands[v.index].trace]
			ct.Route = strings.TrimPrefix(strings.TrimSpace(ct.Route+"; healthy equivalent after preferred source exceeded grace"), "; ")
			tr.Candidates[cands[outcome.Lead].trace].Attempted = true
		}
		if v.healthChecked && !v.healthCached {
			s.markVerifiedKeys(ctx, s.allVerificationKeys(cands[v.index].rel, v.extraContentKeys...))
			s.noteStartup(cands[v.index].rel, max(v.fetchDuration+v.healthDuration, time.Microsecond))
		}
		s.recordReputation(ctx, cands[v.index].rel.Indexer, true)
		return v.index, v.nzb, nil
	}
	if foregroundOnly {
		return -1, nil, errors.Join(transient, errOnDemandForeground)
	}
	if transient == nil && (budgetHit || len(cands) > limit || (core.IsSpeculative(ctx) && len(cands) > speculativeHealthAttempts)) {
		return -1, nil, errHealthBudget
	}
	return -1, nil, transient
}

package playback

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestOpenInitialContinuesPastFourFailuresToWorkingRelease verifies the lasting
// invariant that an explicitly increased Remix cap does not give up after 4 candidates
// when more candidates remain in the search pool.
func TestOpenInitialContinuesPastFourFailuresToWorkingRelease(t *testing.T) {
	const total = 10
	const workingIdx = 7 // 8th release (well past the old default cap of 4)
	rels := make([]*fakeRelease, 0, total)
	ids := make([]string, 0, total)
	for i := range total {
		id := fmt.Sprintf("rel-%02d", i)
		r := mkv(id, 100_000, 90*time.Minute)
		if i < workingIdx {
			r.OpenErr = core.ErrArticleMissing
		}
		rels = append(rels, r)
		ids = append(ids, id)
	}
	h := newHarness(t, rels, ids, func(h *harness, _ *Options) { h.cfg.Resolver.FailoverAttempts = 10 })
	ps := h.prepare(owner, movieKey, "c1")
	if ps.Resolution == nil || ps.Resolution.Release.ID != fmt.Sprintf("rel-%02d", workingIdx) {
		t.Fatalf("played %+v, want rel-%02d", ps.Resolution, workingIdx)
	}
}

type continuationResolver struct {
	core.Resolver
	mu           sync.Mutex
	moreLeft     int
	resolveCalls int
	excludeSeen  [][]string
}

func (r *continuationResolver) Resolve(ctx context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	r.mu.Lock()
	r.resolveCalls++
	r.excludeSeen = append(r.excludeSeen, slices.Clone(req.Exclude))
	if r.moreLeft > 0 {
		r.moreLeft--
		r.mu.Unlock()
		return nil, fmt.Errorf("source validation limit reached; %w", core.ErrMoreCandidates)
	}
	r.mu.Unlock()
	return r.Resolver.Resolve(ctx, req)
}

// TestOpenInitialContinuesAcrossResolverMoreCandidates verifies that playback
// continues asking the resolver for the next batch when a health-validation
// pass stops with core.ErrMoreCandidates rather than aborting Play.
func TestOpenInitialContinuesAcrossResolverMoreCandidates(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("winner", 100_000, 90*time.Minute)}, []string{"winner"})
	cr := &continuationResolver{Resolver: h.resolver, moreLeft: 2}
	h.m.deps.Resolver = cr

	ps := h.prepare(owner, movieKey, "c1")
	if ps.Resolution == nil || ps.Resolution.Release.ID != "winner" {
		t.Fatalf("played %+v, want winner", ps.Resolution)
	}
	cr.mu.Lock()
	calls := cr.resolveCalls
	cr.mu.Unlock()
	if calls != 3 {
		t.Fatalf("Resolve calls = %d, want 3 (2 continuation passes + 1 winner)", calls)
	}
}

// TestOpenInitialRacesAlternatesAfterPrimaryFails proves two invariants:
//  1. When attempt 0 succeeds, zero alternate fetches are started (happy path).
//  2. When attempt 0 fails and FetchNZB is available, alternates open in
//     parallel rather than strictly one after another.
func TestOpenInitialRacesAlternatesAfterPrimaryFails(t *testing.T) {
	t.Run("happy path fetches zero alternates", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{
			mkv("p0", 100_000, 90*time.Minute),
			mkv("a1", 100_000, 90*time.Minute),
			mkv("a2", 100_000, 90*time.Minute),
		}, []string{"p0", "a1", "a2"})
		var altFetches atomic.Int32
		h.m.deps.FetchNZB = func(_ context.Context, r core.Release) ([]byte, error) {
			altFetches.Add(1)
			return []byte("nzb-" + r.ID), nil
		}
		ps := h.prepare(owner, movieKey, "c1")
		if ps.Resolution.Release.ID != "p0" {
			t.Fatalf("played %s, want p0", ps.Resolution.Release.ID)
		}
		if got := altFetches.Load(); got != 0 {
			t.Fatalf("FetchNZB called %d times on happy path, want 0", got)
		}
	})

	t.Run("alternates race concurrently after attempt 0 fails", func(t *testing.T) {
		p0 := mkv("p0", 100_000, 90*time.Minute)
		p0.OpenErr = core.ErrArticleMissing
		a1 := mkv("a1", 100_000, 90*time.Minute)
		a2 := mkv("a2", 100_000, 90*time.Minute)
		h := newHarness(t, []*fakeRelease{p0, a1, a2}, []string{"p0", "a1", "a2"})

		// a1 waits until a2 has entered FetchNZB, proving both alternates are
		// in flight concurrently (a sequential loop would deadlock until ctx
		// cancelled).
		a2Started := make(chan struct{})
		var closeOnce sync.Once
		h.m.deps.FetchNZB = func(ctx context.Context, r core.Release) ([]byte, error) {
			switch r.ID {
			case "a1":
				select {
				case <-a2Started:
					return nil, core.ErrArticleMissing
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case "a2":
				closeOnce.Do(func() { close(a2Started) })
				return []byte("nzb-a2"), nil
			default:
				return []byte("nzb-" + r.ID), nil
			}
		}

		ps := h.prepare(owner, movieKey, "c1")
		if ps.Resolution == nil || ps.Resolution.Release.ID != "a2" {
			t.Fatalf("played %+v, want a2", ps.Resolution)
		}
	})
}

// TestNextResolutionPropagatesFailedAlternateFetchesToResolveExclude verifies
// that alternates whose FetchNZB fails are appended to the caller's tried list
// so subsequent Resolve calls exclude them instead of repeating them.
func TestNextResolutionPropagatesFailedAlternateFetchesToResolveExclude(t *testing.T) {
	p0 := mkv("p0", 100_000, 90*time.Minute)
	p0.OpenErr = core.ErrArticleMissing
	a1 := mkv("a1", 100_000, 90*time.Minute)
	good := mkv("good", 100_000, 90*time.Minute)
	h := newHarness(t, []*fakeRelease{p0, a1, good}, []string{"p0", "a1"})

	// Resolve call 1 returns p0 with alternate a1 (1 alternate -> sequential
	// nextResolutionTracking). After a1 fails FetchNZB, Resolve call 2 must
	// exclude both p0 and a1 and return good.
	cr := &continuationResolver{Resolver: h.resolver}
	h.m.deps.Resolver = cr
	h.m.deps.FetchNZB = func(_ context.Context, r core.Release) ([]byte, error) {
		if r.ID == "a1" {
			h.resolver.mu.Lock()
			h.resolver.candidates = append(h.resolver.candidates, core.Release{ID: "good", Name: "Release.good", Size: good.Size})
			h.resolver.mu.Unlock()
			return nil, errors.New("indexer 502")
		}
		return []byte("nzb-" + r.ID), nil
	}

	ps := h.prepare(owner, movieKey, "c1")
	if ps.Resolution == nil || ps.Resolution.Release.ID != "good" {
		t.Fatalf("played %+v, want good", ps.Resolution)
	}
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if len(cr.excludeSeen) < 2 {
		t.Fatalf("expected >= 2 Resolve calls, got %d", len(cr.excludeSeen))
	}
	secondExclude := cr.excludeSeen[1]
	if !slices.Contains(secondExclude, "p0") || !slices.Contains(secondExclude, "a1") {
		t.Fatalf("second Resolve excluded %v, want both p0 and a1", secondExclude)
	}
}

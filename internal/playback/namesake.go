package playback

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// namesakeRuntimeTolerance is the allowed difference between a probed file
// and the catalogue runtime: the larger of these two bounds.
const (
	namesakeRuntimeSlack      = 8 * time.Minute
	namesakeRuntimeSlackRatio = 0.12
)

// NamesakeCatalog, implemented by the catalogue, reports whether another
// catalogue title namesakes key. The answer is an on-demand indexed lookup;
// playback caches it per title for the life of the manager so a title's
// reselection does not repeat it.
type NamesakeCatalog interface {
	Namesakes(ctx context.Context, key core.RatingKey) (bool, error)
}

// RuntimeUpdater, implemented by the catalogue, persists a consensus probed
// runtime back onto catalog_titles when upstream metadata is wrong.
type RuntimeUpdater interface {
	UpdateRuntime(ctx context.Context, key core.RatingKey, runtime time.Duration) error
}

const maxProbedRuntimes = 4096

func (m *Manager) probedRuntimeLookup(key core.RatingKey) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.probedRuntimes[key]
	return d, ok
}

func (m *Manager) titleWithEffectiveRuntime(t *core.Title) *core.Title {
	if t == nil || t.KindLevel != "" || t.Key == 0 {
		return t
	}
	d, ok := m.probedRuntimeLookup(t.Key)
	if !ok || d <= 0 || d == t.Runtime {
		return t
	}
	cp := *t
	cp.Runtime = d
	return &cp
}

func (m *Manager) recordConsensusRuntime(ctx context.Context, t *core.Title, d time.Duration) {
	if t == nil || t.KindLevel != "" || t.Key == 0 || d <= 0 {
		return
	}
	rounded := d.Round(time.Second)
	if rounded <= 0 {
		return
	}
	m.mu.Lock()
	if m.probedRuntimes == nil {
		m.probedRuntimes = map[core.RatingKey]time.Duration{}
	}
	if len(m.probedRuntimes) >= maxProbedRuntimes {
		clear(m.probedRuntimes)
	}
	m.probedRuntimes[t.Key] = rounded
	m.mu.Unlock()
	if updater, ok := m.deps.Catalog.(RuntimeUpdater); ok {
		if err := updater.UpdateRuntime(ctx, t.Key, rounded); err != nil {
			m.log.Debug("update consensus runtime failed", "key", t.Key, "runtime", rounded, "err", err)
		}
	}
}

// namesakeRequest reports whether t is one of two namesake catalogue titles.
// Plugin kind projections (KindLevel set) are not catalogue titles and are
// never gated.
func (m *Manager) namesakeRequest(ctx context.Context, t *core.Title) bool {
	if t == nil || t.KindLevel != "" || t.Key == 0 {
		return false
	}
	cat, ok := m.deps.Catalog.(NamesakeCatalog)
	if !ok {
		return false
	}
	if ans, ok := m.namesakeLookup(t.Key); ok {
		return ans
	}
	ans, err := cat.Namesakes(ctx, t.Key)
	if err != nil {
		m.log.Debug("namesake lookup failed", "key", t.Key, "err", err)
		return false
	}
	m.rememberNamesake(t.Key, ans)
	return ans
}

// namesakeLookup returns a cached answer for key, if the manager holds one.
func (m *Manager) namesakeLookup(key core.RatingKey) (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ans, ok := m.namesake[key]
	return ans, ok
}

// rememberNamesake caches a namesake answer for key.
func (m *Manager) rememberNamesake(key core.RatingKey, ans bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.namesake == nil {
		m.namesake = map[core.RatingKey]bool{}
	}
	m.namesake[key] = ans
}

// namesakeTitleConflict reports whether r's reported identifiers name a
// different catalogue title than t. Only explicit identifier conflicts count;
// missing identifiers carry no evidence.
func namesakeTitleConflict(t *core.Title, r core.Release) bool {
	if t == nil || t.IDs == (core.IDs{}) || r.TitleIDs == (core.IDs{}) {
		return false
	}
	_, conflict := releaseparse.CompareTitleIDs(r.TitleIDs, t.IDs)
	return conflict
}

// namesakeDurationMismatch reports whether a probed duration contradicts the
// catalogue runtime by more than the tolerance.
func namesakeDurationMismatch(runtime, probed time.Duration) bool {
	if runtime <= 0 || probed <= 0 {
		return false
	}
	allow := math.Max(namesakeRuntimeSlack.Seconds(), namesakeRuntimeSlackRatio*runtime.Seconds())
	return math.Abs(probed.Seconds()-runtime.Seconds()) > allow
}

// releaseNamesakeRejected gates a candidate for an ambiguous (namesake)
// request: a release whose reported identifiers name the other title is
// rejected before opening. Unambiguous titles are never gated.
func (m *Manager) releaseNamesakeRejected(ctx context.Context, s *session, r core.Release) bool {
	if s.title == nil || !m.namesakeRequest(ctx, s.title) {
		return false
	}
	return namesakeTitleConflict(s.title, r)
}

// releaseRuntimeMismatch checks actual container facts independently of catalogue
// namesake coverage. Only explicit multi-episode filenames scale the runtime;
// a season pack's selected file is normally a single episode.
func releaseRuntimeMismatch(t *core.Title, r core.Release, probed time.Duration) bool {
	if t == nil || t.KindLevel != "" || t.Runtime <= 0 || probed <= 0 {
		return false
	}
	expected := t.Runtime
	if t.Type == core.TypeEpisode {
		info := releaseparse.Parse(r.Name)
		if len(info.Episodes) > 1 {
			expected *= time.Duration(len(info.Episodes))
		}
	}
	slack := math.Max((8 * time.Minute).Seconds(), 0.35*expected.Seconds())
	return math.Abs(probed.Seconds()-expected.Seconds()) > slack
}

// segmentStart is where the requested episode begins inside a combined
// multi-episode file: for the second episode of an "S01E01-E02" file it is
// one segment in, so a server-started stream starts at that episode rather
// than replaying the first. The segment length is the probed file duration
// divided by the episodes the file names (broadcast segments run equal),
// falling back to the catalogue runtime before a probe. It is zero when
// the file holds only the requested episode, when the requested episode is
// the first it names, or when it does not name the requested episode at
// all — acceptance of such a release is the resolver's decision, never
// widened here. A season pack's selected file is normally a single
// episode, so packs never offset.
func segmentStart(t *core.Title, r core.Release, probed time.Duration) time.Duration {
	if t == nil || t.KindLevel != "" || t.Type != core.TypeEpisode || r.Info.FullSeason {
		return 0
	}
	episodes := r.Info.Episodes
	if len(episodes) < 2 {
		episodes = releaseparse.Parse(r.Name).Episodes
	}
	if len(episodes) < 2 {
		return 0
	}
	want := t.Episode
	if r.Target != nil && r.Target.Episode > 0 {
		want = r.Target.Episode
	}
	idx := slices.Index(episodes, want)
	if idx <= 0 {
		return 0
	}
	segment := t.Runtime
	if probed > 0 {
		segment = probed / time.Duration(len(episodes))
	}
	if segment <= 0 {
		return 0
	}
	start := time.Duration(idx) * segment
	if probed > 0 && start >= probed {
		return 0
	}
	return start
}

// errRuntimeMismatchDeferred wraps core.ErrReleaseRejected when a runtime
// mismatch is held back from resolver_rejected and openFailures until either
// a catalogue-matching release succeeds or cross-candidate consensus decides
// whether the catalogue runtime itself is wrong.
var errRuntimeMismatchDeferred = fmt.Errorf("runtime mismatch deferred: %w", core.ErrReleaseRejected)

type runtimeDecision int

const (
	runtimeRejectImmediate runtimeDecision = iota
	runtimeDefer
	runtimeHoldFallback
	runtimeAcceptLive
)

type runtimeVote struct {
	release  core.Release
	duration time.Duration
	matched  bool
}

type runtimePending struct {
	release  core.Release
	duration time.Duration
}

// runtimeTracker gathers probed durations across independent candidate
// releases for one Play or warm-up attempt. When upstream metadata (e.g.
// TMDB) reports a wrong runtime, independent releases that all agree on the
// true duration override the stale catalogue runtime instead of being
// permanently rejected one by one.
type runtimeTracker struct {
	mu           sync.Mutex
	usable       func(*generation, *core.Resolution) bool
	votes        []runtimeVote
	pending      []runtimePending
	matchedCount int
	consensus    bool
	consensusDur time.Duration
	fallbackGen  *generation
	fallbackRes  *core.Resolution
	fallbackDur  time.Duration
}

type runtimeTrackerKey struct{}

func newRuntimeTracker(usable func(*generation, *core.Resolution) bool) *runtimeTracker {
	return &runtimeTracker{usable: usable}
}

func withRuntimeTracker(ctx context.Context, rt *runtimeTracker) context.Context {
	if rt == nil {
		return ctx
	}
	return context.WithValue(ctx, runtimeTrackerKey{}, rt)
}

func runtimeTrackerFrom(ctx context.Context) *runtimeTracker {
	rt, _ := ctx.Value(runtimeTrackerKey{}).(*runtimeTracker)
	return rt
}

// durationsAgree reports whether two probed media durations represent the same
// cut (covering small encode differences and 24fps/25fps PAL speedup while
// separating genuinely distinct films or episodes).
func durationsAgree(a, b time.Duration) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	hi := math.Max(a.Seconds(), b.Seconds())
	allow := math.Max((3 * time.Minute).Seconds(), 0.05*hi)
	return math.Abs(a.Seconds()-b.Seconds()) <= allow
}

// runtimeConsensusEligible guards against trailers, samples, multi-episode
// files, or full-season rips ever forming a consensus runtime.
func runtimeConsensusEligible(t *core.Title, r core.Release, probed time.Duration) bool {
	if t == nil || t.KindLevel != "" || t.Key == 0 || t.Runtime <= 0 || probed <= 0 {
		return false
	}
	if namesakeTitleConflict(t, r) {
		return false
	}
	if t.Type == core.TypeEpisode {
		if len(releaseparse.Parse(r.Name).Episodes) > 1 {
			return false
		}
	}
	minDur := max(5*time.Minute, time.Duration(0.40*float64(t.Runtime)))
	if t.Type == core.TypeMovie && t.Runtime >= 30*time.Minute && minDur < 15*time.Minute {
		minDur = 15 * time.Minute
	}
	if probed < minDur || probed > 3*t.Runtime {
		return false
	}
	return true
}

// sameRuntimeRelease reports whether two releases are reposts/mirrors of the
// same upload, so reposts of one wrong file cannot manufacture consensus.
func sameRuntimeRelease(a, b core.Release) bool {
	if a.ID != "" && a.ID == b.ID {
		return true
	}
	if a.SourceHash != "" && a.SourceHash == b.SourceHash {
		return true
	}
	if a.InfoHash != "" && strings.EqualFold(a.InfoHash, b.InfoHash) {
		return true
	}
	if a.GUID != "" && a.GUID == b.GUID && a.Indexer != "" && a.Indexer == b.Indexer {
		return true
	}
	an := strings.Join(releaseparse.Tokens(a.Name), "")
	bn := strings.Join(releaseparse.Tokens(b.Name), "")
	return an != "" && an == bn
}

func (rt *runtimeTracker) hasVoteLocked(r core.Release) bool {
	for _, v := range rt.votes {
		if sameRuntimeRelease(v.release, r) {
			return true
		}
	}
	return false
}

func (rt *runtimeTracker) clusterLocked(target time.Duration) (agree int, median time.Duration) {
	var durs []time.Duration
	for _, v := range rt.votes {
		if !v.matched && durationsAgree(v.duration, target) {
			durs = append(durs, v.duration)
		}
	}
	if len(durs) == 0 {
		return 0, 0
	}
	slices.Sort(durs)
	return len(durs), durs[len(durs)/2].Round(time.Second)
}

func (rt *runtimeTracker) observeMatch(r core.Release, probed time.Duration) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.hasVoteLocked(r) {
		rt.votes = append(rt.votes, runtimeVote{release: r, duration: probed, matched: true})
		rt.matchedCount++
	}
}

func (rt *runtimeTracker) observeMismatch(r core.Release, probed time.Duration, eligible bool) (runtimeDecision, time.Duration, []core.Release) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.consensus && durationsAgree(rt.consensusDur, probed) {
		return runtimeAcceptLive, rt.consensusDur, nil
	}
	if !eligible {
		return runtimeRejectImmediate, 0, nil
	}
	seenPending := false
	for _, p := range rt.pending {
		if p.release.ID == r.ID && r.ID != "" {
			seenPending = true
			break
		}
	}
	if !seenPending {
		rt.pending = append(rt.pending, runtimePending{release: r, duration: probed})
	}
	if !rt.hasVoteLocked(r) {
		rt.votes = append(rt.votes, runtimeVote{release: r, duration: probed})
	}
	agree, med := rt.clusterLocked(probed)
	total := len(rt.votes)
	if rt.matchedCount == 0 && agree >= 3 && agree*2 > total {
		rt.consensus = true
		rt.consensusDur = med
		var disagree []core.Release
		for _, p := range rt.pending {
			if !durationsAgree(med, p.duration) {
				disagree = append(disagree, p.release)
			}
		}
		rt.pending = nil
		return runtimeAcceptLive, med, disagree
	}
	if rt.matchedCount == 0 && agree >= 2 && agree*2 > total && rt.fallbackGen == nil {
		return runtimeHoldFallback, med, nil
	}
	return runtimeDefer, 0, nil
}

func (rt *runtimeTracker) tryStashFallback(g *generation, res *core.Resolution, probed time.Duration) bool {
	if rt.usable != nil && !rt.usable(g, res) {
		return false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.fallbackGen != nil || rt.matchedCount > 0 || rt.consensus {
		return false
	}
	rt.fallbackGen = g
	rt.fallbackRes = res
	rt.fallbackDur = probed
	return true
}

func (rt *runtimeTracker) takeFallback() (*generation, *core.Resolution, time.Duration, []core.Release, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.fallbackGen == nil || rt.matchedCount > 0 {
		return nil, nil, 0, nil, false
	}
	agree, med := rt.clusterLocked(rt.fallbackDur)
	if agree < 2 || agree*2 <= len(rt.votes) {
		return nil, nil, 0, nil, false
	}
	rt.consensus = true
	rt.consensusDur = med
	var disagree []core.Release
	for _, p := range rt.pending {
		if !durationsAgree(med, p.duration) {
			disagree = append(disagree, p.release)
		}
	}
	rt.pending = nil
	g, res := rt.fallbackGen, rt.fallbackRes
	rt.fallbackGen, rt.fallbackRes = nil, nil
	return g, res, med, disagree, true
}

func (rt *runtimeTracker) commitPendingRejections(ctx context.Context, m *Manager, s *session) {
	if s == nil || s.title == nil {
		return
	}
	rt.mu.Lock()
	pending := rt.pending
	consensus := rt.consensus
	consensusDur := rt.consensusDur
	rt.pending = nil
	rt.mu.Unlock()
	for _, p := range pending {
		if consensus && durationsAgree(consensusDur, p.duration) {
			continue
		}
		if err := m.RejectRelease(ctx, s.title.Key, p.release); err != nil {
			m.log.Debug("persist runtime rejection failed", "key", s.title.Key, "release", p.release.ID, "err", err)
		}
		m.rememberOpenFailure(s, p.release, core.ErrReleaseRejected)
	}
}

func (rt *runtimeTracker) close(m *Manager) {
	rt.mu.Lock()
	g := rt.fallbackGen
	rt.fallbackGen = nil
	rt.fallbackRes = nil
	rt.mu.Unlock()
	if g != nil {
		m.dropGeneration(g)
	}
}

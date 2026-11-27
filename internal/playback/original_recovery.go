package playback

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// ReplaceLostOriginal runs only after a real range failed. The broker holds
// its owner gate while admitting and publishing the new client grant.
func (m *Manager) ReplaceLostOriginal(ctx context.Context, id, revision string, position time.Duration, explicit bool, admit func(*core.PlaySession) float64) (*core.PlaySession, time.Duration, error) {
	s := m.lookup(id)
	if s == nil || revision == "" {
		return nil, position, core.ErrSourceChanged
	}
	if err := lock(ctx, s.failSem); err != nil {
		return nil, position, err
	}
	defer unlock(s.failSem)
	before := s.snapshot(m.now())
	s.mu.Lock()
	old := s.cur
	if s.closed || old == nil || !errors.Is(old.sourceFailure(), core.ErrOriginalLost) || core.SourceRevision(before) != revision {
		s.mu.Unlock()
		return nil, position, core.ErrSourceChanged
	}
	replaceable := !explicit && s.binding != core.SourceBindingExplicit && (!s.exact || s.binding == core.SourceBindingAuto || s.binding == core.SourceBindingRemembered)
	alreadyTried := s.originalRecoveryTried
	alreadyRejected := old.originalRejected
	old.originalRejected = true
	alternates, tried := s.alternates, slices.Clone(s.tried)
	s.originalRecoveryTried = true
	s.mu.Unlock()
	if !alreadyRejected {
		m.forgetUnreadable(s, old, "provider lost original file")
	}
	if alreadyTried {
		return nil, position, core.ErrVersionUnreadable
	}

	if !replaceable || m.deps.FetchNZB == nil {
		return nil, position, core.ErrVersionUnreadable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.stopCtx, cancel)
	defer stop()
	search, stopSearch := context.WithTimeout(ctx, 15*time.Second)
	defer stopSearch()
	attempts := 0
	var winner *generation
	var selected *core.PlaySession
	var best float64
	defer func() {
		if winner != nil {
			m.dropGeneration(winner)
		}
	}()
	for _, alt := range alternates {
		if search.Err() != nil {
			break
		}
		if alt.Protocol != core.ProtocolTorrent || alt.OpensOnDemand() || alt.ID == "" || alt.ID == old.releaseID() ||
			slices.Contains(tried, alt.ID) || !strings.EqualFold(alt.Info.Edition, old.res.Release.Info.Edition) ||
			!m.ownerPinAllows(s, alt.ID) || m.titleReleaseRejected(search, s.ident.key, alt) ||
			m.releaseNamesakeRejected(search, s, alt) || !releaseMatchesTitle(alt, s.title) {
			continue
		}
		if !m.attemptAllowed(attempts) {
			break
		}
		attempts++
		payload, err := m.deps.FetchNZB(search, alt)
		if err != nil {
			continue
		}
		res := &core.Resolution{Title: s.ident.key, Release: alt, NZB: payload, ResolvedAt: m.now()}
		g, err := m.openGeneration(search, s, res, func(int64) core.MediaInfo { return core.MediaInfo{Estimated: true} })
		if err != nil {
			continue
		}
		next := *before
		next.Info, next.Source, next.Resolution = g.infoSnapshot(), g.src, res
		next.Origin = sourceOrigin(g.src, res.Release.Protocol)
		next.SegmentStart = segmentStart(s.title, res.Release, next.Info.Duration)
		next.RequiresRemux = false
		oldDuration, duration := before.Info.Duration, next.Info.Duration
		if next.Info.Estimated || oldDuration <= 0 || duration <= 0 ||
			max(duration-oldDuration, oldDuration-duration) > max(2*time.Second, oldDuration/100) ||
			core.SourceRevision(&next) == "" {
			m.dropGeneration(g)
			continue
		}
		score := admit(&next)
		if score <= best {
			m.dropGeneration(g)
			continue
		}
		if winner != nil {
			m.dropGeneration(winner)
		}
		winner, selected, best = g, &next, score
		if best == 1 {
			break
		}
	}
	if winner == nil {
		if err := ctx.Err(); err != nil {
			return nil, position, err
		}
		return nil, position, core.ErrVersionUnreadable
	}

	at := m.alignForFailover(ctx, s, old, winner, position)
	newRevision := core.SourceRevision(selected)
	selected.OriginalRevision = newRevision
	m.pinLive(winner.releaseID())
	s.mu.Lock()
	if s.closed || ctx.Err() != nil {
		s.mu.Unlock()
		m.unpinLive(winner.releaseID())
		return nil, position, core.ErrSourceChanged
	}
	g := winner
	winner = nil
	s.cur = g
	s.originalRevision = newRevision
	s.requiresRemux = false
	s.tried = append(tried, old.releaseID())
	s.failovers++
	s.mu.Unlock()
	m.dropGeneration(old)
	m.unpinLive(old.releaseID())
	m.saveResume(ctx, s, g)
	s.resetSpeed(m.now(), m.opts.SpeedGrace)
	m.emitSourceFailover(s, old, g, false, "provider lost original file")
	m.emitEvent("failover", s, nil)
	return selected, at, nil
}

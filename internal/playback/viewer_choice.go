package playback

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Mirrors internal/state's StartedThreshold (5% meaningful play) and
// WatchedThreshold (90% completion/scrobble), with a 1-minute fallback when
// duration is unknown, so quick samples never overwrite a viewer's remembered
// version choice.
const (
	viewerChoiceStartFraction    = 0.05
	viewerChoiceCompleteFraction = 0.90
	viewerChoiceFallbackDuration = time.Minute
	// viewerChoiceMaxStep caps a single progress delta credited toward real
	// playback; larger jumps are forward seeks rather than continuous watching.
	viewerChoiceMaxStep = 2 * time.Minute
)

type viewerChoiceRecorder interface {
	RecordViewerChoice(ctx context.Context, userID int64, title *core.Title, selector string, rel core.Release, probed *core.MediaInfo) error
}

type viewerChoiceReader interface {
	ActiveViewerChoiceRelease(core.ResolveRequest) string
}

func isAutoVersionSelector(prefer string) bool {
	return core.IsAutoVersionSelector(prefer)
}

// NoteStartPosition anchors explicit-version real-play tracking at pos (used
// when opening at a resume offset, switching versions mid-stream, or seeking)
// so a jump or quick sample does not count as real playback on the version.
func (m *Manager) NoteStartPosition(playID string, pos time.Duration) {
	if m == nil || playID == "" {
		return
	}
	if pos < 0 {
		pos = 0
	}
	m.mu.Lock()
	s := m.sessions[playID]
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.choiceStartSet = true
	s.choiceLastPos = pos
	s.mu.Unlock()
}

// observeViewerVersionChoice records a viewer's explicit version pick once
// playback on that version crosses the real-play threshold.
func (m *Manager) observeViewerVersionChoice(s *session, position time.Duration, state, prevState string) {
	recorder, ok := m.deps.Resolver.(viewerChoiceRecorder)
	if !ok || s == nil || s.ident.userID <= 0 || s.title == nil || s.caps.Download {
		return
	}
	if kind := s.ident.key.Kind(); kind != core.KindMovie && kind != core.KindEpisode && !s.ident.key.IsPluginKey() {
		return
	}
	if s.prefer == "" || isAutoVersionSelector(s.prefer) {
		return
	}
	resumeAfter := time.Duration(m.config().Server.ResumeAfterSeconds) * time.Second
	s.mu.Lock()
	if s.choiceRecorded || s.cur == nil || s.cur.res == nil || s.cur.res.Release.ID == "" {
		s.mu.Unlock()
		return
	}
	if core.IsExplicitVersionSlotSelector(s.prefer) && !s.cur.res.MatchesVersion(s.prefer) {
		s.mu.Unlock()
		return
	}
	if !s.choiceStartSet {
		s.choiceStartSet = true
		s.choiceLastPos = 0
	}
	if position < s.choiceLastPos {
		// Backward scrub: reset anchor without crediting playback.
		s.choiceLastPos = position
	} else if position > s.choiceLastPos {
		delta := position - s.choiceLastPos
		if delta <= viewerChoiceMaxStep && (state == "playing" || prevState == "playing") {
			s.choicePlayed += delta
		}
		s.choiceLastPos = position
	}
	g := s.cur
	info := g.infoSnapshot()
	dur := info.Duration
	if dur <= 0 && s.title != nil {
		dur = s.title.Runtime
	}
	threshold := viewerChoiceFallbackDuration
	if resumeAfter > 0 {
		threshold = resumeAfter
	} else if dur > 0 {
		threshold = time.Duration(float64(dur) * viewerChoiceStartFraction)
	}
	completed := dur > 0 && float64(position)/float64(dur) >= viewerChoiceCompleteFraction && s.choicePlayed > 0
	shouldRecord := s.choicePlayed >= threshold || completed
	if !shouldRecord {
		s.mu.Unlock()
		return
	}
	s.choiceRecorded = true
	rel := g.res.Release
	selector := s.prefer
	userID := s.ident.userID
	title := *s.title
	s.mu.Unlock()

	var probed *core.MediaInfo
	if !info.Estimated && len(info.Streams) > 0 {
		cloned := core.CloneMediaInfo(info)
		probed = &cloned
	}
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	if err := recorder.RecordViewerChoice(ctx, userID, &title, selector, rel, probed); err != nil && m.log != nil {
		m.log.Debug("viewer version choice not saved", "user", userID, "title", title.Key, "err", err)
	}
}

// rememberedChoiceAllows prevents warm/resume reuse from shadowing a viewer's
// newer remembered title choice (while leaving failover alternates allowed).
func (m *Manager) rememberedChoiceAllows(s *session, releaseID string) bool {
	if reader, ok := m.deps.Resolver.(viewerChoiceReader); ok && s != nil && s.title != nil {
		id := reader.ActiveViewerChoiceRelease(core.ResolveRequest{Title: s.title, Client: s.caps, User: &s.user, Prefer: s.prefer})
		return id == "" || id == releaseID
	}
	return true
}

func (m *Manager) hasLiveOrCachedRelease(userID int64, key core.RatingKey, releaseID string) bool {
	if m == nil || releaseID == "" {
		return false
	}
	if sizer, ok := m.deps.Cache.(releaseSizer); ok && sizer.ReleaseBytes(releaseID) > 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s == nil || s.ident.key != key || (userID > 0 && s.ident.userID != userID) {
			continue
		}
		if g := s.current(); g != nil && g.releaseID() == releaseID {
			return true
		}
	}
	for id, w := range m.warm {
		if w == nil || id.key != key || (userID > 0 && id.userID != userID) {
			continue
		}
		if w.res != nil && w.res.Release.ID == releaseID {
			return true
		}
	}
	return false
}

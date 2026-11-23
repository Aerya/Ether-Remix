package playback

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

var _ core.PlaybackSelectionFence = (*Manager)(nil)

// lockUnlessFailingOver acquires s.failSem for short-lived selection and
// activity checks without stalling client heartbeats behind a slow failover.
func lockUnlessFailingOver(ctx context.Context, s *session) error {
	select {
	case s.failSem <- struct{}{}:
		return nil
	default:
	}
	if s.failingOver.Load() {
		return core.ErrSourceChanged
	}
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case s.failSem <- struct{}{}:
		return nil
	case <-timer.C:
		if s.failingOver.Load() {
			return core.ErrSourceChanged
		}
		return lock(ctx, s.failSem)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithSourceSelection holds the existing source transition fence before fn may
// acquire a manual-intent database writer. No bytes are read or source prepared.
func (m *Manager) WithSourceSelection(ctx context.Context, id, revision string, fn func(core.MediaInfo) error) error {
	s := m.lookup(id)
	if s == nil || revision == "" || fn == nil {
		return core.ErrSourceChanged
	}
	if err := lockUnlessFailingOver(ctx, s); err != nil {
		return err
	}
	defer unlock(s.failSem)
	ps := s.snapshot(m.now())
	if core.SourceRevision(ps) != revision {
		return core.ErrSourceChanged
	}
	s.mu.Lock()
	closed := s.closed || s.cur == nil
	s.mu.Unlock()
	if closed {
		return core.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ps.Info)
}

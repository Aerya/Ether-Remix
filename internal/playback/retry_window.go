package playback

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A Plex client that cannot start a title asks again within seconds, and a
// transcoder reading a dead source fails over again on every read. Each ask
// used to start a complete resolution or failover (dozens of provider calls
// an hour for one title). Concurrent asks already share one flight
// (prepareOwnedWithin); these windows extend that sharing to the moments
// after a flight or failover ended in failure: the same title and profile
// get the same answer for a few seconds instead of a new search.
const (
	// retryFailureWindow is how long a failed preparation or failover
	// answers identical requests without starting another.
	retryFailureWindow = 5 * time.Second
	maxRecentFailures  = 256
)

// prepareFailKey is what makes two preparations the same request: the
// client, title and surface (identity) and the picked version.
type prepareFailKey struct {
	id     identity
	prefer string
	exact  bool
}

type recentFailure struct {
	err error
	at  time.Time
}

// retryableByWindow reports a failure that says nothing is playable right
// now (as opposed to a cancelled request, a stream limit or a slow start,
// which a retry may resolve at once).
func retryableByWindow(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, core.ErrPrepareSlow) {
		return false
	}
	return errors.Is(err, core.ErrNotAvailable) || errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrDebridQuota) ||
		errors.Is(err, core.ErrDebridAuth) || errors.Is(err, core.ErrSourceUnavailable) || errors.Is(err, core.ErrReleaseRejected)
}

// notePrepareFailureLocked remembers a failed preparation. The caller holds m.mu.
func (m *Manager) notePrepareFailureLocked(fk flightKey, err error) {
	if !retryableByWindow(err) {
		return
	}
	now := m.now()
	if m.prepareFailures == nil {
		m.prepareFailures = map[prepareFailKey]recentFailure{}
	}
	if len(m.prepareFailures) >= maxRecentFailures {
		for k, f := range m.prepareFailures {
			if now.Sub(f.at) >= retryFailureWindow {
				delete(m.prepareFailures, k)
			}
		}
		if len(m.prepareFailures) >= maxRecentFailures {
			clear(m.prepareFailures)
		}
	}
	m.prepareFailures[prepareFailKey{fk.id, fk.prefer, fk.exact}] = recentFailure{err: err, at: now}
}

// recentPrepareFailureLocked returns the failure of an identical preparation
// that ended moments ago. The caller holds m.mu.
func (m *Manager) recentPrepareFailureLocked(fk flightKey) error {
	k := prepareFailKey{fk.id, fk.prefer, fk.exact}
	f, ok := m.prepareFailures[k]
	if !ok {
		return nil
	}
	if m.now().Sub(f.at) >= retryFailureWindow {
		delete(m.prepareFailures, k)
		return nil
	}
	return f.err
}

// forgetPrepareFailuresLocked drops the remembered failures of a title (the
// owner pinned, blocked or re-searched it) or, with all, of every title (a
// settings change). The caller holds m.mu.
func (m *Manager) forgetPrepareFailuresLocked(key core.RatingKey, all bool) {
	for k := range m.prepareFailures {
		if all || titleIncludes(key, k.id.key) {
			delete(m.prepareFailures, k)
		}
	}
}

// recentFailoverFailure is a failover that found no working release for a
// generation: the transcoder's next read of the same dead source asks again
// within moments and gets the same answer.
type recentFailoverFailure struct {
	gen *generation
	err error
	at  time.Time
}

// recentFailoverFailure returns the failure of a failover of observed that
// ended less than retryFailureWindow ago, if any.
func (s *session) recentFailoverFailure(observed *generation, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failoverFailure
	if f.gen != observed || f.err == nil || now.Sub(f.at) >= retryFailureWindow {
		return nil
	}
	return f.err
}

func (s *session) noteFailoverFailure(observed *generation, err error, now time.Time) {
	if !retryableByWindow(err) {
		return
	}
	s.mu.Lock()
	s.failoverFailure = recentFailoverFailure{gen: observed, err: err, at: now}
	s.mu.Unlock()
}

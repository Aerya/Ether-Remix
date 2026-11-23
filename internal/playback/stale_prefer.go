package playback

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A Plex client keeps the variant Part id of the version it was shown and
// plays it on every open, long after the release behind it failed its
// health check (the resolver then rejects it for a cooldown, so it is "not
// among candidates"). Each open used to ask the resolver for that release
// again, fall back to a fresh normal ranking that skips the warm entry, and
// so play a different release from the one the opening prefetch warmed.
//
// When a non-exact pick falls back, the Manager forgets it for
// stalePreferTTL: later opens of that version are the automatic choice
// (which the warm-up and resume already hold). After the TTL one open asks
// for the picked release again. A refreshed search with a matching candidate
// clears the cooldown on the next open, without waiting for that TTL.
const (
	stalePreferTTL  = 10 * time.Minute
	maxStalePrefers = 1024
)

type stalePreference struct {
	at     time.Time
	title  *core.Title
	played string
	reason string
}

type stalePreferKey struct {
	key     core.RatingKey
	release string
}

// preferenceStaleLocked reports a picked concrete release that recently fell
// back. Menu slots (ether-slot:v1:...) are never latched here: stale-pick
// memory is keyed per concrete release so a repeat pick moves on to the next
// copy. The caller holds m.mu.
func (m *Manager) preferenceStaleLocked(key core.RatingKey, release string, now time.Time) bool {
	if release == "" || core.IsExplicitVersionSlotSelector(release) {
		return false
	}
	entry, ok := m.stalePrefers[stalePreferKey{key, release}]
	if !ok {
		return false
	}
	if now.Sub(entry.at) >= stalePreferTTL {
		delete(m.stalePrefers, stalePreferKey{key, release})
		return false
	}
	if observer, ok := m.deps.Resolver.(core.PreferenceFallbackObserver); ok && observer.PreferredVersionRecovered(entry.title, release, entry.played, entry.at) {
		delete(m.stalePrefers, stalePreferKey{key, release})
		m.log.Info("picked version found in refreshed search; retrying the pick", "key", key, "title", entry.title.Title, "prefer", release)
		return false
	}
	return true
}

// isReleaseStale reports whether a concrete release for key is inside the
// 10-minute stale-pick window.
func (m *Manager) isReleaseStale(key core.RatingKey, release string) bool {
	if release == "" || core.IsExplicitVersionSlotSelector(release) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preferenceStaleLocked(key, release, m.now())
}

// staleReleasesLocked returns concrete release IDs for key that failed within
// stalePreferTTL so a repeat pick of a menu slot skips them and moves on to the
// next copy. The caller holds m.mu.
func (m *Manager) staleReleasesLocked(key core.RatingKey, now time.Time) []string {
	if len(m.stalePrefers) == 0 {
		return nil
	}
	var out []string
	for k, entry := range m.stalePrefers {
		if k.key != key || k.release == "" || core.IsExplicitVersionSlotSelector(k.release) {
			continue
		}
		if now.Sub(entry.at) >= stalePreferTTL {
			delete(m.stalePrefers, k)
			continue
		}
		if observer, ok := m.deps.Resolver.(core.PreferenceFallbackObserver); ok && observer.PreferredVersionRecovered(entry.title, k.release, entry.played, entry.at) {
			delete(m.stalePrefers, k)
			if entry.title != nil {
				m.log.Info("picked release found in refreshed search; retrying", "key", key, "title", entry.title.Title, "release", k.release)
			}
			continue
		}
		out = append(out, k.release)
	}
	return out
}

// noteStaleRelease records a single concrete release failure for stalePreferTTL
// so repeat picks of the same menu slot skip it rather than latching the slot.
func (m *Manager) noteStaleRelease(key core.RatingKey, release string, title *core.Title, played, reason string) (stalePreference, bool) {
	if release == "" || core.IsExplicitVersionSlotSelector(release) {
		return stalePreference{}, false
	}
	now := m.now()
	m.mu.Lock()
	if m.stalePrefers == nil {
		m.stalePrefers = map[stalePreferKey]stalePreference{}
	}
	if len(m.stalePrefers) >= maxStalePrefers {
		var oldest stalePreferKey
		var oldestAt time.Time
		for k, entry := range m.stalePrefers {
			at := entry.at
			if now.Sub(at) >= stalePreferTTL {
				delete(m.stalePrefers, k)
			} else if oldestAt.IsZero() || at.Before(oldestAt) {
				oldest, oldestAt = k, at
			}
		}
		if len(m.stalePrefers) >= maxStalePrefers {
			delete(m.stalePrefers, oldest)
		}
	}
	entry := stalePreference{at: now, title: title, played: played, reason: reason}
	m.stalePrefers[stalePreferKey{key, release}] = entry
	m.mu.Unlock()
	return entry, true
}

func (m *Manager) notePreferenceStale(ctx context.Context, s *session, played, reason string) {
	key, release := s.ident.key, s.prefer
	entry, ok := m.noteStaleRelease(key, release, s.title, played, reason)
	if !ok {
		return
	}
	m.recordPreferenceFallback(ctx, key, release, entry, stalePreferTTL, false)
}

// forgetStalePrefersLocked drops a title's stale picks (InvalidateTitle:
// the owner pinned, blocked or re-searched it). The caller holds m.mu.
func (m *Manager) forgetStalePrefersLocked(key core.RatingKey) {
	m.forgetPrepareFailuresLocked(key, false)
	for k := range m.stalePrefers {
		if titleIncludes(key, k.key) {
			delete(m.stalePrefers, k)
		}
	}
}

func (m *Manager) recordPreferenceFallback(ctx context.Context, key core.RatingKey, prefer string, entry stalePreference, retry time.Duration, latched bool) {
	title := ""
	if entry.title != nil {
		title = entry.title.Title
	}
	m.log.Info("picked version unavailable; playing the automatic choice until it recovers", "key", key, "title", title, "prefer", prefer, "release", entry.played, "reason", entry.reason, "retry_after", retry, "cooldown_active", latched)
	if observer, ok := m.deps.Resolver.(core.PreferenceFallbackObserver); ok {
		observer.RecordPreferenceFallback(ctx, core.ResolveRequest{Title: entry.title, Prefer: prefer}, entry.played, entry.reason, retry)
	}
}

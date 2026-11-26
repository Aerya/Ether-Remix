package playback

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// pseNoReleaseTTL is how long a "no release exists" verdict stays usable by
// a stream list. Short: a release can appear at any time.
const pseNoReleaseTTL = 5 * time.Minute

// pseNoRelease remembers the warm-ups whose resolver answered that no
// release exists (not a transient failure). The zero value is ready.
type pseNoRelease struct {
	mu sync.Mutex
	at map[identity]time.Time
}

// pseNoteNoRelease records a first-attempt resolve that ended with
// ErrNotAvailable, except when a source exists but cannot be used here.
func (m *Manager) pseNoteNoRelease(id identity, err error) {
	if !errors.Is(err, core.ErrNotAvailable) || errors.Is(err, core.ErrSourceUnavailable) || errors.Is(err, core.ErrVideoTranscodingDisabled) {
		return
	}
	n := &m.pseNoRelease
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.at == nil {
		n.at = map[identity]time.Time{}
	}
	now := m.now()
	if len(n.at) >= maxWarmMisses {
		for k, t := range n.at {
			if now.Sub(t) >= pseNoReleaseTTL {
				delete(n.at, k)
			}
		}
		if len(n.at) >= maxWarmMisses {
			return
		}
	}
	n.at[id] = now
}

func (m *Manager) pseNoReleaseRecent(id identity) bool {
	n := &m.pseNoRelease
	n.mu.Lock()
	defer n.mu.Unlock()
	t, ok := n.at[id]
	return ok && m.now().Sub(t) < pseNoReleaseTTL
}

// WaitWarmDecision implements core.WarmDecider. It waits up to budget for the
// warm-up of this user, client and title to find a source or to learn that
// none exists. It never starts, joins or cancels a warm-up.
func (m *Manager) WaitWarmDecision(ctx context.Context, user *core.User, key core.RatingKey, caps core.ClientCaps, budget time.Duration) core.WarmDecision {
	if user == nil || caps.ClientID == "" {
		return core.WarmUnknown
	}
	id := identity{userID: user.ID, clientID: caps.ClientID, key: key, surface: m.playbackSurface(string(caps.Protocol))}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	for {
		m.mu.Lock()
		e := m.warm[id]
		if e != nil && !m.now().Before(e.expires) {
			e = nil
		}
		has := e != nil && (e.sourceReady || e.res != nil)
		finished := e == nil
		if e != nil {
			select {
			case <-e.done:
				finished = true
			default:
			}
		}
		m.mu.Unlock()
		switch {
		case has:
			return core.WarmHasSource
		case finished && m.pseNoReleaseRecent(id):
			return core.WarmNoSource
		case finished:
			return core.WarmUnknown
		}
		select {
		case <-e.done:
		case <-e.sourced:
		case <-ctx.Done():
			return core.WarmUnknown
		case <-timer.C:
			return core.WarmUnknown
		}
	}
}

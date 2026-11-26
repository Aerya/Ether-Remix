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

// pseNoReleaseErr tells "the resolver found nothing" from every failure that
// leaves a source possible (uncached, unavailable, foreground only...).
func pseNoReleaseErr(err error) bool {
	if !errors.Is(err, core.ErrNotAvailable) {
		return false
	}
	for _, other := range []error{core.ErrNotCached, core.ErrNoSources, core.ErrSourceUnavailable, core.ErrVideoTranscodingDisabled, core.ErrForegroundOnly, core.ErrBudgetExhausted, core.ErrDownloadQueued, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, other) {
			return false
		}
	}
	var uncached *core.UncachedError
	return !errors.As(err, &uncached)
}

// pseNoteNoRelease records a first-attempt resolve that ended with
// ErrNotAvailable, except when a source exists but cannot be used here.
func (m *Manager) pseNoteNoRelease(id identity, err error) {
	if !pseNoReleaseErr(err) {
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

// WaitWarmDecision implements core.WarmDecider. A warm-up that already holds
// a source answers at once. Otherwise it asks the resolver, within budget and
// under the playback rules but without queuing a download, whether any
// release exists: a warm-up search can queue behind other work for longer
// than a stream list may wait. Any doubt answers WarmUnknown.
func (m *Manager) WaitWarmDecision(ctx context.Context, user *core.User, key core.RatingKey, caps core.ClientCaps, budget time.Duration) core.WarmDecision {
	if user == nil || caps.ClientID == "" || m.deps.Resolver == nil || m.deps.Catalog == nil {
		return core.WarmUnknown
	}
	id := identity{userID: user.ID, clientID: caps.ClientID, key: key, surface: m.playbackSurface(string(caps.Protocol))}
	m.mu.Lock()
	e := m.warm[id]
	has := e != nil && m.now().Before(e.expires) && e.res != nil && !e.detailOnly
	m.mu.Unlock()
	if has {
		return core.WarmHasSource
	}
	if m.pseNoReleaseRecent(id) {
		return core.WarmNoSource
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	title, err := m.deps.Catalog.Get(ctx, key)
	if err != nil || title == nil || title.Key != key || title.IsFutureRelease(m.now()) || title.Type == core.TypeShow || title.Type == core.TypeSeason {
		return core.WarmUnknown
	}
	u := m.effectiveResolveUser(ctx, *user)
	rctx := core.WithoutDownloadQueue(core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback))
	res, err := m.deps.Resolver.Resolve(rctx, core.ResolveRequest{Title: title, Client: caps, User: &u})
	switch {
	case ctx.Err() != nil:
		return core.WarmUnknown
	case err == nil && res != nil:
		return core.WarmHasSource
	case pseNoReleaseErr(err):
		m.pseNoteNoRelease(id, err)
		return core.WarmNoSource
	}
	return core.WarmUnknown
}

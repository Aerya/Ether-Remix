package plugins

import (
	"context"
	"sort"
	"strings"
)

// ActiveLiveFeed describes one live event feed contributed by a currently
// enabled, non-shadow plugin whose live_events capability has not been revoked.
type ActiveLiveFeed struct {
	PluginID       string
	PluginName     string
	FeedID         string
	Title          string
	MinIntervalSec int
	EntityKinds    []string
}

func (m *Manager) buildLiveFeeds(ctx context.Context, r Record) []ActiveLiveFeed {
	if !r.Enabled || r.Shadow {
		return nil
	}
	man := r.Manifest
	man.Ether = man.EffectiveEther()
	if man.Ether == nil || !man.HasCapability(CapLiveEvents) {
		return nil
	}
	if g, err := m.store.grants(ctx, r.ID); err == nil && g.revoked(string(CapLiveEvents)) {
		return nil
	}
	decls := man.Ether.Contributes.LiveFeeds
	if len(decls) == 0 {
		return nil
	}
	out := make([]ActiveLiveFeed, 0, len(decls))
	for _, lf := range decls {
		title := lf.Title
		if strings.HasPrefix(title, "$t:") {
			if resolved, ok := m.ResolveText(ctx, r.ID, strings.TrimPrefix(title, "$t:"), "en"); ok && resolved != "" {
				title = resolved
			} else {
				title = lf.ID
			}
		}
		interval := lf.MinIntervalSec
		if interval < MinLiveFeedIntervalSec {
			interval = MinLiveFeedIntervalSec
		}
		var kinds []string
		if len(lf.EntityKinds) > 0 {
			kinds = append([]string(nil), lf.EntityKinds...)
		}
		out = append(out, ActiveLiveFeed{
			PluginID:       r.ID,
			PluginName:     r.Name,
			FeedID:         lf.ID,
			Title:          title,
			MinIntervalSec: interval,
			EntityKinds:    kinds,
		})
	}
	return out
}

// syncLiveFeeds updates the in-memory active live-feed index when a plugin is
// installed, updated, enabled, disabled, shadowed, grant-changed or removed.
func (m *Manager) syncLiveFeeds(ctx context.Context, r Record) {
	feeds := m.buildLiveFeeds(ctx, r)
	m.mu.Lock()
	if m.liveFeeds == nil {
		m.liveFeeds = map[string][]ActiveLiveFeed{}
	}
	if len(feeds) == 0 {
		delete(m.liveFeeds, r.ID)
	} else {
		m.liveFeeds[r.ID] = feeds
	}
	m.mu.Unlock()
}

// ActiveLiveFeeds returns all live event feeds contributed by currently
// enabled, non-shadow plugins in deterministic order without hitting the
// database once loaded.
func (m *Manager) ActiveLiveFeeds(ctx context.Context) []ActiveLiveFeed {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	loaded := m.liveFeedsLoaded
	m.mu.Unlock()
	if !loaded {
		if recs, err := m.store.list(ctx); err == nil {
			built := make(map[string][]ActiveLiveFeed, len(recs))
			for _, r := range recs {
				if feeds := m.buildLiveFeeds(ctx, r); len(feeds) > 0 {
					built[r.ID] = feeds
				}
			}
			m.mu.Lock()
			m.liveFeeds = built
			m.liveFeedsLoaded = true
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.liveFeeds) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.liveFeeds))
	for id := range m.liveFeeds {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []ActiveLiveFeed
	for _, id := range ids {
		for _, lf := range m.liveFeeds[id] {
			cp := lf
			if len(lf.EntityKinds) > 0 {
				cp.EntityKinds = append([]string(nil), lf.EntityKinds...)
			}
			out = append(out, cp)
		}
	}
	return out
}

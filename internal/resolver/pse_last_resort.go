package resolver

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

type lastResortPhaseKey struct{}

func (s *Service) lastResortPlayback(ctx context.Context) bool {
	if !core.PlaybackIntent(ctx) {
		return false
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	for _, enabled := range s.lastResortSources {
		if enabled {
			return true
		}
	}
	return false
}

// Indexation still sees every source. Only automatic playback separates the
// candidates into two passes, independently of quality and source priority.
func (s *Service) sourceInPlaybackPhase(ctx context.Context, req core.ResolveRequest, rel core.Release, pinID string) bool {
	if core.QuickSource(ctx) != "" {
		return true
	}
	if !s.lastResortPlayback(ctx) || req.PreferredOnly || pinID != "" {
		return true
	}
	fallback, _ := ctx.Value(lastResortPhaseKey{}).(bool)
	s.cfgMu.RLock()
	isFallback := s.lastResortSources[rel.IndexerID]
	s.cfgMu.RUnlock()
	return isFallback == fallback
}

func (s *Service) resolveWithLastResort(ctx context.Context, req core.ResolveRequest, profile core.QualityProfile) (*core.Resolution, error) {
	// PSE-HOOK: opt-in quick source order — see docs/pse/FORK.fr.md.
	if s.resolverCfg().SourceOrder.Quick.Enabled && (req.Title.Type == core.TypeMovie || req.Title.Type == core.TypeEpisode || req.Title.Type == core.TypeShow) {
		return s.resolveQuick(ctx, req, profile)
	}
	res, err := s.resolve(ctx, req, profile)
	if err == nil || ctx.Err() != nil || req.PreferredOnly || s.PinnedReleaseID(req) != "" || !s.lastResortPlayback(ctx) {
		return res, err
	}
	// Reuse discovery/search results, but only last-resort candidates may open.
	fallback := context.WithValue(ctx, lastResortPhaseKey{}, true)
	return s.resolve(fallback, req, profile)
}

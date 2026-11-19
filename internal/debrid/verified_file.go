package debrid

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

func (s *Service) hasFileProvider(kind string) bool {
	if kind != "alldebrid" {
		return false
	}
	for _, e := range s.active() {
		if e.cfg.Kind == kind {
			return true
		}
	}
	return false
}

// acquireFile unlocks an already identified file. No Find, CheckCached,
// Acquire, Remove, or torrent download fallback is allowed on this path.
func (s *Service) acquireFile(ctx context.Context, r core.Release) ([]byte, error) {
	if r.OpensOnDemand() && !core.OnDemandStreamsAllowed(ctx) && !core.OnDemandWarmGranted(ctx, r) {
		return nil, core.ErrForegroundOnly
	}
	if r.StreamDebrid != "alldebrid" || !core.ValidAllDebridFileURL(r.StreamURL) || r.Size <= 0 {
		return nil, core.ErrReleaseRejected
	}
	if p, ok := s.reusablePayload(ctx, r); ok && p.VerifiedFile && p.Link == r.StreamURL {
		p, err := s.Refresh(ctx, p, false)
		if err != nil {
			return nil, err
		}
		return p.Encode(), nil
	}
	var errs []error
	for _, e := range s.active() {
		if e.cfg.Kind != r.StreamDebrid {
			continue
		}
		if err := e.blocked(s.now()); err != nil {
			errs = append(errs, err)
			continue
		}
		p := Payload{VerifiedFile: true, Key: payloadKey(r), ReleaseID: r.ID, Provider: e.cfg.ID, Kind: e.cfg.Kind, Path: r.Name, Size: r.Size, Link: r.StreamURL, Release: r.Name, Target: r.Target}
		out, err := s.relink(ctx, e, p)
		if err == nil {
			return out.Encode(), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, ErrNoProvider
	}
	return nil, s.joinProviderErrors(errs)
}

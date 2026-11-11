package resolver

import (
	"context"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

// SubtitledHook reports, for one title, which releases already have
// subtitles prepared (Remix SubMaker). The result is aligned with releases;
// a shorter or nil result marks nothing.
type SubtitledHook func(ctx context.Context, t *core.Title, releases []core.Release) []bool

// pseState holds Remix ranking hooks.
type pseState struct {
	mu        sync.Mutex
	subtitled SubtitledHook
}

// SetSubtitled installs the hook that marks releases with prepared
// subtitles. They rank first among otherwise equal releases (ranking.go).
func (s *Service) SetSubtitled(h SubtitledHook) {
	s.pse.mu.Lock()
	s.pse.subtitled = h
	s.pse.mu.Unlock()
}

// markSubtitled asks the hook once for every candidate of a ranking pass.
func (s *Service) markSubtitled(ctx context.Context, t *core.Title, cands []candidate, trace *Trace) {
	s.pse.mu.Lock()
	h := s.pse.subtitled
	s.pse.mu.Unlock()
	if h == nil || t == nil || len(cands) == 0 {
		return
	}
	rels := make([]core.Release, len(cands))
	for i, c := range cands {
		rels[i] = c.rel
	}
	ready := h(ctx, t, rels)
	for i := range cands {
		if i >= len(ready) || !ready[i] {
			continue
		}
		cands[i].subtitled = true
		if trace != nil && cands[i].trace >= 0 && cands[i].trace < len(trace.Candidates) {
			ct := &trace.Candidates[cands[i].trace]
			ct.CompatibilityReason = strings.TrimPrefix(ct.CompatibilityReason+"; subtitles already prepared by SubMaker: ranked first among equal releases", "; ")
		}
	}
}

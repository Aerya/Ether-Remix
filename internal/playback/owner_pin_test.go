package playback

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type ownerPinResolver struct {
	core.Resolver
	id string
}

func (r ownerPinResolver) PinnedReleaseID(core.ResolveRequest) string { return r.id }

// Cached alternatives were selected before the pin. Neither sequential nor
// raced failover may fetch them, and the shared open boundary rejects them too.
func TestOwnerPinPreventsCachedSourceSubstitution(t *testing.T) {
	h := newHarness(t, nil, []string{"A"})
	h.m.deps.Resolver = ownerPinResolver{Resolver: h.resolver, id: "A"}
	s := &session{title: h.catalog.titles[movieKey], caps: defaultCaps("pin")}
	fetched := 0
	h.m.deps.FetchNZB = func(context.Context, core.Release) ([]byte, error) {
		fetched++
		return []byte("payload"), nil
	}
	wrong := core.Release{ID: "B"}
	res, err := h.m.nextResolution(t.Context(), s, nil, []core.Release{wrong})
	if err != nil || res == nil || res.Release.ID != "A" || fetched != 0 {
		t.Fatalf("sequential substituted cached release: %+v %v fetches=%d", res, err, fetched)
	}
	g, _, err := h.m.raceAlternates(t.Context(), s, nil, []core.Release{wrong}, nil, 2, nil, nil)
	if err != nil || g != nil || fetched != 0 {
		t.Fatalf("raced a cached release: %v fetches=%d", err, fetched)
	}
	_, err = h.m.openGeneration(t.Context(), s, &core.Resolution{Release: wrong}, nil)
	if !errors.Is(err, core.ErrNotAvailable) || h.engine.openCount() != 0 {
		t.Fatalf("cached source reached engine: %v opens=%d", err, h.engine.openCount())
	}
}

package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// #806: a Silo start plans the Auto file through its offer, which opens the
// release exactly. When that release is corrupt near its start, the encoder
// input failure must fail over to the next candidate instead of ending the
// session; only the viewer's own pick ends, with a clear reason.
func TestUnreadableExactStartFailsOverUnlessPicked(t *testing.T) {
	corrupt := fmt.Errorf("%w: stream: offset 2150400 not found in part 4: archive: corrupt or truncated header", core.ErrReleaseRejected)
	start := func(t *testing.T, binding core.SourceBinding) (*harness, string, error) {
		h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), mkv("B", 100_000, time.Minute)}, []string{"A", "B"})
		caps := defaultCaps("silo-tv")
		caps.Protocol = "silo"
		ps, err := h.m.PrepareSurface(context.Background(), owner, movieKey, caps, "A", true)
		if err != nil || ps.Resolution.Release.ID != "A" {
			t.Fatalf("offer start: %+v %v", ps, err)
		}
		h.m.BindSource(ps.ID, binding)
		h.tr.addHLS(ps.ID)
		h.m.lookup(ps.ID).current().noteReadFailure(corrupt)
		return h, ps.ID, h.m.FailoverInput(t.Context(), ps.ID, h.m.InputURL(ps.ID), 0)
	}
	t.Run("auto offer fails over", func(t *testing.T) {
		h, id, err := start(t, core.SourceBindingAuto)
		if err != nil {
			t.Fatalf("auto start did not fail over: %v", err)
		}
		current, ok := h.m.Session(id)
		if !ok || current.Resolution.Release.ID != "B" {
			t.Fatalf("playback did not continue on the next candidate: %+v", current)
		}
		if !h.resolver.isBad("A") {
			t.Fatal("corrupt release was not marked bad")
		}
	})
	t.Run("explicit pick ends with a clear reason", func(t *testing.T) {
		h, id, err := start(t, core.SourceBindingExplicit)
		if !errors.Is(err, core.ErrVersionUnreadable) || !errors.Is(err, core.ErrReleaseRejected) {
			t.Fatalf("explicit pick: %v", err)
		}
		if _, ok := h.m.Session(id); ok {
			t.Fatal("unreadable explicit pick kept a dead session")
		}
		if !h.resolver.isBad("A") {
			t.Fatal("corrupt release was not marked bad")
		}
	})
}

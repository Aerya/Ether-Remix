package playback

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRecoveredOriginalFencesOldByteCoordinates(t *testing.T) {
	old := mkv("A", 100_000, time.Minute)
	best := mkv("C", 190_000, time.Minute)
	h := newHarness(t, []*fakeRelease{old, mkv("B", 130_000, time.Minute), best}, []string{"A", "B", "C"})
	for j := range h.resolver.candidates {
		h.resolver.candidates[j].Protocol = core.ProtocolTorrent
	}
	h.m.deps.FetchNZB = func(_ context.Context, r core.Release) ([]byte, error) {
		return []byte("nzb-" + r.ID), nil
	}
	ps, err := h.m.PrepareSurface(t.Context(), owner, movieKey, defaultCaps("direct"), "A", true)
	if err != nil {
		t.Fatal(err)
	}
	h.m.BindSource(ps.ID, core.SourceBindingAuto)
	before := core.SourceRevision(ps)
	if err := h.m.PinSourceRevision(t.Context(), ps.ID, before); err != nil {
		t.Fatal(err)
	}
	const offset = 64_000
	old.setFail(offset, old.Size, core.ErrOriginalLost)
	serve := func(revision string, lost func()) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/file", nil)
		r.Header.Set("Range", "bytes=64000-64015")
		r = r.WithContext(core.WithOriginalRange(r.Context(), core.OriginalRange{Revision: revision, Lost: lost}))
		w := httptest.NewRecorder()
		h.m.ServeRange(w, r, ps.ID)
		return w
	}
	notified := false
	if w := serve(before, func() { notified = true }); w.Code != http.StatusConflict || !notified {
		t.Fatalf("loss response = %d, notified = %t", w.Code, notified)
	}
	next, at, err := h.m.ReplaceLostOriginal(t.Context(), ps.ID, before, 23*time.Second, false, func(candidate *core.PlaySession) float64 {
		if candidate.Resolution.Release.ID == "C" {
			return 1
		}
		return .5
	})
	if err != nil || next.Resolution.Release.ID != "C" || at != 23*time.Second || !h.resolver.isBad("A") {
		t.Fatalf("replacement = %+v, position = %s, error = %v", next, at, err)
	}
	if w := serve(before, nil); w.Code != http.StatusConflict {
		t.Fatalf("old revision read replacement bytes: status %d", w.Code)
	}
	newRevision := core.SourceRevision(next)
	if w := serve(newRevision, nil); w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), expected("C", offset, 16)) {
		t.Fatalf("new revision response = %d %q", w.Code, w.Body.Bytes())
	}
	h.m.lookup(ps.ID).current().noteReadFailure(core.ErrOriginalLost)
	if _, _, err = h.m.ReplaceLostOriginal(t.Context(), ps.ID, newRevision, at, false, func(*core.PlaySession) float64 { return 1 }); !errors.Is(err, core.ErrVersionUnreadable) {
		t.Fatalf("second replacement: %v", err)
	}
	if !h.resolver.isBad("C") {
		t.Fatal("replacement loss was not recorded")
	}
}

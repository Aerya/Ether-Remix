package pluginitems

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type stubCaller struct {
	fn func(context.Context, string, plugins.Request) (plugins.Response, error)
}

func (c stubCaller) Call(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, error) {
	return c.fn(ctx, pluginID, req)
}

func TestPartResolutionsCacheInvalidateAndDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})

	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "series-1", Level: "series", Title: "Series"},
		{ID: "book-1", Level: "book", ParentID: "series-1", Title: "Multi Volume", Playable: true},
		{ID: "book-2", Level: "book", ParentID: "series-1", Title: "Delete Me", Playable: true},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	ref1 := core.NewItemRef("community.books", "book", "book-1")
	ref2 := core.NewItemRef("community.books", "book", "book-2")
	p0, p1 := 0, 1

	if err := s.SavePartResolution(ctx, ref1, &p0, core.PlayableSource{ID: "s0", Pattern: "range", URL: "https://ex/0"}); err != nil {
		t.Fatalf("SavePartResolution(0): %v", err)
	}
	if err := s.SavePartResolution(ctx, ref1, &p1, core.PlayableSource{ID: "s1", Pattern: "range", URL: "https://ex/1"}); err != nil {
		t.Fatalf("SavePartResolution(1): %v", err)
	}

	got0, ok, err := s.GetPartResolution(ctx, ref1, &p0)
	if err != nil || !ok || got0.ID != "s0" {
		t.Fatalf("GetPartResolution(0) = %+v, ok=%v, err=%v", got0, ok, err)
	}
	got1, ok, err := s.GetPartResolution(ctx, ref1, &p1)
	if err != nil || !ok || got1.ID != "s1" {
		t.Fatalf("GetPartResolution(1) = %+v, ok=%v, err=%v", got1, ok, err)
	}

	// Targeted InvalidatePartResolution clears only part 0.
	if err := s.InvalidatePartResolution(ctx, ref1, &p0); err != nil {
		t.Fatalf("InvalidatePartResolution(0): %v", err)
	}
	if _, ok, err := s.GetPartResolution(ctx, ref1, &p0); err != nil || ok {
		t.Fatalf("expected part 0 invalidated, got ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetPartResolution(ctx, ref1, &p1); err != nil || !ok {
		t.Fatalf("expected part 1 preserved, got ok=%v err=%v", ok, err)
	}

	// Whole-item InvalidateResolution clears all #part:* rows too.
	if err := s.InvalidateResolution(ctx, ref1); err != nil {
		t.Fatalf("InvalidateResolution: %v", err)
	}
	if _, ok, err := s.GetPartResolution(ctx, ref1, &p1); err != nil || ok {
		t.Fatalf("expected part 1 cleared by InvalidateResolution, got ok=%v err=%v", ok, err)
	}

	// Deleting an item removes its #part:* resolutions.
	if err := s.SavePartResolution(ctx, ref2, &p0, core.PlayableSource{ID: "d0", Pattern: "range", URL: "https://ex/d0"}); err != nil {
		t.Fatalf("SavePartResolution(ref2, 0): %v", err)
	}
	if err := s.KindDelete(ctx, "community.books", "book", []string{"book-2"}); err != nil {
		t.Fatalf("KindDelete(book-2): %v", err)
	}
	if _, ok, err := s.GetPartResolution(ctx, ref2, &p0); err != nil || ok {
		t.Fatalf("expected deleted item's part resolution purged, got ok=%v err=%v", ok, err)
	}
}

func TestPartCandidatesCachePreservesWinnerAndExpires(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s, _ := openTestService(t, Options{Now: func() time.Time { return now }})

	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "series-1", Level: "series", Title: "Series"},
		{ID: "book-1", Level: "book", ParentID: "series-1", Title: "Cached Versions", Playable: true},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}
	ref := core.NewItemRef("community.books", "book", "book-1")
	cands := []core.PlayableSource{
		{ID: "4k", Label: "4K", Pattern: "range", URL: "https://ex/4k.mkv", ExpiresAt: now.Add(5 * time.Minute).Unix()},
		{ID: "1080p", Label: "1080p", Pattern: "range", URL: "https://ex/1080.mkv", ExpiresAt: now.Add(10 * time.Minute).Unix()},
	}

	// Saving candidates alone must NOT look like a winning source in GetPartResolution.
	if err := s.SavePartCandidates(ctx, ref, nil, cands); err != nil {
		t.Fatalf("SavePartCandidates: %v", err)
	}
	if _, ok, err := s.GetPartResolution(ctx, ref, nil); err != nil || ok {
		t.Fatalf("candidates-only row leaked into GetPartResolution: ok=%v err=%v", ok, err)
	}
	got, ok, err := s.GetPartCandidates(ctx, ref, nil)
	if err != nil || !ok || len(got) != 2 || got[0].ID != "4k" || got[1].ID != "1080p" {
		t.Fatalf("GetPartCandidates = (%+v, %v, %v), want 2 candidates", got, ok, err)
	}

	// Saving the winning source preserves the cached candidates list.
	if err := s.SavePartResolution(ctx, ref, nil, cands[0]); err != nil {
		t.Fatalf("SavePartResolution: %v", err)
	}
	win, ok, err := s.GetPartResolution(ctx, ref, nil)
	if err != nil || !ok || win.ID != "4k" {
		t.Fatalf("GetPartResolution = (%+v, %v, %v), want 4k", win, ok, err)
	}
	got, ok, err = s.GetPartCandidates(ctx, ref, nil)
	if err != nil || !ok || len(got) != 2 {
		t.Fatalf("SavePartResolution wiped candidates: (%+v, %v, %v)", got, ok, err)
	}

	// Negative resolution clears candidates and surfaces ErrNotAvailable.
	if err := s.SaveNegativePartResolution(ctx, ref, nil); err != nil {
		t.Fatalf("SaveNegativePartResolution: %v", err)
	}
	if _, ok, err := s.GetPartCandidates(ctx, ref, nil); !ok || !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("negative GetPartCandidates = (ok=%v, err=%v), want (true, ErrNotAvailable)", ok, err)
	}

	// Re-saving candidates clears negative state and respects min(ExpiresAt) expiry.
	if err := s.SavePartCandidates(ctx, ref, nil, cands); err != nil {
		t.Fatalf("re-SavePartCandidates: %v", err)
	}
	now = now.Add(6 * time.Minute)
	if _, ok, err := s.GetPartCandidates(ctx, ref, nil); err != nil || ok {
		t.Fatalf("expired GetPartCandidates = (ok=%v, err=%v), want miss", ok, err)
	}
}

func TestMatchItemPendingReturnsErrPreparingWithoutSavingEmptyMatches(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})

	ref := core.NewItemRef("community.books", "book", "book-1")
	item := core.KindItem{Ref: ref, ID: "book-1", Level: "book", Title: "Dune"}

	caller := stubCaller{fn: func(_ context.Context, _ string, _ plugins.Request) (plugins.Response, error) {
		return plugins.Response{
			Status: http.StatusAccepted,
			Body:   json.RawMessage(`{"pending":true,"retryInMs":6000}`),
		}, nil
	}}

	scores, err := s.MatchItem(ctx, caller, "community.matcher", "book", item, []core.SourceCandidate{{Title: "Dune.epub"}}, nil, 0.85)
	if !errors.Is(err, core.ErrPreparing) {
		t.Fatalf("MatchItem pending err = %v, want core.ErrPreparing (scores=%v)", err, scores)
	}
	if got := core.PreparingRetrySeconds(err, 5); got != 6 {
		t.Fatalf("PreparingRetrySeconds = %d, want 6", got)
	}
	saved, err := s.ListMatches(ctx, ref)
	if err != nil || len(saved) != 0 {
		t.Fatalf("expected 0 saved matches while pending, got %+v (err=%v)", saved, err)
	}
}

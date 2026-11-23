package experience

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type vibeCalls struct {
	n        int
	local    bool
	describe int
	result   *core.DescriptiveResult
}

func (v *vibeCalls) VibeCollection(ctx context.Context, _ *core.User, _ string) (*core.Collection, []core.Title, bool, error) {
	v.n++
	v.local = core.LocalResponse(ctx)
	return &core.Collection{Key: core.CollectionKey(1), Title: "vibe"}, []core.Title{{Key: core.MovieKey(11), Type: core.TypeMovie, Title: "Member"}, {Key: core.MovieKey(12), Type: core.TypeMovie, Title: "Hidden", Adult: true}}, true, nil
}

func (v *vibeCalls) DescriptiveSearchAvailable() bool { return true }
func (v *vibeCalls) DescribeSearch(ctx context.Context, _ *core.User, _ string, wait bool) (core.DescriptiveResult, error) {
	v.describe++
	if v.result != nil {
		if v.result.State == "failed" {
			return *v.result, errors.New("provider body must not escape")
		}
		return *v.result, nil
	}
	return core.DescriptiveResult{State: "ready", Items: []core.Title{{Key: core.MovieKey(11), Type: core.TypeMovie, Title: "Member"}, {Key: core.MovieKey(12), Type: core.TypeMovie, Title: "Hidden", Adult: true}}}, nil
}

func TestSearchDescriptiveAIFirstAndTitleFastPath(t *testing.T) {
	ctx := context.Background()
	v := &core.User{ID: 1}
	weakHit := core.Title{Key: core.MovieKey(5), Type: core.TypeMovie, Title: "Cozy Rainstorm", SearchScore: 0.42}
	titleHit := core.Title{Key: core.MovieKey(6), Type: core.TypeMovie, Title: "Pacific Rim", SearchScore: TitleMatchScore}
	calls := &vibeCalls{}
	var catHits []core.Title
	s := &Service{d: ServiceDeps{
		Protocol:   "jellyfin",
		Active:     func(context.Context, int64) (*core.User, error) { return v, nil },
		Libraries:  []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
		Search: Search{
			SettleDelay: -1,
			Visibility:  Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
			Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
				return catHits, nil
			},
			Vibe: VibeSearch{Source: calls, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}, Unrestricted: func(*core.User) bool { return true }},
		},
	}}

	// Single-word query is not descriptive: keyword results return with zero AI work.
	catHits = []core.Title{weakHit}
	page, err := s.Search(ctx, v, core.Device{ID: "jf-1"}, "rain", frontend.ItemQuery{})
	if err != nil || calls.describe != 0 || calls.n != 0 || len(page.Page.Items) != 1 || page.Page.Items[0].Key != weakHit.Key {
		t.Fatalf("single-word keyword %+v describe=%d vibe=%d err=%v", page, calls.describe, calls.n, err)
	}

	// Multi-word title match (>= TitleMatchScore) returns instantly with zero AI work.
	catHits = []core.Title{titleHit}
	page, err = s.Search(ctx, v, core.Device{ID: "jf-1"}, "Pacific Rim", frontend.ItemQuery{})
	if err != nil || calls.describe != 0 || calls.n != 0 || len(page.Page.Items) != 1 || page.Page.Items[0].Key != titleHit.Key {
		t.Fatalf("title match %+v describe=%d vibe=%d err=%v", page, calls.describe, calls.n, err)
	}

	// Descriptive query without a TitleMatchScore hit waits for AI and puts AI items first,
	// even when weak keyword hits exist, while filtering adult items out.
	catHits = []core.Title{weakHit}
	page, err = s.Search(ctx, v, core.Device{ID: "jf-1"}, "cozy rain", frontend.ItemQuery{})
	if err != nil || calls.describe != 1 || calls.n != 1 || len(page.Page.Items) != 2 || page.Page.Items[0].Title != "Member" || page.Page.Items[1].Key != weakHit.Key {
		t.Fatalf("jellyfin AI-first %+v describe=%d vibe=%d err=%v", page, calls.describe, calls.n, err)
	}

	// Vela's fast search lane stays non-waiting while its explicit DescriptiveSearch lane serves job states.
	s.d.Protocol = "vela"
	page, err = s.Search(ctx, v, core.Device{ID: "vela-1"}, "cozy rain", frontend.ItemQuery{})
	if err != nil || calls.describe != 1 || len(page.Page.Items) != 1 || page.Page.Items[0].Key != weakHit.Key {
		t.Fatalf("vela fast lane %+v describe=%d err=%v", page, calls.describe, err)
	}

	page, err = s.DescriptiveSearch(ctx, v, core.Device{}, "cozy rain", frontend.ItemQuery{})
	if err != nil || !s.DescriptiveSearchAvailable() || calls.describe != 2 || len(page.Page.Items) != 1 || page.Page.Items[0].Title != "Member" || page.Descriptive == nil || page.Descriptive.State != frontend.DescriptiveReady {
		t.Fatalf("shared AI descriptive lane %+v describe=%d local=%v err=%v", page, calls.describe, calls.local, err)
	}
	for _, state := range []string{"pending", "busy", "failed"} {
		calls.result = &core.DescriptiveResult{State: state, RetryAfterSeconds: 2}
		page, err = s.DescriptiveSearch(ctx, v, core.Device{}, "cozy rain", frontend.ItemQuery{})
		if err != nil || page.Descriptive == nil || string(page.Descriptive.State) != state || page.Descriptive.RetryAfterSeconds != 2 || len(page.Page.Items) != 0 {
			t.Fatalf("shared job state %s: %+v %v", state, page, err)
		}
	}
}

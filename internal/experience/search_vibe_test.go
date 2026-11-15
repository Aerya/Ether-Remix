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

func TestSearchUsesVibeMembersOnlyAfterKeywordsMiss(t *testing.T) {
	ctx := context.Background()
	v := &core.User{ID: 1}
	hit := core.Title{Key: core.MovieKey(5), Type: core.TypeMovie, Title: "Keyword"}
	calls := &vibeCalls{}
	keyword := true
	s := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return v, nil },
		Libraries:  []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
		Search: Search{
			Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
			Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
				if keyword {
					return []core.Title{hit}, nil
				}
				return nil, nil
			},
			Vibe: VibeSearch{Source: calls, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}, Unrestricted: func(*core.User) bool { return true }},
		},
	}}
	page, err := s.Search(ctx, v, core.Device{}, "rain", frontend.ItemQuery{})
	if err != nil || calls.n != 0 || len(page.Page.Items) != 1 || page.Page.Items[0].Key != hit.Key {
		t.Fatalf("keyword %+v calls %d %v", page, calls.n, err)
	}
	keyword = false
	page, err = s.Search(ctx, v, core.Device{}, "cozy rain", frontend.ItemQuery{})
	if err != nil || calls.n != 1 || len(page.Page.Items) != 1 || page.Page.Items[0].Title != "Member" {
		t.Fatalf("vibe %+v calls %d %v", page, calls.n, err)
	}
	keyword = true
	page, err = s.DescriptiveSearch(ctx, v, core.Device{}, "cozy rain", frontend.ItemQuery{})
	if err != nil || !s.DescriptiveSearchAvailable() || calls.n != 1 || calls.describe != 1 || len(page.Page.Items) != 1 || page.Page.Items[0].Title != "Member" || page.Descriptive == nil || page.Descriptive.State != frontend.DescriptiveReady {
		t.Fatalf("shared AI descriptive lane %+v calls %d local %v %v", page, calls.n, calls.local, err)
	}
	for _, state := range []string{"pending", "busy", "failed"} {
		calls.result = &core.DescriptiveResult{State: state, RetryAfterSeconds: 2}
		page, err = s.DescriptiveSearch(ctx, v, core.Device{}, "cozy rain", frontend.ItemQuery{})
		if err != nil || page.Descriptive == nil || string(page.Descriptive.State) != state || page.Descriptive.RetryAfterSeconds != 2 || len(page.Page.Items) != 0 {
			t.Fatalf("shared job state %s: %+v %v", state, page, err)
		}
	}

}

package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// One list: a named title never starts AI work; a description starts it
// only when asked again (the client's retry), and its titles then lead.
func TestUnifiedSearchOneListAIOnlyForDescriptions(t *testing.T) {
	ctx := context.Background()
	v := &core.User{ID: 7}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	unifiedNow = func() time.Time { return clock }
	t.Cleanup(func() { unifiedNow = time.Now })
	vis := Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}
	calls := &vibeCalls{}
	var catHits []core.Title
	s := &Service{d: ServiceDeps{
		Protocol:   "vela",
		Active:     func(context.Context, int64) (*core.User, error) { return v, nil },
		Libraries:  []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility: vis,
		Search: Search{
			Visibility: vis,
			Catalog:    func(context.Context, string, []core.MediaType, int) ([]core.Title, error) { return catHits, nil },
			Vibe:       VibeSearch{Source: calls, Visibility: vis, Unrestricted: func(*core.User) bool { return true }},
		},
	}}
	dev := core.Device{ID: "unified-1"}

	// A franchise being typed names titles: no AI, nothing pending.
	catHits = []core.Title{{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Skyfall", SearchScore: 0.85}}
	page, err := s.UnifiedSearch(ctx, v, dev, "james bo", frontend.ItemQuery{})
	if err != nil || calls.describe != 0 || page.Pending || page.Match != frontend.SearchMatchTitle || len(page.Page.Items) != 1 {
		t.Fatalf("title query: %+v describe=%d err=%v", page, calls.describe, err)
	}

	// A description: the first ask only reports pending, starting nothing.
	catHits = []core.Title{{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Rainy Afternoon", SearchScore: 0.42}}
	page, err = s.UnifiedSearch(ctx, v, dev, "cozy rain films", frontend.ItemQuery{})
	if err != nil || calls.describe != 0 || !page.Pending || page.Descriptive == nil || page.Descriptive.State != frontend.DescriptivePending {
		t.Fatalf("first ask: %+v describe=%d err=%v", page, calls.describe, err)
	}
	// Asked again once settled: the description's titles lead, the title
	// matches follow, adult titles are filtered out.
	clock = clock.Add(unifiedSettle)
	page, err = s.UnifiedSearch(ctx, v, dev, "cozy rain films", frontend.ItemQuery{})
	if err != nil || calls.describe != 1 || page.Pending || page.Match != frontend.SearchMatchMixed || len(page.Page.Items) != 2 ||
		page.Page.Items[0].Title != "Member" || !page.Described[page.Page.Items[0].Key] || page.Described[page.Page.Items[1].Key] {
		t.Fatalf("settled ask: %+v describe=%d err=%v", page, calls.describe, err)
	}

	// Another query typed after it starts nothing until it is asked again.
	page, err = s.UnifiedSearch(ctx, v, dev, "cozy rain films for", frontend.ItemQuery{})
	if err != nil || calls.describe != 1 || !page.Pending {
		t.Fatalf("superseding prefix: %+v describe=%d err=%v", page, calls.describe, err)
	}
}

package personalize

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPreviewContinueUsesSharedNextEpisodes(t *testing.T) {
	next := core.Title{Key: core.EpisodeKey(123, 1, 2), Type: core.TypeEpisode, Title: "Next episode"}
	h := newHarness(t, func(o *Options) {
		o.ExtraHomeRows = func(context.Context, *core.User, core.HubContext, string, int) []core.Hub {
			return []core.Hub{{ID: "home.ondeck", Title: "On Deck", Items: []core.Title{next}}}
		}
		o.PreviewContinue = func(_ context.Context, u *core.User, limit int) ([]core.Title, error) {
			if u.ID != 1 || limit != 12 {
				t.Errorf("wrong preview context: %d / %d", u.ID, limit)
			}
			return []core.Title{next}, nil
		}
	})
	u := core.User{ID: 1, Username: "preview", IsOwner: true}
	p := preview(t, h, u, hubContext(testNow, core.DeviceDesktop), "home")
	found := false
	for _, r := range p.Rows {
		if r.Kind == KindContinue {
			found = true
			if len(r.Items) != 1 || r.Items[0].Key != next.Key || len(r.Hub.Items) != 1 {
				t.Fatalf("next episode missing: %+v", r)
			}
		}
		if r.HubID == "home.ondeck" && !r.Hidden {
			t.Fatal("duplicate On Deck visible")
		}
	}
	if !found {
		t.Fatal("Continue Watching missing")
	}
	if _, err := h.e.SetHomeLayout(context.Background(), core.HomeLayout{Global: []core.LayoutPage{{Section: "home", Rows: []core.RowPolicy{{Row: KindContinue, Status: "off"}}}}}); err != nil {
		t.Fatal(err)
	}
	p = preview(t, h, u, hubContext(testNow, core.DeviceDesktop), "home")
	deckVisible := false
	for _, r := range p.Rows {
		if r.HubID == "home.ondeck" && !r.Hidden {
			deckVisible = true
		}
	}
	if !deckVisible {
		t.Fatal("On Deck must remain available when Continue is disabled")
	}
	// A failed shared lookup must retain the ordinary preview, not fail the page.
	h.e.opts.PreviewContinue = func(context.Context, *core.User, int) ([]core.Title, error) { return nil, errors.New("unavailable") }
	if _, err := h.e.PreviewHome(context.Background(), &u, hubContext(testNow, core.DeviceDesktop), "home", 12); err != nil {
		t.Fatal(err)
	}
}

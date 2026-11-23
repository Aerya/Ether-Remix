package personalize

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Two custom show libraries with disjoint memberships (the first five test
// genres vs the last five) must get disjoint recommendation rows: a section's
// Recommended rows are populated from that library's titles, not the global
// show pool (#549).
func TestSectionHubsScopedToLibrary(t *testing.T) {
	members := func(_ context.Context, id core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]bool, int64, bool, error) {
		if id != 10 && id != 11 {
			return nil, 0, false, nil
		}
		out := map[core.RatingKey]bool{}
		for _, k := range keys {
			gi := int(k.TMDB()/1000) - 1 // fake catalogue: TMDB encodes the genre index
			if (id == 10 && gi < 5) || (id == 11 && gi >= 5) {
				out[k] = true
			}
		}
		return out, 1, true, nil
	}
	h := newHarness(t, func(o *Options) { o.LibraryMembers = members })
	ctx := context.Background()
	sciFiFan(h, 1)
	u := core.User{ID: 1}
	h.rebuild(u)

	keysFor := func(sectionID int) map[core.RatingKey]bool {
		hc := hubContext(testNow, core.DeviceTV)
		hc.SectionID = sectionID
		hubs, err := h.e.SectionHubsFor(ctx, &u, core.TypeShow, hc, 12)
		if err != nil {
			t.Fatal(err)
		}
		out := map[core.RatingKey]bool{}
		for _, hb := range hubs {
			for _, it := range hb.Items {
				out[it.Key] = true
			}
		}
		return out
	}
	a, b := keysFor(10), keysFor(11)
	if len(a) == 0 || len(b) == 0 {
		t.Fatalf("scoped sections must still populate rows: lib10=%d lib11=%d titles", len(a), len(b))
	}
	for k := range a {
		if gi := int(k.TMDB()/1000) - 1; gi >= 5 {
			t.Fatalf("library 10 row contains non-member %v (genre %d)", k, gi)
		}
		if b[k] {
			t.Fatalf("title %v appears in both disjoint libraries' rows", k)
		}
	}
	for k := range b {
		if gi := int(k.TMDB()/1000) - 1; gi < 5 {
			t.Fatalf("library 11 row contains non-member %v (genre %d)", k, gi)
		}
	}
	// The canonical TV Shows section (unscoped) still serves rows.
	if got := keysFor(2); len(got) == 0 {
		t.Fatal("canonical shows section returned no titles")
	}
}

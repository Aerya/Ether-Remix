package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestForeignAndUncertifiedAdultLiveTVEnforcement(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{})
	pg13 := &core.User{ID: 7, MaxContentRating: "PG-13"}
	owner := &core.User{ID: 1, IsOwner: true, MaxContentRating: ""}

	iptv := Channel{
		ID:        "intl-iptv",
		Name:      "Intl Cinema",
		Number:    "10",
		Kind:      "iptv",
		Enabled:   true,
		Rating:    "PG",
		StreamURL: "https://example.invalid/live.ts",
	}
	if _, err := s.SaveChannel(ctx, iptv); err != nil {
		t.Fatal(err)
	}
	s.ReconcileEnabled()

	cases := []struct {
		name string
		item Item
		want bool
	}{
		{name: "gb 12A allowed under PG-13", item: Item{Title: "Teen Film", Rating: "gb/12A"}, want: true},
		{name: "ph R-18 blocked under PG-13", item: Item{Title: "Manila R18", Rating: "ph/R-18"}, want: false},
		{name: "PH X blocked under PG-13 despite channel PG default", item: Item{Title: "Banned Film", Rating: "PH X"}, want: false},
		{name: "de 16 blocked under PG-13", item: Item{Title: "Berlin 16", Rating: "de/16"}, want: false},
		{name: "uncertified adult blocked despite channel PG fallback", item: itemFromTitle(core.Title{
			Key:      core.MovieKey(91),
			Type:     core.TypeMovie,
			Title:    "Softcore Unrated",
			Runtime:  90 * time.Minute,
			Keywords: []string{"softcore", "erotic"},
		}), want: false},
		{name: "genuinely unrated IPTV inherits channel PG fallback", item: Item{Title: "Local News", Rating: ""}, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Programme{Item: tc.item}
			p.Item.Key = 0 // exercise unkeyed programme rating path first
			if got := s.allowed(ctx, pg13, iptv, &p); got != tc.want {
				t.Fatalf("allowed(%s) = %v, want %v (item=%+v)", tc.name, got, tc.want, p.Item)
			}
		})
	}

	// Verify itemFromTitle preserves displayed Rating while computing EnforcementRating.
	foreignItem := itemFromTitle(core.Title{
		Key:           core.MovieKey(92),
		Type:          core.TypeMovie,
		Title:         "PH X Movie",
		ContentRating: "PH X",
		Runtime:       90 * time.Minute,
	})
	if foreignItem.Rating != "PH X" || foreignItem.EnforcementRating != "NC-17" {
		t.Fatalf("foreignItem = %+v, want Rating=\"PH X\" EnforcementRating=\"NC-17\"", foreignItem)
	}
	softcoreItem := itemFromTitle(core.Title{
		Key:      core.MovieKey(93),
		Type:     core.TypeMovie,
		Title:    "Uncertified Adult",
		Keywords: []string{"softcore", "nudity"},
		Runtime:  90 * time.Minute,
	})
	if softcoreItem.Rating != "" || softcoreItem.EnforcementRating != contentrating.UncertifiedAdult {
		t.Fatalf("softcoreItem = %+v", softcoreItem)
	}
	_ = owner
}

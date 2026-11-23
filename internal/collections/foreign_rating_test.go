package collections

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestForeignAndUncertifiedAdultCollectionEnforcement(t *testing.T) {
	s := &Service{log: slog.Default(), now: time.Now}
	in := newInterner()
	titles := []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "GB Teen", ContentRating: "gb/12A", EnforcementRating: "PG-13"},
		{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "PH R18", ContentRating: "ph/R-18", EnforcementRating: "NC-17"},
		{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "PH X", ContentRating: "PH X", EnforcementRating: "NC-17"},
		{Key: core.MovieKey(4), Type: core.TypeMovie, Title: "Uncertified Softcore", ContentRating: "", EnforcementRating: contentrating.UncertifiedAdult, Keywords: []string{"softcore", "erotic"}},
		{Key: core.MovieKey(5), Type: core.TypeMovie, Title: "DE 16", ContentRating: "de/16", EnforcementRating: "R"},
	}
	entries := map[core.RatingKey]*entry{}
	var keys []core.RatingKey
	for _, it := range titles {
		entries[it.Key] = entryFromTitle(it, in)
		keys = append(keys, it.Key)
	}
	snap := &snapshot{entries: entries}
	pg13 := &core.User{ID: 2, MaxContentRating: "PG-13"}
	got := s.filterForUser(context.Background(), keys, personalRules{}, pg13, snap)
	if !slices.Equal(got, []core.RatingKey{core.MovieKey(1)}) {
		t.Fatalf("filterForUser PG-13 = %v, want [1]", got)
	}
	// Franchise member filtering also enforces mapped foreign ratings while keeping ContentRating unchanged.
	franchiseGot := filterTitlesByRating(titles, pg13)
	if len(franchiseGot) != 1 || franchiseGot[0].Key != core.MovieKey(1) || franchiseGot[0].ContentRating != "gb/12A" {
		t.Fatalf("filterTitlesByRating PG-13 = %+v", franchiseGot)
	}
}

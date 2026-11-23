package experience

import (
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestDailyFacetPoolRotatesWithoutAlphabeticalTies(t *testing.T) {
	buckets := make([]frontend.FacetBucket, 0, 31)
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("dev-%02d", i)
		buckets = append(buckets, frontend.FacetBucket{ID: id, Label: id, Count: 500})
	}
	buckets = append(buckets, frontend.FacetBucket{ID: "aaa-small", Label: "aaa-small", Count: 1})

	const day = "2026-11-19"
	pool := dailyFacetPool(buckets, core.LibraryID(9), "developers", day, 24)
	if len(pool) != 24 {
		t.Fatalf("pool has %d buckets, want 24", len(pool))
	}
	for _, b := range pool {
		if b.ID == "aaa-small" {
			t.Fatal("smallest bucket entered the pool ahead of larger buckets")
		}
	}

	again := dailyFacetPool(buckets, core.LibraryID(9), "developers", day, 24)
	for i := range pool {
		if again[i].ID != pool[i].ID {
			t.Fatalf("pool is not stable within a day: [%d] %q != %q", i, again[i].ID, pool[i].ID)
		}
	}

	alphabetical := true
	for i := range pool {
		if pool[i].ID != fmt.Sprintf("dev-%02d", i) {
			alphabetical = false
			break
		}
	}
	if alphabetical {
		t.Fatal("equal-size buckets fell back to A-Z order")
	}

	inPool := func(pool []frontend.FacetBucket) map[string]bool {
		set := make(map[string]bool, len(pool))
		for _, b := range pool {
			set[b.ID] = true
		}
		return set
	}
	nextDay := dailyFacetPool(buckets, core.LibraryID(9), "developers", "2026-11-20", 24)
	today, tomorrow := inPool(pool), inPool(nextDay)
	rotated := false
	for id := range today {
		if !tomorrow[id] {
			rotated = true
		}
	}
	for id := range tomorrow {
		if !today[id] {
			rotated = true
		}
	}
	if !rotated {
		t.Fatal("equal-size buckets did not rotate between days")
	}

	otherLibrary := dailyFacetPool(buckets, core.LibraryID(10), "developers", day, 24)
	same := len(otherLibrary) == len(pool)
	if same {
		for i := range pool {
			if otherLibrary[i].ID != pool[i].ID {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatal("pool should differ between libraries on the same day")
	}
}

func TestNextNonPeopleStringFacetSkipsPeopleAndNumericFacets(t *testing.T) {
	facets := []frontend.ViewFacet{
		{ID: "developers", Type: "strings"},
		{ID: "voiceCast", Type: "people"},
		{ID: "year", Type: "int"},
		{ID: "genres", Type: "strings"},
	}
	facet, ok := nextNonPeopleStringFacet(facets)
	if !ok || facet.ID != "genres" {
		t.Fatalf("nextNonPeopleStringFacet = %+v, %v; want genres", facet, ok)
	}
	if _, ok := nextNonPeopleStringFacet(facets[:3]); ok {
		t.Fatal("nextNonPeopleStringFacet should report none when only people/numeric facets follow")
	}
}

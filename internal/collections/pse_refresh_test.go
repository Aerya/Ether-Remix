package collections

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPSERefreshEvery(t *testing.T) {
	for in, want := range map[string]time.Duration{"6h": 6 * time.Hour, "1h": time.Hour, "168h": 168 * time.Hour} {
		if d, ok := pseRefreshEvery(in); !ok || d != want || refreshInterval(in) != want {
			t.Fatalf("%s : %v %v", in, d, ok)
		}
	}
	for _, in := range []string{"0h", "169h", "06h", "h", "6", "6m", "-2h", "daily"} {
		if _, ok := pseRefreshEvery(in); ok {
			t.Fatalf("%s accepté", in)
		}
	}
	if refreshInterval("daily") != 24*time.Hour || refreshInterval("hourly") != time.Hour {
		t.Fatal("cadences d'Ether inchangées")
	}
}

func TestPSERefreshValidates(t *testing.T) {
	sp := core.CollectionSpec{Name: "X", MediaType: "movie", Match: "all", Sort: "popularity", Refresh: "6h",
		Artwork: core.CollectionArtwork{Mode: "auto"}, Sources: []core.CollectionSource{{Kind: "stevenlu", List: "movies"}}}
	if err := Validate(&sp); err != nil {
		t.Fatalf("6h refusé : %v", err)
	}
	sp.Refresh = "200h"
	if err := Validate(&sp); err == nil {
		t.Fatal("200h accepté")
	}
}

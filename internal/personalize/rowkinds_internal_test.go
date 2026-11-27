package personalize

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRowSourceRegistry(t *testing.T) {
	reg := NewRowKindRegistry()
	srcs := reg.Sources()
	if len(srcs) < len(kinds) {
		t.Fatalf("sources = %d, want at least the %d built-in kinds", len(srcs), len(kinds))
	}
	seen := map[string]bool{}
	for _, s := range srcs {
		if s.Kind == "" || s.Title == "" || s.Group == "" {
			t.Fatalf("incomplete source %+v", s)
		}
		if seen[s.Kind] {
			t.Fatalf("source %s registered twice", s.Kind)
		}
		seen[s.Kind] = true
	}
	trending, ok := reg.Source(KindTrending)
	if !ok || trending.Personal || !trending.Split || trending.Title != "Trending" {
		t.Fatalf("trending = %+v", trending)
	}
	if err := trending.ValidateParams(map[string]string{"window": "day"}); err != nil {
		t.Fatal(err)
	}
	if trending.ValidateParams(map[string]string{"window": "month"}) == nil || trending.ValidateParams(map[string]string{"colour": "red"}) == nil {
		t.Fatal("trending accepted a bad option")
	}
	because, _ := reg.Source(KindBecause)
	if !because.Personal || because.ValidateParams(map[string]string{"count": "2"}) != nil || because.ValidateParams(map[string]string{"count": "9"}) == nil {
		t.Fatalf("because-you-watched = %+v", because)
	}
	coll, _ := reg.Source(SourceCollection)
	if coll.ValidateParams(nil) == nil {
		t.Fatal("a collection row needs its collection")
	}
	airing, _ := reg.Source(KindAiringToday)
	if !hasCaps([]string{core.CapCharts, core.CapEpisodes}, airing.Needs) || hasCaps([]string{core.CapCharts, core.CapMovies}, airing.Needs) {
		t.Fatalf("airing needs = %v", airing.Needs)
	}

	// Runtime sources register and unregister.
	fake := staticSource{core.RowSourceInfo{Kind: "plugin:x/y", Title: "Plugin list", Group: groupPlugins}}
	if err := reg.Register(fake); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(fake); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	if _, ok := reg.Source("plugin:x/y"); !ok {
		t.Fatal("runtime source missing")
	}
	reg.Unregister("plugin:x/y")
	if _, ok := reg.Source("plugin:x/y"); ok {
		t.Fatal("runtime source still registered")
	}
	if len(reg.Descriptors()) != len(reg.Sources()) {
		t.Fatal("extend descriptors don't match the sources")
	}
}

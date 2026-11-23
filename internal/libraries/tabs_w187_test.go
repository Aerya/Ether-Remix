package libraries

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestValidateTabsPluginLevels(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	pluginLib := core.Library{
		ID:       99,
		Key:      "music",
		Name:     "Music",
		Source:   core.LibraryPlugin,
		PluginID: "community.example.music",
		KindKey:  "music",
	}
	schema := &PluginTabSchema{
		Levels:           map[string]bool{"artist": true, "album": true, "track": true},
		CollectionFacets: map[string]bool{"genre": true, "year": true},
		PeopleFacets:     map[string]bool{"composer": true},
		Sorts:            map[string]bool{"title": true, "released": true, "lastViewed": true},
	}

	valid := []core.LibraryTab{
		{Key: "recommended", Label: "Recommended", Kind: core.TabRecommended},
		{Key: "artists", Label: "Artists", Kind: core.TabLibrary, Level: "artist"},
		{Key: "albums", Label: "Albums", Kind: core.TabLibrary, Level: "album"},
		{Key: "recent-tracks", Label: "Recent Tracks", Kind: core.TabView, Level: "track", Sort: "lastViewed", Direction: "desc"},
		{Key: "album-genres", Label: "Genres", Kind: core.TabCollections, Level: "album", Facet: "genre"},
		{Key: "composers", Label: "Composers", Kind: core.TabPeople, Level: "track", Facet: "composer"},
	}
	got, err := s.ValidateTabs(ctx, pluginLib, valid, schema)
	if err != nil {
		t.Fatalf("valid plugin level tabs failed: %v", err)
	}
	if got[2].Level != "album" || got[3].Level != "track" || got[4].Level != "album" || got[5].Level != "track" {
		t.Fatalf("validated levels = %+v", got)
	}

	// Undeclared level must fail.
	if _, err := s.ValidateTabs(ctx, pluginLib, []core.LibraryTab{
		{Key: "discs", Label: "Discs", Kind: core.TabLibrary, Level: "disc"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared level err = %v, want ErrInvalid", err)
	}

	// Level on recommended or live tabs must fail.
	if _, err := s.ValidateTabs(ctx, pluginLib, []core.LibraryTab{
		{Key: "rec", Label: "Rec", Kind: core.TabRecommended, Level: "album"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("level on recommended tab err = %v, want ErrInvalid", err)
	}
	if _, err := s.ValidateTabs(ctx, pluginLib, []core.LibraryTab{
		{Key: "live", Label: "Live", Kind: core.TabLive, Level: "track"},
	}, schema); !errors.Is(err, ErrInvalid) {
		t.Fatalf("level on live tab err = %v, want ErrInvalid", err)
	}

	// Non-plugin catalogue library must reject level.
	movies, err := s.Get(ctx, core.LibraryMovies)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ValidateTabs(ctx, movies, []core.LibraryTab{
		{Key: "library", Label: "Library", Kind: core.TabLibrary, Level: "album"},
	}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("level on catalogue library err = %v, want ErrInvalid", err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/sessions"
)

func TestPublishedTVFilterLibraryProfile(t *testing.T) {
	a := setupTestAppConfigured(t, true, Options{}, func(c *config.Config) {
		c.Quality.Profiles = append(c.Quality.Profiles, core.QualityProfile{Name: "Anime 1080p", MaxHeight: 1080})
	})
	ctx := context.Background()
	show := core.Title{Key: core.ShowKey(464), Type: core.TypeShow, Title: "Anime show", Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Animation"}}
	if err := a.catalog.UpsertTitle(ctx, show, true); err != nil {
		t.Fatal(err)
	}
	var present core.Presentations
	if err := json.Unmarshal([]byte(`{"plex":{"base":"show"}}`), &present); err != nil {
		t.Fatal(err)
	}
	lib, err := a.libraries.Create(ctx, "Anime Shows", core.TypeShow, libraryRules(core.Filter{Type: core.TypeShow, Genres: []string{"Animation"}}), "hide", present)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules = []config.QualityRule{{ID: "r_anime", Libraries: []int64{int64(lib.ID)}, Profile: "Anime 1080p"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	exp, err := a.newSurfaceExperience("plex")
	if err != nil {
		t.Fatal(err)
	}
	srv := plex.NewServer(plex.Deps{Host: frontend.Host{Experience: exp, Sessions: sessions.NewLocalRuntime(sessions.LocalRuntimeDeps{})}, DisableEnrichment: true})
	defer srv.Close()
	found := false
	for _, sec := range srv.LibrarySections() {
		if sec.LibraryID == lib.ID {
			found = sec.Title == "Anime Shows" && sec.Type == core.TypeShow
		}
	}
	if !found {
		t.Fatal("published filter library missing or renamed")
	}
	profiles := newLibraryProfiles(a)
	for _, title := range []*core.Title{
		{Key: core.EpisodeKey(464, 1, 1), Type: core.TypeEpisode, ShowKey: show.Key},
		{Key: core.SeasonKey(464, 1), Type: core.TypeSeason, ShowKey: show.Key},
		&show,
		{Key: core.EpisodeKey(464, 1, 2), Type: core.TypeEpisode},
		{Key: core.SeasonKey(464, 2), Type: core.TypeSeason},
	} {
		if !profiles.InLibrary(ctx, title, lib.ID) {
			t.Fatalf("%s not reported in library %d", title.Type, lib.ID)
		}
		if got := a.resolver.ProfileForTitle(ctx, core.ClientCaps{Protocol: "plex"}, nil, title).Name; got != "Anime 1080p" {
			t.Fatalf("%s profile = %q, want Anime 1080p", title.Type, got)
		}
	}
	profiles.mu.Lock()
	_, cached := profiles.cache.Peek(show.Key)
	profiles.mu.Unlock()
	if !cached {
		t.Fatal("missing parent show cache entry")
	}
}

// Overlapping libraries must make the same choice for every client entry point.
func TestLibraryProfileResolutionInvariant(t *testing.T) {
	a := setupTestAppConfigured(t, true, Options{}, func(c *config.Config) {
		c.Quality.Profiles = []core.QualityProfile{
			{Name: "Home", MaxHeight: 2160},
			{Name: "Custom", MaxHeight: 720},
			{Name: "Other", MaxHeight: 1080},
			{Name: "Shows", MaxHeight: 1080},
		}
		c.Quality.Default = "Home"
		c.Quality.DefaultAway = ""
	})
	ctx := t.Context()
	title := core.Title{Key: core.MovieKey(311), Type: core.TypeMovie, Title: "Test", Year: 2000, Rating: 7, Genres: []string{"Animation"}}
	if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	var libs []core.Library
	for _, name := range []string{"Default library", "First assigned", "Second assigned"} {
		lib, err := a.libraries.Create(ctx, name, core.TypeMovie, libraryRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Animation"}}), "hide", core.Presentations{})
		if err != nil {
			t.Fatal(err)
		}
		libs = append(libs, lib)
	}
	// Compound visible rules must use membership mode too: this title has
	// no release date, but its source/rule membership still assigns a profile.
	if err := a.libraries.SetRules(ctx, libs[1].ID, libraryRules(core.Filter{Type: core.TypeMovie, Genres: []string{"Animation"}, YearFrom: 1990, MinRating: 6}), "hide"); err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules = []config.QualityRule{
			{ID: "r_first", Libraries: []int64{int64(libs[1].ID)}, Profile: "Custom"},
			{ID: "r_second", Libraries: []int64{int64(libs[2].ID)}, Profile: "Other"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lookup := newLibraryProfiles(a)
	if !lookup.InLibrary(ctx, &title, libs[1].ID) {
		t.Fatal("title not in First assigned library")
	}
	for _, protocol := range []string{"plex", "jellyfin", "emby", "silo", "stremio", "xtream", "vela"} {
		caps := core.ClientCaps{Protocol: core.Protocol(protocol)}
		if got := a.playbackProfile(caps, nil, title.Key); got.Name != "Custom" {
			t.Fatalf("%s playback = %q", protocol, got.Name)
		}
		if got := a.resolver.ProfileForTitle(ctx, caps, nil, &title); got.Name != "Custom" {
			t.Fatalf("%s resolver = %q", protocol, got.Name)
		}
	}
	// An unrelated collection must not capture a title merely because it exists.
	collection, err := a.libraries.CreateCollection(ctx, "Unrelated", 311, core.TypeMovie, core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules = append(c.Quality.Rules, config.QualityRule{ID: "r_coll", Libraries: []int64{int64(collection.ID)}, Profile: "Other"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	outsider := core.Title{Key: core.MovieKey(312), Type: core.TypeMovie, Title: "Outside"}
	if err := a.catalog.UpsertTitle(ctx, outsider, true); err != nil {
		t.Fatal(err)
	}
	if lookup.InLibrary(ctx, &outsider, collection.ID) {
		t.Fatal("nonmember captured by collection")
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO collections_members(spec_id,pos,key,shared) VALUES(311,0,?,1)`, outsider.Key); err != nil {
		t.Fatal(err)
	}
	// A library or config edit invalidates membership immediately; catalogue writes do too.
	if err := a.libraries.SetRules(ctx, collection.ID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if !lookup.InLibrary(ctx, &outsider, collection.ID) {
		t.Fatal("collection member not recognized after library edit")
	}
	if got := a.resolver.ProfileForTitle(ctx, core.ClientCaps{}, nil, &outsider).Name; got != "Other" {
		t.Fatalf("collection member profile = %q, want Other", got)
	}
	before := lookup.PolicyKey(ctx)
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality.Rules[0].Profile = "Shows"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := a.resolver.ProfileForTitle(ctx, core.ClientCaps{}, nil, &title).Name; got != "Shows" {
		t.Fatalf("library choice = %q, want Shows", got)
	}
	if before == lookup.PolicyKey(ctx) {
		t.Fatal("rule edit did not invalidate evidence policy")
	}
	if got := a.playbackProfile(core.ClientCaps{}, nil, core.PluginKey(1)); got.Name != "" || got.MinHeight != 0 {
		t.Fatalf("plugin video policy = %+v", got)
	}
}

func libraryRules(f core.Filter) []core.LibraryExclusion {
	return []core.LibraryExclusion{{Action: "show", MediaType: string(f.Type), Match: "all", Rules: core.LibraryPredicateConditions(f)}}
}

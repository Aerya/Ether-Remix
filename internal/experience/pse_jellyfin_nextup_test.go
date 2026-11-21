package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPSEJellyfinNextUpAcceptsThemeTitlesOnly(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inTheme, outside := core.ShowKey(5), core.ShowKey(6)
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{{Section: 5101, Title: "Animation", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "series", Title: "Séries"}}}},
		rows:   map[string][]core.RatingKey{"series": {inTheme}},
	})
	t.Cleanup(func() { frontend.SetPSEThemes(nil) })
	watched := map[core.RatingKey]core.WatchStatus{}
	var history []core.HistoryEntry
	for _, tmdb := range []int64{5, 6} {
		key := core.EpisodeKey(tmdb, 1, 1)
		watched[key] = core.WatchStatus{ViewCount: 1}
		history = append(history, core.HistoryEntry{Key: key, Completed: true, ViewedAt: now})
	}
	c := Continue{
		State: continueState{history: history, values: watched}, Now: func() time.Time { return now },
		GetTitles: func(context.Context, []core.RatingKey) []core.Title { return nil },
		NextEpisode: func(_ context.Context, key core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: core.EpisodeKey(key.TMDB(), 1, 2), ShowKey: core.ShowKey(key.TMDB()), Type: core.TypeEpisode, Season: 1}, nil
		},
		Visible:            func(_ context.Context, _ *core.User, title *core.Title) bool { return title.Key.TMDB() != 7 },
		SameDayUnavailable: func(context.Context, *core.Title) bool { return false },
		// A theme-only profile has no base section.
		SectionAllowed: func(*core.User, core.MediaType) bool { return false },
	}
	themeOnly := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{5100, 5101}}
	themed := context.WithValue(context.Background(), pseThemeNextUpKey{}, true)

	got, _ := c.InProgress(themed, themeOnly, core.TypeShow, 10)
	if len(got) != 1 || got[0].Key != core.EpisodeKey(5, 1, 2) {
		t.Fatalf("theme-only next up %+v", got)
	}
	if got, _ := c.InProgress(context.Background(), themeOnly, core.TypeShow, 10); len(got) != 0 {
		t.Fatalf("an unmarked request must keep the base rule: %+v", got)
	}
	// The parental check still applies to a title of the theme.
	frontend.SetPSEThemes(activityThemes{
		themes: []frontend.PSETheme{{Section: 5101, Title: "Animation", Type: core.TypeShow, Rows: []frontend.PSEThemeRow{{ID: "series", Title: "Séries"}}}},
		rows:   map[string][]core.RatingKey{"series": {inTheme, outside}},
	})
	c.Visible = func(_ context.Context, _ *core.User, title *core.Title) bool { return title.Key.TMDB() != 5 }
	if got, _ := c.InProgress(themed, &core.User{ID: 2, AllowedLibraries: []core.LibraryID{5100, 5101}}, core.TypeShow, 10); len(got) != 1 || got[0].Key.TMDB() != 6 {
		t.Fatalf("parental control lost: %+v", got)
	}
	// A normal profile keeps exactly its result, marked request or not.
	c.SectionAllowed = func(*core.User, core.MediaType) bool { return true }
	c.Visible = func(context.Context, *core.User, *core.Title) bool { return true }
	normal := &core.User{ID: 3, AllowedLibraries: []core.LibraryID{1, 2}}
	for _, ctx := range []context.Context{context.Background(), themed} {
		if got, _ := c.InProgress(ctx, normal, core.TypeShow, 10); len(got) != 2 {
			t.Fatalf("normal profile %+v", got)
		}
	}
}

func TestPSEJellyfinNextUpMarksOnlyJellyfin(t *testing.T) {
	for protocol, want := range map[string]bool{"jellyfin": true, "plex": false, "emby": false} {
		s := &Service{d: ServiceDeps{Protocol: core.Protocol(protocol)}}
		got, _ := s.pseJellyfinNextUpContext(context.Background()).Value(pseThemeNextUpKey{}).(bool)
		if got != want {
			t.Errorf("%s: marked %v, want %v", protocol, got, want)
		}
	}
}

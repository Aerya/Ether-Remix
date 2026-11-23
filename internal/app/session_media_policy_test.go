package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

func TestCachedSessionTitleNeverRefreshesMetadata(t *testing.T) {
	ctx := context.Background()
	episode := core.EpisodeKey(4, 1, 1)
	show := core.ShowKey(4)
	calls := 0
	remote := 0
	titles := map[core.RatingKey]*core.Title{episode: {Key: episode, Type: core.TypeEpisode, ShowKey: show, ContentRating: "PG"}, show: {Key: show, Type: core.TypeShow, ContentRating: "PG"}}
	cached := func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		calls++
		if titles[k] == nil {
			return nil, core.ErrNotFound
		}
		return titles[k], nil
	}
	vis := experience.Visibility{GetTitle: func(context.Context, core.RatingKey) (*core.Title, error) { remote++; return nil, core.ErrNotFound }}
	viewer := &core.Viewer{ID: 1, MaxContentRating: "PG"}
	for i := 0; i < 10; i++ {
		if _, err := cachedSessionTitle(ctx, viewer, episode, cached, vis); err != nil {
			t.Fatal(err)
		}
	}
	if remote != 0 || calls != 20 {
		t.Fatalf("remote=%d cached=%d", remote, calls)
	}
	titles[show].ContentRating = "R"
	if _, err := cachedSessionTitle(ctx, viewer, episode, cached, vis); err == nil {
		t.Fatal("restricted parent allowed")
	}
	delete(titles, show)
	if _, err := cachedSessionTitle(ctx, viewer, episode, cached, vis); err == nil {
		t.Fatal("missing parent allowed")
	}
	if remote != 0 {
		t.Fatalf("remote metadata calls: %d", remote)
	}
}

func TestSessionAdmissionLibraryDeletionAndRetainedPolicy(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := core.WithLiveApp(context.Background(), "plex")
	title := core.Title{Key: core.MovieKey(9123), Type: core.TypeMovie, Title: "Retained", Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), HomeReleased: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	vis := experience.Visibility{GetTitle: a.catalog.CachedGet, Policy: a.safety}
	viewer := &core.Viewer{ID: 1}
	check := func(retained bool, want bool) {
		t.Helper()
		for _, cached := range []bool{false, true} {
			_, err := a.librarySessionTitle(ctx, viewer, title.Key, vis, cached, retained)
			if (err == nil) != want {
				t.Fatalf("retained=%v cached=%v err=%v", retained, cached, err)
			}
		}
	}
	check(false, true)
	if err := a.libraries.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	check(false, false)
	check(true, true)
	var present core.Presentations
	if err := json.Unmarshal([]byte(`{"plex":{"base":"movie"}}`), &present); err != nil {
		t.Fatal(err)
	}
	lib, err := a.libraries.Create(ctx, "Other movies", core.TypeMovie, nil, "show", present)
	if err != nil {
		t.Fatal(err)
	}
	viewer.AllowedLibraries = []core.LibraryID{lib.ID}
	check(false, true)
	viewer.AllowedLibraries = []core.LibraryID{2}
	check(false, false)
	check(true, false)
	viewer.AllowedLibraries = []core.LibraryID{1}
	check(true, true)
	check(false, false)
	viewer.AllowedLibraries = nil
	if err := a.libraries.SetPresent(ctx, lib.ID, core.Presentations{}); err != nil {
		t.Fatal(err)
	}
	check(false, false)
}

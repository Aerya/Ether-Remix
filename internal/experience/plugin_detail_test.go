package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type fakeArtistDetailPluginLibraries struct {
	fakePluginPeopleLibraries
	children map[core.RatingKey][]core.Title
}

func (f *fakeArtistDetailPluginLibraries) Children(_ context.Context, parent core.RatingKey, _ bool, _ string, limit int) (PluginBrowsePage, error) {
	items := f.children[parent]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return PluginBrowsePage{Items: items, Total: len(f.children[parent]), TotalExact: true}, nil
}

func TestPluginDetailArtistTopTracksSynthesis(t *testing.T) {
	ctx := context.Background()
	artistKey := core.PluginKey(400)
	albumKey := core.PluginKey(401)
	bookKey := core.PluginKey(402)

	artist := core.Title{
		Key:        artistKey,
		LibraryID:  11,
		Type:       core.TypeShow,
		Title:      "Daft Punk",
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleArtist,
	}
	album := core.Title{
		Key:        albumKey,
		LibraryID:  11,
		Type:       core.TypeSeason,
		Title:      "Discovery",
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleAlbum,
	}
	book := core.Title{
		Key:        bookKey,
		LibraryID:  12,
		Type:       core.TypeMovie,
		Title:      "Dune",
		MediaClass: core.MediaClassBook,
		MediaRole:  core.MediaRoleBook,
	}

	tracks := []core.Title{
		album, // non-track container must be skipped
		{Key: core.PluginKey(410), LibraryID: 11, Type: core.TypeEpisode, Title: "One More Time", Rating: 9.8, Popularity: 100, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(411), LibraryID: 11, Type: core.TypeEpisode, Title: "One More Time", Rating: 7.0, Popularity: 20, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(412), LibraryID: 11, Type: core.TypeEpisode, Title: "Harder, Better, Faster, Stronger", Rating: 9.5, Popularity: 95, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(413), LibraryID: 11, Type: core.TypeEpisode, Title: "Digital Love", Rating: 9.2, Popularity: 80, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(414), LibraryID: 11, Type: core.TypeEpisode, Title: "Aerodynamic", Rating: 9.2, Popularity: 90, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(415), LibraryID: 11, Type: core.TypeEpisode, Title: "Voyager", Rating: 8.5, Popularity: 50, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(416), LibraryID: 11, Type: core.TypeEpisode, Title: "Crescendolls", Rating: 8.5, Popularity: 50, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(417), LibraryID: 11, Type: core.TypeEpisode, Title: "Veridis Quo", Rating: 8.9, Popularity: 75, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(418), LibraryID: 11, Type: core.TypeEpisode, Title: "Face to Face", Rating: 8.8, Popularity: 70, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(419), LibraryID: 11, Type: core.TypeEpisode, Title: "Something About Us", Rating: 9.1, Popularity: 85, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(420), LibraryID: 11, Type: core.TypeEpisode, Title: "Too Long", Rating: 8.0, Popularity: 40, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(421), LibraryID: 11, Type: core.TypeEpisode, Title: "Nightvision", Rating: 7.5, Popularity: 30, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
		{Key: core.PluginKey(422), LibraryID: 11, Type: core.TypeEpisode, Title: "Short Circuit", Rating: 7.2, Popularity: 25, MediaClass: core.MediaClassMusic, MediaRole: core.MediaRoleTrack},
	}

	plug := &fakeArtistDetailPluginLibraries{
		fakePluginPeopleLibraries: fakePluginPeopleLibraries{
			fakePluginLibraries: fakePluginLibraries{
				libs: []core.Library{
					{ID: 11, Key: "plugin:community.music:music", Name: "Music", Source: core.LibraryPlugin, PluginID: "community.music", KindKey: "music", MediaType: core.TypeShow},
					{ID: 12, Key: "plugin:community.books:book", Name: "Books", Source: core.LibraryPlugin, PluginID: "community.books", KindKey: "book", MediaType: core.TypeMovie},
				},
			},
			items: map[core.RatingKey]core.Title{
				artistKey: artist,
				albumKey:  album,
				bookKey:   book,
			},
		},
		children: map[core.RatingKey][]core.Title{
			artistKey: tracks,
		},
	}

	svc, cat := peopleFixture()
	cat.titles = map[core.RatingKey]core.Title{}
	svc.d.PluginLibraries = plug
	svc.d.LibraryPresentation = func(_ core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
		return mt, "music", true
	}
	viewer := &core.User{ID: 1}

	rows, err := svc.Rows(ctx, frontend.RowRequest{
		Viewer:    viewer,
		Placement: frontend.PlacementDetail,
		Item:      artistKey,
	})
	if err != nil {
		t.Fatalf("Rows(PlacementDetail, artist) err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (%+v)", len(rows), rows)
	}
	top := rows[0]
	if top.ID != "plugin:11:detail:top" || top.Title != "Top Tracks" || top.Purpose != frontend.RowRecommendation || top.Kind != "detail" || top.Style != "shelf" {
		t.Fatalf("unexpected synthesized top row: %+v", top)
	}
	if len(top.Items) != 10 {
		t.Fatalf("len(top.Items) = %d, want 10", len(top.Items))
	}
	wantOrder := []string{
		"One More Time",
		"Harder, Better, Faster, Stronger",
		"Aerodynamic",
		"Digital Love",
		"Something About Us",
		"Veridis Quo",
		"Face to Face",
		"Crescendolls",
		"Voyager",
		"Too Long",
	}
	for i, want := range wantOrder {
		if top.Items[i].Title != want {
			t.Fatalf("top.Items[%d] = %q, want %q", i, top.Items[i].Title, want)
		}
	}
	if top.Items[0].Key != core.PluginKey(410) {
		t.Fatalf("expected highest-rated duplicate key 410, got %v", top.Items[0].Key)
	}

	// SnapshotRows returns the same synthesized row and honors smaller Limit.
	snap, err := svc.SnapshotRows(ctx, frontend.RowRequest{
		Viewer:    viewer,
		Placement: frontend.PlacementDetail,
		Item:      artistKey,
		Limit:     3,
	})
	if err != nil || len(snap) != 1 || len(snap[0].Items) != 3 {
		t.Fatalf("SnapshotRows(limit=3) = (%+v, %v), want 1 row with 3 items", snap, err)
	}

	// Non-artist plugin items return an empty row list without error.
	bookRows, err := svc.Rows(ctx, frontend.RowRequest{
		Viewer:    viewer,
		Placement: frontend.PlacementDetail,
		Item:      bookKey,
	})
	if err != nil || len(bookRows) != 0 {
		t.Fatalf("Rows(book) = (%+v, %v), want empty slice", bookRows, err)
	}

	// Unknown plugin key returns ErrNotFound.
	if _, err := svc.Rows(ctx, frontend.RowRequest{
		Viewer:    viewer,
		Placement: frontend.PlacementDetail,
		Item:      core.PluginKey(99999),
	}); err == nil {
		t.Fatalf("expected error for unknown plugin key")
	}
}

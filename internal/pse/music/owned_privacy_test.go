package music

import (
	"context"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"testing"
)

func TestOwnedPrivacyCachesSnapshotsAndRevocation(t *testing.T) {
	h := newHarness(t, "")
	db := h.srv.o.Library.db
	if err := mediaaccess.Create(t.Context(), db, "ibroadcast:7", 7); err != nil {
		t.Fatal(err)
	}
	track := Track{ID: localmedia.AudioBase + 123, Title: "Private recording", Artist: Artist{ID: localmedia.AudioBase + 124, Name: "Private artist"}, Album: Album{ID: localmedia.AudioBase + 125, Title: "Private album"}}
	if err := ReplaceOwned(t.Context(), db, "ibroadcast:7", []Track{track}); err != nil {
		t.Fatal(err)
	}
	source := &OwnedSource{DB: db}
	h.srv.o.Source = source
	ctx := func(id int64) context.Context { return withMusicViewer(t.Context(), id) }
	// Populate owner caches first, then ask as another viewer.
	_, _, tt := h.srv.Search(ctx(7), "Private", 20)
	if len(tt) != 1 {
		t.Fatal(tt)
	}
	_, _, tt = h.srv.Search(ctx(8), "Private", 20)
	if len(tt) != 0 {
		t.Fatal("search cache leaked", tt)
	}
	if _, _, err := h.srv.AlbumTracks(ctx(7), track.Album.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.srv.AlbumTracks(ctx(8), track.Album.ID); err == nil {
		t.Fatal("album cache leaked")
	}
	if err := mediaaccess.Update(ctx(7), db, "ibroadcast:7", 7, mediaaccess.Policy{Viewers: []int64{8}}); err != nil {
		t.Fatal(err)
	}
	_, _, tt = h.srv.Search(ctx(8), "Private", 20)
	if len(tt) != 1 {
		t.Fatal("share did not refresh", tt)
	}
	if err := h.srv.o.Library.StarTrack(ctx(8), 8, track, true); err != nil {
		t.Fatal(err)
	}
	playlists := &Playlists{DB: db}
	pl, err := playlists.CreatePersonal(ctx(8), 8, "Saved", []Track{track})
	if err != nil {
		t.Fatal(err)
	}
	if err := mediaaccess.Update(ctx(7), db, "ibroadcast:7", 7, mediaaccess.Policy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.srv.TrackFor(ctx(8), 8, track.ID); err == nil {
		t.Fatal("snapshot bypassed revoked access")
	}
	_, _, tt = h.srv.Search(ctx(8), "Private", 20)
	if len(tt) != 0 {
		t.Fatal("revoked cache leaked")
	}
	if items, err := h.srv.o.Library.List(ctx(8), 8, KindTrack, ListOptions{}); err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	if got, _, err := playlists.Get(ctx(8), 8, pl.ID); err != nil || len(got.Tracks) != 0 {
		t.Fatal(got, err)
	}
	if _, err := source.Track(t.Context(), track.ID); err == nil {
		t.Fatal("anonymous lookup succeeded")
	}
}

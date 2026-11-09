package music

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlayQueueFromMusicHubPlaylist(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const viewer = int64(1)
	pls := &Playlists{DB: db}
	s := NewServer(Options{Source: trackSource{}, Library: NewLibrary(db), Playlists: pls})
	pl, err := pls.CreatePersonal(ctx, viewer, "Top Monde", []Track{{ID: 42, Title: "Titre"}})
	if err != nil {
		t.Fatal(err)
	}

	q := &psemReq{m: s, viewer: viewer, r: httptest.NewRequest("POST", "/playQueues", nil)}
	uri := "server://machine/com.plexapp.plugins.library/music.pl." + pl.Ref
	tracks, ok := q.playlistTracksFor(url.Values{"uri": {uri}})
	if !ok || len(tracks) != 1 || tracks[0].ID != 42 {
		t.Fatalf("rangée non retrouvée : ok=%v tracks=%v", ok, tracks)
	}
	if _, ok := q.playlistTracksFor(url.Values{"uri": {"server://machine/com.plexapp.plugins.library/music.pl.inconnue"}}); ok {
		t.Fatal("une rangée inconnue ne doit pas désigner une playlist")
	}
	if tracks, ok := q.tracksForURI("/library/metadata/" + psemKey(psemTrack, 42)); !ok || len(tracks) != 1 {
		t.Fatalf("élément choisi non retrouvé : ok=%v tracks=%v", ok, tracks)
	}
}

package music

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlaylistQueueAcceptsJavaScriptRoundedKey(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const viewer = int64(1)
	pls := &Playlists{DB: db}
	s := NewServer(Options{Source: trackSource{}, Library: NewLibrary(db), Playlists: pls})

	var exact int64
	var rounded string
	for i := 0; exact == 0 || rounded == strconv.FormatInt(exact, 10); i++ {
		pl, err := pls.CreatePersonal(ctx, viewer, "Découvertes "+strconv.Itoa(i), []Track{{ID: 42, Title: "Titre"}})
		if err != nil {
			t.Fatal(err)
		}
		exact = int64(psemPlaylist)*psemBase + pl.ID
		rounded = strconv.FormatFloat(float64(exact), 'f', 0, 64)
	}

	q := &psemReq{m: s, viewer: viewer, r: httptest.NewRequest("POST", "/playQueues", nil)}
	tracks, ok := q.playlistTracksFor(url.Values{"playlistID": {rounded}})
	if !ok || len(tracks) != 1 || tracks[0].ID != 42 {
		t.Fatalf("playlist arrondie non retrouvée : ok=%v tracks=%v", ok, tracks)
	}
	if _, ok := q.playlistTracksFor(url.Values{"playlistID": {"53000000000000008"}}); ok {
		t.Fatal("une clé inconnue ne doit pas désigner une playlist")
	}
}

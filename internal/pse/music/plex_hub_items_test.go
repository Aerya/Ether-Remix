package music

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestMusicHubAddressesAreServed(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pls := &Playlists{DB: db}
	if err := pls.replace(ctx, 1, SourceCharts, []Playlist{{Ref: "top-ww", Name: "Top Monde", Tracks: []Track{{ID: 42, Title: "Titre"}}}}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Options{Source: trackSource{}, Library: NewLibrary(db), Playlists: pls})

	r := httptest.NewRequest("GET", "/music.pl.top-ww", nil)
	if !psemCandidate(r) {
		t.Fatal("l'adresse d'une rangée doit être traitée par la musique")
	}
	q := &psemReq{m: s, viewer: 1, r: r}
	found := false
	for _, hub := range q.musicHubs() {
		if hub.HubIdentifier == "music.pl.top-ww" && len(hub.Metadata) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("rangée music.pl.top-ww absente")
	}
}

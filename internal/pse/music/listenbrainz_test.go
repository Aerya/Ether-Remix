package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestParseListenBrainzAccount(t *testing.T) {
	for in, want := range map[string]string{"rob": "rob", "https://listenbrainz.org/user/mr_monkey/": "mr_monkey", " ": ""} {
		if got, err := ParseListenBrainzAccount(in); err != nil || got != want {
			t.Fatalf("%q → %q, %v", in, got, err)
		}
	}
	if _, err := ParseListenBrainzAccount("a b/c"); err == nil {
		t.Fatal("nom invalide accepté")
	}
	if lbSeries("Daily Jams for rob, 2026-11-08 Sun") != "Daily Jams" || lbSeries("Ma playlist") != "" {
		t.Fatal("série mal lue")
	}
}

// Import ListenBrainz : la dernière playlist de chaque série générée et
// les playlists de l'utilisateur, retrouvées dans le catalogue Deezer.
func TestListenBrainzImport(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dzSrv, _ := fakePlaylistServices(t)
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	list := func(title, mbid string) map[string]any {
		return map[string]any{"playlist": map[string]any{"title": title, "identifier": "https://listenbrainz.org/playlist/" + mbid}}
	}
	const m1, m2, m3 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"
	mux := http.NewServeMux()
	mux.HandleFunc("/1/user/rob/playlists/createdfor", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"playlists": []any{list("Daily Jams for rob, 2026-11-08 Sun", m1), list("Daily Jams for rob, 2026-11-07 Sat", m3)}})
	})
	mux.HandleFunc("/1/user/rob/playlists", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"playlists": []any{list("Mes favoris", m2)}})
	})
	mux.HandleFunc("/1/playlist/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/1/playlist/"+m3 {
			t.Error("ancienne Daily Jams importée")
		}
		reply(w, map[string]any{"playlist": map[string]any{"track": []any{
			map[string]any{"title": "Papaoutai", "creator": "Stromae"},
			map[string]any{"title": "Papaoutai (Live)", "creator": "Stromae"},
		}}})
	})
	lb := httptest.NewServer(mux)
	defer lb.Close()

	dz := NewDeezerSource(nil, dzSrv.Client())
	dz.API = dzSrv.URL
	pls := &Playlists{DB: db}
	sync := &PlaylistSync{Store: pls, Deezer: dz, ListenBrainz: lb.URL, HTTP: lb.Client(), Pace: 1}
	if err := pls.SetAccount(ctx, 7, ServiceListenBrainz, "rob"); err != nil {
		t.Fatal(err)
	}
	if err := sync.SyncViewer(ctx, 7); err != nil {
		t.Fatal(err)
	}
	lists, _ := pls.List(ctx, 7)
	if len(lists) != 2 || lists[0].Name != "Daily Jams · ListenBrainz" || lists[1].Name != "Mes favoris · ListenBrainz" {
		t.Fatalf("playlists : %+v", lists)
	}
	// La version live n'est pas confondue avec l'original.
	if len(lists[0].Tracks) != 1 || lists[0].Tracks[0].Title != "Papaoutai" {
		t.Fatalf("titres : %+v", lists[0].Tracks)
	}
}

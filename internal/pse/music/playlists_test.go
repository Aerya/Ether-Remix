package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestParsePlaylistAccounts(t *testing.T) {
	got, err := ParseDeezerAccount("https://www.deezer.com/fr/profile/42, 42 https://deezer.com/playlist/9001?utm=x user:7")
	if err != nil || got != "user:42 playlist:9001 user:7" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ParseDeezerAccount("https://link.deezer.com/s/abc"); err == nil || !strings.Contains(err.Error(), "lien court") {
		t.Fatalf("lien court : %v", err)
	}
	if _, err := ParseDeezerAccount("spotify:playlist:1"); err == nil {
		t.Fatal("valeur étrangère acceptée")
	}
	if v, err := ParseLastFMAccount("https://www.last.fm/fr/user/J_Doe-1"); err != nil || v != "J_Doe-1" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := ParseLastFMAccount("a b"); err == nil {
		t.Fatal("nom invalide accepté")
	}
}

func dzJSON(id int64, title, artist string, album int64, readable bool) map[string]any {
	return map[string]any{"id": id, "title": title, "duration": 200, "readable": readable,
		"artist": map[string]any{"id": 5, "name": artist},
		"album":  map[string]any{"id": album, "title": "Racine carrée", "md5_image": "0123456789abcdef0123456789abcdef"}}
}

func fakePlaylistServices(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var searches atomic.Int32
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/user/42/playlists", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"total": 2, "data": []any{
			map[string]any{"id": 900, "title": "Loved Tracks", "is_loved_track": true},
			map[string]any{"id": 901, "title": "Road trip"},
		}})
	})
	mux.HandleFunc("/playlist/900/tracks", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"total": 2, "data": []any{dzJSON(1, "Ta fête", "Stromae", 9, true), dzJSON(2, "Papaoutai", "Stromae", 9, true)}})
	})
	mux.HandleFunc("/playlist/901/tracks", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"total": 2, "data": []any{dzJSON(3, "Bâtard", "Stromae", 9, true), dzJSON(4, "Retiré", "Stromae", 9, false)}})
	})
	mux.HandleFunc("/search/track", func(w http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		if strings.Contains(r.URL.Query().Get("q"), "Stromae") {
			reply(w, map[string]any{"data": []any{dzJSON(2, "Papaoutai", "Stromae", 9, true)}})
			return
		}
		reply(w, map[string]any{"data": []any{}})
	})
	mux.HandleFunc("/2.0/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api_key") != "lfmkey" || q.Get("user") != "jdoe" {
			reply(w, map[string]any{"error": 6, "message": "User not found"})
			return
		}
		switch q.Get("method") {
		case "user.getlovedtracks":
			reply(w, map[string]any{"lovedtracks": map[string]any{"@attr": map[string]any{"totalPages": "1"}, "track": []any{
				map[string]any{"name": "Papaoutai (Radio Edit)", "artist": map[string]any{"name": "Stromae"}},
				map[string]any{"name": "Nope", "artist": map[string]any{"name": "Inconnu"}},
			}}})
		default:
			reply(w, map[string]any{"toptracks": map[string]any{"@attr": map[string]any{"totalPages": "0"}, "track": []any{}}})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &searches
}

func TestPlaylistSyncAndServe(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv, searches := fakePlaylistServices(t)
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	pls := &Playlists{DB: db}
	sync := &PlaylistSync{Store: pls, Deezer: dz, LastFM: srv.URL + "/2.0/", LastFMKey: func() string { return "lfmkey" }, HTTP: srv.Client(), Pace: 1}
	const viewer = 7
	acc, _ := ParseDeezerAccount("https://www.deezer.com/fr/profile/42")
	if err := pls.SetAccount(ctx, viewer, ServiceDeezer, acc); err != nil {
		t.Fatal(err)
	}
	if err := pls.SetAccount(ctx, viewer, ServiceLastFM, "jdoe"); err != nil {
		t.Fatal(err)
	}
	if err := sync.SyncViewer(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	lists, err := pls.List(ctx, viewer)
	if err != nil || len(lists) != 3 {
		t.Fatalf("%d playlists %v", len(lists), err)
	}
	want := []struct {
		name   string
		tracks int
	}{{"Coups de cœur · Deezer", 2}, {"Road trip · Deezer", 1}, {"Titres aimés · Last.fm", 1}}
	for i, w := range want {
		if lists[i].Name != w.name || len(lists[i].Tracks) != w.tracks {
			t.Fatalf("playlist %d : %s (%d)", i, lists[i].Name, len(lists[i].Tracks))
		}
	}
	if lists[2].Tracks[0].ID != 2 {
		t.Fatalf("titre Last.fm mal retrouvé : %+v", lists[2].Tracks[0])
	}
	n := searches.Load()
	if err := sync.SyncViewer(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if searches.Load() != n {
		t.Fatalf("les correspondances Last.fm doivent être gardées : %d → %d recherches", n, searches.Load())
	}
	accounts, _ := pls.Accounts(ctx, viewer)
	for _, a := range accounts {
		if a.SyncedAt == 0 || a.Error != "" {
			t.Fatalf("compte %+v", a)
		}
	}
	// Un autre spectateur ne voit rien.
	if other, _ := pls.List(ctx, 8); len(other) != 0 {
		t.Fatal("playlists partagées entre spectateurs")
	}

	s := NewServer(Options{Source: dz, Library: NewLibrary(db), Playlists: pls,
		Auth: func(context.Context, string, string) (int64, error) { return viewer, nil }})

	// Subsonic
	sub := func(method, extra string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/rest/"+method+"?u=a&p=b&f=json&v=1.16.1&c=t"+extra, nil))
		return rec.Body.String()
	}
	body := sub("getPlaylists", "")
	if !strings.Contains(body, `"name":"Road trip · Deezer"`) || !strings.Contains(body, `"songCount":2`) {
		t.Fatalf("getPlaylists : %s", body)
	}
	id := playlistSubsonicID(lists[0].ID)
	if body := sub("getPlaylist", "&id="+id); !strings.Contains(body, `"entry":[`) || !strings.Contains(body, `"title":"Ta fête"`) {
		t.Fatalf("getPlaylist : %s", body)
	}
	if body := sub("updatePlaylist", "&playlistId="+id+"&name=x"); !strings.Contains(body, `"status":"failed"`) {
		t.Fatalf("playlist importée modifiée : %s", body)
	}

	// Jellyfin
	j := frontend.PSEMusicJellyfin{ServerID: "srv", ViewID: "view"}
	if got := s.JellyfinPlaylists(ctx, viewer, j); len(got) != 3 || got[0].(map[string]any)["Type"] != "Playlist" {
		t.Fatalf("JellyfinPlaylists : %v", got)
	}
	guid := JellyfinGUID(guidPlaylist, lists[0].ID)
	rec := httptest.NewRecorder()
	if !s.ServeJellyfin(rec, httptest.NewRequest("GET", "/Playlists/"+guid+"/Items", nil), viewer, j) ||
		!strings.Contains(rec.Body.String(), JellyfinGUID(guidTrack, 1)) || !strings.Contains(rec.Body.String(), `"TotalRecordCount":2`) {
		t.Fatalf("/Playlists/…/Items : %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	if !s.ServeJellyfin(rec, httptest.NewRequest("GET", "/Users/u/Items?ParentId="+guid, nil), viewer, j) || !strings.Contains(rec.Body.String(), "Papaoutai") {
		t.Fatalf("/Items?ParentId : %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	if !s.ServeJellyfin(rec, httptest.NewRequest("DELETE", "/Playlists/"+guid, nil), viewer, j) || rec.Code != http.StatusForbidden {
		t.Fatalf("suppression : %d", rec.Code)
	}
	if s.ServeJellyfin(httptest.NewRecorder(), httptest.NewRequest("GET", "/Playlists/0000000000000001000000000000002a/Items", nil), viewer, j) {
		t.Fatal("playlist d'Ether interceptée")
	}

	// Plex
	env := frontend.PSEMusicPlex{Write: func(w http.ResponseWriter, _ *http.Request, v any) {
		b, err := model.Marshal(v, model.XML)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}}
	plex := func(method, target string, want ...string) {
		t.Helper()
		rec := httptest.NewRecorder()
		if !s.ServePlex(rec, httptest.NewRequest(method, target, nil), viewer, env) || rec.Code != 200 {
			t.Fatalf("%s %s : code %d", method, target, rec.Code)
		}
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Fatalf("%s sans %q : %s", target, w, rec.Body.String())
			}
		}
	}
	key := psemKey(psemPlaylist, lists[1].ID)
	plex("GET", "/playlists?playlistType=audio", `playlistType="audio"`, `title="Road trip · Deezer"`, `ratingKey="`+key+`"`)
	plex("GET", "/playlists/"+key+"/items", `<Track`, `title="Bâtard"`)
	plex("POST", "/playQueues?type=audio&playlistID="+key, `title="Bâtard"`)
	if s.ServePlex(httptest.NewRecorder(), httptest.NewRequest("GET", "/playlists", nil), viewer, env) {
		t.Fatal("liste sans type laissée à Ether")
	}
	if got := s.PlexPlaylists(ctx, viewer); len(got) != 3 {
		t.Fatalf("PlexPlaylists : %d", len(got))
	}

	// Délier retire les playlists du compte.
	if err := pls.SetAccount(ctx, viewer, ServiceDeezer, ""); err != nil {
		t.Fatal(err)
	}
	if lists, _ := pls.List(ctx, viewer); len(lists) != 1 || lists[0].Source != ServiceLastFM {
		t.Fatalf("après déliaison : %+v", lists)
	}
}

func TestPlaylistSyncReportsErrors(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv, _ := fakePlaylistServices(t)
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	pls := &Playlists{DB: db}
	sync := &PlaylistSync{Store: pls, Deezer: dz, LastFM: srv.URL + "/2.0/", LastFMKey: func() string { return "" }, HTTP: srv.Client(), Pace: 1}
	_ = pls.SetAccount(ctx, 1, ServiceLastFM, "jdoe")
	_ = pls.SetAccount(ctx, 1, ServiceDeezer, "user:404")
	if err := sync.SyncViewer(ctx, 1); err == nil {
		t.Fatal("erreurs attendues")
	}
	accounts, _ := pls.Accounts(ctx, 1)
	msgs := map[string]string{}
	for _, a := range accounts {
		msgs[a.Service] = a.Error
	}
	if !strings.Contains(msgs[ServiceLastFM], "clé API Last.fm manquante") || !strings.Contains(msgs[ServiceDeezer], "introuvable") {
		t.Fatalf("messages : %v", msgs)
	}
}

func TestBestTrackMatch(t *testing.T) {
	c := []Track{
		{ID: 1, Title: "Papaoutai", Artist: Artist{Name: "Autre"}},
		{ID: 2, Title: "Papaoutai - Live", Artist: Artist{Name: "Stromae"}},
		{ID: 3, Title: "Papaoutai", Artist: Artist{Name: "Stromae"}},
	}
	if m, ok := bestTrackMatch(c, "Stromae", "Papaoutai"); !ok || m.ID != 3 {
		t.Fatalf("exact : %+v", m)
	}
	if _, ok := bestTrackMatch(c[:2], "Stromae", "Papaoutai (Remastered 2020)"); ok {
		t.Fatal("version live prise pour l'original")
	}
	if m, ok := bestTrackMatch(c, "Stromae", "Papaoutai (Remastered 2020)"); !ok || m.ID != 3 {
		t.Fatalf("approché : %+v", m)
	}
	if _, ok := bestTrackMatch(c, "Daft Punk", "Papaoutai"); ok {
		t.Fatal("mauvais artiste accepté")
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExpandDeezerShortLinks(t *testing.T) {
	var hosts []string
	client := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		h := http.Header{}
		switch r.URL.Path {
		case "/s/play":
			h.Set("Location", "https://link.deezer.com/?dest=https%3A%2F%2Fwww.deezer.com%2Fplaylist%2F15938816963%3Fhost%3D1")
		case "/s/me":
			h.Set("Location", "https://www.deezer.com/fr/profile/42?utm=x")
		}
		return &http.Response{StatusCode: http.StatusMovedPermanently, Header: h, Body: http.NoBody, Request: r}, nil
	})}
	s := &PlaylistSync{HTTP: client}
	got := s.ExpandDeezerLinks(context.Background(), "https://link.deezer.com/s/play, https://link.deezer.com/s/me https://www.deezer.com/playlist/7")
	acc, err := ParseDeezerAccount(got)
	if err != nil || acc != "playlist:15938816963 user:42 playlist:7" {
		t.Fatalf("%q → %q, %v", got, acc, err)
	}
	for _, h := range hosts {
		if h != "link.deezer.com" {
			t.Fatalf("hôte contacté : %s", h)
		}
	}
	// Lien court sans redirection exploitable : erreur explicite.
	got = s.ExpandDeezerLinks(context.Background(), "https://link.deezer.com/s/dead")
	if _, err := ParseDeezerAccount(got); err == nil || !strings.Contains(err.Error(), "lien court") {
		t.Fatalf("lien mort : %v", err)
	}
}

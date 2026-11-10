package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlaylistImportParsers(t *testing.T) {
	for _, raw := range []string{"https://deezer.com/fr/playlist/123?x=y", "https://open.spotify.com/playlist/1234567890123456789012", "https://listenbrainz.org/playlist/12345678-1234-1234-1234-123456789abc"} {
		if _, _, err := parsePlaylistURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"http://127.0.0.1/playlist/1", "https://deezer.com.evil.test/playlist/123", "https://user:pass@deezer.com/playlist/123", "https://open.spotify.com/playlist/../../me", "file:///etc/passwd", "https://deezer.com:8080/playlist/123"} {
		if _, _, err := parsePlaylistURL(raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	p, err := parsePlaylistFile("Road trip", "csv", "\ufefftitle;artist;album\n\"One; More\";Daft Punk;Discovery\nMissing;;\n")
	if err != nil || len(p.Tracks) != 1 || p.Tracks[0].Title != "One; More" || p.Skipped != 1 || p.Total != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = parsePlaylistFile("Exportify", "csv", "Track Name,Artist Name(s)\nOne More Time,Daft Punk\n")
	if err != nil || len(p.Tracks) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = parsePlaylistFile("XSPF", "xspf", `<playlist xmlns="http://xspf.org/ns/0/"><trackList><track><title>Song &amp; music</title><creator>Artist</creator><location>http://127.0.0.1/private</location></track></trackList></playlist>`)
	if err != nil || len(p.Tracks) != 1 || p.Tracks[0].Title != "Song & music" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, raw := range []string{"name,url\na,b", "title,artist\n\"unterminated", "title,artist\nshort"} {
		if _, err := parsePlaylistFile("Bad", "csv", raw); err == nil {
			t.Fatal("invalid CSV accepted", raw)
		}
	}
	p, err = parsePlaylistFile("Large", "csv", "title,artist\n"+strings.Repeat("Song,Artist\n", 2001))
	if err != nil || !p.Truncated || len(p.Tracks) != 2000 {
		t.Fatal(len(p.Tracks), err)
	}
}

func TestPlaylistPreviewImportIsolationAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	srv, _ := fakePlaylistServices(t)
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	pls := &Playlists{DB: db}
	// A previous personal playlist must survive every import.
	if _, err := pls.CreatePersonal(ctx, 7, "Keep me", nil); err != nil {
		t.Fatal(err)
	}
	a := &PlaylistAPI{Sync: &PlaylistSync{Store: pls, Deezer: dz, Pace: 1}, Viewers: func(context.Context) ([]Viewer, error) { return []Viewer{{7, "Owner"}, {8, "Other"}}, nil }, Background: func(fn func(context.Context)) { fn(ctx) }}
	h := a.Handler()
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/preview", map[string]any{"viewer": 999, "name": "bad", "format": "csv", "file": "title,artist\nPapaoutai,Stromae"})
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	w = call("POST", "/preview", map[string]any{"viewer": 7, "name": "Selection", "format": "csv", "file": "title,artist\nPapaoutai,Stromae\nNo such track,Someone"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p playlistPreview
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p.Token == "" || len(p.Tracks) != 2 {
		t.Fatal(p)
	}
	if w = call("POST", "/import", map[string]any{"viewer": 8, "token": p.Token}); w.Code != 400 {
		t.Fatal("cross viewer import allowed")
	}
	w = call("POST", "/import", map[string]any{"viewer": 7, "token": p.Token})
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	w = call("GET", "/import?viewer=7&id="+p.Token, nil)
	var job playlistImportJob
	_ = json.Unmarshal(w.Body.Bytes(), &job)
	if !job.Done || job.Matched != 1 || len(job.Missing) != 1 || job.PlaylistID == 0 {
		t.Fatalf("%s", w.Body.String())
	}
	if w = call("GET", "/import?viewer=8&id="+p.Token, nil); w.Code != 400 {
		t.Fatal("cross viewer job allowed")
	}
	w = call("POST", "/import", map[string]any{"viewer": 7, "token": p.Token})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	lists, err := pls.List(ctx, 7)
	if err != nil || len(lists) != 2 {
		t.Fatal("duplicate or lost playlists", len(lists), err)
	}
	other, _ := pls.List(ctx, 8)
	if len(other) != 0 {
		t.Fatal("wrong viewer")
	}
}

func TestPlaylistImportDoesNotCacheProviderFailureAsMissing(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	s := &PlaylistSync{Store: &Playlists{DB: db}, Deezer: dz, Pace: 1}
	_, ok, err := s.findTrackResult(ctx, "Artist", "Song")
	if err == nil || ok {
		t.Fatal("outage treated as a match or absence", err)
	}
	_, _, known := s.Store.match(ctx, foldWords("Artist")+"\x00"+foldWords("Song"))
	if known {
		t.Fatal("outage cached as missing")
	}
}

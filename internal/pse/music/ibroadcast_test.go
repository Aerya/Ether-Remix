package music

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestIBroadcastDeviceFlowRefreshCatalogueAndDisconnect(t *testing.T) {
	db := store.OpenTest(t)
	polls, refreshes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/device/code":
			if r.URL.Query().Get("scope") != "user.account:read user.library:read" {
				t.Error("unexpected scopes")
			}
			reply(map[string]any{"device_code": "private-code", "user_code": "ABCD", "verification_uri": "ibroadcast.com/device", "verification_uri_complete": "https://oauth.ibroadcast.com/device?code=ABCD", "interval": 5, "expires_in": 600})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "device_code" {
				polls++
				if polls == 1 {
					w.WriteHeader(400)
					reply(map[string]string{"error": "authorization_pending"})
					return
				}
			} else if r.Form.Get("grant_type") == "refresh_token" {
				refreshes++
				if r.Form.Get("refresh_token") != "refresh-private" {
					t.Error("missing refresh token")
				}
			}
			reply(map[string]any{"access_token": "access-private", "refresh_token": "refresh-private", "expires_in": 3600})
		case "/status":
			if r.Header.Get("Authorization") != "Bearer access-private" {
				t.Error("auth header")
			}
			reply(map[string]any{"result": true, "user": map[string]any{"id": "42"}, "settings": map[string]string{"streaming_server": "https://streaming.ibroadcast.com"}})
		case "/library":
			reply(map[string]any{"result": true, "library": map[string]any{
				"playlists": map[string]any{"map": map[string]int{"name": 0, "tracks": 1}, "1": []any{"Ma sélection", []any{10, 11, 10}}},
				"tracks":    map[string]any{"map": map[string]int{"title": 0, "artist_id": 1, "album_id": 2, "length": 3, "file": 4, "trashed": 5, "artwork_id": 6, "track": 7}, "10": []any{"Song", 20, 30, 99, "/128/a/b/123", false, 40, 1}, "11": []any{"Deleted", 20, 30, 99, "/128/a/b/124", true, 40, 2}},
				"artists":   map[string]any{"map": map[string]any{"name": 0, "artists_additional_map": map[string]int{"artist_id": 0}}, "20": []any{"Artist"}}, "albums": map[string]any{"map": map[string]any{"name": 0, "artists_additional_map": map[string]int{"artist_id": 0}}, "30": []any{"Album"}}}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := &IBroadcast{OwnerID: 7, ImportPlaylists: true, DB: db, Sealer: fakeSealer{}, HTTP: server.Client(), OAuthURL: server.URL, APIURL: server.URL + "/status", LibraryURL: server.URL + "/library"}
	if err := s.Begin(t.Context(), "client"); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.Status(t.Context()))
	if strings.Contains(string(b), "private-code") {
		t.Fatal("device secret leaked")
	}
	if ok, err := s.Poll(t.Context()); ok || err != nil || polls != 0 {
		t.Fatal("poll interval ignored", ok, err, polls)
	}
	for i := 0; i < 2; i++ {
		s.pending.Next = time.Now().Add(-time.Second)
		ok, err := s.Poll(t.Context())
		if err != nil || ok != (i == 1) {
			t.Fatal(ok, err)
		}
	}
	c, err := s.load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c.ExpiresAt = 1
	if err = s.save(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if err = s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || s.Status(t.Context()).Tracks != 1 {
		t.Fatal(refreshes, s.Status(t.Context()))
	}
	var playlistRaw string
	if err := db.QueryRow("SELECT tracks FROM pse_music_playlists WHERE viewer_id=7 AND source='ibroadcast'").Scan(&playlistRaw); err != nil {
		t.Fatal(err)
	}
	var playlistTracks []Track
	if err := json.Unmarshal([]byte(playlistRaw), &playlistTracks); err != nil || len(playlistTracks) != 2 || playlistTracks[0].ID != playlistTracks[1].ID {
		t.Fatal(playlistTracks, err)
	}
	owned := &OwnedSource{DB: db, IB: s}
	tt, err := owned.SearchTracks(t.Context(), "Song", 10, 0)
	if err != nil || len(tt) != 1 || tt[0].Artist.Name != "Artist" {
		t.Fatal(tt, err)
	}
	st, err := owned.Stream(t.Context(), tt[0].ID, "LOSSLESS")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(st.URL)
	if u.Host != "streaming.ibroadcast.com" || u.Query().Get("file_id") != "10" || u.Query().Get("user_id") != "42" || u.Query().Get("Signature") != "access-private" {
		t.Fatal("invalid playback URL")
	}
	if !imageRefRe.MatchString(tt[0].Album.Cover) || owned.ImageURL('c', tt[0].Album.Cover, 300) != "https://artwork.ibroadcast.com/artwork/40-300" {
		t.Fatal("artwork mapping")
	}
	b, _ = json.Marshal(s.Status(t.Context()))
	if strings.Contains(string(b), "private") {
		t.Fatal("token leaked")
	}
	if err = s.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.Status(t.Context()).Connected || s.Status(t.Context()).Tracks != 0 {
		t.Fatal("still connected")
	}
	var playlistsLeft int
	if err := db.QueryRow("SELECT COUNT(*) FROM pse_music_playlists WHERE source='ibroadcast'").Scan(&playlistsLeft); err != nil || playlistsLeft != 0 {
		t.Fatal(playlistsLeft, err)
	}
	if _, err = owned.Stream(t.Context(), tt[0].ID, ""); err == nil {
		t.Fatal("disconnected stream accepted")
	}
}
func TestIBroadcastRejectsUnsafeHostsAndMalformedLibrary(t *testing.T) {
	for _, v := range []string{"http://streaming.ibroadcast.com", "https://evil.test/", "https://ibroadcast.com.evil.test/", "https://user@streaming.ibroadcast.com/", "https://streaming.ibroadcast.com:123/"} {
		if ibHost(v) {
			t.Fatal(v)
		}
	}
	for _, raw := range []string{`{}`, `{"map":null}`, `{"map":{},"123":{}}`} {
		if _, err := parseIBTable(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

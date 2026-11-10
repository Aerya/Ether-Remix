package music

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestSpotifyPKCEStateRefreshAndPreview(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	var challenge string
	var refreshed bool
	var offsetSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "authorization_code" {
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
					t.Error("wrong PKCE verifier")
				}
				reply(map[string]any{"access_token": "initial", "refresh_token": "refresh-private", "expires_in": 0})
			} else {
				refreshed = true
				if r.Form.Get("refresh_token") != "refresh-private" {
					t.Error("refresh lost")
				}
				reply(map[string]any{"access_token": "refreshed", "expires_in": 3600})
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer refreshed" {
			t.Error("invalid access token")
		}
		switch r.URL.Path {
		case "/playlists/1234567890123456789012":
			reply(map[string]any{"name": "Private selection"})
		case "/playlists/1234567890123456789012/items":
			if r.URL.Query().Get("offset") == "0" {
				reply(map[string]any{"total": 3, "next": "https://evil.test/do-not-follow", "items": []any{map[string]any{"item": map[string]any{"name": "Song", "type": "track", "artists": []any{map[string]string{"name": "Artist"}}}}, map[string]any{"item": nil}}})
			} else {
				offsetSeen = true
				reply(map[string]any{"total": 3, "items": []any{map[string]any{"is_local": true}}})
			}
		case "/me/playlists":
			reply(map[string]any{"items": []any{nil, map[string]string{"id": "1234567890123456789012", "name": "Private selection"}}})
		default:
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	s := &SpotifyPlaylists{Store: &Playlists{DB: db}, Sealer: fakeSealer{}, API: server.URL, TokenURL: server.URL + "/token", HTTP: server.Client()}
	client := strings.Repeat("a", 32)
	redirect := "https://remix.example/ether/accent/contenus?content=musique"
	auth, err := s.begin(7, client, redirect)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(auth)
	state := u.Query().Get("state")
	challenge = u.Query().Get("code_challenge")
	if err := s.finish(ctx, 8, state, "code"); err == nil {
		t.Fatal("cross viewer OAuth allowed")
	}
	if err := s.finish(ctx, 7, state, "code"); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(ctx, 7, state, "code"); err == nil {
		t.Fatal("replayed OAuth state")
	}
	var raw string
	_ = db.QueryRow(`SELECT credentials FROM pse_music_spotify WHERE viewer_id=7`).Scan(&raw)
	if !strings.HasPrefix(raw, "sealed:") {
		t.Fatal("unsealed credentials")
	}
	p, err := s.preview(ctx, 7, "1234567890123456789012")
	if err != nil || !refreshed || !offsetSeen || len(p.Tracks) != 1 || p.Skipped != 2 || p.Total != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	lists, _, err := s.lists(ctx, 7, 0)
	if err != nil || len(lists) != 1 {
		t.Fatal(lists, err)
	}
	if _, err := s.access(ctx, 8); err == nil {
		t.Fatal("token shared across viewers")
	}
	var out any
	if err := s.get(ctx, 7, "/forbidden", &out); err == nil || !strings.Contains(err.Error(), "collaboratives") {
		t.Fatal(err)
	}
	auth, _ = s.begin(7, client, redirect)
	u, _ = url.Parse(auth)
	state = u.Query().Get("state")
	pnd := s.pending[state]
	pnd.Until = time.Now().Add(-time.Second)
	s.pending[state] = pnd
	if err := s.finish(ctx, 7, state, "code"); err == nil {
		t.Fatal("expired state accepted")
	}
	for _, bad := range []string{"https://remix.example/evil", "http://remix.example/ether/accent/contenus?content=musique", "javascript:alert(1)"} {
		if _, err := s.begin(7, client, bad); err == nil {
			t.Fatal("bad redirect", bad)
		}
	}
}

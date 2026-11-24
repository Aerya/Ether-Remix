package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestRecordingIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title, artist string
		duration            int64
		isrc                string
		want                bool
	}{
		{"exact", "Song", "Artist", 200, "", true},
		{"remaster", "Song (Remastered 2020)", "Artist", 201, "", true},
		{"title prefix", "Song Two", "Artist", 200, "", false},
		{"artist prefix", "Song", "Artist Tribute Band", 200, "", false},
		{"artist suffix", "Song", "Other Artist", 200, "", false},
		{"missing artist", "Song", "", 200, "", false},
		{"different subtitle", "Song (Part II)", "Artist", 200, "", false},
		{"different remix", "Song (Club Remix)", "Artist", 200, "", false},
		{"isrc subtitle", "Song (Part II)", "Artist", 200, "TEST001", false},
		{"isrc preview", "Song", "Artist", 30, "TEST001", false},
		{"isrc wrong version", "Song (Live)", "Artist", 200, "TEST001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := Track{Title: "Song", Artist: Artist{Name: "Artist"}, Duration: 200, ISRC: "TEST001"}
			_, ok := matchScore(candidate{title: tc.title, artist: tc.artist, duration: tc.duration, isrc: tc.isrc}, target)
			if ok != tc.want {
				t.Fatalf("match=%v, want %v", ok, tc.want)
			}
			if tc.isrc == "" {
				_, ok = bestTrackMatch([]Track{{ID: 1, Title: tc.title, Artist: Artist{Name: tc.artist}}}, "Artist", "Song")
				if ok != tc.want {
					t.Fatalf("playlist match=%v, want %v", ok, tc.want)
				}
			}
		})
	}
	for _, pair := range [][2]string{{"Song (Part I)", "Song (Part II)"}, {"Song - Club Remix", "Song - Radio Remix"}, {"Song - First Movement", "Song - Second Movement"}} {
		_, ok := matchScore(candidate{title: pair[0], artist: "Artist"}, Track{Title: pair[1], Artist: Artist{Name: "Artist"}})
		if ok {
			t.Fatalf("different recordings matched: %v", pair)
		}
	}
}

func TestPlaylistSyncPreservesSnapshotOnPartialFailure(t *testing.T) {
	for _, service := range []string{ServiceDeezer, ServiceLastFM, ServiceListenBrainz} {
		t.Run(service, func(t *testing.T) {
			ctx := context.Background()
			pls := &Playlists{DB: store.OpenTest(t)}
			account := "tester"
			if service == ServiceDeezer {
				account = "user:42"
			}
			if err := pls.SetAccount(ctx, 7, service, account); err != nil {
				t.Fatal(err)
			}
			before := []Playlist{{Ref: "old", Name: "Keep me", Tracks: []Track{{ID: 55, Title: "Old"}}}}
			if err := pls.replace(ctx, 7, service, before); err != nil {
				t.Fatal(err)
			}
			if err := pls.replace(ctx, 8, service, before); err != nil {
				t.Fatal(err)
			}
			otherBefore, err := pls.List(ctx, 8)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := pls.List(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			const mbid = "11111111-1111-1111-1111-111111111111"
			var recovered atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if recovered.Load() {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0, "playlists": []any{}, "lovedtracks": map[string]any{"track": []any{}}, "toptracks": map[string]any{"track": []any{}}})
					return
				}
				var body any
				switch r.URL.Path {
				case "/user/42/playlists":
					body = map[string]any{"total": 2, "data": []any{map[string]any{"id": 1, "title": "New"}, map[string]any{"id": 2, "title": "Unavailable"}}}
				case "/playlist/1/tracks":
					body = map[string]any{"total": 1, "data": []any{dzJSON(1, "Song", "Artist", 1, true)}}
				case "/playlist/2/tracks", "/search/track":
					w.WriteHeader(http.StatusTooManyRequests)
					return
				case "/2.0/":
					if r.URL.Query().Get("method") == "user.getlovedtracks" {
						body = map[string]any{"lovedtracks": map[string]any{"@attr": map[string]any{"totalPages": "1"}, "track": []any{map[string]any{"name": "Song", "artist": map[string]any{"name": "Artist"}}}}}
					} else {
						body = map[string]any{"toptracks": map[string]any{"track": []any{}}}
					}
				case "/1/user/tester/playlists/createdfor":
					body = map[string]any{"playlists": []any{}}
				case "/1/user/tester/playlists":
					body = map[string]any{"playlists": []any{map[string]any{"playlist": map[string]any{"title": "New", "identifier": "https://listenbrainz.org/playlist/" + mbid}}}}
				case "/1/playlist/" + mbid:
					body = map[string]any{"playlist": map[string]any{"track": []any{map[string]any{"title": "Song", "creator": "Artist"}}}}
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer srv.Close()
			dz := NewDeezerSource(nil, srv.Client())
			dz.API = srv.URL
			sync := &PlaylistSync{Store: pls, Deezer: dz, HTTP: srv.Client(), LastFM: srv.URL + "/2.0/", LastFMKey: func() string { return "test" }, ListenBrainz: srv.URL, Pace: 1}
			if err := sync.SyncViewer(ctx, 7); err == nil {
				t.Error("partial failure not reported")
			}
			after, err := pls.List(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot, after) {
				t.Fatalf("snapshot changed: before=%+v after=%+v", snapshot, after)
			}
			accounts, err := pls.Accounts(ctx, 7)
			if err != nil || len(accounts) != 1 || accounts[0].Error == "" {
				t.Fatalf("missing account error: %+v %v", accounts, err)
			}
			recovered.Store(true)
			if err := sync.SyncViewer(ctx, 7); err != nil {
				t.Fatal(err)
			}
			after, err = pls.List(ctx, 7)
			if err != nil || len(after) != 0 {
				t.Fatalf("successful empty snapshot not applied: %+v %v", after, err)
			}
			accounts, err = pls.Accounts(ctx, 7)
			if err != nil || len(accounts) != 1 || accounts[0].Error != "" {
				t.Fatalf("error not cleared after recovery: %+v %v", accounts, err)
			}
			otherAfter, err := pls.List(ctx, 8)
			if err != nil || !reflect.DeepEqual(otherBefore, otherAfter) {
				t.Fatalf("other viewer changed: %+v %v", otherAfter, err)
			}

		})
	}
}

func TestPlaylistMatchRechecksLegacyCache(t *testing.T) {
	ctx := context.Background()
	pls := &Playlists{DB: store.OpenTest(t)}
	bad := Track{ID: 99, Title: "Song Two", Artist: Artist{Name: "Artist"}}
	pls.saveMatch(ctx, "artist\x00song", &bad)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search/track") {
			t.Error(r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{dzJSON(1, "Song", "Artist", 1, true)}})
	}))
	defer srv.Close()
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	sync := &PlaylistSync{Store: pls, Deezer: dz, Pace: 1}
	got, ok, err := sync.findTrackResult(ctx, "Artist", "Song")
	if err != nil || !ok || got.ID != 1 {
		t.Fatalf("legacy wrong match reused: %+v %v %v", got, ok, err)
	}
}

func TestPlaylistMatchPrefersExactTitle(t *testing.T) {
	tracks := []Track{{ID: 1, Title: "Song (Remastered 2020)", Artist: Artist{Name: "Artist"}}, {ID: 2, Title: "Song", Artist: Artist{Name: "Artist"}}}
	got, ok := bestTrackMatch(tracks, "Artist", "Song")
	if !ok || got.ID != 2 {
		t.Fatalf("exact match not preferred: %+v", got)
	}
}

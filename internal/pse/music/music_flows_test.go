package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/store"
)

func flowHarness(t *testing.T) (*PlaylistAPI, *time.Time) {
	t.Helper()
	db := store.OpenTest(t)
	now := time.Unix(1800000000, 0)
	a := &PlaylistAPI{Sync: &PlaylistSync{Store: &Playlists{DB: db, Now: func() time.Time { return now }}}, Viewers: func(context.Context) ([]Viewer, error) { return []Viewer{{ID: 7}, {ID: 8}}, nil }}
	return a, &now
}
func testFlow() MusicFlow {
	return MusicFlow{Viewer: 7, Ref: "daily", Name: "Mon mix", Mode: "mix", Hours: 24, Size: 8, Enabled: true}
}
func mustFlow(t *testing.T, a *PlaylistAPI, f MusicFlow) {
	t.Helper()
	if err := a.saveFlow(t.Context(), f); err != nil {
		t.Fatal(err)
	}
}
func readFlow(t *testing.T, a *PlaylistAPI) MusicFlow {
	t.Helper()
	fs, err := a.flows(t.Context(), 7)
	if err != nil || len(fs) != 1 {
		t.Fatal(fs, err)
	}
	return fs[0]
}
func flowPlaylist(t *testing.T, a *PlaylistAPI) Playlist {
	t.Helper()
	p, ok, err := a.Sync.Store.Get(t.Context(), 7, PlaylistID(7, SourceFlow, "daily"))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	return p
}

func TestMusicFlowRotationCadenceAndIsolation(t *testing.T) {
	a, now := flowHarness(t)
	lib := NewLibrary(a.Sync.Store.DB)
	for id := int64(1); id <= 30; id++ {
		tr := Track{ID: id, Title: fmt.Sprint(id)}
		if err := lib.StarTrack(t.Context(), 7, tr, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.StarTrack(t.Context(), 8, Track{ID: 99, Title: "Autre compte"}, true); err != nil {
		t.Fatal(err)
	}
	f := testFlow()
	mustFlow(t, a, f)
	a.SyncFlows(t.Context())
	first := flowPlaylist(t, a)
	if len(first.Tracks) != 8 {
		t.Fatal(first)
	}
	for _, tr := range first.Tracks {
		if tr.ID == 99 {
			t.Fatal("cross viewer leak")
		}
	}
	*now = now.Add(time.Hour)
	a.SyncFlows(t.Context())
	if !reflect.DeepEqual(first, flowPlaylist(t, a)) {
		t.Fatal("premature refresh")
	}
	// A fresh API instance reads the persisted cadence and preserves the snapshot.
	b := &PlaylistAPI{Sync: &PlaylistSync{Store: a.Sync.Store}, Viewers: a.Viewers}
	b.SyncFlows(t.Context())
	if !reflect.DeepEqual(first, flowPlaylist(t, b)) {
		t.Fatal("restart ignored cadence")
	}
	*now = now.Add(24 * time.Hour)
	b.SyncFlows(t.Context())
	second := flowPlaylist(t, b)
	if first.ID != second.ID || reflect.DeepEqual(first.Tracks, second.Tracks) {
		t.Fatal("rotation or stable identity failed")
	}
	f = readFlow(t, a)
	f.Enabled = false
	mustFlow(t, a, f)
	*now = now.Add(48 * time.Hour)
	a.SyncFlows(t.Context())
	if !reflect.DeepEqual(second, flowPlaylist(t, a)) {
		t.Fatal("paused flow refreshed")
	}
	if err := a.deleteFlow(t.Context(), 8, f.Ref); err != nil {
		t.Fatal(err)
	}
	_ = flowPlaylist(t, a)
	if err := a.deleteFlow(t.Context(), 7, f.Ref); err != nil {
		t.Fatal(err)
	}
	lists, _ := a.Sync.Store.List(t.Context(), 7)
	if len(lists) != 0 {
		t.Fatal(lists)
	}
}

func localFlowTracks(t *testing.T, a *PlaylistAPI) []Track {
	t.Helper()
	base := int64(localmedia.AudioBase)
	tracks := []Track{{ID: base + 1, Title: "Seed", Artist: Artist{Name: "Artist"}}, {ID: base + 2, Title: "Neighbor", Artist: Artist{Name: "Artist"}}}
	if err := mediaaccess.Create(t.Context(), a.Sync.Store.DB, "local:flow", 7); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceOwned(t.Context(), a.Sync.Store.DB, "local:flow", tracks); err != nil {
		t.Fatal(err)
	}
	return tracks
}
func TestAudioMuseContractPrivacyAndFailure(t *testing.T) {
	a, now := flowHarness(t)
	tracks := localFlowTracks(t, a)
	var calls atomic.Int32
	var failing atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/similar_tracks" || r.URL.Query().Get("item_id") != trackID(tracks[0].ID) || r.URL.Query().Get("server") != "Remix" || r.URL.Query().Get("n") != "9" || r.Header.Get("Authorization") != "Bearer test-only-token" {
			t.Errorf("wrong API contract: %s", r.URL.Path)
		}
		if failing.Load() {
			http.Error(w, "upstream private error", 503)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"item_id": trackID(tracks[0].ID), "title": "Seed", "author": "Artist"}, {"item_id": "navidrome-id", "title": "Foreign", "author": "Artist"}, {"item_id": trackID(tracks[1].ID), "title": "Neighbor (Live)", "author": "Artist"}, {"item_id": trackID(tracks[1].ID), "title": "Neighbor", "author": "Artist"}})
	}))
	defer remote.Close()
	a.AudioMuse = func() AudioMuseConfig { return AudioMuseConfig{remote.URL, "test-only-token", "Remix"} }
	f := testFlow()
	f.Mode = "audiomuse"
	f.Seed = tracks[0].ID
	mustFlow(t, a, f)
	if err := a.runFlow(t.Context(), 7, f.Ref, true); err != nil {
		t.Fatal(err)
	}
	pl := flowPlaylist(t, a)
	if len(pl.Tracks) != 1 || pl.Tracks[0].ID != tracks[1].ID {
		t.Fatal(pl)
	}
	state := readFlow(t, a)
	if state.Missing != 2 {
		t.Fatal(state)
	}
	// A different viewer cannot use the seed or learn metadata through suggestions.
	f.Viewer = 8
	if err := a.saveFlow(t.Context(), f); err == nil {
		t.Fatal("private seed accepted")
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/flows/seeds?viewer=8", nil))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatal(rec.Body.String())
	}
	failing.Store(true)
	*now = now.Add(25 * time.Hour)
	a.SyncFlows(t.Context())
	if !reflect.DeepEqual(pl, flowPlaylist(t, a)) {
		t.Fatal("failure replaced snapshot")
	}
	state = readFlow(t, a)
	if state.Error == "" || strings.Contains(state.Error, "private") {
		t.Fatal(state)
	}
	count := calls.Load()
	a.SyncFlows(t.Context())
	if calls.Load() != count {
		t.Fatal("failed run retried before cadence")
	}
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/flows?viewer=7", nil))
	if strings.Contains(rec.Body.String(), "test-only-token") || strings.Contains(rec.Body.String(), remote.URL) {
		t.Fatal("configuration leaked")
	}
	// No redirects, including those that could expose a bearer token.
	var redirected atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer redirect.Close()
	a.AudioMuse = func() AudioMuseConfig { return AudioMuseConfig{redirect.URL, "test-only-token", "Remix"} }
	if err := a.runFlow(t.Context(), 7, "daily", true); err == nil || redirected.Load() {
		t.Fatal("redirect followed")
	}
}

func TestMusicFlowConcurrentEditAndDeletion(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(fmt.Sprint(deleting), func(t *testing.T) {
			a, _ := flowHarness(t)
			tracks := localFlowTracks(t, a)
			started := make(chan struct{})
			release := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				_ = json.NewEncoder(w).Encode([]map[string]string{{"item_id": trackID(tracks[1].ID), "title": "Neighbor", "author": "Artist"}})
			}))
			defer remote.Close()
			a.AudioMuse = func() AudioMuseConfig { return AudioMuseConfig{remote.URL, "", "Remix"} }
			f := testFlow()
			f.Mode = "audiomuse"
			f.Seed = tracks[0].ID
			mustFlow(t, a, f)
			done := make(chan error, 1)
			go func() { done <- a.runFlow(t.Context(), 7, "daily", true) }()
			<-started
			if deleting {
				if err := a.deleteFlow(t.Context(), 7, "daily"); err != nil {
					t.Fatal(err)
				}
			} else {
				f = readFlow(t, a)
				f.Name = "New name"
				mustFlow(t, a, f)
			}
			close(release)
			if err := <-done; err == nil {
				t.Fatal("obsolete generation committed")
			}
			lists, _ := a.Sync.Store.List(t.Context(), 7)
			if len(lists) != 0 {
				t.Fatal("obsolete snapshot created", lists)
			}
		})
	}
}

func TestMusicFlowEmptyPreservesSnapshotAndValidation(t *testing.T) {
	a, _ := flowHarness(t)
	f := testFlow()
	mustFlow(t, a, f)
	if err := a.Sync.Store.replace(t.Context(), 7, SourceFlow, []Playlist{{Ref: f.Ref, Name: f.Name, Tracks: []Track{{ID: 1, Title: "Old"}}}}); err != nil {
		t.Fatal(err)
	}
	old := flowPlaylist(t, a)
	if err := a.runFlow(t.Context(), 7, "daily", true); err == nil {
		t.Fatal("empty pool accepted")
	}
	if !reflect.DeepEqual(old, flowPlaylist(t, a)) {
		t.Fatal("empty replaced snapshot")
	}
	f = readFlow(t, a)
	f.Hours = 0
	if err := a.saveFlow(t.Context(), f); err == nil {
		t.Fatal("zero cadence")
	}
	f.Hours = 24
	f.Mode = "playlist"
	f.URL = "https://untrusted.example/playlist/1"
	if err := a.saveFlow(t.Context(), f); err == nil {
		t.Fatal("arbitrary URL accepted")
	}
	f.Mode = "mix"
	f.URL = ""
	f.Revision++
	if err := a.saveFlow(t.Context(), f); err == nil {
		t.Fatal("stale revision accepted")
	}
}

func TestMusicFlowLinkedPlaylistReconciliation(t *testing.T) {
	a, _ := flowHarness(t)
	var version atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version.Load() == 2 {
			http.Error(w, "failed", 503)
			return
		}
		switch r.URL.Path {
		case "/playlist/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "title": "Source", "nb_tracks": 2})
		case "/playlist/1/tracks":
			id := int64(1)
			if version.Load() == 1 {
				id = 3
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 2, "data": []any{dzJSON(id, "Song", "Artist", 1, true), dzJSON(2, "Unavailable", "Artist", 1, false)}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	a.Sync.Deezer = NewDeezerSource(nil, remote.Client())
	a.Sync.Deezer.API = remote.URL
	a.Sync.Pace = 1
	f := testFlow()
	f.Mode = "playlist"
	f.URL = "https://www.deezer.com/playlist/1"
	mustFlow(t, a, f)
	if err := a.runFlow(t.Context(), 7, "daily", true); err != nil {
		t.Fatal(err)
	}
	first := flowPlaylist(t, a)
	if len(first.Tracks) != 1 || first.Tracks[0].ID != 1 || readFlow(t, a).Missing != 1 {
		t.Fatal(first)
	}
	version.Store(1)
	if err := a.runFlow(t.Context(), 7, "daily", true); err != nil {
		t.Fatal(err)
	}
	second := flowPlaylist(t, a)
	if len(second.Tracks) != 1 || second.Tracks[0].ID != 3 || first.ID != second.ID {
		t.Fatal(second)
	}
	version.Store(2)
	if err := a.runFlow(t.Context(), 7, "daily", true); err == nil {
		t.Fatal("upstream failure accepted")
	}
	if !reflect.DeepEqual(second, flowPlaylist(t, a)) {
		t.Fatal("failed import replaced snapshot")
	}
}

func TestAudioMuseRevokedAccess(t *testing.T) {
	a, _ := flowHarness(t)
	tracks := localFlowTracks(t, a)
	if err := mediaaccess.Update(t.Context(), a.Sync.Store.DB, "local:flow", 7, mediaaccess.Policy{Viewers: []int64{8}}); err != nil {
		t.Fatal(err)
	}
	f := testFlow()
	f.Viewer = 8
	f.Mode = "audiomuse"
	f.Seed = tracks[0].ID
	mustFlow(t, a, f)
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]string{{"item_id": trackID(tracks[1].ID), "title": "Neighbor", "author": "Artist"}})
	}))
	defer remote.Close()
	a.AudioMuse = func() AudioMuseConfig { return AudioMuseConfig{remote.URL, "", "Remix"} }
	if err := a.runFlow(t.Context(), 8, "daily", true); err != nil {
		t.Fatal(err)
	}
	if err := mediaaccess.Update(t.Context(), a.Sync.Store.DB, "local:flow", 7, mediaaccess.Policy{}); err != nil {
		t.Fatal(err)
	}
	if err := a.runFlow(t.Context(), 8, "daily", true); err == nil {
		t.Fatal("revoked seed accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("request sent for inaccessible seed")
	}
	p, ok, err := a.Sync.Store.Get(t.Context(), 8, PlaylistID(8, SourceFlow, "daily"))
	if err != nil || !ok || len(p.Tracks) != 0 {
		t.Fatal("revoked track exposed", p, err)
	}
}

func TestAudioMuseRejectsMalformedAndOversizedResponses(t *testing.T) {
	for _, body := range []string{`{"error":"not an array"}`, strings.Repeat("x", (1<<20)+1)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			a, _ := flowHarness(t)
			tracks := localFlowTracks(t, a)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer remote.Close()
			a.AudioMuse = func() AudioMuseConfig { return AudioMuseConfig{remote.URL, "", "Remix"} }
			f := testFlow()
			f.Mode = "audiomuse"
			f.Seed = tracks[0].ID
			if _, _, err := a.audioMuseTracks(t.Context(), f); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestMusicFlowRejectsLooseCachedMatches(t *testing.T) {
	a, _ := flowHarness(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"playlist": map[string]any{"title": "Source", "track": []map[string]string{{"title": "Song", "creator": "Artist"}, {"title": "Other", "creator": "Artist"}}}})
	}))
	defer remote.Close()
	a.Sync.ListenBrainz = remote.URL
	a.Sync.HTTP = remote.Client()
	wrong := Track{ID: 1, Title: "Song (Live)", Artist: Artist{Name: "Artist"}}
	right := Track{ID: 2, Title: "Other", Artist: Artist{Name: "Artist"}}
	a.Sync.Store.saveMatch(t.Context(), foldWords("Artist")+"\x00"+foldWords("Song"), &wrong)
	a.Sync.Store.saveMatch(t.Context(), foldWords("Artist")+"\x00"+foldWords("Other"), &right)
	f := testFlow()
	f.Mode = "playlist"
	f.URL = "https://listenbrainz.org/playlist/11111111-1111-1111-1111-111111111111"
	tracks, missing, err := a.flowTracks(t.Context(), f)
	if err != nil || len(tracks) != 1 || tracks[0].ID != 2 || missing != 1 {
		t.Fatal(tracks, missing, err)
	}
}

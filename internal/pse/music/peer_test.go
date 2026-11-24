package music

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/peerclient"
	"github.com/braedonsaunders/ether/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPeerMusicIdentity(t *testing.T) {
	track := Track{Title: "A Song", Artist: Artist{Name: "Example Artist"}, Album: Album{Title: "An Album"}}
	for _, tc := range []struct {
		file string
		want bool
	}{
		{`Example Artist\An Album\01 - A Song.flac`, true},
		{`Example Artist\An Album\01 - A Song Live.flac`, false},
		{`Other Artist\An Album\01 - A Song.flac`, false},
		{`Example Artist\Other Album\01 - A Song.flac`, false},
		{`Example Artist\An Album\01 - Another Song.flac`, false},
		{`Example Artist\An Album\01 - A Song.zip`, false},
		{`A Song.flac`, false},
	} {
		h := peerclient.Hit{Name: tc.file, Filename: tc.file}
		if got := peerMatches(h, track); got != tc.want {
			t.Errorf("%s: %v", tc.file, got)
		}
	}
}

type peerQueueResolver struct {
	called *int
	queued bool
}

func (r peerQueueResolver) Resolve(context.Context, Track) (Stream, error) {
	*r.called++
	if r.queued {
		return Stream{}, core.ErrDownloadQueued
	}
	return Stream{}, ErrNotFound
}
func TestPeerQueuedMusicStopsFallback(t *testing.T) {
	first, next := 0, 0
	rs := Resolvers{peerQueueResolver{&first, true}, peerQueueResolver{&next, false}}
	_, err := rs.Resolve(t.Context(), Track{})
	if !errors.Is(err, core.ErrDownloadQueued) || first != 1 || next != 0 {
		t.Fatalf("%v, calls %d/%d", err, first, next)
	}
}

func TestPeerMusicNumericTitle(t *testing.T) {
	track := Track{Title: "1979", Artist: Artist{Name: "Artist"}, Album: Album{Title: "Album"}}
	for _, name := range []string{"1979.flac", "01 - 1979.flac"} {
		if !peerMatches(peerclient.Hit{Name: name, Filename: "Artist/Album/" + name}, track) {
			t.Fatal(name)
		}
	}
}

func TestPeerMusicResumesWithoutSearching(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	root := t.TempDir()
	relative := filepath.Join("remix", id, "01 - A Song.flac")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, relative)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, relative), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	state := "Queued"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v0/transfers/downloads/batches/01234567-89ab-cdef-0123-456789abcdef" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"username": "peer", "options": map[string]string{"destination": "remix/" + id}, "transfers": []map[string]any{{"filename": "Artist/Album/01 - A Song.flac", "size": 5, "state": state}}})
	}))
	defer srv.Close()
	db := store.OpenTest(t)
	cfg := peerclient.Settings{Kind: "slskd", URL: srv.URL, Secret: "test", Root: root}
	svc := &peerclient.Service{DB: db, Client: &peerclient.Client{Settings: cfg}}
	item := peerclient.Item{Hit: peerclient.Hit{ID: id, Name: "01 - A Song.flac", Filename: "Artist/Album/01 - A Song.flac", User: "peer", Size: 5}, Started: time.Now().Unix(), State: "queued"}
	raw, _ := json.Marshal(item)
	if _, err := db.Exec("INSERT INTO pse_peer_items(source,id,data,started,seen) VALUES(?,?,?,?,?)", cfg.Identity(), id, string(raw), item.Started, item.Started); err != nil {
		t.Fatal(err)
	}
	resolver := PeerResolver{Service: func() *peerclient.Service { return svc }}
	track := Track{Title: "A Song", Artist: Artist{Name: "Artist"}, Album: Album{Title: "Album"}}
	if _, err := resolver.Resolve(t.Context(), track); !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatal(err)
	}
	state = "Completed, Succeeded"
	stream, err := resolver.Resolve(t.Context(), track)
	if err != nil {
		t.Fatal(err)
	}
	file, _, err := stream.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "audio" {
		t.Fatalf("%s %v", data, err)
	}
}

package music

import (
	"context"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/localmedia"
)

func TestLocalMusicLibraryStateAndRange(t *testing.T) {
	h := newHarness(t, "")
	db := h.srv.o.Library.db
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "track.flac"), []byte("fLaC0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	l := &localmedia.Library{DB: db, Probe: func(context.Context, *os.File, string) (localmedia.File, error) {
		return localmedia.File{Name: "Track", Artist: "Artist", Album: "Album", Duration: 80, MIME: "audio/flac"}, nil
	}, Publish: func(ctx context.Context, r localmedia.Root, ff []localmedia.File) error {
		return ImportLocal(ctx, db, r, ff)
	}}
	if err := l.Add(withMusicViewer(t.Context(), 7), localmedia.Root{Name: "Audio", Path: dir, Kind: "audio"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(withMusicViewer(t.Context(), 7)); err != nil {
		t.Fatal(err)
	}
	roots, _ := l.Roots(t.Context())
	if err := mediaaccess.Update(t.Context(), db, "local:"+roots[0].ID, 7, mediaaccess.Policy{Viewers: []int64{8}}); err != nil {
		t.Fatal(err)
	}
	s := &OwnedSource{DB: db, Local: l, Remote: h.srv.o.Source}
	h.srv.o.Source = s
	tt, err := s.SearchTracks(withMusicViewer(t.Context(), 7), "Track", 1, 0)
	if err != nil || len(tt) != 1 {
		t.Fatal(tt, err)
	}
	track := tt[0]
	for _, viewer := range []int64{7, 8} {
		items, err := h.srv.o.Library.List(withMusicViewer(t.Context(), 7), viewer, KindTrack, ListOptions{})
		if err != nil || len(items) != 1 || items[0].PlayCount != 0 || items[0].StarredAt != 0 {
			t.Fatal(items, err)
		}
	}
	if err := h.srv.o.Library.StarTrack(withMusicViewer(t.Context(), 7), 7, track, true); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(withMusicViewer(t.Context(), 7)); err != nil {
		t.Fatal(err)
	} // preserves per-viewer stars, no duplicate
	for _, viewer := range []int64{7, 8} {
		n, err := h.srv.o.Library.Count(withMusicViewer(t.Context(), 7), viewer, KindTrack)
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
		items, err := h.srv.o.Library.List(withMusicViewer(t.Context(), 7), viewer, KindTrack, ListOptions{Starred: true})
		if err != nil || len(items) != map[int64]int{7: 1, 8: 0}[viewer] {
			t.Fatal(items, err)
		}
	}
	p := url.Values{"u": {"alice"}, "p": {"secret"}, "v": {APIVersion}, "c": {"fixture"}, "id": {"t-" + strconv.FormatInt(track.ID, 10)}}
	// Subsonic's public ID spelling is taken from the same response helpers.
	p.Set("id", trackID(track.ID))
	w := h.raw("stream", p, http.Header{"Range": {"bytes=4-7"}})
	if w.Code != 206 || w.Body.String() != "0123" || w.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), w.Header())
	}
	p.Set("p", "wrong")
	w = h.raw("stream", p, nil)
	if w.Body.String() == "fLaC0123456789" {
		t.Fatal("unauthenticated file exposure")
	}
	roots, _ = l.Roots(withMusicViewer(t.Context(), 7))
	if err := l.Remove(withMusicViewer(t.Context(), 7), roots[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stream(withMusicViewer(t.Context(), 7), track.ID, ""); err == nil {
		t.Fatal("removed source playable")
	}
}

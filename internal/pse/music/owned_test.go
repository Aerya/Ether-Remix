package music

import (
	"context"
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
	if err := l.Add(t.Context(), localmedia.Root{Name: "Audio", Path: dir, Kind: "audio"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := &OwnedSource{DB: db, Local: l, Remote: h.srv.o.Source}
	h.srv.o.Source = s
	tt, err := s.SearchTracks(t.Context(), "Track", 1, 0)
	if err != nil || len(tt) != 1 {
		t.Fatal(tt, err)
	}
	track := tt[0]
	for _, viewer := range []int64{7, 8} {
		items, err := h.srv.o.Library.List(t.Context(), viewer, KindTrack, ListOptions{})
		if err != nil || len(items) != 1 || items[0].PlayCount != 0 || items[0].StarredAt != 0 {
			t.Fatal(items, err)
		}
	}
	if err := h.srv.o.Library.StarTrack(t.Context(), 7, track, true); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	} // preserves per-viewer stars, no duplicate
	for _, viewer := range []int64{7, 8} {
		n, err := h.srv.o.Library.Count(t.Context(), viewer, KindTrack)
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
		items, err := h.srv.o.Library.List(t.Context(), viewer, KindTrack, ListOptions{Starred: true})
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
	roots, _ := l.Roots(t.Context())
	if err := l.Remove(t.Context(), roots[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stream(t.Context(), track.ID, ""); err == nil {
		t.Fatal("removed source playable")
	}
}

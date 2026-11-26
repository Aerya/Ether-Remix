package vela

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// Recommendations rows reach Vela with the page row's presentation, never
// the retired style; surface rows are explicit stubs, and track lists carry
// only playable leaves.
func TestRowPresentationWire(t *testing.T) {
	source := frontendtest.Experience{UserDataFunc: func(context.Context, *frontend.Viewer, []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
		return nil, nil
	}}
	inst := &instance{host: frontend.Host{Experience: source}}
	r := httptest.NewRequest("GET", "/vela/v0/rows", nil)
	viewer := &frontend.Viewer{ID: 1}

	entries := make([]frontend.RowEntry, 12)
	for n := range entries {
		entries[n] = frontend.RowEntry{Kind: frontend.RowEntryTitle, Title: &frontend.Title{Key: frontend.MovieKey(int64(n + 1)), Type: frontend.TypeMovie, Title: "Movie"}, Handle: "h"}
	}
	row := frontend.Row{ID: "r1", Title: "Trending Movies", Entries: entries, More: true, Style: "hero"}
	row.Layout.Present = core.RowPresentation{Layout: "wide", Card: "landscape", Personal: true, Library: 4, Experience: "watch", Numbered: true}
	wire, err := inst.rowWire(r, viewer, "token", row, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := wire["style"]; has {
		t.Fatal("style is retired on the Vela wire", wire)
	}
	want := map[string]any{"layout": "wide", "card": "landscape", "role": "movie", "personal": true, "library": "4", "experience": "watch", "presentation": "ranked", "continuation": "exhausted"}
	for k, v := range want {
		if wire[k] != v {
			t.Fatalf("%s = %v, want %v (%v)", k, wire[k], v, wire)
		}
	}
	if n := len(wire["items"].([]object)); n != 10 {
		t.Fatalf("numbered row kept %d items, want 10", n)
	}

	album := core.TitleParent{Key: frontend.MovieKey(900), Title: "Album", Role: frontend.MediaRoleAlbum}
	track := func(id int64, parents ...core.TitleParent) frontend.RowEntry {
		return frontend.RowEntry{Kind: frontend.RowEntryTitle, Title: &frontend.Title{Key: frontend.MovieKey(id), Title: "Song", MediaClass: frontend.MediaClassMusic, MediaRole: frontend.MediaRoleTrack, Runtime: 3 * time.Minute, MediaParents: parents}}
	}
	albumEntry := frontend.RowEntry{Kind: frontend.RowEntryTitle, Title: &frontend.Title{Key: frontend.MovieKey(50), Title: "Album", MediaClass: frontend.MediaClassMusic, MediaRole: frontend.MediaRoleAlbum}}
	tracks := frontend.Row{ID: "top-songs", Title: "Top songs", Entries: []frontend.RowEntry{track(41, album), track(42), albumEntry}}
	tracks.Layout.Present = core.RowPresentation{Layout: "tracks", Experience: "listen"}
	wire, err = inst.rowWire(r, viewer, "token", tracks, 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if items := wire["items"].([]object); len(items) != 1 || wire["role"] != "track" {
		t.Fatalf("tracks row = %v, want only the track with an album parent", wire)
	}

	surface := frontend.Row{ID: "surface:sports:tonight", Title: "Sports Tonight", Kind: "surface"}
	surface.Layout.Present = core.RowPresentation{Library: 7, Surface: &core.RowSurface{Plugin: "sports", Surface: "sports.tonight"}}
	got := surfaceRowWire(surface)
	if got["kind"] != "surface" || got["id"] != "surface:sports:sports.tonight@7" || got["plugin"] != "sports" || got["surface"] != "sports.tonight" || got["library"] != "7" {
		t.Fatalf("surface row = %v", got)
	}
	// Without a document renderer (or plugin screens) the surface row is dropped
	// and the shelves keep their page order.
	out, err := inst.rowsToWire(r, viewer, "token", []frontend.Row{row, surface, tracks}, nil, nil, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0]["id"] != "r1" || out[1]["id"] != "top-songs" {
		t.Fatalf("rows = %v", out)
	}
}

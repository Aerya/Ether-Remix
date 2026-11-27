package livetv

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A scheduled VOD refresh that finds the same catalogue must not announce
// VODChanged: the resolver forgets its cached searches on that signal, and
// every hourly refresh announcing it made Ether re-search indexers until
// their daily API budget ran out. A real change (an item added, edited or
// removed) is still announced.
func TestVODRefreshAnnouncesOnlyRealChanges(t *testing.T) {
	var revision atomic.Int64
	var extra atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "get_vod_categories":
			fmt.Fprint(w, `[{"category_id":"1","category_name":"Films"}]`)
		case "get_vod_streams":
			items := fmt.Sprintf(`{"stream_id":1,"name":"Film.2020.1080p.WEB-DL.H264","tmdb_id":"123","container_extension":"mkv","added":"%d"}`, revision.Load())
			if extra.Load() {
				items += `,{"stream_id":2,"name":"A Different Film (2021)","container_extension":"mp4"}`
			}
			fmt.Fprintf(w, `[%s]`, items)
		case "get_series":
			fmt.Fprint(w, `[]`)
		default:
			fmt.Fprint(w, `{"user_info":{"auth":1,"status":"Active","max_connections":"1"}}`)
		}
	}))
	defer up.Close()
	var announced atomic.Int64
	s := testService(t, Options{HTTPClient: up.Client(), VODChanged: func() { announced.Add(1) }})
	off := false
	if _, err := s.ImportSource(t.Context(), Source{ID: "vod", Name: "Provider", VOD: true, LiveTV: &off,
		Auth: &SourceAuth{Type: "xtream", Server: up.URL, Username: "u", Password: "p"}}); err != nil {
		t.Fatal(err)
	}
	refresh := func() int64 {
		t.Helper()
		before := announced.Load()
		if err := s.RefreshVOD(t.Context(), "vod"); err != nil {
			t.Fatal(err)
		}
		return announced.Load() - before
	}

	refresh() // first fill may announce; the catalogue is now stored
	if n := refresh(); n != 0 {
		t.Fatalf("an unchanged catalogue announced VODChanged %d times", n)
	}
	if n := refresh(); n != 0 {
		t.Fatalf("a second unchanged refresh announced VODChanged %d times", n)
	}

	revision.Store(7) // the provider edited the item
	if n := refresh(); n != 1 {
		t.Fatalf("an edited item announced %d times, want 1", n)
	}
	extra.Store(true) // an item was added
	if n := refresh(); n != 1 {
		t.Fatalf("an added item announced %d times, want 1", n)
	}
	extra.Store(false) // and removed again
	if n := refresh(); n != 1 {
		t.Fatalf("a removed item announced %d times, want 1", n)
	}
}

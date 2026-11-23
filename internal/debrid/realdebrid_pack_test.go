package debrid

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A season pack cached on Real-Debrid as the whole pack must play any of
// its episodes: selecting the wanted episode alone is a file set Real-Debrid
// does not hold and would start a download (#262).
func TestRealDebridSelectsWholePackForAnEpisode(t *testing.T) {
	const pack = `[{"id":1,"path":"/Sleepers.S01/Sleepers.S01E01.mkv","bytes":2000000000,"selected":%[1]d},` +
		`{"id":2,"path":"/Sleepers.S01/Sleepers.S01E02.mkv","bytes":2000000000,"selected":%[1]d},` +
		`{"id":3,"path":"/Sleepers.S01/Sleepers.S01E03.mkv","bytes":2000000000,"selected":%[1]d},` +
		`{"id":4,"path":"/Sleepers.S01/Sample/sample.mkv","bytes":30000000,"selected":0},` +
		`{"id":5,"path":"/Sleepers.S01/Sleepers.S01E02.nl.srt","bytes":40000,"selected":0}]`
	var mu sync.Mutex
	selected := ""
	mux := http.NewServeMux()
	mux.HandleFunc("POST /torrents/addMagnet", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"T1"}`)
	})
	mux.HandleFunc("GET /torrents/info/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch selected {
		case "":
			fmt.Fprintf(w, `{"id":"T1","status":"waiting_files_selection","files":`+pack+`,"links":[]}`, 0)
		case "2,1,3": // the cached variant: every episode
			fmt.Fprintf(w, `{"id":"T1","status":"downloaded","files":`+pack+`,"links":["https://rd.example/d/E1","https://rd.example/d/E2","https://rd.example/d/E3"]}`, 1)
		default:
			fmt.Fprintf(w, `{"id":"T1","status":"queued","files":`+pack+`,"links":[]}`, 0)
		}
	})
	mux.HandleFunc("POST /torrents/selectFiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		selected = r.PostForm.Get("files")
		mu.Unlock()
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newRealDebrid(testKey, fastOpts(srv.URL))
	target := &core.ReleaseTarget{Season: 1, Episode: 2}
	req := acquireReq(true)
	req.Choose = func(fs []File) (File, error) { return ChooseFile(fs, target) }
	acq, err := p.Acquire(t.Context(), req)
	if err != nil {
		t.Fatalf("cached pack not served: %v (selected %q)", err, selected)
	}
	if acq.File.ID != "2" || acq.File.Link != "https://rd.example/d/E2" {
		t.Fatalf("acquired %+v, want episode 2 and its link", acq.File)
	}
}

// An album cached on Real-Debrid as the whole set of audio tracks must select
// every sibling audio file alongside the chosen track: selecting one track
// misses the cached variant just like a single episode of a season pack (#753).
func TestRealDebridSelectsWholeAlbumForAudioTrack(t *testing.T) {
	const album = `[{"id":1,"path":"/Artist - Album/cover.jpg","bytes":500000,"selected":0},` +
		`{"id":2,"path":"/Artist - Album/01 - Intro.flac","bytes":20000000,"selected":%[1]d},` +
		`{"id":3,"path":"/Artist - Album/02 - Main Track.flac","bytes":35000000,"selected":%[1]d},` +
		`{"id":4,"path":"/Artist - Album/03 - Trailer Trash.mp3","bytes":12000000,"selected":%[1]d},` +
		`{"id":5,"path":"/Artist - Album/Album.cue","bytes":1200,"selected":0},` +
		`{"id":6,"path":"/Artist - Album/Bonus/video.mkv","bytes":100000000,"selected":0},` +
		`{"id":7,"path":"/Artist - Album/empty.flac","bytes":0,"selected":0}]`
	var mu sync.Mutex
	selected := ""
	mux := http.NewServeMux()
	mux.HandleFunc("POST /torrents/addMagnet", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		fmt.Fprint(w, `{"id":"T2"}`)
	})
	mux.HandleFunc("GET /torrents/info/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch selected {
		case "":
			fmt.Fprintf(w, `{"id":"T2","status":"waiting_files_selection","files":`+album+`,"links":[]}`, 0)
		case "3,2,4": // the cached variant: every audio track
			fmt.Fprintf(w, `{"id":"T2","status":"downloaded","files":`+album+`,"links":["https://rd.example/d/T1","https://rd.example/d/T2","https://rd.example/d/T3"]}`, 1)
		default:
			fmt.Fprintf(w, `{"id":"T2","status":"queued","files":`+album+`,"links":[]}`, 0)
		}
	})
	mux.HandleFunc("POST /torrents/selectFiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		selected = r.PostForm.Get("files")
		mu.Unlock()
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := newRealDebrid(testKey, fastOpts(srv.URL))
	target := &core.ReleaseTarget{AllowNonVideo: true, Member: "02 - Main Track.flac", MIME: "audio/flac"}
	req := acquireReq(true)
	req.Choose = func(fs []File) (File, error) { return ChooseFile(fs, target) }
	acq, err := p.Acquire(t.Context(), req)
	if err != nil {
		t.Fatalf("cached album not served: %v (selected %q)", err, selected)
	}
	if acq.File.ID != "3" || acq.File.Link != "https://rd.example/d/T2" {
		t.Fatalf("acquired %+v, want track 2 and its link", acq.File)
	}

	// Ebook and comic packs follow the same sibling-class rule without pulling
	// in sidecar images, metadata, or other media classes.
	books := []File{
		{ID: "1", Path: "Series/cover.jpg", Size: 200_000},
		{ID: "2", Path: "Series/01.epub", Size: 2_000_000},
		{ID: "3", Path: "Series/02.mobi", Size: 2_500_000},
		{ID: "4", Path: "Series/notes.nfo", Size: 500},
	}
	if got := rdSelection(books, books[2]); got != "3,2" {
		t.Fatalf("rdSelection(books) = %q, want 3,2", got)
	}
	comics := []File{
		{ID: "1", Path: "Run/Issue 01.cbz", Size: 40_000_000},
		{ID: "2", Path: "Run/Issue 02.cbr", Size: 42_000_000},
		{ID: "3", Path: "Run/cover.png", Size: 500_000},
	}
	if got := rdSelection(comics, comics[0]); got != "1,2" {
		t.Fatalf("rdSelection(comics) = %q, want 1,2", got)
	}
}

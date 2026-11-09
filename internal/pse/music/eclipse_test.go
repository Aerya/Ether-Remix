package music

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

const dzMD5 = "2e018122cb56986277102d2041a592c8"

func fakeDeezer(t *testing.T) *httptest.Server {
	t.Helper()
	artist := map[string]any{"id": 27, "name": "Daft Punk",
		"picture_xl": "https://cdn-images.dzcdn.net/images/artist/f2bc007e9133c946ac3c3907ddc5d2ea/1000x1000-000000-80-0-0.jpg"}
	album := map[string]any{"id": 302127, "title": "Discovery", "md5_image": dzMD5, "release_date": "2001-03-07", "nb_tracks": 2, "artist": artist}
	track := map[string]any{"id": 3135553, "title": "One More Time", "duration": 320, "track_position": 1, "disk_number": 1,
		"isrc": "GBDUW0000053", "readable": true, "artist": artist, "album": album}
	mux := http.NewServeMux()
	send := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/search/track", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"data": []any{track, map[string]any{"id": 9, "title": "Bloquée", "readable": false}}})
	})
	mux.HandleFunc("/search/album", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{album}}) })
	mux.HandleFunc("/search/artist", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{artist}}) })
	mux.HandleFunc("/album/302127", func(w http.ResponseWriter, r *http.Request) { send(w, album) })
	mux.HandleFunc("/album/302127/tracks", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"data": []any{track, map[string]any{"id": 3135554, "title": "Aerodynamic", "duration": 212, "track_position": 2, "disk_number": 1, "artist": artist}}})
	})
	mux.HandleFunc("/artist/27", func(w http.ResponseWriter, r *http.Request) { send(w, artist) })
	mux.HandleFunc("/artist/27/albums", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"data": []any{album, album}})
	})
	mux.HandleFunc("/track/3135553", func(w http.ResponseWriter, r *http.Request) { send(w, track) })
	mux.HandleFunc("/chart/0/albums", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{album}}) })
	mux.HandleFunc("/chart/0/artists", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{artist}}) })
	mux.HandleFunc("/chart/0/tracks", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{track}}) })
	mux.HandleFunc("/artist/27/top", func(w http.ResponseWriter, r *http.Request) { send(w, map[string]any{"data": []any{track}}) })
	mux.HandleFunc("/images/cover/", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "jpeg-pochette") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"error": map[string]any{"type": "DataException", "message": "no data", "code": 800}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeEclipse imite un addon Eclipse ; le jeton est dans le chemin, comme
// sur les vrais addons.
func fakeEclipse(t *testing.T, audio string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var streams atomic.Int32
	mux := http.NewServeMux()
	var self *httptest.Server
	mux.HandleFunc("/u/tok/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"x","name":"x","types":["track"],"resources":["search","stream","settings"]}`)
	})
	mux.HandleFunc("/u/tok/search", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("q"), "One More Time") {
			_, _ = io.WriteString(w, `{"tracks":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"tracks":[
			{"id":"remix-1","title":"One More Time (Remix)","artist":"Someone","album":"X","duration":410},
			{"id":"t/42","title":"One More Time","artist":"Daft Punk","album":"Discovery","duration":320}]}`)
	})
	mux.HandleFunc("/u/tok/stream/", func(w http.ResponseWriter, r *http.Request) {
		streams.Add(1)
		if r.URL.EscapedPath() != "/u/tok/stream/t%2F42" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"url": self.URL + "/audio/42.flac", "quality": "LOSSLESS"})
	})
	mux.HandleFunc("/audio/42.flac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "42.flac", time.Time{}, strings.NewReader(audio))
	})
	self = httptest.NewServer(mux)
	t.Cleanup(self.Close)
	return self, &streams
}

func TestParseEclipseURLs(t *testing.T) {
	got := ParseEclipseURLs(" https://a/u/1/manifest.json,https://b/x/manifest.json\nftp://c/x https://a/u/1/manifest.json ")
	if len(got) != 2 || got[0] != "https://a/u/1/manifest.json" || got[1] != "https://b/x/manifest.json" {
		t.Fatalf("got %v", got)
	}
	b, err := eclipseBase("https://h/addon/manifest.json?token=abc")
	if err != nil || b != "https://h/addon?token=abc" {
		t.Fatalf("base = %q, %v", b, err)
	}
	u, _ := endpoint(b, []string{"stream", "x/y"}, url.Values{"q": {"a b"}})
	if u != "https://h/addon/stream/x%2Fy?q=a+b&token=abc" {
		t.Fatalf("endpoint = %q", u)
	}
}

func TestBestHit(t *testing.T) {
	tr := Track{Title: "Ça plane pour moi", Duration: 180, Artist: Artist{Name: "Plastic Bertrand"}}
	hits := []eclipseHit{
		{id: "1", title: "Ca plane pour moi (Live)", artist: "Plastic Bertrand", duration: 240},
		{id: "2", title: "Ça plane pour moi", artist: "Plastic Bertrand", duration: 181000},
	}
	if h, _ := bestHit(hits, tr); h.id != "2" {
		t.Fatalf("choisi %q", h.id)
	}
	if _, ok := bestHit([]eclipseHit{{id: "z", title: "autre"}}, tr); ok {
		t.Fatal("un résultat sans rapport ne doit pas être pris")
	}
	// Seule une version live, un remix ou une reprise : refusés.
	for _, title := range []string{"Ça plane pour moi (Live)", "Ça plane pour moi - Remix", "Ça plane pour moi (Acoustic Version)", "Ça plane pour moi (Karaoke)"} {
		if _, ok := bestHit([]eclipseHit{{id: "v", title: title, artist: "Plastic Bertrand", duration: 180}}, tr); ok {
			t.Fatalf("%q accepté", title)
		}
	}
	// Remasterisé : même enregistrement.
	if _, ok := bestHit([]eclipseHit{{id: "r", title: "Ça plane pour moi (Remastered 2010)", artist: "Plastic Bertrand", duration: 181}}, tr); !ok {
		t.Fatal("version remasterisée refusée")
	}
	// Durée trop éloignée, autre artiste : refusés.
	if _, ok := bestHit([]eclipseHit{{id: "d", title: "Ça plane pour moi", artist: "Plastic Bertrand", duration: 400}}, tr); ok {
		t.Fatal("durée trop éloignée acceptée")
	}
	if _, ok := bestHit([]eclipseHit{{id: "a", title: "Ça plane pour moi", artist: "Sonic Youth", duration: 180}}, tr); ok {
		t.Fatal("autre artiste accepté")
	}
	// L'ISRC tranche, dans les deux sens.
	tr.ISRC = "BEA017700010"
	if h, ok := bestHit([]eclipseHit{{id: "i", title: "Ca Plane Pour Moi", artist: "X", isrc: "bea017700010"}}, tr); !ok || h.id != "i" {
		t.Fatal("ISRC identique refusé")
	}
	if _, ok := bestHit([]eclipseHit{{id: "j", title: "Ça plane pour moi", artist: "Plastic Bertrand", isrc: "FRX000000001"}}, tr); ok {
		t.Fatal("ISRC différent accepté")
	}
}

func TestEclipseCheck(t *testing.T) {
	addon, _ := fakeEclipse(t, "x")
	if err := NewEclipse([]string{addon.URL + "/u/tok/manifest.json"}, addon.Client()).Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := NewEclipse([]string{addon.URL + "/nope/manifest.json"}, addon.Client()).Check(context.Background()); err == nil {
		t.Fatal("un manifeste absent doit échouer")
	}
	if err := NewEclipse(nil, nil).Check(context.Background()); err == nil {
		t.Fatal("aucun lien doit échouer")
	}
}

func TestDeezerEclipseSubsonic(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dz := fakeDeezer(t)
	const audio = "fLaC-données-audio-de-test"
	addon, streams := fakeEclipse(t, audio)
	// Un premier addon en panne : le second doit prendre le relais.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	t.Cleanup(dead.Close)
	ecl := NewEclipse([]string{dead.URL + "/manifest.json", addon.URL + "/u/tok/manifest.json"}, addon.Client())
	src := NewDeezerSource(ecl, dz.Client())
	src.API = dz.URL
	src.Images = "https://img.example"
	s := NewServer(Options{Source: src, Library: NewLibrary(db), Media: addon.Client(),
		Auth: func(_ context.Context, u, p string) (int64, error) {
			if u == "alice" && p == "secret" {
				return 7, nil
			}
			return 0, errors.New("unauthorized")
		}})
	get := func(method string, kv ...string) *httptest.ResponseRecorder {
		q := url.Values{"u": {"alice"}, "p": {"secret"}, "f": {"json"}}
		for i := 0; i+1 < len(kv); i += 2 {
			q.Set(kv[i], kv[i+1])
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rest/"+method+"?"+q.Encode(), nil))
		return rec
	}
	body := get("search3", "query", "one more time").Body.String()
	for _, want := range []string{`"title":"One More Time"`, `"name":"Daft Punk"`, `"coverArt":"c-` + dzMD5 + `"`, "Discovery"} {
		if !strings.Contains(body, want) {
			t.Fatalf("search3 sans %s : %s", want, body)
		}
	}
	if strings.Contains(body, "Bloquée") {
		t.Fatal("un titre non lisible sur Deezer ne doit pas apparaître")
	}
	album := get("getAlbum", "id", "al-302127").Body.String()
	if !strings.Contains(album, "Aerodynamic") || !strings.Contains(album, `"track":2`) {
		t.Fatalf("getAlbum : %s", album)
	}
	artist := get("getArtist", "id", "ar-27").Body.String()
	if strings.Count(artist, `"id":"al-302127"`) != 1 {
		t.Fatalf("getArtist doit dédoublonner : %s", artist)
	}

	rec := get("stream", "id", "tr-3135553")
	if rec.Code != 200 || rec.Body.String() != audio {
		t.Fatalf("stream = %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/flac" {
		t.Fatalf("Content-Type = %q", ct)
	}
	rec = get("stream", "id", "tr-3135553")
	if rec.Code != 200 || streams.Load() != 1 {
		t.Fatalf("la résolution doit être mise en cache (%d appels)", streams.Load())
	}
	if rec := get("stream", "id", "tr-1"); !strings.Contains(rec.Body.String(), `"code":70`) {
		t.Fatalf("titre inconnu : %s", rec.Body.String())
	}
	if u := src.ImageURL('c', strings.ToUpper(dzMD5), 300); u != "https://img.example/cover/"+dzMD5+"/500x500-000000-80-0-0.jpg" {
		t.Fatalf("ImageURL = %s", u)
	}
	if u := src.ImageURL('p', "f2bc007e9133c946ac3c3907ddc5d2ea", 0); !strings.Contains(u, "/artist/f2bc007e9133c946ac3c3907ddc5d2ea/1000x1000") {
		t.Fatalf("ImageURL artiste = %s", u)
	}
}

package music

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

const coverUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const pictureUUID = "11111111-2222-3333-4444-555555555555"

var audio = bytes.Repeat([]byte("fLaC-audio-"), 400)

// fakeHiFi reproduit les réponses d'une HiFi API telles que Monochrome les lit.
func fakeHiFi(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	artist := map[string]any{"id": 1, "name": "Daft Punk", "picture": pictureUUID}
	album := map[string]any{"id": 10, "title": "Discovery", "cover": coverUUID, "numberOfTracks": 3,
		"releaseDate": "2001-03-12", "artist": artist, "type": "ALBUM"}
	other := map[string]any{"id": 11, "title": "Pas Daft Punk", "numberOfTracks": 1, "artists": []any{map[string]any{"id": 2, "name": "Autre"}}}
	track := func(id int, title string, n int) map[string]any {
		return map[string]any{"id": id, "title": title, "duration": 200 + n, "trackNumber": n, "volumeNumber": 1,
			"artists": []any{artist}, "album": map[string]any{"id": 10, "title": "Discovery", "cover": coverUUID},
			"isrc": fmt.Sprintf("GBDUW01000%02d", n), "explicit": n == 2}
	}
	t1, t2, t3 := track(100, "One More Time", 1), track(101, "Aerodynamic", 2), track(102, "Digital Love", 3)
	var srv *httptest.Server
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/search/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		q := r.URL.Query()
		switch {
		case q.Get("s") != "":
			reply(w, map[string]any{"version": "2.0", "data": map[string]any{"items": []any{t1, t2}, "totalNumberOfItems": 2}})
		case q.Get("a") != "":
			reply(w, map[string]any{"data": map[string]any{"artists": map[string]any{"items": []any{artist}}}})
		case q.Get("al") != "":
			reply(w, map[string]any{"data": map[string]any{"albums": map[string]any{"items": []any{album}}}})
		default:
			http.Error(w, "bad", 400)
		}
	})
	mux.HandleFunc("/album/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		if r.URL.Query().Get("id") != "10" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("offset") == "2" {
			reply(w, map[string]any{"data": map[string]any{"items": []any{map[string]any{"item": t3, "type": "track"}}}})
			return
		}
		page := map[string]any{}
		for k, v := range album {
			page[k] = v
		}
		page["items"] = []any{map[string]any{"item": t1, "type": "track"}, map[string]any{"item": t2, "type": "track"}}
		reply(w, map[string]any{"version": "2.0", "data": page})
	})
	mux.HandleFunc("/artist/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		q := r.URL.Query()
		if q.Get("f") == "1" {
			reply(w, map[string]any{"albums": map[string]any{"items": []any{album, other}}})
			return
		}
		if q.Get("id") != "1" {
			http.NotFound(w, r)
			return
		}
		reply(w, map[string]any{"data": map[string]any{"artist": artist}})
	})
	mux.HandleFunc("/info/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		for _, tr := range []map[string]any{t1, t2, t3} {
			if fmt.Sprint(tr["id"]) == r.URL.Query().Get("id") {
				reply(w, map[string]any{"data": tr})
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/track/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		if r.URL.Query().Get("id") != "100" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("quality") {
		case "HI_RES_LOSSLESS":
			mpd := base64.StdEncoding.EncodeToString([]byte(`<?xml version="1.0"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011"></MPD>`))
			reply(w, map[string]any{"data": map[string]any{"manifestMimeType": "application/dash+xml", "manifest": mpd}})
		case "LOSSLESS":
			man, _ := json.Marshal(map[string]any{"mimeType": "audio/flac", "codecs": "flac", "urls": []any{srv.URL + "/media/100.flac"}})
			reply(w, map[string]any{"version": "2.0", "data": map[string]any{"trackId": 100, "audioQuality": "LOSSLESS",
				"manifestMimeType": "application/vnd.tidal.bts", "manifest": base64.StdEncoding.EncodeToString(man)}})
		default:
			http.Error(w, "nope", 500)
		}
	})
	mux.HandleFunc("/media/100.flac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "100.flac", time.Unix(0, 0), bytes.NewReader(audio))
	})
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEG" + r.URL.Path))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

type harness struct {
	t     *testing.T
	srv   *Server
	hifi  *httptest.Server
	calls *[]string
}

func newHarness(t *testing.T, quality string) *harness {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hifi, calls := fakeHiFi(t)
	auth := func(_ context.Context, u, p string) (int64, error) {
		switch {
		case u == "alice" && p == "secret":
			return 7, nil
		case u == "bob" && p == "s3cret":
			return 8, nil
		}
		return 0, errors.New("unauthorized")
	}
	s := NewServer(Options{Source: NewHiFiSource(NewClient(hifi.URL, hifi.Client()), hifi.URL+"/images"), Library: NewLibrary(db), Auth: auth,
		Quality: quality, Media: hifi.Client()})
	return &harness{t: t, srv: s, hifi: hifi, calls: calls}
}

func (h *harness) raw(method string, params url.Values, hdr http.Header) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/rest/"+method+"?"+params.Encode(), nil)
	for k, v := range hdr {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

func (h *harness) call(user, method string, kv ...string) map[string]any {
	h.t.Helper()
	p := url.Values{"u": {user}, "v": {APIVersion}, "c": {"test"}, "f": {"json"}}
	if user == "alice" {
		p.Set("p", "secret")
	} else {
		p.Set("p", "enc:"+hex.EncodeToString([]byte("s3cret")))
	}
	for i := 0; i+1 < len(kv); i += 2 {
		p.Add(kv[i], kv[i+1])
	}
	rec := h.raw(method, p, nil)
	var out map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		h.t.Fatalf("%s: JSON invalide %q: %v", method, rec.Body.String(), err)
	}
	return out["subsonic-response"]
}

func okResp(t *testing.T, r map[string]any) {
	t.Helper()
	if r["status"] != "ok" {
		t.Fatalf("réponse en échec: %v", r)
	}
}

func list(v any) []map[string]any {
	switch t := v.(type) {
	case []any:
		out := []map[string]any{}
		for _, e := range t {
			out = append(out, e.(map[string]any))
		}
		return out
	case map[string]any:
		return []map[string]any{t}
	}
	return nil
}

func TestSubsonicAuth(t *testing.T) {
	h := newHarness(t, "LOSSLESS")
	okResp(t, h.call("alice", "ping"))
	okResp(t, h.call("bob", "ping.view"))

	r := h.raw("ping", url.Values{"u": {"alice"}, "p": {"faux"}, "f": {"json"}}, nil)
	if !strings.Contains(r.Body.String(), `"code":40`) {
		t.Fatalf("mauvais mot de passe: %s", r.Body.String())
	}
	r = h.raw("ping", url.Values{"u": {"alice"}, "t": {"abc"}, "s": {"x"}, "f": {"json"}}, nil)
	if !strings.Contains(r.Body.String(), `"code":41`) {
		t.Fatalf("jeton: %s", r.Body.String())
	}
	r = h.raw("ping", url.Values{"f": {"json"}}, nil)
	if !strings.Contains(r.Body.String(), `"code":10`) {
		t.Fatalf("sans identifiants: %s", r.Body.String())
	}
	for i := 0; i < 10; i++ {
		h.raw("ping", url.Values{"u": {"mallory"}, "p": {fmt.Sprint(i)}}, nil)
	}
	r = h.raw("ping", url.Values{"u": {"mallory"}, "p": {"x"}}, nil)
	if !strings.Contains(r.Body.String(), "Trop d") {
		t.Fatalf("blocage attendu: %s", r.Body.String())
	}

	// Réponse XML par défaut, conforme au schéma Subsonic.
	r = h.raw("ping", url.Values{"u": {"alice"}, "p": {"secret"}}, nil)
	var x struct {
		XMLName xml.Name `xml:"http://subsonic.org/restapi subsonic-response"`
		Status  string   `xml:"status,attr"`
		Version string   `xml:"version,attr"`
	}
	if err := xml.Unmarshal(r.Body.Bytes(), &x); err != nil || x.Status != "ok" || x.Version != APIVersion {
		t.Fatalf("XML: %v %+v %s", err, x, r.Body.String())
	}
}

func TestSubsonicBrowseAndSearch(t *testing.T) {
	h := newHarness(t, "LOSSLESS")

	r := h.call("alice", "search3", "query", "daft punk")
	okResp(t, r)
	res := r["searchResult3"].(map[string]any)
	if a := list(res["artist"]); len(a) != 1 || a[0]["id"] != "ar-1" || a[0]["coverArt"] != "p-"+pictureUUID {
		t.Fatalf("artistes: %v", res["artist"])
	}
	if a := list(res["album"]); len(a) != 1 || a[0]["id"] != "al-10" || a[0]["name"] != "Discovery" || a[0]["year"] != float64(2001) {
		t.Fatalf("albums: %v", res["album"])
	}
	songs := list(res["song"])
	if len(songs) != 2 || songs[0]["id"] != "tr-100" || songs[0]["albumId"] != "al-10" || songs[0]["artistId"] != "ar-1" ||
		songs[0]["contentType"] != "audio/flac" || songs[0]["coverArt"] != "c-"+coverUUID {
		t.Fatalf("titres: %v", songs)
	}
	if songs[1]["explicitStatus"] != "explicit" {
		t.Fatalf("explicite: %v", songs[1])
	}

	r = h.call("alice", "getAlbum", "id", "al-10")
	okResp(t, r)
	al := r["album"].(map[string]any)
	tracks := list(al["song"])
	if len(tracks) != 3 || tracks[2]["title"] != "Digital Love" || al["songCount"] != float64(3) || al["artist"] != "Daft Punk" {
		t.Fatalf("album paginé: %v", al)
	}

	r = h.call("alice", "getArtist", "id", "ar-1")
	okResp(t, r)
	ar := r["artist"].(map[string]any)
	if albums := list(ar["album"]); len(albums) != 1 || albums[0]["id"] != "al-10" || ar["albumCount"] != float64(1) {
		t.Fatalf("artiste: %v", ar)
	}

	r = h.call("alice", "getSong", "id", "tr-101")
	okResp(t, r)
	if s := r["song"].(map[string]any); s["title"] != "Aerodynamic" || s["track"] != float64(2) {
		t.Fatalf("titre: %v", s)
	}

	r = h.call("alice", "getAlbum", "id", "al-99")
	if r["status"] != "failed" || r["error"].(map[string]any)["code"] != float64(70) {
		t.Fatalf("album absent: %v", r)
	}
	r = h.call("alice", "getAlbum", "id", "tr-100")
	if r["status"] != "failed" {
		t.Fatalf("mauvais type d'identifiant: %v", r)
	}

	r = h.call("alice", "getMusicDirectory", "id", "al-10")
	okResp(t, r)
	if c := list(r["directory"].(map[string]any)["child"]); len(c) != 3 || c[0]["isDir"] != false {
		t.Fatalf("dossier album: %v", r)
	}
	r = h.call("alice", "getMusicDirectory", "id", "ar-1")
	okResp(t, r)
	if c := list(r["directory"].(map[string]any)["child"]); len(c) != 1 || c[0]["isDir"] != true || c[0]["title"] != "Discovery" {
		t.Fatalf("dossier artiste: %v", r)
	}
}

func TestSubsonicLibrary(t *testing.T) {
	h := newHarness(t, "LOSSLESS")

	// Bibliothèque vide au départ.
	r := h.call("alice", "getArtists")
	okResp(t, r)
	if idx := r["artists"].(map[string]any)["index"]; idx != nil {
		t.Fatalf("bibliothèque non vide: %v", idx)
	}

	okResp(t, h.call("alice", "star", "id", "tr-100"))
	okResp(t, h.call("alice", "star", "albumId", "al-10"))
	okResp(t, h.call("alice", "scrobble", "id", "tr-102", "submission", "true"))
	okResp(t, h.call("alice", "scrobble", "id", "tr-101", "submission", "false"))

	r = h.call("alice", "getStarred2")
	okResp(t, r)
	st := r["starred2"].(map[string]any)
	if s := list(st["song"]); len(s) != 1 || s[0]["id"] != "tr-100" || s[0]["starred"] == nil {
		t.Fatalf("titres favoris: %v", st)
	}
	if a := list(st["album"]); len(a) != 1 || a[0]["id"] != "al-10" || a[0]["songCount"] != float64(3) {
		t.Fatalf("albums favoris (instantané complet attendu): %v", st)
	}
	if st["artist"] != nil {
		t.Fatalf("aucun artiste mis en favori: %v", st["artist"])
	}

	r = h.call("alice", "getArtists")
	okResp(t, r)
	idx := list(r["artists"].(map[string]any)["index"])
	if len(idx) != 1 || idx[0]["name"] != "D" {
		t.Fatalf("index: %v", idx)
	}
	if a := list(idx[0]["artist"]); len(a) != 1 || a[0]["name"] != "Daft Punk" || a[0]["albumCount"] != float64(1) {
		t.Fatalf("artistes de la bibliothèque: %v", idx)
	}

	r = h.call("alice", "getAlbumList2", "type", "recent")
	okResp(t, r)
	if a := list(r["albumList2"].(map[string]any)["album"]); len(a) != 1 || a[0]["playCount"] != float64(1) {
		t.Fatalf("écoutés récemment: %v", r)
	}
	r = h.call("alice", "getAlbumList2", "type", "starred")
	if a := list(r["albumList2"].(map[string]any)["album"]); len(a) != 1 {
		t.Fatalf("albums favoris: %v", r)
	}

	// Recherche vide : synchronisation complète de la bibliothèque.
	r = h.call("alice", "search3", "query", "", "songCount", "50")
	okResp(t, r)
	if s := list(r["searchResult3"].(map[string]any)["song"]); len(s) != 2 {
		t.Fatalf("synchronisation: %v", r)
	}

	// La bibliothèque est propre à chaque utilisateur.
	r = h.call("bob", "getStarred2")
	okResp(t, r)
	if len(r["starred2"].(map[string]any)) != 0 {
		t.Fatalf("bibliothèque de bob: %v", r)
	}

	okResp(t, h.call("alice", "unstar", "id", "tr-100"))
	r = h.call("alice", "getStarred2")
	if s := r["starred2"].(map[string]any)["song"]; s != nil {
		t.Fatalf("favori retiré: %v", s)
	}
	r = h.call("alice", "getRandomSongs", "size", "5")
	okResp(t, r)
	if s := list(r["randomSongs"].(map[string]any)["song"]); len(s) != 2 {
		t.Fatalf("titres au hasard: %v", r)
	}
}

func TestSubsonicStreamAndCover(t *testing.T) {
	h := newHarness(t, "HI_RES_LOSSLESS")
	p := url.Values{"u": {"alice"}, "p": {"secret"}, "id": {"tr-100"}}

	rec := h.raw("stream", p, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), audio) {
		t.Fatalf("flux: %d %d octets", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/flac" {
		t.Fatalf("type de contenu: %q", ct)
	}
	var sawDASH, sawLossless bool
	for _, c := range *h.calls {
		sawDASH = sawDASH || strings.Contains(c, "quality=HI_RES_LOSSLESS")
		sawLossless = sawLossless || strings.Contains(c, "quality=LOSSLESS")
	}
	if !sawDASH || !sawLossless {
		t.Fatalf("repli DASH → LOSSLESS attendu: %v", *h.calls)
	}

	rec = h.raw("stream", p, http.Header{"Range": {"bytes=10-19"}})
	if rec.Code != 206 || !bytes.Equal(rec.Body.Bytes(), audio[10:20]) || rec.Header().Get("Content-Range") == "" {
		t.Fatalf("plage: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}

	rec = h.raw("getCoverArt", url.Values{"u": {"alice"}, "p": {"secret"}, "id": {"c-" + coverUUID}, "size": {"300"}}, nil)
	if rec.Code != 200 || rec.Body.String() != "JPEG/images/aaaaaaaa/bbbb/cccc/dddd/eeeeeeeeeeee/320x320.jpg" {
		t.Fatalf("pochette: %d %q", rec.Code, rec.Body.String())
	}
	rec = h.raw("getCoverArt", url.Values{"u": {"alice"}, "p": {"secret"}, "id": {"c-../../etc"}}, nil)
	if rec.Code != 404 {
		t.Fatalf("pochette invalide: %d", rec.Code)
	}
	rec = h.raw("stream", url.Values{"u": {"alice"}, "p": {"secret"}, "id": {"tr-999"}, "f": {"json"}}, nil)
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"status":"failed"`) {
		t.Fatalf("flux absent: %s", body)
	}
}

func TestParseManifestAndSettings(t *testing.T) {
	man, _ := json.Marshal(map[string]any{"mimeType": "audio/mp4", "urls": []any{"https://cdn/x.mp4", "https://cdn/lossless/x.flac"}})
	s, err := streamFrom(map[string]any{"data": map[string]any{"manifest": base64.StdEncoding.EncodeToString(man)}})
	if err != nil || s.URL != "https://cdn/lossless/x.flac" || s.Mime != "audio/mp4" {
		t.Fatalf("manifeste: %+v %v", s, err)
	}
	s, err = streamFrom([]any{map[string]any{"id": 1, "duration": 3}, map[string]any{"OriginalTrackUrl": "https://cdn/o.flac"}})
	if err != nil || s.URL != "https://cdn/o.flac" {
		t.Fatalf("OriginalTrackUrl: %+v %v", s, err)
	}

	t.Setenv("PSE_MUSIC_HIFI_URL", "")
	if LoadSettings().Enabled() {
		t.Fatal("désactivé par défaut")
	}
	t.Setenv("PSE_MUSIC_HIFI_URL", "file:///etc/passwd")
	if LoadSettings().Enabled() {
		t.Fatal("seuls http et https sont acceptés")
	}
	t.Setenv("PSE_MUSIC_HIFI_URL", "https://hifi.example/")
	t.Setenv("PSE_MUSIC_QUALITY", "hi_res_lossless")
	if c := LoadSettings(); !c.Enabled() || c.Quality != "HI_RES_LOSSLESS" {
		t.Fatalf("réglages: %+v", c)
	}
	t.Setenv("PSE_MUSIC_QUALITY", "n'importe quoi")
	if LoadSettings().Quality != "LOSSLESS" {
		t.Fatal("qualité par défaut")
	}
}

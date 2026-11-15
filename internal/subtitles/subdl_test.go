package subtitles

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// TestSubDLSearchDownloadRank exercises the SubDL adapter end to end: the
// search query carries the ids/season/episode and language, results map
// onto Candidates, the zip download extracts the matching .srt, and both
// providers merge into one ranked list.
func TestSubDLSearchDownloadRank(t *testing.T) {
	var searchQuery string
	zipBytes := func(name, text string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, _ := zw.Create(name)
		f.Write([]byte(text))
		zw.Close()
		return buf.Bytes()
	}
	sub := "1\n00:00:01,000 --> 00:00:02,000\nBonjour tout le monde\n\n"
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/subtitles", func(w http.ResponseWriter, r *http.Request) {
		searchQuery = r.URL.RawQuery
		if r.URL.Query().Get("api_key") != "subdl-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"status": true,
			"results": []any{map[string]any{
				"imdb_id": "tt1375666", "tmdb_id": 27205, "type": "movie", "name": "Inception", "sd_id": 123456,
			}},
			"subtitles": []any{
				map[string]any{
					"release_name": "Inception.2010.1080p.BluRay.x264-GRP",
					"name":         "Inception.2010.1080p.BluRay.x264-GRP.fr.zip",
					"url":          "/subtitle/3197651-3213944.zip",
					"hi":           false,
					"language":     "French",
				},
				map[string]any{
					"release_name": "Some.Other.Edition.720p",
					"name":         "other.fr.zip",
					"url":          "/subtitle/3197651-3213945.zip",
					"hi":           true,
					"language":     "French",
				},
			},
		})
	})
	mux.HandleFunc("/subtitle/3197651-3213944.zip", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "subdl-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write(zipBytes("Inception.2010.1080p.BluRay.x264-GRP.fr.srt", sub))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	if err := testSubDL(context.Background(), srv.Client(), srv.URL, config.SubDL{APIKey: "subdl-key"}); err != nil {
		t.Fatalf("connection test: %v", err)
	}

	svc := New(Options{
		SubDL:            config.SubDL{Enabled: true, APIKey: "subdl-key"},
		CacheDir:         t.TempDir(),
		SubDLURL:         srv.URL,
		SubDLDownloadURL: srv.URL,
	})
	title := &core.Title{Key: core.MovieKey(27205), Type: core.TypeMovie, IDs: core.IDs{TMDB: 27205, IMDB: "tt1375666"}}

	cands, err := svc.SearchOnDemand(context.Background(), title, "fr", "Inception.2010.1080p.BluRay.x264-GRP")
	if err != nil || len(cands) != 2 {
		t.Fatalf("search: %+v %v", cands, err)
	}
	for _, key := range []string{"tmdb_id=27205", "imdb_id=1375666", "languages=FR", "type=movie"} {
		if !bytes.Contains([]byte(searchQuery), []byte(key)) {
			t.Fatalf("query missing %q: %s", key, searchQuery)
		}
	}
	// The exact release match ranks first despite the HI penalty elsewhere.
	if cands[0].Provider != "SubDL" || cands[0].ProviderID != config.SubtitleProviderSubDL ||
		cands[0].Release != "Inception.2010.1080p.BluRay.x264-GRP" {
		t.Fatalf("top candidate: %+v", cands[0])
	}

	st, err := svc.DownloadOnDemand(context.Background(), title, cands[0].FileID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Language != "fr" || st.ProviderTitle != "SubDL" {
		t.Fatalf("pick: %+v", st)
	}
	rc, format, err := svc.OpenFormat(st.ExternalURL, "srt")
	if err != nil || format != "srt" {
		t.Fatalf("open: %v %s", err, format)
	}
	defer rc.Close()
	got := make([]byte, 256)
	n, _ := rc.Read(got)
	if !bytes.Contains(got[:n], []byte("Bonjour tout le monde")) {
		t.Fatalf("downloaded body: %q", got[:n])
	}
}

// TestSubDLOnlyWhenEnabled checks the service reports disabled without a key.
func TestSubDLOnlyWhenEnabled(t *testing.T) {
	svc := New(Options{CacheDir: t.TempDir(), SubDL: config.SubDL{Enabled: true}})
	if svc.Enabled() {
		t.Fatal("SubDL without a key must not enable search")
	}
	if svc.subdlEnabled() {
		t.Fatal("subdlEnabled must require a key")
	}
}

// TestSubDLLanguageSet covers the provider contract: query aliases, language
// filtering, multilingual offers and language-scoped negative caching.
func TestSubDLLanguageSet(t *testing.T) {
	var queries []string
	const release = "Troll.2022.1080p.WEB.mkv"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("languages"))
		if r.URL.Query().Get("file_name") != release || r.URL.Query().Get("releases") != "1" {
			t.Errorf("missing release query: %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":true,"subtitles":[
   {"language":"Norwegian","name":"n.zip","url":"/n.zip"},
   {"language":"EN","name":"e.zip","url":"/e.zip","releases":["Troll.2022.1080p.WEB.mkv"]},
   {"language":"French","name":"f.zip","url":"/f.zip"},
   {"language":"sv","name":"s.zip","url":"/s.zip"},
   {"language":"da","name":"d.zip","url":"/d.zip"},
   {"language":"","name":"unknown.zip","url":"/unknown.zip"}
  ]}`))
	}))
	defer srv.Close()
	svc := New(Options{SubDL: config.SubDL{Enabled: true, APIKey: "test"}, CacheDir: t.TempDir(), SubDLURL: srv.URL})
	title := &core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt11116912"}}
	ctx := context.Background()
	cands, err := svc.SearchOnDemandLanguages(ctx, title, []string{"no", "nb", "nob", "eng", "swe", "dan"}, release)
	if err != nil || len(cands) != 4 {
		t.Fatalf("preferred results: %+v, %v", cands, err)
	}
	if cands[0].Language != "en" {
		t.Fatalf("release match did not beat language order: %+v", cands)
	}
	if queries[0] != "DA,EN,NO,SV" {
		t.Fatalf("query: %q", queries[0])
	}
	for _, c := range cands {
		svc.offersMu.Lock()
		o := svc.offers[offerKey{title.Key, c.FileID}]
		svc.offersMu.Unlock()
		if o.lang != c.Language || o.lang == "" {
			t.Fatalf("offer lost language: %+v", o)
		}
	}
	cands, err = svc.SearchOnDemand(ctx, title, "fra", release)
	if err != nil || len(cands) != 1 || cands[0].Language != "fr" || queries[1] != "FR" {
		t.Fatalf("explicit search: %+v, %v, %v", cands, err, queries)
	}
	// A miss for a different language cannot suppress a later matching search.
	_, err = svc.SearchOnDemand(ctx, title, "de", release)
	if err != nil {
		t.Fatal(err)
	}
	before := len(queries)
	cands, err = svc.SearchOnDemand(ctx, title, "nb", release)
	if err != nil || len(cands) != 1 || len(queries) != before+1 || queries[len(queries)-1] != "NO" {
		t.Fatalf("Norwegian: %+v %v %v", cands, err, queries)
	}
	before = len(queries)
	if _, err = svc.SearchOnDemandLanguages(ctx, title, nil, ""); err != nil || len(queries) != before {
		t.Fatal("empty preferences queried provider")
	}
	os := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("moviehash") != "000000000000abcd" || q.Get("moviebytesize") != "1048576" || q.Get("languages") != "en,no" {
			t.Errorf("OpenSubtitles query: %v", q)
		}
		w.Write([]byte(`{"data":[{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":99}]}},{"attributes":{"language":"fr","files":[{"file_id":100}]}}]}`))
	}))
	defer os.Close()
	opensubs := New(Options{Config: config.OpenSubtitles{Enabled: true, APIKey: "test"}, CacheDir: t.TempDir(), BaseURL: os.URL})
	cands, err = opensubs.SearchOnDemandLanguagesWithHash(ctx, title, []string{"nb", "en"}, release, "000000000000abcd", 1048576)
	if err != nil || len(cands) != 1 || !cands[0].HashMatch {
		t.Fatalf("OpenSubtitles result: %+v %v", cands, err)
	}

}

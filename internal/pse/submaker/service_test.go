package submaker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const english = "1\n00:00:01,000 --> 00:00:03,000\nGood morning.\n\n2\n00:00:04,000 --> 00:00:06,000\nSee you soon.\n"
const french = "1\n00:00:01,000 --> 00:00:03,000\nBonjour.\n\n2\n00:00:04,000 --> 00:00:06,000\nÀ bientôt.\n"

func fixture(t *testing.T, bad bool) (*Service, core.SearchRequest, core.Release, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	name := "Example.Movie.2024.1080p.WEB-DL.x264-GROUP"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/subtitles/"):
			if !strings.Contains(r.URL.Path, "filename=") {
				t.Error("missing filename")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"subtitles": []any{map[string]string{"id": "source", "lang": "eng", "label": name + ".srt / Other.Release.1080p-GROUP • fixture", "url": server.URL + "/addon/private/v1.4.97/subtitle/source/eng.srt"}}})
		case r.URL.Path == "/addon/private/v1.4.97/subtitle/source/eng.srt":
			fmt.Fprint(w, english)
		case r.URL.Path == "/api/translate-file":
			calls.Add(1)
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input["configStr"] != "private" {
				t.Error("missing private config")
			}
			if bad {
				fmt.Fprint(w, "\n[TRANSLATION_ERROR]\nquota")
			} else {
				fmt.Fprint(w, "\n"+french)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	restore := pseenv.Replace(map[string]string{"PSE_SUBMAKER": "on", "PSE_SUBMAKER_MANIFEST": server.URL + "/addon/private/manifest.json", "PSE_SUBMAKER_DAILY": "2"})
	t.Cleanup(restore)
	s := New(store.OpenTest(t), server.Client())
	t.Cleanup(s.Close)
	s.scopes.Global = true
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 10, IMDB: "tt1234567"}, Title: "Example Movie"}
	rel := core.Release{Name: name, InfoHash: strings.Repeat("a", 40)}
	return s, req, rel, calls
}
func await(t *testing.T, s *Service, req core.SearchRequest, rel core.Release) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		_ = s.DB.QueryRow("SELECT state FROM remix_submaker_jobs WHERE id=?", fingerprint(Load(), req, rel)).Scan(&state)
		if state == "ready" || state == "failed" {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("worker did not finish")
	return ""
}
func TestTranslationCachedReleaseBoundAndNoSecret(t *testing.T) {
	s, req, rel, calls := fixture(t, false)
	if p, pending := s.Ensure(context.Background(), req, rel); p != nil || !pending {
		t.Fatal("must queue")
	}
	for i := 0; i < 10; i++ {
		s.Ensure(context.Background(), req, rel)
	}
	if await(t, s, req, rel) != "ready" {
		t.Fatal("not ready")
	}
	p, pending := s.Ensure(context.Background(), req, rel)
	if pending || p == nil || !p.Translated || !strings.Contains(p.SRT, "Bonjour") {
		t.Fatalf("invalid proof %+v", p)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "private") || calls.Load() != 1 {
		t.Fatal("token leak or duplicate translation")
	}
	rel.InfoHash = strings.Repeat("b", 40)
	if s.Cached(req, rel) != nil {
		t.Fatal("wrong release binding")
	}
	req.Type = core.TypeEpisode
	req.Season = 1
	req.Episode = 2
	if s.Cached(req, rel) != nil {
		t.Fatal("wrong episode")
	}
}
func TestFailedTranslationNeverReadyAndBudget(t *testing.T) {
	s, req, rel, calls := fixture(t, true)
	s.Ensure(context.Background(), req, rel)
	if await(t, s, req, rel) != "failed" {
		t.Fatal("error became ready")
	}
	if p, pending := s.Ensure(context.Background(), req, rel); p != nil || pending {
		t.Fatal("failed work retried immediately")
	}
	_, _ = s.DB.Exec("UPDATE remix_submaker_jobs SET updated=0")
	s.Ensure(context.Background(), req, rel)
	await(t, s, req, rel)
	_, _ = s.DB.Exec("UPDATE remix_submaker_jobs SET updated=0")
	s.Ensure(context.Background(), req, rel)
	if calls.Load() != 2 {
		t.Fatal("retry budget not respected")
	}
}
func TestScopePrecedenceAndDisabled(t *testing.T) {
	s, req, rel, calls := fixture(t, false)
	s.scopes = Scopes{Global: true, Rules: []Rule{{"library", "1", false}, {"collection", "2", true}}}
	s.Match = func(_ context.Context, r Rule, _ core.RatingKey) bool { return true }
	if !s.Allowed(context.Background(), req) {
		t.Fatal("collection must win")
	}
	s.scopes.Rules = s.scopes.Rules[:1]
	if s.Allowed(context.Background(), req) {
		t.Fatal("library disabled")
	}
	s.Ensure(context.Background(), req, rel)
	if calls.Load() != 0 {
		t.Fatal("disabled scope called provider")
	}
}
func TestStrictSubtitleAndEndpointValidation(t *testing.T) {
	if _, e := ValidSRT([]byte("<html>Error</html>")); e == nil {
		t.Fatal("HTML accepted")
	}
	if SameTiming(english, strings.ReplaceAll(french, "00:00:04", "00:00:05")) {
		t.Fatal("retiming accepted")
	}
	for _, u := range []string{"https://example/configure", "https://u:p@example/addon/token/manifest.json", "https://example/addon/token/manifest.json?key=secret"} {
		if _, _, e := Endpoint(u); e == nil {
			t.Fatal("bad manifest accepted")
		}
	}
	if Canonical("Example.Movie.1080p-GROUP.mkv") != Canonical("Example.Movie.1080p-GROUP.srt • provider") {
		t.Fatal("matching stem rejected")
	}
	if Canonical("Show.S01E01.1080p") == Canonical("Show.S01E02.1080p") {
		t.Fatal("episode mismatch")
	}
}

func TestProviderPathsAndExactAliases(t *testing.T) {
	for _, p := range []string{"/addon/token/subtitle/id/fr.srt", "/addon/token/v1.4.97/subtitle/id/fr.srt"} {
		if !subtitlePath(p, "token") {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"/addon/other/subtitle/id/fr.srt", "/addon/token/admin/id/fr.srt", "/addon/token/../subtitle/id/fr.srt"} {
		if subtitlePath(p, "token") {
			t.Fatal(p)
		}
	}
	if !matchesRelease("Movie.1080p-A / Movie.720p-B • provider", "Movie.720p-B.mkv") {
		t.Fatal("exact alias rejected")
	}
	if matchesRelease("Movie.1080p-A / Movie.720p-B • provider", "Movie.720p-C") {
		t.Fatal("wrong group accepted")
	}
}

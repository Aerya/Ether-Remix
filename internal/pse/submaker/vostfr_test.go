package submaker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
)

func vostfrService(t *testing.T, ready bool) *Service {
	t.Helper()
	env := map[string]string{"PSE_SUBMAKER": "off"}
	if ready {
		env = map[string]string{"PSE_SUBMAKER": "on", "PSE_SUBMAKER_MANIFEST": "https://submaker.example/addon/private/manifest.json"}
	}
	t.Cleanup(pseenv.Replace(env))
	s := New(store.OpenTest(t), http.DefaultClient)
	t.Cleanup(s.Close)
	return s
}

func TestVostfrSwitchScopesChosenTitles(t *testing.T) {
	ctx := context.Background()
	s := vostfrService(t, true)
	movie, show, other := core.MovieKey(10), core.ShowKey(20), core.MovieKey(30)
	s.VOSTFR.Keys = func(context.Context) []core.RatingKey { return []core.RatingKey{movie, show} }
	episode := core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 20}, Season: 1, Episode: 2}
	film := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 10}}
	if s.Allowed(ctx, film) {
		t.Fatal("switch off: the global scope decides")
	}
	if err := s.SetVostfr(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !s.Allowed(ctx, film) || !s.Allowed(ctx, episode) {
		t.Fatal("chosen movie and show episodes must be in scope")
	}
	if s.Allowed(ctx, core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: other.TMDB()}}) {
		t.Fatal("other titles stay out of scope")
	}
	// A library rule turning SubMaker off loses; an explicit collection rule wins.
	s.Match = func(_ context.Context, r Rule, _ core.RatingKey) bool { return true }
	s.scopes.Rules = []Rule{{Scope: "library", Target: "1", Enabled: false}}
	if !s.Allowed(ctx, film) {
		t.Fatal("library rule must not beat the VOSTFR switch")
	}
	s.scopes.Rules = []Rule{{Scope: "collection", Target: "c", Enabled: false}}
	if s.Allowed(ctx, film) {
		t.Fatal("explicit collection rule must win")
	}
	// The switch survives a restart.
	again := New(s.DB, http.DefaultClient)
	t.Cleanup(again.Close)
	if !again.Vostfr() {
		t.Fatal("switch not persisted")
	}
}

func TestVostfrStates(t *testing.T) {
	ctx := context.Background()
	s := vostfrService(t, true)
	keys := []core.RatingKey{core.MovieKey(1), core.MovieKey(2), core.MovieKey(3), core.ShowKey(4), core.MovieKey(5), core.MovieKey(6), core.MovieKey(7)}
	s.VOSTFR.Keys = func(context.Context) []core.RatingKey { return keys }
	langs := map[core.RatingKey][2][]string{
		keys[0]: {{"fr"}, nil},
		keys[1]: {{"en"}, {"fre"}},
		keys[2]: {{"en"}, {"en"}},
		keys[3]: {{"ja"}, nil},
		keys[4]: {{"en"}, nil},
		keys[5]: {{"en"}, nil},
	}
	s.VOSTFR.Languages = func(_ context.Context, k core.RatingKey) (int, []string, []string) {
		l, ok := langs[k]
		if !ok {
			return 0, nil, nil
		}
		return 1, l[0], l[1]
	}
	for _, job := range []struct {
		key        core.RatingKey
		state      string
		translated bool
	}{{core.EpisodeKey(4, 1, 1), "ready", true}, {core.MovieKey(5), "running", false}, {core.MovieKey(6), "failed", false}} {
		if _, err := s.DB.Exec(`INSERT INTO remix_submaker_jobs(id,title_key,release_name,state,language,translated,updated,created) VALUES(?,?,?,?,?,?,1,1)`, job.key.String(), int64(job.key), "r", job.state, "fr", job.translated); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{VostfrFrenchAudio, VostfrAnnounced, VostfrNone, VostfrTranslated, VostfrPending, VostfrFailed, VostfrUnindexed}
	got := s.VostfrStates(ctx)
	for i, title := range got {
		if title.Key != keys[i].String() || title.State != want[i] {
			t.Fatalf("title %d: %+v, want %s", i, title, want[i])
		}
	}
}

func TestVostfrAPI(t *testing.T) {
	s := vostfrService(t, false)
	h := s.Handler()
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/vostfr", strings.NewReader(body)))
		return rec
	}
	if rec := put(`{"enabled":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("enabling without SubMaker: %d", rec.Code)
	}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_SUBMAKER": "on", "PSE_SUBMAKER_MANIFEST": "https://submaker.example/addon/private/manifest.json"}))
	s.VOSTFR.Keys = func(context.Context) []core.RatingKey { return []core.RatingKey{core.MovieKey(1)} }
	var searched []core.RatingKey
	s.VOSTFR.Search = func(_ context.Context, keys []core.RatingKey) (int, error) {
		searched = keys
		return len(keys), nil
	}
	rec := put(`{"enabled":true}`)
	var out struct {
		Available, Enabled bool
		Queued             int
		Titles             []VostfrTitle
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if !out.Available || !out.Enabled || out.Queued != 1 || len(searched) != 1 || out.Titles[0].State != VostfrUnindexed {
		t.Fatalf("enable: %+v searched %v", out, searched)
	}
	if strings.Contains(rec.Body.String(), "private") {
		t.Fatal("manifest leaked")
	}
}

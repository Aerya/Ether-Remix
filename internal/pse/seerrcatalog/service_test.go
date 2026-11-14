package seerrcatalog

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stremio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, *[]Snapshot, *int) {
	t.Helper()
	db, e := store.OpenMemory(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	items := []Snapshot{observation(7, "movie", 603)}
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("token missing")
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"schema": Schema, "revision": 1, "items": items, "nextCursor": nil})
	}))
	t.Cleanup(srv.Close)
	restore := pseenv.Replace(map[string]string{"PSE_SEERRCATALOG": "on", "PSE_SEERRCATALOG_URL": srv.URL, "PSE_SEERRCATALOG_TOKEN": strings.Repeat("a", 64)})
	t.Cleanup(restore)
	return New(db, srv.Client()), &items, &status
}
func observation(id int64, typ string, tmdb int64) Snapshot {
	now := time.Now().UTC()
	v := Snapshot{Schema: Schema, Media: Media{ID: id, Type: typ, TMDB: tmdb, IMDB: "tt0133093", Title: "The Matrix", Year: 1999}, Available: true, Status: "available", Checked: now, Expires: now.Add(26 * time.Hour), ReleasesExpire: now.Add(30 * time.Minute)}
	v.Releases = []Release{{AddonID: "one", AddonName: "Addon", StreamID: v.Media.IMDB, Name: "The.Matrix.1999.FRENCH.1080p.WEB-DL", Stream: stremio.Stream{URL: "https://video.example/watch?token=private"}}}
	return v
}
func TestSyncReplacesUpdatedAndDeletedItemsWithoutSearching(t *testing.T) {
	s, items, status := fixture(t)
	ctx := t.Context()
	if e := s.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if !Trusted(ctx, s.DB, core.MovieKey(603)) {
		t.Fatal("availability not imported")
	}
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 603, IMDB: "tt0133093"}, Title: "The Matrix", Year: 1999}
	rs, ok := s.Lookup(ctx, req)
	if !ok || len(rs) != 1 || !strings.Contains(rs[0].StreamURL, "token=private") {
		t.Fatalf("sources not reused: %+v, %v", rs, ok)
	}
	*status = 503
	if s.Sync(ctx) == nil || !Trusted(ctx, s.DB, core.MovieKey(603)) {
		t.Fatal("outage destroyed valid snapshot")
	}
	*status = 200
	(*items)[0].Available = false
	(*items)[0].Status = "unavailable"
	if e := s.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if Trusted(ctx, s.DB, core.MovieKey(603)) {
		t.Fatal("old ID update missed")
	}
	*items = nil
	if e := s.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM pse_seerrcatalog").Scan(&n)
	if n != 0 {
		t.Fatal("missed deletion retained")
	}
}
func TestLifetimesDisableAndEpisodeIdentity(t *testing.T) {
	s, items, _ := fixture(t)
	v := observation(1, "series", 1399)
	v.Releases[0].StreamID = "tt0133093:3:1"
	v.Releases[0].Name = "Show.S03E01.FRENCH.1080p.WEB-DL"
	*items = []Snapshot{v}
	if e := s.Sync(t.Context()); e != nil {
		t.Fatal(e)
	}
	req := core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 1399, IMDB: "tt0133093"}, Title: "Show", Season: 3, Episode: 1}
	if rs, ok := s.Lookup(t.Context(), req); !ok || len(rs) == 0 {
		t.Fatalf("episode lost: %v %v", rs, ok)
	}
	req.Episode = 2
	if _, ok := s.Lookup(t.Context(), req); ok {
		t.Fatal("different episode trusted")
	}
	req.Episode = 1
	s.snapshots[0].ReleasesExpire = time.Now().Add(-time.Minute)
	if _, ok := s.Lookup(t.Context(), req); ok {
		t.Fatal("expired URL reused for playback")
	}
	if _, ok := s.Lookup(core.WithIndexerPurpose(t.Context(), core.IndexerPurposeAvailability), req); !ok {
		t.Fatal("duplicate background search")
	}
	s.snapshots[0].Releases[0].Stream.InfoHash = strings.Repeat("a", 40)
	rs, ok := s.Lookup(t.Context(), req)
	if !ok || len(rs) != 1 || rs[0].StreamURL != "" || rs[0].InfoHash == "" {
		t.Fatal("hash not retained safely", rs)
	}
	if _, ok := s.Lookup(core.WithIndexerPurpose(t.Context(), core.IndexerPurposeRescue), req); ok {
		t.Fatal("rescue used same cache")
	}
	restore := pseenv.Replace(map[string]string{})
	defer restore()
	if _, ok := s.Lookup(t.Context(), req); ok {
		t.Fatal("disabled lookup")
	}
	if Trusted(t.Context(), s.DB, core.ShowKey(1399)) {
		t.Fatal("disabled SQL trust")
	}
	if e := s.Sync(t.Context()); e != nil {
		t.Fatal(e)
	}
	if len(s.snapshots) != 0 {
		t.Fatal("credentials retained after disable")
	}
}
func TestPagedFailureIsAtomicAndTokenDoesNotFollowRedirect(t *testing.T) {
	s, _, _ := fixture(t)
	if e := s.Sync(t.Context()); e != nil {
		t.Fatal(e)
	}
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	s.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("after") == "0" {
			body := `{"schema":"` + Schema + `","revision":1,"items":[],"nextCursor":7}`
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if s.Sync(t.Context()) == nil || !Trusted(t.Context(), s.DB, core.MovieKey(603)) {
		t.Fatal("partial traversal changed snapshot")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	c := Load()
	restore := pseenv.Replace(map[string]string{"PSE_SEERRCATALOG": "on", "PSE_SEERRCATALOG_URL": redirect.URL, "PSE_SEERRCATALOG_TOKEN": c.Token})
	defer restore()
	safe := New(s.DB, nil)
	if safe.Sync(t.Context()) == nil || leaked {
		t.Fatal("redirect followed")
	}
}
func TestWebhookAuthorizationAndRevocation(t *testing.T) {
	s, _, status := fixture(t)
	for _, token := range []string{"", "wrong", strings.Repeat("a", 64)} {
		r := httptest.NewRequest("POST", EventsPath, strings.NewReader(`{"schema":"`+Schema+`","event":"media.removed"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := 401
		if len(token) == 64 {
			want = 202
		}
		if w.Code != want {
			t.Fatal(w.Code, want)
		}
	}
	if e := s.Sync(t.Context()); e != nil {
		t.Fatal(e)
	}
	*status = 401
	_ = s.Sync(t.Context())
	if Trusted(t.Context(), s.DB, core.MovieKey(603)) {
		t.Fatal("revoked export still trusted")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConfirmedAvailabilitySurvivesAgeButDoesNotReviveSignedURLs(t *testing.T) {
	s, items, status := fixture(t)
	v := observation(7, "movie", 603)
	v.Confirmed = true
	v.Checked = time.Now().Add(-30 * 24 * time.Hour)
	v.Expires = v.Checked.Add(26 * time.Hour)
	v.ReleasesExpire = v.Checked.Add(30 * time.Minute)
	*items = []Snapshot{v}
	if err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !Trusted(t.Context(), s.DB, core.MovieKey(603)) {
		t.Fatal("confirmation expired")
	}
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 603, IMDB: "tt0133093"}, Title: "The Matrix", Year: 1999}
	releases, _ := s.Lookup(t.Context(), req)
	if len(releases) != 0 {
		t.Fatal("expired signed URL resurrected")
	}
	// Database trust remains after an application restart without a successful feed fetch.
	restarted := New(s.DB, s.HTTP)
	*status = 503
	if restarted.Sync(t.Context()) == nil || !Trusted(t.Context(), s.DB, core.MovieKey(603)) {
		t.Fatal("outage removed confirmation")
	}
	*status = 200
	*items = nil
	if err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if Trusted(t.Context(), s.DB, core.MovieKey(603)) {
		t.Fatal("deleted title retained")
	}
}

func TestSnapshotFallbackReadsAppliedFilters(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"appliedFilters":{"languageTags":["FRENCH","MULTI"],"subtitleFallbackUsed":false}}`: false,
		`{"appliedFilters":{"languageTags":["VOSTFR"],"subtitleFallbackUsed":true}}`:          true,
		`{"appliedFilters":{"languageTags":["VO + STFR"],"externalSubtitleLanguage":"fr"}}`:   true,
		`{"appliedFilters":null}`: false,
		`{}`:                      false,
	} {
		var v Snapshot
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		if v.Fallback() != want {
			t.Fatalf("%s : secours = %v", raw, v.Fallback())
		}
	}
}

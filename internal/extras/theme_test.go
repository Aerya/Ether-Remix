package extras

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Cache-only reads, single-flight demand, negative cooldown, restart durability,
// show inheritance and policy disabling form the shared cross-protocol invariant.
func TestThemeCachePolicy(t *testing.T) {
	cfg := config.Extras{Enabled: true}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	opts := Options{CacheDir: t.TempDir(), Config: func() config.Extras { return cfg }, LookPath: lookOK, Runner: func(context.Context, string, []string) ([]byte, error) {
		calls.Add(1)
		close(started)
		<-release
		return nil, errors.New("offline")
	}}
	s := New(opts)
	s.themeWait = 0
	t.Cleanup(s.Close)
	show := core.Title{Key: core.ShowKey(42), Title: "Example"} // no TVDB id: fallback only
	if s.Theme(show, false) != "" || calls.Load() != 0 {
		t.Fatal("cache read resolved source")
	}
	if s.Theme(show, true) != "" {
		t.Fatal("missing theme advertised")
	}
	<-started
	for range 20 {
		s.Theme(show, true)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate theme fetches for one show")
	}
	close(release)
	s.themeWG.Wait()
	s.Theme(show, true)
	s.themeWG.Wait()
	if calls.Load() != 1 {
		t.Fatal("negative cooldown ignored")
	}
	path := s.resolverThemePath(show.Key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := New(opts)
	t.Cleanup(restarted.Close)
	episode := core.Title{Key: core.EpisodeKey(42, 1, 2), ShowKey: show.Key, ShowTitle: show.Title}
	if restarted.Theme(episode, false) != ThemeM4A {
		t.Fatal("durable show theme not inherited")
	}
	req := httptest.NewRequest("GET", "/theme", nil)
	req.Header.Set("Range", "bytes=2-4")
	rec := httptest.NewRecorder()
	restarted.ServeTheme(rec, req, episode)
	if rec.Code != 206 || rec.Body.String() != "234" || rec.Header().Get("Content-Type") != "audio/mp4" {
		t.Fatalf("range: %d %q", rec.Code, rec.Body.String())
	}
	cfg.ThemeMusic = new(false)
	if restarted.Theme(show, true) != "" {
		t.Fatal("disabled theme advertised")
	}
	rec = httptest.NewRecorder()
	restarted.ServeTheme(rec, req, show)
	if rec.Code != 404 {
		t.Fatal("disabled theme served")
	}
}

type themeTransport func(*http.Request) (*http.Response, error)

func (f themeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Plex's theme service (by TVDB id) is the source; YouTube is used only when
// Plex has no theme, and a bot check pauses YouTube for every show instead of
// being retried per show. A cached YouTube guess is upgraded to the Plex theme.
func TestThemeSourceSelection(t *testing.T) {
	mp3 := "ID3\x04\x00\x00\x00\x00\x00\x00theme"
	var mu sync.Mutex
	hits := map[string]int{}
	client := &http.Client{Transport: themeTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Host != "tvthemes.plexapp.com" {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		if r.URL.Path == "/81189.mp3" {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(mp3)), ContentLength: int64(len(mp3))}, nil
		}
		return &http.Response{StatusCode: 404, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("<html>"))}, nil
	})}
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var resolverCalls atomic.Int32
	s := New(Options{
		CacheDir: t.TempDir(), CacheHTTP: client, LookPath: lookOK,
		Config: func() config.Extras { return config.Extras{Enabled: true} },
		Now:    func() time.Time { return now },
		Runner: func(context.Context, string, []string) ([]byte, error) {
			resolverCalls.Add(1)
			return nil, errors.New("extras resolver: exit status 1: ERROR: [youtube] abc: Sign in to confirm you’re not a bot")
		},
	})
	s.themeWait = 0
	t.Cleanup(s.Close)
	open := func(title core.Title) string {
		s.Theme(title, true)
		s.themeWG.Wait()
		return s.Theme(title, false)
	}

	// Plex hit: no search at all, MP3 served as-is.
	breaking := core.Title{Key: core.ShowKey(1), Title: "Breaking Bad", IDs: core.IDs{TVDB: 81189}}
	if got := open(breaking); got != ThemeMP3 || resolverCalls.Load() != 0 {
		t.Fatalf("plex theme = %q, resolver calls %d", got, resolverCalls.Load())
	}
	season := core.Title{Key: core.SeasonKey(1, 1), ShowKey: breaking.Key, ShowTitle: breaking.Title}
	rec := httptest.NewRecorder()
	s.ServeTheme(rec, httptest.NewRequest("GET", "/theme", nil), season)
	if rec.Code != 200 || rec.Body.String() != mp3 || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("serve: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	// Plex 404: falls back to YouTube once; the bot check pauses YouTube.
	obscure := core.Title{Key: core.ShowKey(2), Title: "Obscure", IDs: core.IDs{TVDB: 404}}
	if got := open(obscure); got != "" || resolverCalls.Load() != 1 {
		t.Fatalf("fallback = %q, resolver calls %d", got, resolverCalls.Load())
	}
	other := core.Title{Key: core.ShowKey(3), Title: "Other"}
	if got := open(other); got != "" || resolverCalls.Load() != 1 {
		t.Fatalf("resolver ran during bot-check pause: calls %d", resolverCalls.Load())
	}
	if open(obscure); hits["/404.mp3"] != 1 {
		t.Fatalf("show retried inside its cooldown: %d plex lookups", hits["/404.mp3"])
	}
	now = now.Add(themeBotFirst + time.Minute)
	if open(core.Title{Key: core.ShowKey(4), Title: "Later"}); resolverCalls.Load() != 2 || s.themeBotStep != 2*themeBotFirst {
		t.Fatalf("pause did not expire and double: calls %d step %v", resolverCalls.Load(), s.themeBotStep)
	}

	// A cached YouTube guess plays now and is replaced by the Plex theme.
	guessed := core.Title{Key: core.ShowKey(5), Title: "Guessed", IDs: core.IDs{TVDB: 81189}}
	legacy := s.resolverThemePath(guessed.Key)
	if err := os.WriteFile(legacy, []byte("m4a"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := s.Theme(guessed, true); got != ThemeM4A {
		t.Fatalf("cached guess = %q", got)
	}
	s.themeWG.Wait()
	if got := s.Theme(guessed, false); got != ThemeMP3 {
		t.Fatalf("upgrade = %q", got)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("YouTube guess kept beside the Plex theme")
	}
}

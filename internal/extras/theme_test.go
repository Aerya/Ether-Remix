package extras

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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
	show := core.Title{Key: core.ShowKey(42), Title: "Example"}
	if s.Theme(show, false) || calls.Load() != 0 {
		t.Fatal("cache read resolved source")
	}
	if s.Theme(show, true) {
		t.Fatal("missing theme advertised")
	}
	<-started
	for range 20 {
		s.Theme(show, true)
	}
	if calls.Load() != 1 {
		t.Fatal("unbounded theme workers")
	}
	close(release)
	s.themeWork.Lock()
	s.themeWork.Unlock() //nolint:staticcheck // SA2001: wait for the worker to finish
	s.Theme(show, true)
	if calls.Load() != 1 {
		t.Fatal("negative cooldown ignored")
	}
	path := s.themePath(show)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := New(opts)
	episode := core.Title{Key: core.EpisodeKey(42, 1, 2), ShowKey: show.Key, ShowTitle: show.Title}
	if !restarted.Theme(episode, false) {
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
	if restarted.Theme(show, true) {
		t.Fatal("disabled theme advertised")
	}
	rec = httptest.NewRecorder()
	restarted.ServeTheme(rec, req, show)
	if rec.Code != 404 {
		t.Fatal("disabled theme served")
	}
}

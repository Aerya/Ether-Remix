package localmedia

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthEmptyPartialMountAndRecovery(t *testing.T) {
	for _, kind := range []string{"video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			db := store.OpenTest(t)
			dir := t.TempDir()
			ext := ".mp4"
			if kind == "audio" {
				ext = ".mp3"
			}
			path := filepath.Join(dir, "media"+ext)
			write := func() {
				t.Helper()
				if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write()
			published := 0
			l := &Library{DB: db, Probe: func(context.Context, *os.File, string) (File, error) { return File{}, nil }, Publish: func(_ context.Context, _ Root, f []File) error { published = len(f); return nil }}
			ctx := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
			if err := l.Add(ctx, Root{Name: "Test", Path: dir, Kind: kind}); err != nil {
				t.Fatal(err)
			}
			scan := func() {
				t.Helper()
				if err := l.Scan(ctx); err != nil {
					t.Fatal(err)
				}
			}
			health := func() RootHealth {
				t.Helper()
				h, err := l.Health(ctx)
				if err != nil || len(h.Roots) != 1 {
					t.Fatal(h, err)
				}
				return h.Roots[0]
			}
			scan()
			h := health()
			if h.Bytes != 10 || h.Files[0].State != "available" || h.Files[0].Unidentified != (kind == "video") {
				t.Fatal(h)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			scan()
			l = &Library{DB: db, Probe: l.Probe, Publish: l.Publish} // restart must preserve absent entries
			scan()
			h = health()
			if published != 1 || len(h.Files) != 1 || h.Bytes != 0 || h.KnownBytes != 10 || h.Files[0].State != "missing" {
				t.Fatal(h, published)
			}
			write()
			scan()
			if health().Files[0].State != "available" {
				t.Fatal(health())
			}
			moved := dir + "-offline"
			if err := os.Rename(dir, moved); err != nil {
				t.Fatal(err)
			}
			defer os.Rename(moved, dir)
			if err := l.Scan(ctx); err == nil {
				t.Fatal("offline scan accepted")
			}
			h = health()
			if h.State != "missing" || h.Files[0].State != "unavailable" {
				t.Fatal(h)
			}
		})
	}
}
func TestHealthDuplicatesAndFailedProbe(t *testing.T) {
	db := store.OpenTest(t)
	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "movie.mp4"), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l := &Library{DB: db, Probe: func(context.Context, *os.File, string) (File, error) { return File{}, nil }}
	ctx := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
	if err := l.Add(ctx, Root{Name: "Movies", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	h, err := l.Health(ctx)
	if err != nil || len(h.Roots[0].Files) != 2 {
		t.Fatal(h, err)
	}
	for _, f := range h.Roots[0].Files {
		if !f.Duplicate {
			t.Fatal(f)
		}
	}
	if err := os.Remove(filepath.Join(dir, "a", "movie.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "movie.mp4"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	l.Probe = func(context.Context, *os.File, string) (File, error) { return File{}, errors.New("probe unavailable") }
	if err := l.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	h, err = l.Health(ctx)
	if err != nil || len(h.Roots[0].Files) != 2 {
		t.Fatal(h, err)
	}
	states := map[string]int{}
	for _, f := range h.Roots[0].Files {
		states[f.State]++
		if f.Duplicate {
			t.Fatal(f)
		}
	}
	if states["missing"] != 1 || states["changed"] != 1 {
		t.Fatal(h)
	}
	if healthState(fs.ErrPermission) != "permission" {
		t.Fatal("permission classification")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = l.Health(cancelled); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestHealthPermissionsAndConfinement(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks require an unprivileged process")
	}
	db := store.OpenTest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "private.mp4")
	if err := os.WriteFile(path, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	l := &Library{DB: db, Probe: func(context.Context, *os.File, string) (File, error) { return File{}, nil }}
	ctx := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
	if err := l.Add(ctx, Root{Name: "Private", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	h, err := l.Health(ctx)
	if err != nil || h.Roots[0].Files[0].State != "permission" {
		t.Fatal(h, err)
	}
	if err := os.Chmod(dir, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if err := l.Scan(ctx); err == nil {
		t.Fatal("unreadable scan accepted")
	}
	h, err = l.Health(ctx)
	if err != nil || h.Roots[0].State != "permission" || h.Roots[0].Files[0].State != "unavailable" {
		t.Fatal(h, err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	h, err = l.Health(ctx)
	if err != nil || h.Roots[0].Files[0].State != "inaccessible" || h.Roots[0].Bytes != 0 {
		t.Fatal(h, err)
	}
}

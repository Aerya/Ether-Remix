package ftp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheFixture(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	s := &Service{Settings: Settings{Mode: ModeDownload, DownloadDir: dir, CacheBytes: 10, Retention: time.Hour}, Log: quiet}
	s.tasks = map[string]*Task{}
	s.lives = map[string]*live{}
	s.readers = map[string]int{}
	s.ctx = context.Background()
	p := filepath.Join(dir, "film.mkv")
	write(t, dir, "film.mkv", []byte("0123456789"))
	s.tasks["film"] = &Task{ID: "film", Local: p, Size: 10, State: StateDone, Finished: time.Now().Add(-2 * time.Hour)}
	return s
}

func TestCleanupRechecksKeepAndReaders(t *testing.T) {
	for _, action := range []string{"keep", "read"} {
		t.Run(action, func(t *testing.T) {
			s := cacheFixture(t)
			entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			s.Protected = func(string) bool {
				select {
				case <-entered:
				default:
					close(entered)
					<-resume
				}
				return false
			}
			go func() { s.cleanup(); close(done) }()
			<-entered
			if action == "keep" {
				if err := s.Keep("film"); err != nil {
					t.Fatal(err)
				}
			} else {
				s.reading("film", 1)
			}
			close(resume)
			<-done
			if _, err := os.Stat(s.tasks["film"].Local); err != nil {
				t.Fatal("copie protégée effacée", err)
			}
		})
	}
}

func TestCacheReclaimsSpaceBeforeNewCopy(t *testing.T) {
	s := cacheFixture(t)
	s.tasks["film"].Finished = time.Now()
	if err := s.checkSpace(5, false); err != nil {
		t.Fatal(err)
	}
	if s.tasks["film"].State != StateRemoved {
		t.Fatal("cache non libéré")
	}
}

func TestLocalPathReservesNamesAndPreservesPartials(t *testing.T) {
	s := cacheFixture(t)
	write(t, s.Settings.DownloadDir, "autre.mkv.part", []byte("étranger"))
	f := File{ID: "a", Path: "/autre.mkv"}
	p, err := s.localPath(f)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.localPath(File{ID: "b", Path: "/autre.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if p == q || p == filepath.Join(s.Settings.DownloadDir, "autre.mkv") {
		t.Fatal("collision", p, q)
	}
	b, _ := os.ReadFile(filepath.Join(s.Settings.DownloadDir, "autre.mkv.part"))
	if string(b) != "étranger" {
		t.Fatal("partiel écrasé")
	}
}

func TestResumeDoesNotCopyInStreamingOrFromAnotherServer(t *testing.T) {
	for _, mode := range []string{ModeStream, ModeDownload} {
		s := cacheFixture(t)
		s.Settings.Mode = mode
		s.tasks["film"].State = StateQueued
		s.resumeTasks()
		if len(s.lives) != 0 {
			t.Fatal("ancienne copie relancée")
		}
	}
}

func TestServerIdentityPreservesUsernameCase(t *testing.T) {
	a := Settings{Protocol: ProtoSFTP, Host: "example.test", User: "Alice"}
	b := a
	b.User = "alice"
	if a.FileID("/film.mkv") == b.FileID("/film.mkv") {
		t.Fatal("comptes confondus")
	}
}

func TestScrubAlsoMasksShortSecrets(t *testing.T) {
	if got := scrub(fmt.Errorf("password ab rejected"), Settings{Password: "ab"}); strings.Contains(got, "ab") {
		t.Fatal(got)
	}
}

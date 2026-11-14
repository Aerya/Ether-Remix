package xdcc

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestConcurrentStartsRespectLimit(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB", true)
	a := f.addPacket(1, bot, "A.2010.mkv", mkv(1024))
	b := f.addPacket(2, bot, "B.2010.mkv", mkv(1024))
	s := f.service(ModeStream)
	s.init()
	s.Settings.MaxActive = 1
	s.Settings.MinFree = 1
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	s.Usage = func(string) (int64, int64, error) { entered <- struct{}{}; <-release; return 1 << 40, 1 << 40, nil }
	errs := make(chan error, 2)
	for _, p := range []string{a, b} {
		go func(p string) { _, err := s.ensure(context.Background(), p, true, startOptions{}); errs <- err }(p)
	}
	<-entered
	select {
	case <-entered:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Log(err)
		}
	}
	if n := f.addurlCount(); n != 1 {
		t.Fatalf("MaxActive=1 mais %d téléchargements lancés", n)
	}
}

func TestKeepDuringCleanupProtectsFile(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 1024)
	file := f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s.tasks[p].Done = time.Now().Add(-72 * time.Hour)
	s.Protected = func(string) bool {
		if err := s.Keep(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		return false
	}
	s.clean(context.Background(), true)
	if localSize(file) < 0 {
		t.Fatal("fichier effacé malgré Keep validé avant l'appel de suppression")
	}
}

func TestCacheMakesRoomForIncomingFile(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 1024)
	f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s.Settings.CacheBytes = 1500
	q := f.addPacket(2, f.addBot(2, "LAB2", true), "B.2010.mkv", mkv(1024))
	if _, err := s.ensure(context.Background(), q, true, startOptions{}); err != nil {
		t.Fatalf("cache évictable de 1024/1500, nouveau 1024 refusé: %v", err)
	}
}

func TestReadDuringCleanupProtectsFile(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 1024)
	file := f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s.tasks[p].Done = time.Now().Add(-72 * time.Hour)
	s.Protected = func(string) bool { s.reading(p, +1); return false }
	s.clean(context.Background(), true)
	defer s.reading(p, -1)
	if localSize(file) < 0 {
		t.Fatal("fichier en lecture supprimé")
	}
}

func TestCacheCannotEvictKeptOrReadingFiles(t *testing.T) {
	for _, kind := range []string{"kept", "reading", "library"} {
		t.Run(kind, func(t *testing.T) {
			f, s, p, _ := streamFixture(t, ModeStream, 1024)
			file := f.complete(p)
			if err := s.refresh(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "kept":
				if err := s.Keep(context.Background(), p); err != nil {
					t.Fatal(err)
				}
			case "reading":
				s.reading(p, +1)
				defer s.reading(p, -1)
			case "library":
				s.Protected = func(string) bool { return true }
			}
			s.Settings.CacheBytes = 1500
			s.cleanFor(context.Background(), true, 1024)
			if localSize(file) < 0 {
				t.Fatal("fichier protégé supprimé pour libérer de la place")
			}
		})
	}
}

func TestKeepRemovedFileFails(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 1024)
	f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s.tasks[p].Done = time.Now().Add(-72 * time.Hour)
	s.clean(context.Background(), true)
	if err := s.Keep(context.Background(), p); err == nil {
		t.Fatal("Garder confirme un fichier déjà supprimé")
	}
}

func TestDelayedSavePreservesKeep(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 1024)
	f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s.DB = store.OpenTest(t)
	stale := *s.tasks[p]
	if err := s.Keep(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	s.save(context.Background(), stale)
	again := f.service(ModeStream)
	again.DB = s.DB
	again.init()
	again.load(context.Background())
	if !again.tasks[p].Keep {
		t.Fatal("ancienne sauvegarde a annulé Garder")
	}
}

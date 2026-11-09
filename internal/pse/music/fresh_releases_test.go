package music

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// artistSource : l'artiste 1 a un album récent (10) et un ancien (11).
type artistSource struct{ trackSource }

func (artistSource) Artist(_ context.Context, id int64) (Artist, []Album, error) {
	return Artist{ID: id}, []Album{
		{ID: 10, Title: "Nouveau", ReleaseDate: "2026-10-20"},
		{ID: 11, Title: "Ancien", ReleaseDate: "2019-03-01"},
	}, nil
}

func (artistSource) Album(_ context.Context, id int64) (Album, []Track, error) {
	return Album{ID: id}, []Track{{ID: id*100 + 1, Title: "A"}, {ID: id*100 + 2, Title: "B"}}, nil
}

func TestFreshReleases(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lib := NewLibrary(db)
	s := NewServer(Options{Source: artistSource{}, Library: lib})
	s.clock = func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }
	if err := lib.StarArtist(ctx, 7, Artist{ID: 1, Name: "Stromae"}, true); err != nil {
		t.Fatal(err)
	}
	// Premier appel : calcul lancé en arrière-plan, rien encore.
	if _, ok := s.freshPlaylist(ctx, 7); ok {
		t.Fatal("playlist servie avant calcul")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pl, ok := s.freshPlaylist(ctx, 7); ok {
			if len(pl.Tracks) != 2 || pl.Tracks[0].ID != 1001 {
				t.Fatalf("titres : %+v", pl.Tracks)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("calcul jamais terminé")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := s.smartByID(ctx, 7, PlaylistID(7, SourceSmart, freshRef)); !ok {
		t.Fatal("playlist introuvable par identifiant")
	}
}

func TestByYear(t *testing.T) {
	albums := []Album{{ID: 1, ReleaseDate: "1985-01-01"}, {ID: 2, ReleaseDate: "1999-05-01"}, {ID: 3, ReleaseDate: "1992-01-01"}, {ID: 4}}
	st := make([]userState, len(albums))
	got, _ := byYear(albums, st, 1990, 1999, 10, 0)
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 2 {
		t.Fatalf("années 90 : %+v", got)
	}
	got, _ = byYear(albums, st, 1999, 1980, 1, 0)
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("ordre inverse : %+v", got)
	}
}

type panicSource struct{ trackSource }

func (panicSource) Artist(context.Context, int64) (Artist, []Album, error) { panic("boum") }

// Une panne du catalogue pendant le calcul n'arrête pas le serveur.
func TestFreshReleasesSurvivesPanic(t *testing.T) {
	ctx := context.Background()
	db, _ := store.OpenMemory(ctx)
	t.Cleanup(func() { _ = db.Close() })
	lib := NewLibrary(db)
	_ = lib.StarArtist(ctx, 7, Artist{ID: 1, Name: "X"}, true)
	s := NewServer(Options{Source: panicSource{}, Library: lib})
	s.freshPlaylist(ctx, 7)
	for i := 0; i < 200; i++ {
		s.fresh.mu.Lock()
		running := s.fresh.m[7].running
		s.fresh.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("calcul resté bloqué")
}

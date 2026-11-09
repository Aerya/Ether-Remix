package releasedb

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestTypeAndKeyCounts(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s := NewStore(db)
	now := time.Now()
	// Le catalogue d'Ether n'est pas migré dans cette base de test : on
	// n'en garde que les colonnes lues.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS catalog_titles(key INTEGER PRIMARY KEY, type TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', browsable INTEGER NOT NULL DEFAULT 0, language TEXT NOT NULL DEFAULT ''); CREATE TABLE catalog_keywords(key INTEGER,keyword TEXT); CREATE TABLE catalog_genres(key INTEGER,genre TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,title,browsable) VALUES(5,'movie','caché',0)`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []int64{1, 2, 3, 4} {
		typ := "movie"
		if k == 4 {
			typ = "show"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,title,browsable) VALUES(?,?,?,1)`, k, typ, "t"); err != nil {
			t.Fatal(err)
		}
	}
	// 1 : cherché, 3 releases ; 2 : cherché, rien ; 3 : jamais cherché.
	for _, r := range []struct {
		key   core.RatingKey
		typ   core.MediaType
		found int
	}{{1, core.TypeMovie, 3}, {2, core.TypeMovie, 0}, {4, core.TypeShow, 1}} {
		if err := s.markIndexed(ctx, core.Title{Key: r.key, Type: r.typ}, r.found, "", now, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pse_playable(title_key,releases) VALUES(1,3),(4,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_keywords VALUES(1,'Anime'); INSERT INTO catalog_genres VALUES(4,'Animation'),(3,'Animation'); UPDATE catalog_titles SET language='JA' WHERE key=4`); err != nil {
		t.Fatal(err)
	}
	types, err := s.TypeCounts(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if m := types["movie"]; m != (Counts{Titles: 3, Indexed: 2, Found: 1, Playable: 1, Recent: 2}) {
		t.Fatalf("movie = %+v", m)
	}
	if sh := types["show"]; sh != (Counts{Titles: 1, Indexed: 1, Found: 1, Playable: 0, Recent: 1}) {
		t.Fatalf("show = %+v", sh)
	}
	if a := types["anime"]; a != (Counts{Titles: 2, Indexed: 2, Found: 2, Playable: 1, Recent: 2}) {
		t.Fatalf("anime subset = %+v", a)
	}
	c, err := s.KeyCounts(ctx, []core.RatingKey{1, 3, 3, 9}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Counts{Titles: 3, Indexed: 1, Found: 1, Playable: 1, Recent: 1}) {
		t.Fatalf("keys = %+v", c)
	}
}

func TestStaleAndFailingCounts(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s := NewStore(db)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	old := now.Add(-Stale - time.Hour)
	// 1 : affiché, cherché il y a longtemps ; 2 : affiché, cherché hier
	// mais une source en panne ; 3 : jamais affiché, recherche en panne.
	for _, r := range []struct {
		key    core.RatingKey
		at     time.Time
		status string
	}{{1, old, "found"}, {2, now.Add(-2 * time.Hour), "partial"}, {3, now.Add(-time.Hour), "failed"}} {
		if err := s.markIndexed(ctx, core.Title{Key: r.key, Type: core.TypeMovie}, 1, r.status, r.at, r.at.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pse_playable(title_key,releases) VALUES(1,2),(2,2)`); err != nil {
		t.Fatal(err)
	}
	c, err := s.KeyCounts(ctx, []core.RatingKey{1, 2, 3}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Playable != 2 || c.Stale != 1 || c.Failing != 2 || c.Recent != 2 {
		t.Fatalf("counts = %+v", c)
	}
}

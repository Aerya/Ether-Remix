package releasedb

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestOrderPriority(t *testing.T) {
	keys, levels := orderPriority([]PriorityKey{{Key: 1}, {Key: 2, Level: LevelHigh}, {Key: 3, Level: LevelMax}, {Key: 1, Level: LevelHigh}})
	if len(keys) != 3 || keys[0] != 3 || levels[1] != LevelHigh {
		t.Fatalf("ordre = %v, niveaux = %v", keys, levels)
	}
	// La limite garde les niveaux élevés, même placés en fin de liste.
	in := make([]PriorityKey, 0, priorityMax+1)
	for i := 1; i <= priorityMax; i++ {
		in = append(in, PriorityKey{Key: core.RatingKey(i)})
	}
	in = append(in, PriorityKey{Key: 99999, Level: LevelMax})
	keys, _ = orderPriority(in)
	if len(keys) != priorityMax || keys[0] != 99999 {
		t.Fatalf("limite : %d titres, premier %v", len(keys), keys[0])
	}
}

func TestDueForRetriesMissingTitlesEarlier(t *testing.T) {
	ctx := context.Background()
	st := NewStore(store.OpenTest(t))
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	insert := func(key core.RatingKey, found int) {
		t.Helper()
		if _, err := st.db.ExecContext(ctx, `INSERT INTO pse_index_titles(title_key,media_type,last_indexed,next_due,found,status) VALUES(?,?,?,?,?,'ok')`,
			int64(key), "movie", now.Unix(), now.Add(7*24*time.Hour).Unix(), found); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 0)
	insert(2, 3)
	cases := []struct {
		key   core.RatingKey
		level int
		want  time.Duration
	}{
		{1, LevelNormal, 7 * 24 * time.Hour},
		{1, LevelHigh, 24 * time.Hour},
		{1, LevelMax, 6 * time.Hour},
		{2, LevelMax, 7 * 24 * time.Hour}, // des releases : délai inchangé
	}
	for _, c := range cases {
		due, err := st.dueFor(ctx, c.key, c.level)
		if err != nil || !due.Equal(now.Add(c.want)) {
			t.Errorf("titre %d niveau %d : %v (%v), attendu +%v", c.key, c.level, due, err, c.want)
		}
	}
	if due, err := st.dueFor(ctx, 42, LevelMax); err != nil || !due.IsZero() {
		t.Errorf("titre jamais indexé : %v, %v", due, err)
	}
}

func TestSearchNowJumpsTheQueue(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	key := core.MovieKey(5)
	s := New(Deps{
		DB:  store.OpenTest(t),
		Now: func() time.Time { return now },
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			var items []core.Title
			for _, k := range q.Filter.IncludeKeys {
				items = append(items, core.Title{Key: k, Type: core.TypeMovie})
			}
			return core.Page{Items: items}, nil
		},
		Settings: Settings{Enabled: true, Types: []core.MediaType{core.TypeMovie}},
	})
	if err := s.st.markIndexed(ctx, core.Title{Key: key, Type: core.TypeMovie}, 3, "ok", now, now.Add(14*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SearchNow(ctx, []core.RatingKey{key, key}); err != nil || n != 1 {
		t.Fatalf("SearchNow = %d, %v", n, err)
	}
	got, ok, err := s.nextPriority(ctx, core.TypeMovie, now)
	if err != nil || !ok || got.Key != key {
		t.Fatalf("titre suivant = %v, %v, %v", got.Key, ok, err)
	}
	if due, _ := s.st.dueFor(ctx, key, LevelNormal); due.After(now) {
		t.Fatalf("échéance non remise à zéro : %v", due)
	}

	off := New(Deps{DB: store.OpenTest(t), Settings: Settings{}})
	if _, err := off.SearchNow(ctx, []core.RatingKey{key}); err != ErrIndexerOff {
		t.Fatalf("indexation désactivée : %v", err)
	}
}

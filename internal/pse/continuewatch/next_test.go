package continuewatch

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	_ "github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	_ "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

var tNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Store{DB: db, Now: func() time.Time { return tNow }}
}

func addEp(t *testing.T, db *sql.DB, show int64, season, ep int, released time.Time) core.RatingKey {
	t.Helper()
	k := core.EpisodeKey(show, season, ep)
	ts := int64(0)
	if !released.IsZero() {
		ts = released.Unix()
	}
	if _, err := db.Exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,released_ts) VALUES(?,?,?,?,?,?)`,
		int64(k), "episode", int64(core.ShowKey(show)), season, ep, ts); err != nil {
		t.Fatal(err)
	}
	return k
}

func watch(t *testing.T, db *sql.DB, uid int64, k core.RatingKey, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO state_history(user_id,rating_key,viewed_at,completed) VALUES(?,?,?,1)`, uid, int64(k), at.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at) VALUES(?,?,1,?) ON CONFLICT(user_id,rating_key) DO UPDATE SET view_count=1`, uid, int64(k), at.UnixNano()); err != nil {
		t.Fatal(err)
	}
}

func row(t *testing.T, s *Store, uid int64, show int64) (Row, bool) {
	t.Helper()
	var r Row
	var last, lastAt, next, rel, rank int64
	err := s.DB.QueryRow(`SELECT last_key,last_watched_at,next_key,next_released_ts,rank_ts FROM pse_continue_next WHERE user_id=? AND show_key=?`, uid, int64(core.ShowKey(show))).
		Scan(&last, &lastAt, &next, &rel, &rank)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	r.LastKey, r.NextKey = core.RatingKey(last), core.RatingKey(next)
	r.RankAt = time.Unix(0, rank)
	return r, true
}

func day(n int) time.Time { return tNow.Add(-time.Duration(n) * 24 * time.Hour) }

func TestComputeNextEpisode(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	e11 := addEp(t, s.DB, 10, 1, 1, day(90))
	e12 := addEp(t, s.DB, 10, 1, 2, day(80))
	e13 := addEp(t, s.DB, 10, 1, 3, tNow.Add(48*time.Hour)) // pas encore sorti
	addEp(t, s.DB, 10, 0, 1, day(100))                      // saison 0 ignorée
	watch(t, s.DB, 1, e11, day(5))

	if err := s.Compute(ctx, 1, core.ShowKey(10)); err != nil {
		t.Fatal(err)
	}
	if r, ok := row(t, s, 1, 10); !ok || r.NextKey != e12 || r.LastKey != e11 {
		t.Fatalf("après 1x1 : %+v %v", r, ok)
	}
	// 1x2 terminé : 1x3 n'est pas sorti, la série est à jour.
	watch(t, s.DB, 1, e12, day(1))
	_ = s.Compute(ctx, 1, core.ShowKey(10))
	if r, _ := row(t, s, 1, 10); r.NextKey != 0 || r.LastKey != e12 {
		t.Fatalf("à jour attendu : %+v", r)
	}
	// 1x3 sort, puis une nouvelle saison arrive : la série revient.
	s.DB.Exec(`UPDATE catalog_titles SET released_ts=? WHERE key=?`, day(0).Unix(), int64(e13))
	_ = s.Compute(ctx, 1, core.ShowKey(10))
	if r, _ := row(t, s, 1, 10); r.NextKey != e13 {
		t.Fatalf("1x3 attendu : %+v", r)
	}
	watch(t, s.DB, 1, e13, day(1))
	e21 := addEp(t, s.DB, 10, 2, 1, tNow.Add(-time.Hour))
	_ = s.Compute(ctx, 1, core.ShowKey(10))
	r, _ := row(t, s, 1, 10)
	if r.NextKey != e21 {
		t.Fatalf("2x1 attendu : %+v", r)
	}
	if !r.RankAt.Equal(tNow.Add(-time.Hour).Truncate(time.Second)) {
		t.Fatalf("rang = max(visionnage, sortie) : %v", r.RankAt)
	}
	// Un épisode vu n'est jamais proposé.
	s.DB.Exec(`INSERT INTO state_watch(user_id,rating_key,view_count) VALUES(1,?,1)`, int64(e21))
	_ = s.Compute(ctx, 1, core.ShowKey(10))
	if r, _ := row(t, s, 1, 10); r.NextKey != 0 {
		t.Fatalf("2x1 vu : %+v", r)
	}
}

func TestIncrementalFromWatermark(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	addUser(t, s.DB, 2)
	a1 := addEp(t, s.DB, 20, 1, 1, day(30))
	a2 := addEp(t, s.DB, 20, 1, 2, day(30))
	b1 := addEp(t, s.DB, 21, 1, 1, day(30))
	b2 := addEp(t, s.DB, 21, 1, 2, day(30))
	watch(t, s.DB, 1, b1, day(9)) // avant le filigrane : couvert par le remplissage
	if n, err := s.Incremental(ctx); err != nil || n != 0 {
		t.Fatalf("premier passage : %d %v", n, err)
	}
	if _, ok := row(t, s, 1, 21); ok {
		t.Fatal("l'historique d'avant le filigrane ne se calcule pas ici")
	}
	watch(t, s.DB, 1, a1, day(1))
	watch(t, s.DB, 2, a1, day(1))
	if n, err := s.Incremental(ctx); err != nil || n != 2 {
		t.Fatalf("incrémental : %d %v", n, err)
	}
	if r, ok := row(t, s, 1, 20); !ok || r.NextKey != a2 {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := row(t, s, 2, 20); !ok {
		t.Fatal("second spectateur absent")
	}
	if n, _ := s.Incremental(ctx); n != 0 {
		t.Fatalf("rien de neuf : %d", n)
	}
	// Le remplissage couvre l'ancien historique.
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	if r, ok := row(t, s, 1, 21); !ok || r.NextKey != b2 {
		t.Fatalf("remplissage : %+v %v", r, ok)
	}
}

func TestNewEpisodesRecomputesUpToDateShows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	e1 := addEp(t, s.DB, 30, 1, 1, day(60))
	watch(t, s.DB, 1, e1, day(2))
	if _, err := s.Incremental(ctx); err != nil { // fixe le filigrane
		t.Fatal(err)
	}
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := row(t, s, 1, 30); r.NextKey != 0 {
		t.Fatalf("à jour attendu : %+v", r)
	}
	e2 := addEp(t, s.DB, 30, 2, 1, tNow.Add(-time.Minute))
	if n, err := s.NewEpisodes(ctx); err != nil || n != 1 {
		t.Fatalf("nouveaux épisodes : %d %v", n, err)
	}
	if r, _ := row(t, s, 1, 30); r.NextKey != e2 {
		t.Fatalf("nouvelle saison attendue : %+v", r)
	}
}

func TestNextReadsAllShowsSortedAndFallsBackWhenEmpty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	addUser(t, s.DB, 2)
	if rows, ok, err := s.Next(ctx, 1); err != nil || ok || rows != nil {
		t.Fatalf("table vide : %v %v %v", rows, ok, err)
	}
	for i := int64(0); i < 250; i++ {
		a := addEp(t, s.DB, 1000+i, 1, 1, day(400))
		addEp(t, s.DB, 1000+i, 1, 2, day(400))
		watch(t, s.DB, 1, a, tNow.Add(-time.Duration(i)*time.Hour))
	}
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	rows, ok, err := s.Next(ctx, 1)
	if err != nil || !ok || len(rows) != 250 {
		t.Fatalf("lecture : %d %v %v", len(rows), ok, err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].RankAt.After(rows[i-1].RankAt) {
			t.Fatalf("pas triées à %d", i)
		}
	}
	if rows[0].ShowKey != core.ShowKey(1000) {
		t.Fatalf("la plus récente d'abord : %v", rows[0].ShowKey)
	}
	// Un compte sans épisode terminé n'est pas prêt : l'ancien calcul s'applique.
	if _, ok, _ := s.Next(ctx, 2); ok {
		t.Fatal("compte sans historique marqué rempli")
	}
}

func addUser(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO auth_users(id, plex_id, created_at, updated_at) VALUES(?,?,0,0)`, id, id+9000); err != nil {
		t.Fatal(err)
	}
}

// Un visionnage fait avant le remplissage mais après la pose du filigrane est rattrapé.
func TestRunSetsWatermarkBeforeFill(t *testing.T) {
	s := newStore(t)
	addUser(t, s.DB, 1)
	addEp(t, s.DB, 40, 1, 1, day(30))
	b := addEp(t, s.DB, 40, 1, 2, day(30))
	if _, _, err := s.loadState(context.Background()); err != nil { // ce que Run fait en premier
		t.Fatal(err)
	}
	watch(t, s.DB, 1, core.EpisodeKey(40, 1, 1), day(1)) // pendant le remplissage
	if n, err := s.Incremental(context.Background()); err != nil || n != 1 {
		t.Fatalf("visionnage perdu : %d %v", n, err)
	}
	if r, _ := row(t, s, 1, 40); r.NextKey != b {
		t.Fatalf("%+v", r)
	}
}

// Une fois tous les comptes remplis, Fill ne touche plus state_history.
func TestFillSkipsHistoryWhenAllFilled(t *testing.T) {
	s := newStore(t)
	addUser(t, s.DB, 1)
	watch(t, s.DB, 1, addEp(t, s.DB, 70, 1, 1, day(30)), day(1))
	if err := s.Fill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE state_history`); err != nil {
		t.Fatal(err)
	}
	if err := s.Fill(context.Background()); err != nil {
		t.Fatalf("Fill lit state_history : %v", err)
	}
	if _, ok, _ := s.Next(context.Background(), 1); !ok {
		t.Fatal("compte rempli attendu")
	}
}

// La saison suivante d'une série encore diffusée est chargée en arrière-plan.
func TestNewEpisodesRefreshesAiringShows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	e1 := addEp(t, s.DB, 50, 1, 1, day(60))
	s.DB.Exec(`INSERT INTO catalog_titles(key,type,status) VALUES(?,?,?)`, int64(core.ShowKey(50)), "show", "Returning Series")
	watch(t, s.DB, 1, e1, day(2))
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	var asked []core.RatingKey
	var e2 core.RatingKey
	s.Refresh = func(_ context.Context, keys []core.RatingKey) {
		asked = keys
		e2 = addEp(t, s.DB, 50, 2, 1, day(100)) // sortie ancienne : seule l'hydratation la révèle
	}
	if _, err := s.NewEpisodes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != e1 {
		t.Fatalf("hydratation demandée : %v", asked)
	}
	if r, _ := row(t, s, 1, 50); r.NextKey != e2 {
		t.Fatalf("nouvelle saison attendue : %+v", r)
	}
}

func TestComputeAcceptsUndatedEpisode(t *testing.T) {
	s := newStore(t)
	e1 := addEp(t, s.DB, 60, 1, 1, day(60))
	e2 := addEp(t, s.DB, 60, 1, 2, time.Time{})
	watch(t, s.DB, 1, e1, day(1))
	_ = s.Compute(context.Background(), 1, core.ShowKey(60))
	if r, _ := row(t, s, 1, 60); r.NextKey != e2 {
		t.Fatalf("épisode sans date attendu : %+v", r)
	}
}

func TestFillLeavesViewerWithoutHistoryUnfilled(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Next(ctx, 1); ok {
		t.Fatal("sans historique : repli attendu")
	}
	watch(t, s.DB, 1, addEp(t, s.DB, 71, 1, 1, day(30)), day(1))
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Next(ctx, 1); !ok {
		t.Fatal("rempli après le premier visionnage")
	}
}

// Rattrapage : une série terminée dont le catalogue ne contient que l'épisode
// vu est complétée une fois, puis n'est plus redemandée ; rien pendant une lecture.
func TestCompleteLoadsMissingEpisodesOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	e1 := addEp(t, s.DB, 60, 1, 1, day(400))
	s.DB.Exec(`INSERT INTO catalog_titles(key,type,status) VALUES(?,?,?)`, int64(core.ShowKey(60)), "show", "Ended")
	watch(t, s.DB, 1, e1, day(200))
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.loadState(ctx); err != nil { // filigrane posé, comme Run
		t.Fatal(err)
	}
	busy, calls := true, 0
	var e2 core.RatingKey
	s.Busy = func() bool { return busy }
	s.Refresh = func(_ context.Context, keys []core.RatingKey) {
		calls++
		if e2 == 0 {
			e2 = addEp(t, s.DB, 60, 1, 2, day(399))
		}
	}
	if _, err := s.Complete(ctx); err != nil || calls != 0 {
		t.Fatalf("rattrapage pendant une lecture : %d appels, %v", calls, err)
	}
	busy = false
	if _, err := s.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := row(t, s, 1, 60); calls != 1 || r.NextKey != e2 {
		t.Fatalf("épisode suivant attendu après rattrapage : %d appels, %+v", calls, r)
	}
	// Une série vue jusqu'au bout reste sans suivant : marquée, elle n'est pas redemandée.
	watch(t, s.DB, 1, e2, day(1))
	if _, err := s.Incremental(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := row(t, s, 1, 60); r.NextKey != 0 {
		t.Fatalf("série vue jusqu'au bout : %+v", r)
	}
	if _, err := s.Complete(ctx); err != nil || calls != 1 {
		t.Fatalf("série déjà complétée redemandée : %d appels, %v", calls, err)
	}
	// Revérifiée après 6 h, même terminée : un épisode peut sortir entre-temps.
	s.Now = func() time.Time { return tNow.Add(7 * time.Hour) }
	if _, err := s.Complete(ctx); err != nil || calls != 2 {
		t.Fatalf("revérification après 6 h attendue : %d appels, %v", calls, err)
	}
}

// Une série en production (statut vide ou autre que terminée/annulée) est
// revérifiée à chaque passe des nouveaux épisodes, comme une série diffusée.
func TestNewEpisodesRefreshesShowsInProduction(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	addUser(t, s.DB, 1)
	e1 := addEp(t, s.DB, 70, 1, 1, day(60))
	s.DB.Exec(`INSERT INTO catalog_titles(key,type,status) VALUES(?,?,?)`, int64(core.ShowKey(70)), "show", "In Production")
	watch(t, s.DB, 1, e1, day(2))
	if err := s.Fill(ctx); err != nil {
		t.Fatal(err)
	}
	asked := 0
	s.Refresh = func(context.Context, []core.RatingKey) { asked++ }
	if _, err := s.NewEpisodes(ctx); err != nil || asked != 1 {
		t.Fatalf("série en production non revérifiée : %d, %v", asked, err)
	}
}

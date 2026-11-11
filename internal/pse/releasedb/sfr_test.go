package releasedb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type sfrFake struct {
	rows      []sfrRow
	since     []int64
	asked     [][]int64
	titlesErr error
	onTitles  func()
}

func (f *sfrFake) close() {}
func (f *sfrFake) page(_ context.Context, after string, since int64, _ bool, limit int) ([]sfrRow, error) {
	f.since = append(f.since, since)
	var out []sfrRow
	for _, r := range f.rows {
		if r.ID > after && r.Stamp >= since && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *sfrFake) titles(_ context.Context, ids []int64, after string, _ bool, limit int) ([]sfrRow, error) {
	f.asked = append(f.asked, ids)
	if f.onTitles != nil {
		f.onTitles()
	}
	if f.titlesErr != nil {
		return nil, f.titlesErr
	}
	var out []sfrRow
	for _, r := range f.rows {
		if r.ID > after && slices.Contains(ids, r.TMDB) && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *sfrFake) wasAsked(id int64) bool {
	return slices.ContainsFunc(f.asked, func(ids []int64) bool { return slices.Contains(ids, id) })
}

func sfrCatalog(t *testing.T, e *freshEnv, typ string, keys ...core.RatingKey) {
	t.Helper()
	if _, err := e.s.st.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_titles(key INTEGER PRIMARY KEY,type TEXT,tmdb_id INTEGER,popularity REAL NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, err := e.s.st.db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id) VALUES(?,?,?)`, int64(k), typ, int64(k)%sfrTestKeyBase); err != nil {
			t.Fatal(err)
		}
	}
}

// Base qui retrouve l'identifiant TMDB d'une clé de test.
const sfrTestKeyBase = 1_000_000

func sfrUse(e *freshEnv, fake *sfrFake) SFRSettings {
	e.s.openSFR = func(context.Context, string) (sfrReader, error) { return fake, nil }
	return SFRSettings{Enabled: true, URL: "postgres://x"}
}

func TestSFRFirstEpisode(t *testing.T) {
	cases := []struct {
		s, e string
		want bool
	}{{"[1]", "[1]", true}, {"[1]", "[2]", false}, {"[1]", "", true}, {"[1,2]", "[]", true}, {"[2]", "[1]", false}, {"", "", false}}
	for _, c := range cases {
		if got := sfrFirstEpisode(c.s, c.e); got != c.want {
			t.Errorf("%q %q : %v", c.s, c.e, got)
		}
	}
}

func TestSFRCycle(t *testing.T) {
	e := newFreshEnv(t)
	movie := core.MovieKey(42)
	sfrCatalog(t, e, "movie", movie)
	fake := &sfrFake{rows: []sfrRow{
		{ID: "a", Name: "Film.2020.MULTi.VFF.1080p.WEB.x264-GRP", Hash: "ABC", Media: "movie", TMDB: 42, Stamp: 1000000},
		{ID: "b", Name: "Inconnu.2020.FRENCH.1080p", Hash: "DEF", Media: "movie", TMDB: 7, Stamp: 2000000},
	}}
	e.s.openSFR = func(context.Context, string) (sfrReader, error) { return fake, nil }
	cfg := SFRSettings{Enabled: true, URL: "postgres://x"}
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	rels, err := e.s.st.Releases(e.ctx, movie, Filter{})
	if err != nil || len(rels) != 1 {
		t.Fatalf("releases %v %v", rels, err)
	}
	if rels[0].SourceID != SFRSourceID || len(rels[0].DebridCached) != 0 {
		t.Fatalf("release %+v", rels[0])
	}
	if st := e.s.st.LoadSFRStatus(e.ctx); st.State != "ok" || st.Releases != 1 || st.Titles != 1 {
		t.Fatalf("statut %+v", st)
	}
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := fake.since[len(fake.since)-1]; got != 2000000-sfrOverlap {
		t.Fatalf("depuis %d", got)
	}
}

func TestSFRCatchesUpLateTitle(t *testing.T) {
	e := newFreshEnv(t)
	early, late := core.MovieKey(42), core.MovieKey(7)
	sfrCatalog(t, e, "movie", early)
	fake := &sfrFake{rows: []sfrRow{
		{ID: "a", Name: "Tardif.2020.MULTi.VFF.1080p.WEB.x264-GRP", Hash: "DEF", Media: "movie", TMDB: 7, Stamp: 1000000},
		{ID: "b", Name: "Film.2020.MULTi.VFF.1080p.WEB.x264-GRP", Hash: "ABC", Media: "movie", TMDB: 42, Stamp: 2000000},
	}}
	cfg := sfrUse(e, fake)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if rels, err := e.s.st.Releases(e.ctx, late, Filter{}); err != nil || len(rels) != 0 {
		t.Fatalf("avant l'arrivée au catalogue : %v %v", rels, err)
	}
	sfrCatalog(t, e, "movie", late)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if got := fake.since[len(fake.since)-1]; got <= 1000000 {
		t.Fatalf("la lecture incrémentale couvre la release : depuis %d", got)
	}
	rels, err := e.s.st.Releases(e.ctx, late, Filter{})
	if err != nil || len(rels) != 1 || rels[0].SourceID != SFRSourceID {
		t.Fatalf("releases %v %v", rels, err)
	}
	if n := e.playable(t, late); n != 1 {
		t.Fatalf("lisible : %d", n)
	}
	if st := e.s.st.LoadSFRStatus(e.ctx); st.State != "ok" || st.Releases != 2 || st.Titles != 2 {
		t.Fatalf("statut %+v", st)
	}
}

func TestSFRCheckedTitleWaitsForRecheck(t *testing.T) {
	e := newFreshEnv(t)
	sfrCatalog(t, e, "movie", core.MovieKey(7))
	fake := &sfrFake{}
	cfg := sfrUse(e, fake)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 1 || !fake.wasAsked(7) {
		t.Fatalf("premier passage : %v", fake.asked)
	}
	e.now = e.now.Add(sfrTitlesRecheck - time.Hour)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 1 {
		t.Fatalf("redemandé trop tôt : %v", fake.asked)
	}
	e.now = e.now.Add(2 * time.Hour)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 2 {
		t.Fatalf("nouvelle vérification absente : %v", fake.asked)
	}
}

func TestSFRCatchUpKeepsFirstEpisodeOnly(t *testing.T) {
	e := newFreshEnv(t)
	show := core.ShowKey(9)
	sfrCatalog(t, e, "show", show)
	fake := &sfrFake{rows: []sfrRow{
		{ID: "a", Name: "Serie.S02E03.MULTi.1080p.WEB.x264-GRP", Hash: "AAA", Media: "tv", TMDB: 9, Stamp: 1, Seasons: "[2]", Episodes: "[3]"},
		{ID: "b", Name: "Serie.S01E01.MULTi.1080p.WEB.x264-GRP", Hash: "BBB", Media: "tv", TMDB: 9, Stamp: 1, Seasons: "[1]", Episodes: "[1]"},
		{ID: "c", Name: "Serie.S01.MULTi.1080p.WEB.x264-GRP", Hash: "CCC", Media: "tv", TMDB: 9, Stamp: 1, Seasons: "[1]"},
		{ID: "d", Name: "Serie.S01E02.MULTi.1080p.WEB.x264-GRP", Hash: "DDD", Media: "tv", TMDB: 9, Stamp: 1, Seasons: "[1]", Episodes: "[2]"},
	}}
	// Le passage incrémental ne voit rien : seul le rattrapage importe.
	if err := e.s.sfrCatchUp(e.ctx, fake, SFRSettings{}); err != nil {
		t.Fatal(err)
	}
	rels, err := e.s.st.Releases(e.ctx, show, Filter{})
	if err != nil || len(rels) != 2 {
		t.Fatalf("releases %v %v", rels, err)
	}
	for _, r := range rels {
		if r.Season != 1 || r.Episode != 1 {
			t.Fatalf("release %+v", r)
		}
	}
}

func TestSFRCatchUpReadsEveryPage(t *testing.T) {
	e := newFreshEnv(t)
	show := core.ShowKey(9)
	sfrCatalog(t, e, "show", show)
	fake := &sfrFake{}
	// Les épisodes écartés remplissent la première page sans écriture.
	for i := range sfrTitlesRows {
		fake.rows = append(fake.rows, sfrRow{ID: fmt.Sprintf("a%06d", i), Name: "Serie.S02E03", Hash: "X", Media: "tv", TMDB: 9, Seasons: "[2]", Episodes: "[3]"})
	}
	fake.rows = append(fake.rows, sfrRow{ID: "b", Name: "Serie.S01E01.MULTi.1080p.WEB.x264-GRP", Hash: "BBB", Media: "tv", TMDB: 9, Seasons: "[1]", Episodes: "[1]"})
	if err := e.s.sfrCatchUp(e.ctx, fake, SFRSettings{}); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 2 {
		t.Fatalf("pages lues : %d", len(fake.asked))
	}
	if rels, err := e.s.st.Releases(e.ctx, show, Filter{}); err != nil || len(rels) != 1 {
		t.Fatalf("releases %v %v", rels, err)
	}
}

func sfrManyTitles(t *testing.T, e *freshEnv, n int) {
	t.Helper()
	keys := make([]core.RatingKey, n)
	for i := range keys {
		keys[i] = core.MovieKey(int64(i + 1))
	}
	sfrCatalog(t, e, "movie", keys...)
}

func TestSFRCatchUpBatchesAreBounded(t *testing.T) {
	e := newFreshEnv(t)
	sfrManyTitles(t, e, sfrTitlesBatch+1)
	fake := &sfrFake{}
	if err := e.s.sfrCatchUp(e.ctx, fake, SFRSettings{}); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 2 || len(fake.asked[0]) != sfrTitlesBatch || len(fake.asked[1]) != 1 {
		t.Fatalf("lots : %d", len(fake.asked))
	}
}

func TestSFRCatchUpStopsAtTimeBudget(t *testing.T) {
	e := newFreshEnv(t)
	sfrManyTitles(t, e, 3*sfrTitlesBatch)
	fake := &sfrFake{onTitles: func() { e.now = e.now.Add(sfrTitlesBudget) }}
	if err := e.s.sfrCatchUp(e.ctx, fake, SFRSettings{}); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 1 {
		t.Fatalf("requêtes : %d", len(fake.asked))
	}
}

func TestSFRCatchUpWaitsForPlayback(t *testing.T) {
	e := newFreshEnv(t)
	sfrCatalog(t, e, "movie", core.MovieKey(7))
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.s.d.Active = func() bool { cancel(); return true }
	fake := &sfrFake{}
	if err := e.s.sfrCatchUp(ctx, fake, SFRSettings{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur %v", err)
	}
	if len(fake.asked) != 0 {
		t.Fatalf("lecture pendant une lecture en cours : %v", fake.asked)
	}
}

func TestSFRCatchUpFailureKeepsCycleAndRetries(t *testing.T) {
	e := newFreshEnv(t)
	sfrCatalog(t, e, "movie", core.MovieKey(7))
	fake := &sfrFake{titlesErr: errString("panne")}
	cfg := sfrUse(e, fake)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if st := e.s.st.LoadSFRStatus(e.ctx); st.State != "ok" {
		t.Fatalf("statut %+v", st)
	}
	fake.titlesErr = nil
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 2 {
		t.Fatalf("le lot en échec n'a pas été repris : %v", fake.asked)
	}
}

func TestSFRForgetClearsChecks(t *testing.T) {
	e := newFreshEnv(t)
	sfrCatalog(t, e, "movie", core.MovieKey(7))
	fake := &sfrFake{}
	cfg := sfrUse(e, fake)
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Private = true
	if err := e.s.sfrCycle(e.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.asked) != 2 {
		t.Fatalf("vérifications conservées : %v", fake.asked)
	}
}

func TestSFRErrorHidesDetails(t *testing.T) {
	err := errString("connexion impossible: postgres://u:secret@h")
	if got := sfrPublicError(err); got != "connexion impossible" {
		t.Fatal(got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

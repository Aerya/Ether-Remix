package releasedb

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type sfrFake struct {
	rows  []sfrRow
	since []int64
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
	if _, err := e.s.st.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_titles(key INTEGER PRIMARY KEY,type TEXT,tmdb_id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.st.db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id) VALUES(?,?,?)`, int64(movie), "movie", 42); err != nil {
		t.Fatal(err)
	}
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
	if rels[0].SourceID != SFRSourceID || len(rels[0].DebridCached) != 1 || rels[0].DebridCached[0] != "alldebrid" {
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

func TestSFRErrorHidesDetails(t *testing.T) {
	err := errString("connexion impossible: postgres://u:secret@h")
	if got := sfrPublicError(err); got != "connexion impossible" {
		t.Fatal(got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

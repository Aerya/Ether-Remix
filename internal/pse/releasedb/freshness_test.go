package releasedb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// searchStub renvoie, pour chaque source, la réponse programmée.
type searchStub struct {
	rels map[string][]core.Release
	errs map[string]error
}

func (st *searchStub) search(ctx context.Context, _ core.SearchRequest) ([]core.Release, error) {
	// budgetContext masque toutes les sources sauf celle interrogée.
	for _, id := range []string{"a", "b"} {
		if !core.IndexerSkipped(ctx, id) {
			return st.rels[id], st.errs[id]
		}
	}
	return nil, nil
}

func frRelease(name string) core.Release {
	return core.Release{ID: name, Name: name, Protocol: core.ProtocolHTTP, Info: core.ReleaseInfo{Height: 1080, Languages: []string{"french"}}}
}

type freshEnv struct {
	ctx  context.Context
	s    *Service
	stub *searchStub
	now  time.Time
	cfg  config.Config
}

func newFreshEnv(t *testing.T) *freshEnv {
	t.Helper()
	e := &freshEnv{ctx: context.Background(), stub: &searchStub{rels: map[string][]core.Release{}, errs: map[string]error{}},
		now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	e.cfg = config.Config{Indexers: []config.Indexer{
		{ID: "a", Name: "A", Kind: config.IndexerKindStremio, Enabled: true},
		{ID: "b", Name: "B", Kind: config.IndexerKindStremio, Enabled: true},
	}}
	e.s = New(Deps{
		DB:       store.OpenTest(t),
		Config:   func() config.Config { return e.cfg },
		Search:   e.stub.search,
		Now:      func() time.Time { return e.now },
		Settings: Settings{Enabled: true, Daily: 100, Pace: time.Second, Types: []core.MediaType{core.TypeMovie}},
	})
	t.Cleanup(OverridePlayable(NewPlayable(1, "lang=fr", false)))
	t.Cleanup(func() { activeSources.Store(nil) })
	return e
}

func (e *freshEnv) index(t *testing.T, key core.RatingKey) {
	t.Helper()
	title := core.Title{Key: key, Type: core.TypeMovie, Title: "Film", IDs: core.IDs{IMDB: "tt1"}}
	if err := e.s.IndexTitle(e.ctx, title, e.s.sources()); err != nil {
		t.Fatal(err)
	}
}

func (e *freshEnv) playable(t *testing.T, key core.RatingKey) int {
	t.Helper()
	var n int
	if err := e.s.Store().db.QueryRowContext(e.ctx, `SELECT releases FROM pse_playable WHERE title_key=?`, int64(key)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *freshEnv) names(t *testing.T, key core.RatingKey) map[string]string {
	t.Helper()
	rs, err := e.s.Store().Releases(e.ctx, key, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rs {
		out[r.Name] = r.SourceID
	}
	return out
}

func TestReleasesDisappearAfterSuccessfulSearch(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A1"), frRelease("Film.FRENCH.1080p-A2")}
	e.stub.rels["b"] = []core.Release{frRelease("Film.FRENCH.1080p-B1")}
	e.index(t, 1)
	if n := e.playable(t, 1); n != 3 {
		t.Fatalf("lisibles = %d, 3 attendues", n)
	}

	// Plus tard, A répond sans erreur avec une seule release, B est en panne.
	e.now = e.now.Add(15 * 24 * time.Hour)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A2")}
	e.stub.rels["b"] = nil
	e.stub.errs["b"] = errors.New("addon injoignable")
	e.index(t, 1)
	got := e.names(t, 1)
	if _, ok := got["Film.FRENCH.1080p-A1"]; ok {
		t.Fatalf("A1 a disparu des résultats de A : elle doit être effacée (%v)", got)
	}
	if got["Film.FRENCH.1080p-B1"] != "b" {
		t.Fatalf("B en panne : sa release doit être conservée (%v)", got)
	}
	if n := e.playable(t, 1); n != 2 {
		t.Fatalf("lisibles = %d, 2 attendues (A2 et B1)", n)
	}

	// A répond avec une liste vide, sans erreur : plus rien de A.
	e.stub.rels["a"] = nil
	e.index(t, 1)
	if got := e.names(t, 1); len(got) != 1 || got["Film.FRENCH.1080p-B1"] != "b" {
		t.Fatalf("après réponse vide de A : %v", got)
	}
}

func TestPartialSearchKeepsReleases(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A1"), frRelease("Film.FRENCH.1080p-A2")}
	e.index(t, 1)
	e.now = e.now.Add(time.Hour)
	// Erreur avec un seul résultat : la recherche est partielle.
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A2")}
	e.stub.errs["a"] = errors.New("délai dépassé sur une partie des sources")
	e.index(t, 1)
	if got := e.names(t, 1); len(got) != 2 {
		t.Fatalf("recherche partielle : rien ne doit être effacé (%v)", got)
	}
	var status string
	_ = e.s.Store().db.QueryRowContext(e.ctx, `SELECT status FROM pse_index_titles WHERE title_key=1`).Scan(&status)
	if status != "partial" {
		t.Fatalf("statut = %q, partial attendu", status)
	}
}

func TestReplaceIsolatesEpisodesAndSources(t *testing.T) {
	ctx := context.Background()
	st := NewStore(store.OpenTest(t))
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	show := core.Title{Key: 7, Type: core.TypeShow}
	rel := func(src string, season, episode int, name string) Release {
		return FromCore(show, season, episode, src, frRelease(name), nil)
	}
	if err := st.Save(ctx, []Release{
		rel("a", 1, 1, "S01E01-A"), rel("a", 1, 2, "S01E02-A"), rel("b", 1, 1, "S01E01-B"),
	}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, err := st.Replace(ctx, Scope{SourceID: "a", TitleKey: 7, Season: 1, Episode: 1}, nil, now)
	if err != nil || n != 1 {
		t.Fatalf("Replace = %d, %v ; seule S01E01-A doit être effacée", n, err)
	}
	rs, _ := st.Releases(ctx, 7, Filter{})
	left := map[string]bool{}
	for _, r := range rs {
		left[r.Name] = true
	}
	if len(left) != 2 || !left["S01E02-A"] || !left["S01E01-B"] {
		t.Fatalf("restent %v", left)
	}
}

func TestExpiryAndDisabledSources(t *testing.T) {
	e := newFreshEnv(t)
	e.stub.rels["a"] = []core.Release{frRelease("Film.FRENCH.1080p-A1")}
	e.stub.rels["b"] = []core.Release{frRelease("Film.FRENCH.1080p-B1")}
	e.index(t, 1)
	if n := e.playable(t, 1); n != 2 {
		t.Fatalf("lisibles = %d", n)
	}

	// B est désactivée dans Ether : ses releases ne comptent plus, mais
	// restent en base au cas où elle revient.
	e.cfg.Indexers[1].Enabled = false
	e.s.maintain(e.ctx)
	if n := e.playable(t, 1); n != 1 {
		t.Fatalf("source désactivée : lisibles = %d, 1 attendue", n)
	}
	if got := e.names(t, 1); got["Film.FRENCH.1080p-B1"] != "b" {
		t.Fatalf("la release de B doit rester en base : %v", got)
	}
	e.cfg.Indexers[1].Enabled = true
	e.s.maintain(e.ctx)
	if n := e.playable(t, 1); n != 2 {
		t.Fatalf("source réactivée : lisibles = %d, 2 attendues", n)
	}

	// A tombe en panne pendant plus de MaxAge : son observation expire,
	// B (revue entre-temps) reste.
	e.stub.rels["a"] = nil
	e.stub.errs["a"] = errors.New("panne")
	e.now = e.now.Add(MaxAge - 24*time.Hour)
	e.index(t, 1) // B revue, A en panne : A1 conservée
	if got := e.names(t, 1); len(got) != 2 {
		t.Fatalf("panne de A : %v", got)
	}
	e.now = e.now.Add(48 * time.Hour)
	e.s.maintain(e.ctx)
	got := e.names(t, 1)
	if _, ok := got["Film.FRENCH.1080p-A1"]; ok || len(got) != 1 {
		t.Fatalf("A1 non revue depuis plus de MaxAge doit expirer : %v", got)
	}
	if n := e.playable(t, 1); n != 1 {
		t.Fatalf("après expiration : lisibles = %d, 1 attendue", n)
	}
}

func TestOldObservationNoLongerCounts(t *testing.T) {
	// Une release vue il y a un an ne compte plus, même avant la purge.
	ctx := context.Background()
	st := NewStore(store.OpenTest(t))
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	film := core.Title{Key: 3, Type: core.TypeMovie}
	if err := st.Save(ctx, []Release{FromCore(film, 0, 0, "a", frRelease("Old.FRENCH.1080p"), nil)}, now.Add(-365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshPlayable(ctx, 3, NewPlayable(1, "lang=fr", false)); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.db.QueryRowContext(ctx, `SELECT releases FROM pse_playable WHERE title_key=3`).Scan(&n)
	if n != 0 {
		t.Fatalf("release vieille d'un an comptée : %d", n)
	}
}

// Une liste vide persistée signifie zéro source, pas une liste inconnue.
func TestMaintenanceEmptySourcesAndReactivation(t *testing.T) {
	for _, initiallyEmpty := range []bool{false, true} {
		t.Run(map[bool]string{false: "reactivation", true: "first-maintenance-empty"}[initiallyEmpty], func(t *testing.T) {
			e := newFreshEnv(t)
			e.stub.rels["a"] = []core.Release{frRelease("Film VF")}
			e.index(t, 1)
			if !initiallyEmpty {
				e.s.maintain(e.ctx)
			}
			e.cfg.Indexers = nil
			e.s.maintain(e.ctx)
			if got := e.playable(t, 1); got != 0 {
				t.Fatalf("sans source active : %d, attendu 0", got)
			}
			e.cfg.Indexers = []config.Indexer{{ID: "a", Enabled: true}}
			e.s.maintain(e.ctx)
			if got := e.playable(t, 1); got != 1 {
				t.Fatalf("source réactivée : %d, attendu 1", got)
			}
		})
	}
}

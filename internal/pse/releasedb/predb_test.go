package releasedb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPreAccept(t *testing.T) {
	all := []string{"fr", "multi", "vostfr"}
	cases := []struct {
		name, section string
		langs         []string
		want          bool
	}{
		{"Un.Film.2024.FRENCH.1080p.WEB.H264-GRP", "", all, true},
		{"Un.Film.2024.MULTi.VFF.2160p.WEB.H265-GRP", "", all, true},
		{"Un.Film.2024.VOSTFR.1080p.WEB.H264-GRP", "", all, true},
		{"Un.Film.2024.SUBFRENCH.1080p.BluRay.x264-GRP", "", all, true},
		{"Une.Serie.S01E02.FRENCH.720p.HDTV.x264-GRP", "TV-HD", all, true},
		{"Un.Film.2024.GERMAN.1080p.WEB.H264-GRP", "", all, false},
		{"Un.Film.2024.SPANiSH.MULTi.1080p.WEB.H264-GRP", "", all, false},
		{"Un.Film.2024.FiX.MULTi.1080p.WEB.H264-GRP", "", all, false},
		{"Un.Film.2024.DIRFIX.FRENCH.1080p.WEB-GRP", "", all, false},
		{"Un.Film.2024.FRENCH.1080p.WEB.H264-GRP", "", []string{"multi"}, false},
		{"Artiste-Album-FR-2024-GRP", "MP3", all, false},
		{"Un.Jeu.FRENCH.2024-GRP", "GAMES", all, false},
		{"Sans.Annee.FRENCH.1080p.WEB-GRP", "", all, false},
	}
	for _, c := range cases {
		if _, ok := PreAccept(c.name, c.section, c.langs); ok != c.want {
			t.Errorf("%s (%q, %v) : %v, attendu %v", c.name, c.section, c.langs, ok, c.want)
		}
	}
}

func TestParsePreFeed(t *testing.T) {
	rss := `<?xml version="1.0"?><rss version="2.0"><channel>
<item><title>A.2024.FRENCH.1080p-GRP</title><description>Section: X264-HD | Group: GRP</description><pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate></item>
<item><title>B.2024.MULTi.1080p-GRP</title></item></channel></rss>`
	items, err := ParsePreFeed([]byte(rss))
	if err != nil || len(items) != 2 || items[0].Section != "X264-HD" || items[0].At.IsZero() || !items[1].At.IsZero() {
		t.Fatalf("%v %+v", err, items)
	}
	for _, body := range []string{
		`{"data":[{"name":"A.2024.FRENCH-GRP","cat":{"name":"X264"},"date":"2026-01-02T03:04:05Z"}]}`,
		`[{"release":"A.2024.FRENCH-GRP","section":"X264"}]`,
	} {
		items, err := ParsePreFeed([]byte(body))
		if err != nil || len(items) != 1 || items[0].Name != "A.2024.FRENCH-GRP" || items[0].Section != "X264" {
			t.Fatalf("%s : %v %+v", body, err, items)
		}
	}
	if _, err := ParsePreFeed([]byte("nope")); err == nil {
		t.Fatal("un contenu inconnu doit échouer")
	}
}

// testKey est une valeur factice volontairement sans entropie, pour ne pas ressembler à une clé réelle.
var testKey = strings.Repeat("k", 16)

func TestPreDBSettings(t *testing.T) {
	defer pseenv.Replace(map[string]string{})()
	if s := LoadPreDBSettings(); s.Active() || !s.Public || len(s.Langs) != 3 || s.Delay != time.Hour {
		t.Fatalf("défauts : %+v", s)
	}
	restore := pseenv.Replace(map[string]string{"PSE_PREDB": "on", "PSE_PREDB_PUBLIC": "off", "PSE_PREDB_PRIVATE_URL": "https://a.example/rss ftp://x https://" + "u" + ":" + "p" + "@b.example/ https://c.example/api"})
	defer restore()
	s := LoadPreDBSettings()
	if !s.Active() || s.Public || len(s.Feeds) != 2 {
		t.Fatalf("flux personnels seuls : %+v", s)
	}
}

// Une pre n'est comptée qu'après le délai, son média est résolu, la clé
// n'est envoyée qu'en en-tête, et une pre sans média est retentée puis
// abandonnée.
func TestPreCycle(t *testing.T) {
	var gotKey, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotQuery = r.Header.Get("X-API-Key"), r.URL.RawQuery
		w.Write([]byte(`<rss><channel>
<item><title>Un.Film.2024.FRENCH.1080p.WEB.H264-GRP</title></item>
<item><title>Inconnu.2024.MULTi.1080p.WEB.H264-GRP</title></item>
<item><title>Autre.2024.GERMAN.1080p.WEB.H264-GRP</title></item></channel></rss>`))
	}))
	defer srv.Close()
	old := preClient
	preClient = srv.Client()
	defer func() { preClient = old }()

	now := time.Unix(1_800_000_000, 0)
	db := store.OpenTest(t)
	s := New(Deps{DB: db, Now: func() time.Time { return now }, Resolve: func(_ context.Context, info core.ReleaseInfo) (core.Title, bool) {
		if info.Title == "Un Film" {
			return core.Title{Key: core.MovieKey(42), Type: core.TypeMovie}, true
		}
		return core.Title{}, false
	}})
	cfg := PreDBSettings{Enabled: true, Feeds: []PreFeedConf{{URL: srv.URL + "/feed", Key: testKey}}, Langs: []string{"fr", "multi"}, Delay: time.Hour, Every: 10 * time.Minute}
	ctx := context.Background()

	if err := s.preCycle(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if gotKey != testKey || gotQuery != "" {
		t.Fatalf("clé %q, requête %q", gotKey, gotQuery)
	}
	if n := s.st.preAdded(ctx); n != 0 {
		t.Fatalf("avant le délai : %d release", n)
	}
	if n := s.st.prePending(ctx); n != 2 {
		t.Fatalf("en attente : %d", n)
	}
	now = now.Add(61 * time.Minute)
	if err := s.preCycle(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	rels, err := s.st.Releases(ctx, core.MovieKey(42), Filter{})
	if err != nil || len(rels) != 1 || rels[0].SourceID != PreDBSourceID || len(rels[0].DebridCached) == 0 {
		t.Fatalf("%v %+v", err, rels)
	}
	if !(Filter{Languages: []string{"french"}, CachedOnly: true}).Match(rels[0]) {
		t.Fatal("la pre doit compter comme française et disponible")
	}
	for range preDBMaxTries {
		now = now.Add(11 * time.Minute)
		if err := s.preCycle(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.st.prePending(ctx); n != 0 {
		t.Fatalf("la pre sans média doit être abandonnée : %d", n)
	}
	// Détail par flux : le flux personnel a vu deux pre, l'une indexée, l'autre abandonnée.
	stats := s.st.preFeedStats(ctx, cfg)
	if len(stats) != 1 || stats[0].Feed != "perso:1" || stats[0].Seen != 2 || stats[0].Indexed != 1 || stats[0].Pending != 0 || stats[0].Unmatched != 1 {
		t.Fatalf("détail par flux : %+v", stats)
	}
	if strings.Contains(fmt.Sprint(stats), srv.URL) {
		t.Fatal("l'adresse d'un flux personnel ne doit jamais être exposée")
	}
	// Changer les langues efface ce qui avait été importé.
	cfg.Langs = []string{"vostfr"}
	if err := s.preCycle(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if n := s.st.preAdded(ctx); n != 0 {
		t.Fatalf("après changement de réglage : %d", n)
	}
}

// Les pre figurent dans le détail des releases par source de Statistiques.
func TestSourceOverviewListsPreWhenEnabled(t *testing.T) {
	e := newFreshEnv(t)
	find := func() *SourceStats {
		overview, err := e.s.SourceOverview(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range overview.Sources {
			if overview.Sources[i].ID == PreDBSourceID {
				return &overview.Sources[i]
			}
		}
		return nil
	}
	if find() != nil {
		t.Fatal("les pre ne doivent pas apparaître quand l'option est désactivée")
	}
	e.s.d.Settings.PreDB = PreDBSettings{Enabled: true, Public: true}
	got := find()
	if got == nil || got.Name != preDBName || !got.Enabled {
		t.Fatalf("source pre absente ou mal nommée : %+v", got)
	}
}

// Les flux publics configurés sont listés même sans pre, et une release dont la pre a quitté
// la mémoire de 30 jours est comptée à part.
func TestPreFeedStatsListConfiguredFeedsAndOrphans(t *testing.T) {
	db := store.OpenTest(t)
	s := New(Deps{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
	ctx := context.Background()
	stats := s.st.preFeedStats(ctx, PreDBSettings{Enabled: true, Public: true})
	if len(stats) != len(preDBPublicFeeds) || stats[0].Feed != preDBPublicFeeds[0] || stats[0].Seen != 0 {
		t.Fatalf("flux publics : %+v", stats)
	}
	rel := core.Release{ID: "pre:vieille.release", Name: "Vieille.Release.2020.FRENCH.1080p-GRP", Info: core.ReleaseInfo{Title: "Vieille Release"}}
	saved := FromCore(core.Title{Key: core.MovieKey(7), Type: core.TypeMovie}, 0, 0, PreDBSourceID, rel, []string{preDBMarker})
	if err := s.st.Save(ctx, []Release{saved}, time.Unix(1_800_000_000, 0)); err != nil {
		t.Fatal(err)
	}
	stats = s.st.preFeedStats(ctx, PreDBSettings{Enabled: true, Public: true})
	last := stats[len(stats)-1]
	if last.Feed != "" || last.Indexed != 1 {
		t.Fatalf("release sans flux connu : %+v", stats)
	}
}

// Chaque flux garde le résultat de sa dernière lecture, sans adresse ni clé.
func TestPreCycleReportsEachFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ko" {
			http.Error(w, "interdit", http.StatusForbidden)
			return
		}
		w.Write([]byte(`<rss><channel>
<item><title>Un.Film.2024.FRENCH.1080p.WEB.H264-GRP</title></item>
<item><title>Autre.2024.GERMAN.1080p.WEB.H264-GRP</title></item></channel></rss>`))
	}))
	defer srv.Close()
	old := preClient
	preClient = srv.Client()
	defer func() { preClient = old }()
	db := store.OpenTest(t)
	s := New(Deps{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
	cfg := PreDBSettings{Enabled: true, Feeds: []PreFeedConf{{Name: "Mon site", URL: srv.URL + "/ok"}, {URL: srv.URL + "/ko"}}, Langs: []string{"fr"}, Delay: time.Hour, Every: 10 * time.Minute}
	ctx := context.Background()
	if err := s.preCycle(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	st := s.st.preStatus(ctx)
	if len(st.Runs) != 2 || st.Runs[0].State != "ok" || st.Runs[0].Fetched != 2 || st.Runs[0].Kept != 1 {
		t.Fatalf("flux lisible : %+v", st.Runs)
	}
	if st.Runs[1].State != "error" || st.Runs[1].Error != "flux refusé (403)" || st.Runs[1].Feed != "perso:2" {
		t.Fatalf("flux refusé : %+v", st.Runs)
	}
	if st.Error != "1 flux sur 2 en erreur : voir le détail par flux" {
		t.Fatalf("message global : %q", st.Error)
	}
	if strings.Contains(fmt.Sprint(st), srv.URL) {
		t.Fatal("l'adresse d'un flux personnel ne doit jamais être enregistrée")
	}
}

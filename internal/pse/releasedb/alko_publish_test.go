package releasedb

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func alkoPub(e *freshEnv, remember func(context.Context, core.RatingKey, []core.Release)) *AlkoPublisher {
	return &AlkoPublisher{DB: e.s.st.db, Remember: remember, Now: func() time.Time { return e.now }}
}

func TestAlkoReleaseIsAVerifiedFile(t *testing.T) {
	f := AlkoFile{ID: "11111111-1111-1111-1111-111111111111", Kind: "movie", Name: "Film.2020.FRENCH.1080p.mkv", URL: "https://alldebrid.com/f/verified", Size: 1000, TMDB: 42}
	r, ok := AlkoRelease(f, true)
	if !ok || !core.IsVerifiedFile(r) {
		t.Fatalf("release %+v %v", r, ok)
	}
	if r.ID != "sfr-alko:"+f.ID || r.GUID != r.ID || r.InfoHash != "" || r.MagnetURL != "" || r.StreamURL != f.URL || r.IndexerID != AlkoSourceID {
		t.Fatalf("release %+v", r)
	}
	// Mêmes champs que le chemin PostgreSQL : seule la source change.
	old := sfrAlkoCandidates([]sfrRow{{ID: f.ID, Name: f.Name, Size: f.Size, TMDB: f.TMDB, AlkoURL: f.URL}})[0]
	if old.ID != r.ID || old.GUID != r.GUID || old.Protocol != r.Protocol || old.StreamDebrid != r.StreamDebrid || old.StreamOnDemand != r.StreamOnDemand || !slices.Equal(old.DebridCached, r.DebridCached) {
		t.Fatalf("écart avec sfrAlkoCandidates : %+v / %+v", old, r)
	}
	for _, bad := range []AlkoFile{{ID: f.ID, Name: f.Name, URL: "https://evil.example/f/x", Size: 1}, {ID: f.ID, Name: f.Name, URL: f.URL}, {Name: f.Name, URL: f.URL, Size: 1}} {
		if _, ok := AlkoRelease(bad, false); ok {
			t.Fatalf("fichier invalide accepté : %+v", bad)
		}
	}
	if _, ok := AlkoRelease(AlkoFile{ID: f.ID, Name: "Film.2020.GERMAN.1080p.mkv", URL: f.URL, Size: 1}, true); ok {
		t.Fatal("langue étrangère acceptée avec le filtre")
	}
	if _, ok := AlkoRelease(AlkoFile{ID: f.ID, Name: "Film.2020.GERMAN.1080p.mkv", URL: f.URL, Size: 1}, false); !ok {
		t.Fatal("filtre désactivé : tout doit passer")
	}
}

func TestAlkoPublishCatalogTitlesAllEpisodes(t *testing.T) {
	e := newFreshEnv(t)
	show := core.ShowKey(9)
	movie := core.MovieKey(42)
	sfrCatalog(t, e, "show", show)
	sfrCatalog(t, e, "movie", movie)
	remembered := map[core.RatingKey][]core.Release{}
	p := alkoPub(e, func(_ context.Context, k core.RatingKey, rs []core.Release) {
		remembered[k] = append(remembered[k], rs...)
	})
	files := []AlkoFile{
		{ID: "a", Kind: "movie", TMDB: 42, Name: "Film.2020.FRENCH.1080p.mkv", URL: "https://alldebrid.com/f/a", Size: 10},
		{ID: "b", Kind: "episode", TMDB: 9, Season: 2, Episode: 5, Name: "Serie.S02E05.MULTi.1080p.WEB.x264-GRP.mkv", URL: "https://alldebrid.com/f/b", Size: 10},
		{ID: "c", Kind: "episode", TMDB: 9, Season: 1, Episode: 1, Name: "Serie.S01E01.MULTi.1080p.WEB.x264-GRP.mkv", URL: "https://alldebrid.com/f/c", Size: 10},
		{ID: "s", Kind: "episode", TMDB: 9, Season: 0, Episode: 2, Name: "Serie.S00E02.MULTi.1080p.WEB.x264-GRP.mkv", URL: "https://alldebrid.com/f/s", Size: 10},
		{ID: "d", Kind: "movie", TMDB: 7, Name: "Inconnu.2020.FRENCH.1080p.mkv", URL: "https://alldebrid.com/f/d", Size: 10},
		{ID: "e", Kind: "movie", TMDB: 42, Name: "Film.2020.GERMAN.1080p.mkv", URL: "https://alldebrid.com/f/e", Size: 10},
	}
	res, err := p.Publish(e.ctx, files, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "s"} {
		if res.States[id].State != AlkoPublished || res.States[id].ReleaseID == "" {
			t.Fatalf("%s : %+v", id, res.States[id])
		}
	}
	if _, judged := res.States["d"]; judged {
		t.Fatal("titre hors catalogue jugé : il doit rester à publier")
	}
	if res.States["e"].State != AlkoSkipped {
		t.Fatalf("langue : %+v", res.States["e"])
	}
	got, err := e.s.st.Releases(e.ctx, show, Filter{})
	if err != nil || len(got) != 3 {
		t.Fatalf("série : %v %v", got, err)
	}
	seasons := map[int]int{}
	for _, r := range got {
		if r.SourceID != AlkoSourceID || r.Protocol != core.ProtocolHTTP || !slices.Contains(r.DebridCached, "alldebrid") {
			t.Fatalf("release %+v", r)
		}
		seasons[r.Season*100+r.Episode]++
	}
	if seasons[205] != 1 || seasons[101] != 1 || seasons[2] != 1 {
		t.Fatalf("épisodes publiés : %v", seasons)
	}
	if len(remembered[core.EpisodeKey(9, 2, 5)]) != 1 || len(remembered[movie]) != 1 {
		t.Fatalf("références : %v", remembered)
	}
	for _, rs := range remembered {
		for _, r := range rs {
			if !core.IsVerifiedFile(r) || r.IndexerID != AlkoSourceID {
				t.Fatalf("référence %+v", r)
			}
		}
	}
	// Idempotent, puis retrait.
	if _, err := p.Publish(e.ctx, files[:1], true); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.st.Releases(e.ctx, movie, Filter{}); len(got) != 1 {
		t.Fatalf("film : %v", got)
	}
	if err := p.Forget(e.ctx, []AlkoGone{{PlaybackID: "a", ReleaseID: res.States["a"].ReleaseID, TitleKey: movie}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.st.Releases(e.ctx, movie, Filter{}); len(got) != 0 {
		t.Fatalf("film retiré : %v", got)
	}
	if err := p.ForgetAll(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.st.Releases(e.ctx, show, Filter{}); len(got) != 0 {
		t.Fatalf("tout retiré : %v", got)
	}
}

func TestAlkoCountsAsActiveSource(t *testing.T) {
	e := newFreshEnv(t)
	if slices.Contains(e.s.activeSourceIDs(), AlkoSourceID) {
		t.Fatal("source inactive comptée")
	}
	e.s.d.Settings.Alko = true
	if !slices.Contains(e.s.activeSourceIDs(), AlkoSourceID) {
		t.Fatal("remix-alko doit rester une source active, sinon ses releases cessent de compter")
	}
}

func TestAlkoAPIEnabledNeedsKey(t *testing.T) {
	defer pseenv.Replace(map[string]string{"PSE_ALKO": "on"})()
	if AlkoAPIEnabled() {
		t.Fatal("sans clé")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": "factice"})
	if !AlkoAPIEnabled() {
		t.Fatal("avec clé")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "off", "PSE_ALKO_KEY": "factice"})
	if AlkoAPIEnabled() {
		t.Fatal("désactivée")
	}
}

func TestSFRAlkoPostgresIgnoredWhenAPIActiveAndMirrorFull(t *testing.T) {
	p := &pgxSFR{alkoAvailable: true}
	defer pseenv.Replace(nil)()
	t.Cleanup(func() { SetAlkoMirrorReady(false) })
	SetAlkoMirrorReady(false)
	if !sfrAlkoPostgres(p) {
		t.Fatal("sans la source API, le chemin PostgreSQL reste inchangé")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": "factice"})
	if !sfrAlkoPostgres(p) {
		t.Fatal("source API active mais miroir vide : le chemin PostgreSQL continue")
	}
	SetAlkoMirrorReady(true)
	if sfrAlkoPostgres(p) {
		t.Fatal("miroir rempli : le chemin PostgreSQL doit être ignoré")
	}
	pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": "factice", "PSE_ALKO_URL": "ftp://x"})
	if !sfrAlkoPostgres(p) {
		t.Fatal("adresse invalide : source inactive")
	}
}

func alkoCount(t *testing.T, e *freshEnv, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.s.st.db.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Touch rafraîchit toutes les observations de la source, par tranches, et
// seulement les siennes.
func TestAlkoTouchByChunks(t *testing.T) {
	e := newFreshEnv(t)
	old := e.now.Add(-48 * time.Hour)
	var rs []Release
	for i := range 5*alkoChunk*2 + 7 { // plus de deux tranches, dernière incomplète
		rs = append(rs, Release{ID: "alko-" + strconv.Itoa(i), TitleKey: core.MovieKey(1), Type: core.TypeMovie, SourceID: AlkoSourceID, Name: "Film"})
	}
	rs = append(rs, Release{ID: "autre", TitleKey: core.MovieKey(1), Type: core.TypeMovie, SourceID: SFRSourceID, Name: "Film"})
	if err := e.s.st.Save(e.ctx, rs, old); err != nil {
		t.Fatal(err)
	}
	if err := alkoPub(e, nil).Touch(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := alkoCount(t, e, `SELECT COUNT(*) FROM pse_releases WHERE source_id=? AND last_seen=?`, AlkoSourceID, e.now.Unix()); n != len(rs)-1 {
		t.Fatalf("observations rafraîchies : %d sur %d", n, len(rs)-1)
	}
	if n := alkoCount(t, e, `SELECT COUNT(*) FROM pse_releases WHERE source_id=? AND last_seen=?`, SFRSourceID, old.Unix()); n != 1 {
		t.Fatal("une release d'une autre source a été touchée")
	}
	if err := alkoPub(e, nil).Touch(e.ctx); err != nil { // source vide ou déjà à jour : sans erreur
		t.Fatal(err)
	}
}

// Les anciennes lignes PostgreSQL des Alkodiques sont retirées par tranches ;
// les torrents SFR, les autres références et celles de la source API restent.
func TestAlkoDropPostgresDuplicatesInChunks(t *testing.T) {
	e := newFreshEnv(t)
	var rs []Release
	for i := range 2*alkoChunk + 3 {
		rs = append(rs, Release{ID: "pg-" + strconv.Itoa(i), TitleKey: core.MovieKey(int64(1 + i%3)), Type: core.TypeMovie, SourceID: SFRSourceID, SourceName: alkoPostgresName, Name: "Film"})
	}
	rs = append(rs,
		Release{ID: "torrent", TitleKey: core.MovieKey(1), Type: core.TypeMovie, SourceID: SFRSourceID, SourceName: "StreamFusion Reborn", Name: "Film"},
		Release{ID: "sfr-alko:api", TitleKey: core.MovieKey(1), Type: core.TypeMovie, SourceID: AlkoSourceID, SourceName: AlkoIndexerName, Name: "Film"})
	if err := e.s.st.Save(e.ctx, rs, e.now); err != nil {
		t.Fatal(err)
	}
	lib := func(source, release string) {
		t.Helper()
		if _, err := e.s.st.db.ExecContext(e.ctx, `INSERT INTO pse_release_library(title_key,release_id,source_id,source_signature,payload,first_seen,last_seen,expires) VALUES(1,?,?,'s','p',1,1,1)`, release, source); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2*alkoChunk + 1 {
		lib(alkoPostgresLibrary, core.VerifiedFileIDPrefix+strconv.Itoa(i))
	}
	lib(alkoPostgresLibrary, "autre:reference")
	lib(AlkoSourceID, core.VerifiedFileIDPrefix+"api")
	n, err := alkoPub(e, nil).DropPostgresDuplicates(e.ctx)
	if err != nil || n != 2*alkoChunk+3 {
		t.Fatalf("retirées : %d %v", n, err)
	}
	if got := alkoCount(t, e, `SELECT COUNT(*) FROM pse_releases WHERE source_id=?`, SFRSourceID); got != 1 {
		t.Fatalf("torrents conservés : %d", got)
	}
	if got := alkoCount(t, e, `SELECT COUNT(*) FROM pse_releases WHERE source_id=?`, AlkoSourceID); got != 1 {
		t.Fatalf("releases de la source API : %d", got)
	}
	if got := alkoCount(t, e, `SELECT COUNT(*) FROM pse_release_library`); got != 2 {
		t.Fatalf("références conservées : %d", got)
	}
	if n, err := alkoPub(e, nil).DropPostgresDuplicates(e.ctx); err != nil || n != 0 {
		t.Fatalf("second passage : %d %v", n, err)
	}
}

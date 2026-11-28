package alko

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	movieName = "Film.2020.FRENCH.1080p.WEB.mkv"
	epName    = "Serie.S02E05.MULTi.1080p.WEB.x264-GRP.mkv"
)

func TestFullReadThenIncremental(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	serie := e.catalog("show", 9, "Serie")
	e.f.put(movie(1, 42, movieName), "I")
	e.f.put(episode(2, 9, 2, 5, epName), "I")
	e.f.put(episode(3, 9, 1, 1, "Serie.S01E01.MULTi.1080p.WEB.x264-GRP.mkv"), "I")
	e.f.put(movie(4, 777, "Inconnu.2020.FRENCH.1080p.mkv"), "I") // titre absent du catalogue
	e.mustSync()
	if n := e.count(`SELECT COUNT(*) FROM pse_alko_files`); n != 4 {
		t.Fatalf("miroir : %d fichiers", n)
	}
	if e.releases(film) != 1 || e.releases(serie) != 2 {
		t.Fatalf("publication : film %d série %d", e.releases(film), e.releases(serie))
	}
	if e.f.calls["/api/alko/v1/catalogue"] != 3 {
		t.Fatalf("pages du catalogue : %d", e.f.calls["/api/alko/v1/catalogue"])
	}
	// Le repère est celui de la première page (last_seq) et l'époque est mémorisée.
	if seq, _ := e.svc.st().get(context.Background(), "seq"); seq != "4" {
		t.Fatalf("repère %q", seq)
	}
	st := e.svc.Status(context.Background())
	if st.Files != 4 || st.Movies != 2 || st.Episodes != 2 || st.Published != 3 || st.State != "idle" || st.Journal != "ok" {
		t.Fatalf("statut %+v", st)
	}

	// Aucun changement : ni relecture complète ni relecture par identifiant.
	before := e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] != before || len(e.f.asked) != 0 {
		t.Fatal("rien n'a changé : pas de lecture")
	}

	// Journal : mise à jour, suppression, nouveau fichier ; un D suivi d'un I du même id.
	e.f.put(movie(1, 42, "Film.2020.MULTi.2160p.WEB.mkv"), "U")
	e.f.del(testID(3))
	e.f.put(episode(5, 9, 3, 7, "Serie.S03E07.MULTi.1080p.WEB.x264-GRP.mkv"), "I")
	e.f.put(movie(6, 42, "Film.2020.VOSTFR.1080p.mkv"), "I")
	e.f.del(testID(6))
	e.f.put(movie(6, 42, "Film.2020.VOSTFR.720p.mkv"), "I")
	e.mustSync()
	if n := e.count(`SELECT COUNT(*) FROM pse_alko_files`); n != 5 {
		t.Fatalf("miroir : %d fichiers", n)
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=?`, testID(3)) != 0 {
		t.Fatal("fichier supprimé conservé")
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=? AND filename LIKE '%2160p%'`, testID(1)) != 1 {
		t.Fatal("fichier modifié non relu")
	}
	if e.releases(film) != 2 || e.releases(serie) != 2 { // S01E01 retiré, S03E07 ajouté
		t.Fatalf("publication : film %d série %d", e.releases(film), e.releases(serie))
	}
	if e.f.calls["/api/alko/v1/catalogue"] != before {
		t.Fatal("le suivi du journal ne doit pas relire le catalogue")
	}
	if seq, _ := e.svc.st().get(context.Background(), "seq"); seq != "10" {
		t.Fatalf("repère %q", seq)
	}
}

func TestIncrementalReadsByIDAndNeverDeletesBlindly(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	// Le journal dit D, mais le fichier a repris le même playback_id : il est présent.
	e.f.mu.Lock()
	e.f.record(testID(1), "D", "movie", 42)
	e.f.mu.Unlock()
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 1 {
		t.Fatal("suppression aveugle d'un fichier toujours présent")
	}
	e.f.mu.Lock()
	asked := e.f.asked
	e.f.mu.Unlock()
	if len(asked) == 0 || asked[len(asked)-1][0] != testID(1) {
		t.Fatalf("relecture par identifiant absente : %v", asked)
	}
}

func TestFullReadAgainOnEpochChangeAndPurgesAbsent(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.mustSync()
	if e.releases(film) != 2 {
		t.Fatalf("publication %d", e.releases(film))
	}
	// Restauration côté SFR : nouvelle époque, un fichier a disparu sans journal.
	e.f.mu.Lock()
	delete(e.f.files, testID(2))
	e.f.epoch++
	e.f.mu.Unlock()
	cat := e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] == cat {
		t.Fatal("changement d'époque : relecture complète attendue")
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 1 || e.releases(film) != 1 {
		t.Fatalf("fichier absent non purgé : miroir %d, releases %d", e.count(`SELECT COUNT(*) FROM pse_alko_files`), e.releases(film))
	}
	if ep, _ := e.svc.st().int(context.Background(), "epoch"); ep != e.f.epoch {
		t.Fatalf("époque mémorisée %d", ep)
	}
}

func TestFullReadOnGoneAndOnSeqRegression(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	// 410 : le journal a été purgé au-delà du repère.
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.f.mu.Lock()
	e.f.purge = e.f.seq + 1 // purge_seq > repère
	e.f.mu.Unlock()
	cat := e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] == cat || e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 2 {
		t.Fatal("purge_seq > repère : relecture complète attendue")
	}
	// Réponse 410 du journal alors que l'état ne l'annonce pas.
	e.f.put(movie(3, 42, "Film.2020.MULTi.720p.mkv"), "I")
	e.f.mu.Lock()
	e.f.purge = 0
	e.f.mu.Unlock()
	// Base SFR restaurée en arrière : last_seq < repère.
	e.f.mu.Lock()
	e.f.seq, e.f.jseq = 1, 1
	e.f.mu.Unlock()
	cat = e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] == cat {
		t.Fatal("last_seq < repère : relecture complète attendue")
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 3 {
		t.Fatal("fichiers manquants après relecture")
	}
}

func TestGoneResponseTriggersFullRead(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.f.mu.Lock()
	e.f.status["/api/alko/v1/modifications"] = 410
	e.f.mu.Unlock()
	cat := e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] == cat || e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 2 {
		t.Fatal("HTTP 410 : relecture complète attendue")
	}
}

func TestInvalidJournalWaitsThenReReadsAtMostEverySixHours(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	// Lots appliqués sans journal : journal_seq n'avance plus.
	e.f.mu.Lock()
	e.f.journal = false
	e.f.mu.Unlock()
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.f.mu.Lock()
	e.f.jseq = 1
	e.f.mu.Unlock()
	cat := e.f.calls["/api/alko/v1/catalogue"]
	for i := 0; i < 2; i++ {
		e.now = e.now.Add(10 * time.Minute)
		e.mustSync()
	}
	if e.f.calls["/api/alko/v1/catalogue"] != cat || e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 1 {
		t.Fatal("un journal invalide doit attendre, sans appliquer ni relire")
	}
	if e.svc.Status(context.Background()).Journal != "waiting" && e.svc.Status(context.Background()).Journal != "inactive" {
		t.Fatalf("journal %q", e.svc.Status(context.Background()).Journal)
	}
	// Un écart qui dure mais une relecture récente : on attend encore.
	e.now = e.now.Add(10 * time.Minute)
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] != cat {
		t.Fatal("relecture complète trop fréquente")
	}
	// Au-delà de six heures depuis la dernière relecture complète : relecture.
	e.now = e.now.Add(7 * time.Hour)
	e.mustSync()
	if e.f.calls["/api/alko/v1/catalogue"] == cat || e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 2 {
		t.Fatal("écart persistant : relecture complète attendue")
	}
}

func TestNewCatalogTitleIsCaughtUp(t *testing.T) {
	e := newEnv(t)
	e.f.put(movie(1, 42, movieName), "I")
	e.f.put(episode(2, 9, 2, 5, epName), "I")
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_releases WHERE source_id=?`, "remix-alko") != 0 {
		t.Fatal("rien à publier : aucun titre au catalogue")
	}
	// Le titre entre au catalogue après la synchronisation : rattrapé au passage suivant.
	film := e.catalog("movie", 42, "Film")
	serie := e.catalog("show", 9, "Serie")
	e.mustSync()
	if e.releases(film) != 1 || e.releases(serie) != 1 {
		t.Fatalf("rattrapage : film %d série %d", e.releases(film), e.releases(serie))
	}
	if e.f.calls["/api/alko/v1/catalogue"] != 2 {
		t.Fatal("le rattrapage ne doit pas relire le catalogue d'SFR")
	}
	// Un passage de plus ne republie rien.
	e.mustSync()
	if e.releases(film) != 1 {
		t.Fatal("publication dupliquée")
	}
}

func TestLanguageSettingChangeReevaluates(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, "Film.2020.GERMAN.1080p.mkv"), "I")
	e.f.put(movie(2, 42, movieName), "I")
	e.mustSync()
	if e.releases(film) != 1 {
		t.Fatalf("filtre FR : %d", e.releases(film))
	}
	e.svc.Settings.LangFR = false
	e.mustSync()
	if e.releases(film) != 2 {
		t.Fatalf("filtre désactivé : %d", e.releases(film))
	}
	e.svc.Settings.LangFR = true
	e.mustSync()
	if e.releases(film) != 1 {
		t.Fatalf("filtre réactivé : %d", e.releases(film))
	}
}

func TestOtherServerStartsFromScratch(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	other := newFakeSFR(t)
	other.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.svc.Settings.URL = other.srv.URL
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 1 || e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=?`, testID(2)) != 1 || e.releases(film) != 1 {
		t.Fatal("un autre serveur ne doit rien garder de l'ancien")
	}
}

func TestSourceURLIsSealedAndNeverLogged(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	var sealed string
	if err := e.db.QueryRow(`SELECT url_sealed FROM pse_alko_files`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "alldebrid") || strings.Contains(sealed, "token1") || !strings.HasPrefix(sealed, "sealed:") {
		t.Fatalf("lien en clair en base : %q", sealed)
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE info_json IS NULL`) != 1 {
		t.Fatal("info_json doit rester vide dans cette version")
	}
	// Erreurs de clé, de réseau : rien de secret dans les journaux ni dans l'état.
	e.f.mu.Lock()
	e.f.status["/api/alko/v1/etat"] = 401
	e.f.mu.Unlock()
	err := e.sync()
	if !errors.Is(err, ErrKey) {
		t.Fatalf("erreur %v", err)
	}
	st := e.svc.Status(context.Background())
	if st.State != "error" || st.Error != "key" {
		t.Fatalf("statut %+v", st)
	}
	for _, secret := range []string{testKey, "token1", "alldebrid.com/f/", e.f.srv.URL} {
		if strings.Contains(e.logs.String(), secret) || strings.Contains(err.Error(), secret) {
			t.Fatalf("secret dans les journaux ou l'erreur : %q", secret)
		}
	}
}

func TestSearchLocalOnlyAndVerifiedFiles(t *testing.T) {
	e := newEnv(t)
	e.f.put(movie(1, 42, movieName), "I")
	e.f.put(movie(7, 42, "Film.2020.GERMAN.1080p.mkv"), "I")
	e.f.put(episode(2, 9, 2, 5, epName), "I")
	e.f.put(episode(3, 9, 0, 3, "Serie.S00E03.MULTi.1080p.WEB.x264-GRP.mkv"), "I")
	e.f.put(episode(4, 9, 1, 1, "Serie.S01E01.MULTi.1080p.WEB.x264-GRP.mkv"), "I")
	e.mustSync()
	e.f.srv.Close() // la recherche ne fait aucun appel réseau
	ctx := context.Background()

	rs, err := e.svc.Search(ctx, core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 42}})
	if err != nil || len(rs) != 1 {
		t.Fatalf("film : %v %v", rs, err)
	}
	r := rs[0]
	if !core.IsVerifiedFile(r) || r.ID != "sfr-alko:"+testID(1) || r.GUID != r.ID || r.InfoHash != "" || r.MagnetURL != "" ||
		r.StreamURL != "https://alldebrid.com/f/token1" || r.StreamDebrid != "alldebrid" || !r.StreamOnDemand || r.Protocol != core.ProtocolHTTP {
		t.Fatalf("release %+v", r)
	}
	// Épisode hors S01E01, puis saison 0 exacte.
	if rs, _ = e.svc.Search(ctx, core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 9}, Season: 2, Episode: 5}); len(rs) != 1 || rs[0].ID != "sfr-alko:"+testID(2) {
		t.Fatalf("S02E05 : %v", rs)
	}
	if rs, _ = e.svc.Search(ctx, core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 9}, Season: 0, Episode: 3}); len(rs) != 1 || rs[0].ID != "sfr-alko:"+testID(3) {
		t.Fatalf("S00E03 : %v", rs)
	}
	if rs, _ = e.svc.Search(ctx, core.SearchRequest{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 9}, Season: 2, Episode: 3}); len(rs) != 0 {
		t.Fatalf("S02E03 inexistant : %v", rs)
	}
	if rs, _ = e.svc.Search(ctx, core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 9}}); len(rs) != 0 {
		t.Fatal("un film ne doit pas renvoyer un épisode")
	}
	// Filtre désactivé : tous les fichiers.
	e.svc.Settings.LangFR = false
	if rs, _ = e.svc.Search(ctx, core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 42}}); len(rs) != 2 {
		t.Fatalf("sans filtre : %v", rs)
	}
}

func TestIdleWaitsForPlaybackBetweenPages(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 5; i++ {
		e.f.put(movie(i, 42, movieName), "I")
	}
	calls := 0
	e.svc.Idle = func(context.Context) error { calls++; return nil }
	e.mustSync()
	if calls < 3 {
		t.Fatalf("la pause pendant les lectures doit être consultée à chaque page (%d)", calls)
	}
}

func TestFullReadResumesAfterInterruption(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 6; i++ {
		e.f.put(movie(i, 42, movieName), "I")
	}
	// Coupure réseau au milieu de la lecture complète.
	n := 0
	e.svc.Idle = func(ctx context.Context) error {
		n++
		if n == 3 {
			return errors.New("interruption")
		}
		return nil
	}
	if err := e.sync(); err == nil {
		t.Fatal("interruption attendue")
	}
	if gen, _ := e.svc.st().int(context.Background(), "full_gen"); gen == 0 {
		t.Fatal("lecture complète en cours non mémorisée")
	}
	if after, _ := e.svc.st().get(context.Background(), "full_after"); after == "" {
		t.Fatal("curseur non mémorisé")
	}
	e.svc.Idle = nil
	e.f.mu.Lock()
	e.f.calls = map[string]int{}
	e.f.mu.Unlock()
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 6 {
		t.Fatal("miroir incomplet après reprise")
	}
	if e.f.calls["/api/alko/v1/catalogue"] >= 4 {
		t.Fatalf("reprise : le début a été relu (%d pages)", e.f.calls["/api/alko/v1/catalogue"])
	}
}

func TestStoreMigrationHasAllColumns(t *testing.T) {
	e := newEnv(t)
	if _, err := e.db.Exec(`INSERT INTO pse_alko_files(playback_id,kind,tmdb,season,episode,filename,filesize,fingerprint,synced_at,langs,info_json,url_sealed) VALUES('x','movie',1,0,0,'f',1,'','', '', NULL,'s')`); err != nil {
		t.Fatalf("schéma de la migration v1 : %v", err)
	}
}

func TestStartSyncsInBackgroundAndWakes(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	zero := time.Duration(0)
	e.svc.Delay = &zero
	e.svc.Settings.Every = time.Hour
	if err := e.svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer e.svc.Close()
	wait := func(want int) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if e.count(`SELECT COUNT(*) FROM pse_alko_files`) == want {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("miroir : %d fichiers attendus", want)
	}
	wait(1)
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	e.svc.Wake()
	wait(2)
}

const probeJSON = `{"format":{"format_name":"matroska,webm","duration":"5400.0","size":"1073741824","bit_rate":"1590000"},"streams":[
{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},
{"index":1,"codec_type":"audio","codec_name":"eac3","channels":6,"tags":{"language":"fre"}},
{"index":2,"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"eng"}},
{"index":3,"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"fra"}},
{"index":4,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"fre"}}]}`

func TestMediaInfoFillsInfoJSONAndLangs(t *testing.T) {
	e := newEnv(t)
	good, bad, none := movie(1, 42, movieName), movie(2, 42, movieName), movie(3, 42, movieName)
	p, junk := probeJSON, "pas du json"
	good.MediaInfo, bad.MediaInfo = &p, &junk
	for _, el := range []Element{good, bad, none} {
		e.f.put(el, "I")
	}
	e.mustSync() // un media_info invalide ne fait pas échouer la synchro
	var info, langs string
	if err := e.db.QueryRow(`SELECT info_json,langs FROM pse_alko_files WHERE playback_id=?`, testID(1)).Scan(&info, &langs); err != nil {
		t.Fatal(err)
	}
	if langs != "fr,en" {
		t.Fatalf("langues %q", langs)
	}
	var m core.MediaInfo
	if err := json.Unmarshal([]byte(info), &m); err != nil || m.Container != "mkv" || len(m.Streams) != 5 || m.Video() == nil {
		t.Fatalf("info_json %v %+v", err, m)
	}
	for _, id := range []int{2, 3} {
		if n := e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=? AND info_json IS NULL AND langs=''`, testID(id)); n != 1 {
			t.Fatalf("fichier %d : NULL attendu", id)
		}
	}
	// Un media_info qui change est relu au passage suivant.
	good.MediaInfo = nil
	e.f.put(good, "U")
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=? AND info_json IS NULL`, testID(1)) != 1 {
		t.Fatal("media_info retiré : info_json devrait redevenir NULL")
	}
}

func TestEpochChangeDuringFullReadLeavesNoGhostFiles(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	for i := 1; i <= 4; i++ {
		e.f.put(movie(i, 42, movieName), "I")
	}
	e.mustSync()
	if e.releases(film) != 4 {
		t.Fatalf("publication %d", e.releases(film))
	}
	// Nouvelle lecture complète : après sa première page, le fichier 1 disparaît
	// de SFR et l'époque change encore. La tentative avortée l'avait marqué.
	e.f.mu.Lock()
	e.f.epoch++
	calls := e.f.calls["/api/alko/v1/catalogue"]
	e.f.afterCatalogue = func() {
		if e.f.calls["/api/alko/v1/catalogue"] == calls+1 {
			delete(e.f.files, testID(1))
			e.f.epoch++
		}
	}
	e.f.mu.Unlock()
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files WHERE playback_id=?`, testID(1)) != 0 {
		t.Fatal("fichier fantôme : marqué par la tentative avortée, jamais purgé")
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 3 || e.releases(film) != 3 {
		t.Fatalf("miroir %d, releases %d", e.count(`SELECT COUNT(*) FROM pse_alko_files`), e.releases(film))
	}
}

func TestNetworkErrorDuringFullReadDeletesNothing(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	for i := 1; i <= 4; i++ {
		e.f.put(movie(i, 42, movieName), "I")
	}
	e.mustSync()
	e.f.mu.Lock()
	e.f.epoch++ // relecture complète
	delete(e.f.files, testID(4))
	e.f.failCatalogueFrom = e.f.calls["/api/alko/v1/catalogue"] + 2 // la 2e page échoue
	e.f.mu.Unlock()
	if err := e.sync(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("erreur %v", err)
	}
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 4 || e.releases(film) != 4 {
		t.Fatal("une lecture interrompue ne doit rien supprimer")
	}
	// La reprise termine la lecture : seulement alors le fichier absent est retiré.
	e.f.mu.Lock()
	e.f.failCatalogueFrom = 0
	e.f.mu.Unlock()
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 3 || e.releases(film) != 3 {
		t.Fatal("fichier absent non purgé après la reprise")
	}
}

func TestJournalBecomingInvalidInsideAnIncrementalPage(t *testing.T) {
	e := newEnv(t)
	e.catalog("movie", 42, "Film")
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	e.f.put(movie(2, 42, "Film.2020.MULTi.1080p.mkv"), "I")
	last := e.f.seq
	e.f.mu.Lock()
	e.f.seq += 5 // lots appliqués sans journal
	e.f.jseq = e.f.seq
	e.f.modsJournalInvalid = true // /etat est valide, la page des modifications ne l'est plus
	e.f.mu.Unlock()
	cat := e.f.calls["/api/alko/v1/catalogue"]
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_alko_files`) != 2 {
		t.Fatal("les lignes lues du journal doivent être appliquées")
	}
	if seq, _ := e.svc.st().get(context.Background(), "seq"); seq != strconv.FormatInt(last, 10) {
		t.Fatalf("le repère doit rester au dernier seq lu (%d), pas sauter à last_seq : %q", last, seq)
	}
	if e.f.calls["/api/alko/v1/catalogue"] != cat {
		t.Fatal("pas de relecture complète immédiate")
	}
}

func TestPostgresPathCutOnlyOnceMirrorIsFull(t *testing.T) {
	t.Cleanup(func() { releasedb.SetAlkoMirrorReady(false) })
	releasedb.SetAlkoMirrorReady(false)
	defer pseenv.Replace(map[string]string{"PSE_ALKO": "on", "PSE_ALKO_KEY": testKey})()
	if releasedb.AlkoPostgresIgnored() {
		t.Fatal("miroir vide : le chemin PostgreSQL continue")
	}
	// Clé fausse, SFR trop ancien (404) : toujours le chemin PostgreSQL.
	e := newEnv(t)
	e.f.put(movie(1, 42, movieName), "I")
	e.f.mu.Lock()
	e.f.status["/api/alko/v1/etat"] = 404
	e.f.mu.Unlock()
	if err := e.sync(); !errors.Is(err, ErrDisabled) || releasedb.AlkoPostgresIgnored() {
		t.Fatalf("404 : %v ignoré=%v", err, releasedb.AlkoPostgresIgnored())
	}
	e.f.mu.Lock()
	e.f.status = map[string]int{}
	e.f.failCatalogueFrom = 1 // première synchro en cours (interrompue)
	e.f.mu.Unlock()
	if err := e.sync(); err == nil || releasedb.AlkoPostgresIgnored() {
		t.Fatal("lecture complète inachevée : le chemin PostgreSQL continue")
	}
	e.f.mu.Lock()
	e.f.failCatalogueFrom = 0
	e.f.mu.Unlock()
	e.mustSync()
	if !releasedb.AlkoPostgresIgnored() {
		t.Fatal("une lecture complète réussie coupe le chemin PostgreSQL")
	}
	// Après un redémarrage, l'état est relu de la base.
	releasedb.SetAlkoMirrorReady(false)
	if err := e.svc.Start(); err != nil {
		t.Fatal(err)
	}
	e.svc.Close()
	if !releasedb.AlkoPostgresIgnored() {
		t.Fatal("état perdu au redémarrage")
	}
	// Source désactivée ou sans clé : une seule définition.
	for _, vals := range []map[string]string{{"PSE_ALKO": "off", "PSE_ALKO_KEY": testKey}, {"PSE_ALKO": "on"}, {"PSE_ALKO": "on", "PSE_ALKO_KEY": testKey, "PSE_ALKO_URL": "ftp://x"}} {
		pseenv.Replace(vals)
		if releasedb.AlkoAPIEnabled() || LoadSettings().Enabled || releasedb.AlkoPostgresIgnored() {
			t.Fatalf("%v : source considérée active", vals)
		}
	}
}

func TestFirstFullReadDropsPostgresDuplicates(t *testing.T) {
	e := newEnv(t)
	film := e.catalog("movie", 42, "Film")
	t.Cleanup(func() { releasedb.SetAlkoMirrorReady(false) })
	const pgName = "StreamFusion Reborn (Alkodiques)"
	ins := `INSERT INTO pse_releases(id,title_key,media_type,source_id,source_name,protocol,name,size,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?,1,1)`
	for _, r := range [][]any{
		{"pg-alko", int64(film), "movie", "sfr-db", pgName, "http", "Film.2020.FRENCH.mkv", 1},
		{"pg-torrent", int64(film), "movie", "sfr-db", "StreamFusion Reborn", "torrent", "Film.2020.FRENCH.1080p", 1},
	} {
		if _, err := e.db.Exec(ins, r...); err != nil {
			t.Fatal(err)
		}
	}
	lib := `INSERT INTO pse_release_library(title_key,release_id,source_id,source_signature,payload,first_seen,last_seen,expires) VALUES(?,?,?,?,?,1,1,1)`
	for _, r := range [][]any{
		{int64(film), "sfr-alko:" + testID(9), "remix-sfr-search", "s", "p"},
		{int64(film), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "remix-sfr-search", "s", "p"},
	} {
		if _, err := e.db.Exec(lib, r...); err != nil {
			t.Fatal(err)
		}
	}
	e.f.put(movie(1, 42, movieName), "I")
	e.mustSync()
	if e.count(`SELECT COUNT(*) FROM pse_releases WHERE source_id='sfr-db' AND source_name=?`, pgName) != 0 {
		t.Fatal("doublon PostgreSQL conservé")
	}
	if e.count(`SELECT COUNT(*) FROM pse_releases WHERE id='pg-torrent'`) != 1 || e.count(`SELECT COUNT(*) FROM pse_release_library WHERE release_id LIKE 'aaaa%'`) != 1 {
		t.Fatal("un torrent SFR a été touché")
	}
	if e.count(`SELECT COUNT(*) FROM pse_release_library WHERE release_id LIKE 'sfr-alko:%' AND source_id='remix-sfr-search'`) != 0 {
		t.Fatal("référence PostgreSQL conservée")
	}
	if e.releases(film) != 1 {
		t.Fatal("la source API n'a pas publié")
	}
}

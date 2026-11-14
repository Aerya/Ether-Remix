package ftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

const fileSize = 40 << 20 // au-delà du minimum d'Ether pour un flux HTTP (32 Mo)

var quiet = slog.New(slog.NewTextHandler(logOut(), &slog.HandlerOptions{Level: slog.LevelDebug}))

func logOut() io.Writer {
	if os.Getenv("FTP_TEST_LOG") != "" {
		return os.Stderr
	}
	return io.Discard
}

// content est le contenu déterministe d'un fichier de test : chaque octet
// dépend de sa position, une lecture décalée se voit donc tout de suite.
func content(n int, seed uint64) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(seed, 7))
	for i := 0; i < n; i += 8 {
		v := r.Uint64()
		for j := 0; j < 8 && i+j < n; j++ {
			b[i+j] = byte(v >> (8 * j))
		}
	}
	return b
}

// tree crée l'arborescence du serveur et renvoie le contenu des vidéos.
func tree(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv":      content(fileSize, 1),
		"series/Ma Serie/Saison 1/Ma.Serie.S01E02.FRENCH.720p.WEB.mkv": content(fileSize, 2),
		"films/Autre.Film.2019.1080p.BluRay.x265-GRP/a1b2c3.mkv":       content(fileSize, 3),
	}
	small := map[string][]byte{
		"films/Petit.Film.2020.1080p.mkv":                   []byte("trop petit"),
		"films/Le.Grand.Film.2021.sample.mkv":               content(minVideoSize, 4),
		"films/.cache/Cache.Film.2018.1080p.mkv":            content(minVideoSize, 5),
		"films/incomplete/Pas.Fini.2022.1080p.mkv":          content(minVideoSize, 6),
		"films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264.nfo": []byte("nfo"),
	}
	for rel, b := range files {
		write(t, root, rel, b)
	}
	for rel, b := range small {
		write(t, root, rel, b)
	}
	return root, files
}

func write(t *testing.T, root, rel string, b []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sftpSettings(addr, fp string) Settings {
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return Settings{Enabled: true, Protocol: ProtoSFTP, Host: host, Port: p, User: testUser, Password: testPass, HostKey: fp,
		Roots: []string{"/films", "/series"}, Exclude: []string{"incomplete"}, Depth: 6, ScanEvery: time.Hour, Mode: ModeStream,
		Connections: 4, MaxActive: 1, Retention: time.Hour, CacheBytes: 1 << 40}
}

func ftpSettings(addr string) Settings {
	s := sftpSettings(addr, "")
	s.Protocol = ProtoFTP
	return s
}

func newService(t *testing.T, s Settings) *Service {
	t.Helper()
	svc := &Service{Settings: s, Log: quiet}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if _, err := svc.Rescan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func movieReq() core.SearchRequest {
	return core.SearchRequest{Type: core.TypeMovie, Title: "Le Grand Film", Year: 2021}
}

func one(t *testing.T, svc *Service, req core.SearchRequest) core.Release {
	t.Helper()
	rels, err := svc.Search(context.Background(), req, nil)
	if err != nil || len(rels) != 1 {
		t.Fatalf("recherche %+v : %d résultats, %v", req, len(rels), err)
	}
	return rels[0]
}

func TestSFTPRefusesUnknownOrChangedHostKey(t *testing.T) {
	root, _ := tree(t)
	srv := startSFTP(t, root)
	ctx := ctxT(t)
	for _, fp := range []string{"", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		s := sftpSettings(srv.addr, fp)
		rm, err := NewRemote(s)
		if err != nil {
			t.Fatal(err)
		}
		_, err = rm.ReadDir(ctx, "/films")
		var hk *HostKeyError
		if !errors.As(err, &hk) || hk.Seen != srv.fingerprint {
			t.Fatalf("empreinte %q : erreur %v, attendu un refus d'empreinte", fp, err)
		}
		_ = rm.Close()
	}
	// Le test de connexion montre l'empreinte vue, sans l'accepter.
	res := Test(ctx, sftpSettings(srv.addr, ""))
	if res.OK || res.Fingerprint != srv.fingerprint || !strings.Contains(res.Message, srv.fingerprint) {
		t.Fatalf("test sans empreinte : %+v", res)
	}
	if s := sftpSettings(srv.addr, ""); s.Problem() == "" {
		t.Fatal("SFTP sans empreinte considéré complet")
	}
	res = Test(ctx, sftpSettings(srv.addr, srv.fingerprint))
	if !res.OK || res.Entries == 0 {
		t.Fatalf("test avec empreinte : %+v", res)
	}
	bad := sftpSettings(srv.addr, srv.fingerprint)
	bad.Password = "mauvais-mot-de-passe"
	if res := Test(ctx, bad); res.OK || strings.Contains(res.Message, bad.Password) || !strings.Contains(res.Message, "Authentification refusée") {
		t.Fatalf("mauvais mot de passe : %+v", res)
	}
}

func TestScanFindsMoviesAndEpisodes(t *testing.T) {
	root, _ := tree(t)
	srv := startSFTP(t, root)
	svc := newService(t, sftpSettings(srv.addr, srv.fingerprint))
	st := svc.Status(context.Background())
	if st.Files != 3 || st.Movies != 2 || st.Episodes != 1 {
		t.Fatalf("index : %+v", st)
	}
	rel := one(t, svc, movieReq())
	if rel.Size != fileSize || rel.Protocol != core.ProtocolHTTP || !strings.HasPrefix(rel.StreamURL, PlaceholderURL) {
		t.Fatalf("release : %+v", rel)
	}
	if rel.Info.Height != 1080 {
		t.Fatalf("qualité non lue : %+v", rel.Info)
	}
	ep := one(t, svc, core.SearchRequest{Type: core.TypeEpisode, Title: "Ma Serie", Season: 1, Episode: 2})
	if !strings.Contains(ep.Name, "S01E02") {
		t.Fatalf("épisode : %s", ep.Name)
	}
	// Fichier au nom anonymisé : le nom du dossier de release sert.
	other := one(t, svc, core.SearchRequest{Type: core.TypeMovie, Title: "Autre Film", Year: 2019})
	if !strings.HasPrefix(other.Name, "Autre.Film.2019") {
		t.Fatalf("nom tiré du dossier : %s", other.Name)
	}
	for _, req := range []core.SearchRequest{
		{Type: core.TypeMovie, Title: "Le Grand Film", Year: 2015},
		{Type: core.TypeEpisode, Title: "Ma Serie", Season: 1, Episode: 3},
		{Type: core.TypeMovie, Title: "Pas Fini", Year: 2022},
		{Type: core.TypeMovie, Title: "Cache Film", Year: 2018},
	} {
		if rels, _ := svc.Search(context.Background(), req, nil); len(rels) != 0 {
			t.Fatalf("%+v : %d résultats inattendus", req, len(rels))
		}
	}
	if res := svc.ManualSearch("grand film"); len(res) != 1 || res[0].State != "available" {
		t.Fatalf("recherche manuelle : %+v", res)
	}
}

func TestRescanKeepsFilesOfUnreadableFolderAndDropsRemovedOnes(t *testing.T) {
	root, _ := tree(t)
	srv := startSFTP(t, root)
	svc := newService(t, sftpSettings(srv.addr, srv.fingerprint))
	if err := os.Remove(filepath.Join(root, "films", "Autre.Film.2019.1080p.BluRay.x265-GRP", "a1b2c3.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "series"), filepath.Join(root, "ailleurs")); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Rescan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 1 || res.Failed[0] != "/series" {
		t.Fatalf("dossiers illisibles : %+v", res)
	}
	if rels, _ := svc.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Autre Film", Year: 2019}, nil); len(rels) != 0 {
		t.Fatal("fichier retiré du serveur toujours proposé")
	}
	one(t, svc, core.SearchRequest{Type: core.TypeEpisode, Title: "Ma Serie", Season: 1, Episode: 2}) // gardé : dossier illisible cette fois
}

func TestLocalServerServesExactRanges(t *testing.T) {
	root, files := tree(t)
	srv := startSFTP(t, root)
	svc := newService(t, sftpSettings(srv.addr, srv.fingerprint))
	want := files["films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"]
	out, handled, err := svc.Prepare(ctxT(t), one(t, svc, movieReq()))
	if err != nil || !handled {
		t.Fatalf("préparation : %v %v", handled, err)
	}
	if !strings.HasPrefix(out.StreamURL, "http://127.0.0.1:") || strings.Contains(out.StreamURL, testPass) || strings.Contains(out.StreamURL, testUser) {
		t.Fatalf("lien local : %s", out.StreamURL)
	}
	get := func(method, rng string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, out.StreamURL, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	for _, c := range []struct {
		rng        string
		start, end int
	}{{"bytes=0-0", 0, 0}, {"bytes=1000-299999", 1000, 299999}, {"bytes=20000000-", 20000000, fileSize - 1}, {"bytes=-4096", fileSize - 4096, fileSize - 1}} {
		resp, b := get(http.MethodGet, c.rng)
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", c.start, c.end, fileSize) {
			t.Fatalf("%s : %d %q", c.rng, resp.StatusCode, resp.Header.Get("Content-Range"))
		}
		if !bytes.Equal(b, want[c.start:c.end+1]) {
			t.Fatalf("%s : octets faux", c.rng)
		}
	}
	if resp, b := get(http.MethodHead, "bytes=0-99"); resp.StatusCode != http.StatusPartialContent || len(b) != 0 || resp.Header.Get("Content-Length") != "100" {
		t.Fatalf("HEAD : %d %d", resp.StatusCode, len(b))
	}
	if resp, _ := get(http.MethodGet, fmt.Sprintf("bytes=%d-", fileSize)); resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("plage hors fichier : %d", resp.StatusCode)
	}
	bad := out.StreamURL[:strings.LastIndex(out.StreamURL, "/")+1] + strings.Repeat("0", 48)
	resp, err := http.Get(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("jeton inconnu : %d", resp.StatusCode)
	}
}

func TestRemovedFileIsExplainedAndForgotten(t *testing.T) {
	root, _ := tree(t)
	srv := startSFTP(t, root)
	svc := newService(t, sftpSettings(srv.addr, srv.fingerprint))
	rel := one(t, svc, movieReq())
	if err := os.Remove(filepath.Join(root, "films", "Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv")); err != nil {
		t.Fatal(err)
	}
	_, handled, err := svc.Prepare(ctxT(t), rel)
	if !handled || !errors.Is(err, core.ErrSourceUnavailable) || !strings.Contains(core.LocalDownloadMessage(err, "fr"), "retiré du serveur") {
		t.Fatalf("fichier retiré : %v %v", handled, err)
	}
	if rels, _ := svc.Search(context.Background(), movieReq(), nil); len(rels) != 0 {
		t.Fatal("fichier retiré toujours dans l'index")
	}
	if _, handled, _ := svc.Prepare(ctxT(t), core.Release{GUID: "xdcc:autre"}); handled {
		t.Fatal("release d'une autre source prise en charge")
	}
}

// etherSource ouvre une release comme Ether à la lecture : DirectPayload
// (préparation de la source puis sonde 0-0), puis le moteur debrid.
func etherSource(t *testing.T, svc *Service, rel core.Release) core.MediaSource {
	t.Helper()
	debrid.SetPSEDirectPrepareFor("ftp", svc.Prepare)
	t.Cleanup(func() { debrid.SetPSEDirectPrepareFor("ftp", nil) })
	dsvc := debrid.NewService(config.Debrid{CachedOnly: true}, debrid.Options{Logger: quiet})
	rel.ID = "ftp-test-" + rel.GUID
	raw, err := dsvc.DirectPayload(ctxT(t), rel)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := debrid.DecodePayload(raw)
	if err != nil || pl.Size != rel.Size || !pl.IsDirect() {
		t.Fatalf("payload : %+v %v", pl, err)
	}
	eng := debrid.NewEngine(dsvc, debrid.EngineOptions{Inner: noInner{}, Logger: quiet, HedgeDelay: hedgeDelay})
	t.Cleanup(func() { _ = eng.Close() })
	src, err := eng.Open(ctxT(t), rel.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	if src.Size() != rel.Size {
		t.Fatalf("taille %d", src.Size())
	}
	return src
}

type noInner struct{}

func (noInner) Open(context.Context, string, []byte) (core.MediaSource, error) {
	return nil, errors.New("pas de Usenet ici")
}
func (noInner) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

func checkRead(t *testing.T, src core.MediaSource, want []byte, off, n int64) time.Duration {
	t.Helper()
	start := time.Now()
	buf := make([]byte, n)
	if _, err := src.ReadAt(buf, off); err != nil && err != io.EOF {
		t.Fatalf("lecture à %d : %v", off, err)
	}
	if !bytes.Equal(buf, want[off:off+n]) {
		t.Fatalf("octets faux à %d", off)
	}
	return time.Since(start)
}

// TestEtherEngineSeeksAnywhereOverSFTP passe par le vrai chemin de lecture
// d'Ether : sauts libres (fin, milieu, retour), y compris après une coupure
// de la connexion SSH.
func TestEtherEngineSeeksAnywhereOverSFTP(t *testing.T) {
	root, files := tree(t)
	srv := startSFTP(t, root)
	svc := newService(t, sftpSettings(srv.addr, srv.fingerprint))
	want := files["films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"]
	src := etherSource(t, svc, one(t, svc, movieReq()))
	var worst time.Duration
	for _, off := range []int64{0, fileSize - 65536, 20 << 20, 1 << 20, 33<<20 + 12345, 5 << 20} {
		worst = max(worst, checkRead(t, src, want, off, 65536))
	}
	t.Logf("saut le plus lent : %s", worst.Round(time.Millisecond))
	if worst > 3*time.Second {
		t.Fatalf("saut trop lent : %s", worst)
	}
	srv.dropAll() // panne réseau : la connexion se refait à la demande suivante
	checkRead(t, src, want, 37<<20, 65536)
	checkRead(t, src, want, 10<<20, 1<<20)
}

// TestEtherEngineReadsOverFTPWithFewConnections : FTP ne fait qu'un
// transfert par connexion ; Remix ne dépasse jamais la limite réglée, même
// quand le moteur d'Ether lit en parallèle, et reprend au milieu (REST).
func TestEtherEngineReadsOverFTPWithFewConnections(t *testing.T) {
	root, files := tree(t)
	srv := startFTP(t, root, 2)
	s := ftpSettings(srv.addr)
	s.Connections = 2
	svc := newService(t, s)
	want := files["films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"]
	src := etherSource(t, svc, one(t, svc, movieReq()))
	// Plages simultanées, comme un client qui lit l'index et la vidéo en
	// même temps (lecture concurrente du moteur, qui n'annule pas les autres).
	cc, ok := src.(interface {
		ReadAtConcurrentContext(context.Context, []byte, int64) (int, error)
	})
	if !ok {
		t.Fatal("lecture concurrente absente du moteur")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			off := int64(i) * 9 << 20
			buf := make([]byte, 2<<20)
			if _, err := cc.ReadAtConcurrentContext(ctxT(t), buf, off); err != nil && err != io.EOF {
				errs <- err
				return
			}
			if !bytes.Equal(buf, want[off:off+int64(len(buf))]) {
				errs <- fmt.Errorf("octets faux à %d", off)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("%v (dernière erreur de la source : %q)", err, svc.Status(context.Background()).Error)
	}
	checkRead(t, src, want, fileSize-65536, 65536)
	if p := srv.peak.Load(); p > 2 {
		t.Fatalf("%d connexions FTP ouvertes, limite 2", p)
	}
}

// TestFTPReusesSequentialTransfer : une plage qui continue exactement la
// précédente reprend le transfert ouvert au lieu d'en rouvrir un.
func TestFTPReusesSequentialTransfer(t *testing.T) {
	root, files := tree(t)
	srv := startFTP(t, root, 0)
	rm := newFTP(ftpSettings(srv.addr))
	defer rm.Close()
	want := files["films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"]
	p := "/films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"
	var off int64
	for i := 0; i < 4; i++ {
		rd, err := rm.Open(context.Background(), p, off)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1<<20)
		if _, err := io.ReadFull(rd, buf); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf, want[off:off+int64(len(buf))]) {
			t.Fatalf("octets faux à %d", off)
		}
		_ = rd.Close()
		off += int64(len(buf))
	}
	if n := srv.transfers.Load(); n != 1 {
		t.Fatalf("%d transferts pour une lecture continue, attendu 1", n)
	}
	rd, err := rm.Open(context.Background(), p, 30<<20) // saut : nouveau transfert
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	if _, err := io.ReadFull(rd, buf); err != nil || !bytes.Equal(buf, want[30<<20:30<<20+4096]) {
		t.Fatalf("lecture après saut : %v", err)
	}
	_ = rd.Close()
	if n := srv.transfers.Load(); n != 2 {
		t.Fatalf("%d transferts, attendu 2", n)
	}
	if _, err := rm.Open(context.Background(), "/films/absent.mkv", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fichier absent : %v", err)
	}
}

func TestDownloadModeCopiesThenServesLocalCopy(t *testing.T) {
	root, files := tree(t)
	srv := startSFTP(t, root)
	s := sftpSettings(srv.addr, srv.fingerprint)
	s.Mode, s.DownloadDir = ModeDownload, filepath.Join(t.TempDir(), "copies")
	svc := newService(t, s)
	svc.Usage = func(string) (int64, int64, error) { return 1 << 40, 1 << 40, nil }
	rel := one(t, svc, movieReq())
	// Vérification de disponibilité : jamais de copie lancée.
	if _, _, err := svc.Prepare(core.WithoutOnDemandStreams(ctxT(t)), rel); !errors.Is(err, core.ErrForegroundOnly) {
		t.Fatalf("vérification : %v", err)
	}
	if len(svc.Status(context.Background()).Tasks) != 0 {
		t.Fatal("copie lancée hors lecture")
	}
	var out core.Release
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		o, _, err := svc.Prepare(ctx, rel)
		cancel()
		if err == nil {
			out = o
			break
		}
		if !errors.Is(err, core.ErrDownloadQueued) || !strings.Contains(core.LocalDownloadMessage(err, "fr"), "Copie depuis le serveur FTP") || time.Now().After(deadline) {
			t.Fatalf("préparation : %v", err)
		}
	}
	local := filepath.Join(s.DownloadDir, "Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv")
	got, err := os.ReadFile(local)
	if err != nil || !bytes.Equal(got, files["films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"]) {
		t.Fatalf("copie : %v", err)
	}
	srv.dropAll()
	_ = srv.ln.Close() // serveur arrêté : la copie locale suffit
	req, _ := http.NewRequest(http.MethodGet, out.StreamURL, nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(b, got[100:200]) {
		t.Fatalf("lecture de la copie : %d", resp.StatusCode)
	}
}

func TestCleanupOnlyRemovesOwnTemporaryCopies(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "bibliotheque")
	svc := &Service{Settings: Settings{DownloadDir: filepath.Join(dir, "copies"), Retention: time.Hour, CacheBytes: 1 << 40}, Log: quiet,
		Protected: func(p string) bool { return strings.HasPrefix(p, lib) }}
	svc.tasks, svc.lives, svc.readers = map[string]*Task{}, map[string]*live{}, map[string]int{}
	old := time.Now().Add(-2 * time.Hour)
	mk := func(id, local string, keep bool) {
		write(t, filepath.Dir(local), filepath.Base(local), []byte("x"))
		svc.tasks[id] = &Task{ID: id, Local: local, Size: 1, State: StateDone, Keep: keep, Finished: old}
	}
	mk("temp", filepath.Join(svc.Settings.DownloadDir, "temp.mkv"), false)
	mk("gardee", filepath.Join(svc.Settings.DownloadDir, "gardee.mkv"), true)
	mk("lue", filepath.Join(svc.Settings.DownloadDir, "lue.mkv"), false)
	mk("ailleurs", filepath.Join(dir, "ailleurs.mkv"), false)
	mk("biblio", filepath.Join(svc.Settings.DownloadDir, "..", "bibliotheque", "film.mkv"), false)
	svc.readers["lue"] = 1
	svc.cleanup()
	for id, gone := range map[string]bool{"temp": true, "gardee": false, "lue": false, "ailleurs": false, "biblio": false} {
		_, err := os.Stat(svc.tasks[id].Local)
		if gone != os.IsNotExist(err) {
			t.Errorf("%s : effacé=%v, attendu %v", id, os.IsNotExist(err), gone)
		}
	}
	if svc.tasks["temp"].State != StateRemoved {
		t.Fatalf("état : %s", svc.tasks["temp"].State)
	}
}

func TestSecretsNeverLeakIntoStatusOrIdentity(t *testing.T) {
	s := sftpSettings("127.0.0.1:2222", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	s.Passphrase = "phrase-de-passe-secrete"
	svc := &Service{Settings: s}
	svc.files, svc.tasks, svc.lives, svc.readers = map[string]*indexed{}, map[string]*Task{}, map[string]*live{}, map[string]int{}
	svc.noteErr(errors.New("auth failed for " + testPass))
	raw := fmt.Sprintf("%+v %s %s", svc.Status(context.Background()), s.Identity(), s.Server())
	for _, secret := range []string{testPass, s.Passphrase} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret visible : %s", raw)
		}
	}
}

func TestSettingsDefaultsAndValidation(t *testing.T) {
	for v, ok := range map[string]bool{"seedbox.exemple.net": true, "192.168.1.10": true, "::1": true, "sftp://hote": false, "hote:22": false, "a b": false} {
		if (ValidateHost(v) == nil) != ok {
			t.Errorf("hôte %q : valide=%v", v, !ok)
		}
	}
	if got := ParseRoots(" films ; /series/ ;; /films"); strings.Join(got, ",") != "/films,/series" {
		t.Fatalf("dossiers : %v", got)
	}
	if NormalizeFingerprint("SHA256:abc") != "" || NormalizeFingerprint(strings.Repeat("AB:", 31)+"AB") != strings.Repeat("ab", 32) {
		t.Fatal("empreintes")
	}
	s := Settings{Protocol: ProtoFTPSImpl, Host: "h"}
	if s.Addr() != "h:990" {
		t.Fatal(s.Addr())
	}
	if ReleaseName("/x/Un.Film.2020.1080p/zz9.mkv") != "Un.Film.2020.1080p.mkv" || ReleaseName("/x/Un.Film.2020.mkv") != "Un.Film.2020.mkv" {
		t.Fatal("nom de release")
	}
}

var hedgeDelay = func() time.Duration {
	if v, err := time.ParseDuration(os.Getenv("FTP_TEST_HEDGE")); err == nil {
		return v
	}
	return 0
}()

// TestFTPWithoutResumeStillCopies : un serveur qui refuse REST ne permet
// pas de sauter dans la vidéo, mais une copie complète reste possible.
func TestFTPWithoutResumeStillCopies(t *testing.T) {
	root, files := tree(t)
	srv := startFTP(t, root, 0)
	srv.noRest = true
	s := ftpSettings(srv.addr)
	s.Mode, s.DownloadDir = ModeBoth, filepath.Join(t.TempDir(), "copies")
	rm := newFTP(s)
	defer rm.Close()
	p := "/films/Le.Grand.Film.2021.MULTi.1080p.WEB.x264-REMIX.mkv"
	if _, err := rm.Open(context.Background(), p, 1<<20); !errors.Is(err, ErrNoResume) {
		t.Fatalf("saut sans REST : %v", err)
	}
	svc := newService(t, s)
	svc.Usage = func(string) (int64, int64, error) { return 1 << 40, 1 << 40, nil }
	if _, err := svc.Grab(context.Background(), s.FileID(p)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if local, ok := svc.localCopy(s.FileID(p)); ok {
			got, _ := os.ReadFile(local)
			if !bytes.Equal(got, files[p[1:]]) {
				t.Fatal("copie fausse")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("copie jamais terminée : %+v", svc.Status(context.Background()).Tasks)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st := svc.Status(context.Background()); len(st.Tasks) != 1 || !st.Tasks[0].Keep || !st.Tasks[0].Manual {
		t.Fatalf("copie manuelle : %+v", st.Tasks)
	}
}

// TestStreamModeWritesNothing : en mode Streaming, aucune copie ne peut
// être lancée, même depuis la console.
func TestStreamModeWritesNothing(t *testing.T) {
	root, _ := tree(t)
	srv := startSFTP(t, root)
	s := sftpSettings(srv.addr, srv.fingerprint)
	s.DownloadDir = filepath.Join(t.TempDir(), "copies")
	svc := newService(t, s)
	rel := one(t, svc, movieReq())
	if _, _, err := svc.Prepare(ctxT(t), rel); err != nil {
		t.Fatal(err)
	}
	id, _ := IDOf(rel)
	if _, err := svc.Grab(context.Background(), id); err == nil {
		t.Fatal("copie lancée en mode Streaming")
	}
	if _, err := os.Stat(s.DownloadDir); !os.IsNotExist(err) {
		t.Fatal("dossier de téléchargement créé en mode Streaming")
	}
}

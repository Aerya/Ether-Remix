package xdcc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// mkv renvoie n octets commençant par un en-tête EBML (Matroska).
func mkv(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0x1A, 0x45, 0xDF, 0xA3})
	for i := 4; i < n; i++ {
		b[i] = byte(i * 7)
	}
	return b
}

func movieReq() core.SearchRequest {
	return core.SearchRequest{Type: core.TypeMovie, Title: "Inception", Year: 2010}
}

func TestTmpNameMatchesGrabscher(t *testing.T) {
	// Nom relevé dans le labo Grabscher (dossier tmp pendant un transfert).
	if got := TmpName("Inception.2010.FRENCH.720p.BluRay.x264-SLOW.mkv", 20971520); got != "inception2010french720pblurayx264slowmkv.20971520" {
		t.Fatal(got)
	}
}

func TestSniff(t *testing.T) {
	box := func(kind string, size int) []byte {
		b := make([]byte, size)
		b[0], b[1], b[2], b[3] = byte(size>>24), byte(size>>16), byte(size>>8), byte(size)
		copy(b[4:], kind)
		return b
	}
	ts := make([]byte, 400)
	ts[0], ts[188], ts[376] = 0x47, 0x47, 0x47
	cases := []struct {
		name string
		data []byte
		want Streamability
	}{
		{"mkv", mkv(64), StreamOK},
		{"ts", ts, StreamOK},
		{"mp4 faststart", append(append(box("ftyp", 24), box("moov", 32)...), box("mdat", 64)...), StreamOK},
		{"mp4 index en fin", append(append(box("ftyp", 24), box("mdat", 64)...), box("moov", 32)...), StreamNo},
		{"avi", append([]byte("RIFF\x00\x00\x00\x00AVI LIST"), make([]byte, 32)...), StreamNo},
		{"trop court", []byte{0x1A, 0x45}, StreamUnknown},
		{"inconnu", bytes.Repeat([]byte("x"), 64), StreamNo},
	}
	for _, c := range cases {
		if got := Sniff(bytes.NewReader(c.data), int64(len(c.data))); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestQueriesAndMatches(t *testing.T) {
	q := Queries(core.SearchRequest{Type: core.TypeMovie, Title: "L'Arnaque", OriginalTitle: "The Sting", Year: 1973})
	if strings.Join(q, "|") != "L Arnaque 1973|The Sting 1973" {
		t.Fatalf("requêtes : %q", q)
	}
	ep := core.SearchRequest{Type: core.TypeEpisode, Title: "Good Show", Season: 1, Episode: 2}
	if got := Queries(ep); len(got) != 1 || got[0] != "Good Show S01E02" {
		t.Fatalf("requête épisode : %q", got)
	}
	cases := []struct {
		name string
		req  core.SearchRequest
		ok   bool
	}{
		{"Inception.2010.MULTi.1080p.BluRay.x264-LAB.mkv", movieReq(), true},
		{"Inception.2013.MULTi.1080p.BluRay.x264-LAB.mkv", movieReq(), false},      // autre année (Ether tolère ±1 an)
		{"Inception.MULTi.1080p.BluRay.x264-LAB.mkv", movieReq(), false},           // sans année
		{"Inception.2010.MULTi.1080p.BluRay.x264-LAB.rar", movieReq(), false},      // archive
		{"Inception.The.Cobol.Job.2010.1080p.WEB.x264-LAB.mkv", movieReq(), false}, // autre titre
		{"Good.Show.S01E02.FRENCH.1080p.WEB.x264-LAB.mkv", ep, true},
		{"Good.Show.S01E03.FRENCH.1080p.WEB.x264-LAB.mkv", ep, false},  // autre épisode
		{"Good.Show.S01.FRENCH.1080p.WEB.x264-LAB.mkv", ep, false},     // saison complète
		{"Other.Show.S01E02.FRENCH.1080p.WEB.x264-LAB.mkv", ep, false}, // autre série
	}
	for _, c := range cases {
		if _, ok := Matches(c.name, c.req); ok != c.ok {
			t.Errorf("%s : %v, attendu %v", c.name, ok, c.ok)
		}
	}
}

func TestClientAuthAndKeyNeverInURL(t *testing.T) {
	f := newFakeXG(t)
	ctx := context.Background()
	good := &Client{Base: f.srv.URL, Key: f.key}
	if _, err := good.Version(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := good.Servers(ctx); err != nil {
		t.Fatal(err)
	}
	bad := &Client{Base: f.srv.URL, Key: "wrong"}
	if _, err := bad.Version(ctx); !errors.Is(err, ErrAuth) {
		t.Fatalf("SAB : %v", err)
	}
	if _, err := bad.Search(ctx, "x", 5); !errors.Is(err, ErrAuth) {
		t.Fatalf("REST : %v", err)
	}
	down := &Client{Base: "http://127.0.0.1:1", Key: f.key}
	_, err := down.Search(ctx, "x", 5)
	if !errors.Is(err, ErrUnreachable) || strings.Contains(err.Error(), f.key) || strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("panne : %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.urls {
		if strings.Contains(u, f.key) || strings.Contains(u, "wrong") {
			t.Fatalf("clé dans une adresse : %s", u)
		}
	}
}

func TestSearchSkipsOfflineExcludedAndFailed(t *testing.T) {
	f := newFakeXG(t)
	on := f.addBot(1, "LAB-GOOD", true)
	off := f.addBot(2, "LAB-OFF", false)
	ex := f.addBot(3, "LAB-SKIP", true)
	good := f.addPacket(1, on, "Inception.2010.MULTi.1080p.BluRay.x264-LAB.mkv", mkv(1024))
	f.addPacket(2, off, "Inception.2010.FRENCH.720p.BluRay.x264-OFF.mkv", mkv(1024))
	f.addPacket(3, ex, "Inception.2010.FRENCH.2160p.BluRay.x265-SKIP.mkv", mkv(1024))
	failed := f.addPacket(4, on, "Inception.2010.VOSTFR.1080p.WEB.x264-LAB.mkv", mkv(1024))
	s := f.service(ModeStream)
	s.Settings.ExcludeBots = []string{"lab-skip"}
	s.init()
	s.fails[failed] = failure{at: time.Now(), reason: "did not answer"}
	reserved := 0
	rels, err := s.Search(context.Background(), movieReq(), func(context.Context, string) error { reserved++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].GUID != GUIDPrefix+good || rels[0].Protocol != core.ProtocolHTTP || rels[0].Size != 1024 {
		t.Fatalf("résultats : %+v", rels)
	}
	if reserved != 1 {
		t.Fatalf("budget réservé %d fois", reserved)
	}
	if f.addurlCount() != 0 {
		t.Fatal("une recherche a lancé un téléchargement")
	}
}

// playCtx est le contexte d'une lecture (Play).
func playCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func release(packet, name string, size int) core.Release {
	return core.Release{GUID: GUIDPrefix + packet, Name: name, Size: int64(size), Protocol: core.ProtocolHTTP, StreamURL: PlaceholderURL + packet}
}

func TestPrepareNeverStartsOutsidePlayback(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB-GOOD", true)
	name := "Inception.2010.MULTi.1080p.BluRay.x264-LAB.mkv"
	p := f.addPacket(1, bot, name, mkv(4<<20))
	s := f.service(ModeStream)
	s.init()
	for label, ctx := range map[string]context.Context{
		"vérification de disponibilité": core.WithOnDemandStreamsForAvailability(context.Background()),
		"travail sans lecture":          core.WithoutOnDemandStreams(context.Background()),
	} {
		if _, _, err := s.Prepare(ctx, release(p, name, 4<<20)); !errors.Is(err, core.ErrForegroundOnly) {
			t.Errorf("%s : %v", label, err)
		}
	}
	if f.addurlCount() != 0 {
		t.Fatal("téléchargement lancé hors lecture")
	}
	// Une release d'une autre source n'est pas touchée.
	if out, handled, err := s.Prepare(context.Background(), core.Release{GUID: "other", StreamURL: "http://x"}); handled || err != nil || out.StreamURL != "http://x" {
		t.Fatal("release étrangère modifiée")
	}
}

func TestConcurrentPlaysShareOneDownload(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB-GOOD", true)
	name := "Inception.2010.MULTi.1080p.BluRay.x264-LAB.mkv"
	p := f.addPacket(1, bot, name, mkv(4<<20))
	s := f.service(ModeStream)
	s.init()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.ensure(playCtx(t), p, true, startOptions{name: name, size: 4 << 20})
		}()
	}
	wg.Wait()
	if n := f.addurlCount(); n != 1 {
		t.Fatalf("%d demandes au bot pour un seul paquet", n)
	}
	s.mu.Lock()
	tasks := len(s.tasks)
	s.mu.Unlock()
	if tasks != 1 {
		t.Fatalf("%d tâches", tasks)
	}
}

func TestEnsureChecksPacketAndLimits(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB-GOOD", true)
	off := f.addBot(2, "LAB-OFF", false)
	a := f.addPacket(1, bot, "A.2010.1080p.mkv", mkv(1<<20))
	b := f.addPacket(2, bot, "B.2010.1080p.mkv", mkv(1<<20))
	c := f.addPacket(3, bot, "C.2010.1080p.mkv", mkv(1<<20))
	d := f.addPacket(4, off, "D.2010.1080p.mkv", mkv(1<<20))
	s := f.service(ModeStream)
	s.init()
	ctx := playCtx(t)
	// Le bot propose un autre fichier sous ce numéro : rien n'est demandé.
	if _, err := s.ensure(ctx, a, true, startOptions{name: "Autre.mkv"}); err == nil || core.LocalDownloadMessage(err, "fr") == "" {
		t.Fatalf("numéro réutilisé : %v", err)
	}
	if _, err := s.ensure(ctx, d, true, startOptions{}); err == nil || !strings.Contains(core.LocalDownloadMessage(err, "fr"), "hors ligne") {
		t.Fatalf("bot hors ligne : %v", err)
	}
	for _, p := range []string{a, b} {
		if _, err := s.ensure(ctx, p, true, startOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.ensure(ctx, c, true, startOptions{})
	if !errors.Is(err, core.ErrDownloadLimit) || !strings.Contains(core.LocalDownloadMessage(err, "en"), "already running") {
		t.Fatalf("plafond de téléchargements simultanés : %v", err)
	}
	s.Usage = func(string) (int64, int64, error) { return 100 << 30, 1 << 30, nil }
	s.Settings.MaxActive = 10
	s.Settings.MinFree = 20 << 30
	if _, err := s.ensure(ctx, c, true, startOptions{}); !errors.Is(err, core.ErrLocalNoSpace) {
		t.Fatalf("place libre : %v", err)
	}
	if f.addurlCount() != 2 {
		t.Fatalf("%d demandes", f.addurlCount())
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		h           string
		start, end  int64
		partial, ok bool
	}{
		{"", 0, 99, false, true},
		{"bytes=0-0", 0, 0, true, true},
		{"bytes=10-", 10, 99, true, true},
		{"bytes=10-1000", 10, 99, true, true},
		{"bytes=-5", 95, 99, true, true},
		{"bytes=100-", 0, 0, false, false},
		{"bytes=5-2", 0, 0, false, false},
		{"bytes=0-1,5-6", 0, 0, false, false},
		{"items=0-1", 0, 0, false, false},
	}
	for _, c := range cases {
		s, e, p, ok := parseRange(c.h, 100)
		if ok != c.ok || (ok && (s != c.start || e != c.end || p != c.partial)) {
			t.Errorf("%q : %d-%d %v %v", c.h, s, e, p, ok)
		}
	}
}

// streamFixture prépare un paquet en cours de transfert et sa tâche.
func streamFixture(t *testing.T, mode string, size int) (*fakeXG, *Service, string, string) {
	t.Helper()
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB-SLOW", true)
	name := "Inception.2010.FRENCH.720p.BluRay.x264-SLOW.mkv"
	p := f.addPacket(1, bot, name, mkv(size))
	s := f.service(mode)
	s.init()
	if _, err := s.ensure(playCtx(t), p, true, startOptions{name: name, size: int64(size)}); err != nil {
		t.Fatal(err)
	}
	return f, s, p, name
}

func get(t *testing.T, s *Service, packet, rng string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x/tok", nil)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	w := httptest.NewRecorder()
	s.serveFile(w, req, packet)
	return w
}

func TestServeGrowingFileRanges(t *testing.T) {
	const size = 8 << 20
	f, s, p, _ := streamFixture(t, ModeStream, size)
	f.receive(p, 2<<20)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	content := f.packets[p].content

	// Sonde d'Ether : un octet, taille finale exacte dans Content-Range.
	w := get(t, s, p, "bytes=0-0")
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-0/%d", size) || w.Body.Len() != 1 {
		t.Fatalf("sonde : %d %q %d", w.Code, w.Header().Get("Content-Range"), w.Body.Len())
	}
	// Une plage reçue : octets exacts.
	w = get(t, s, p, "bytes=1000-1999")
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), content[1000:2000]) {
		t.Fatalf("plage reçue : %d", w.Code)
	}
	// Fin du fichier (index MKV, moov) pas encore reçue et bot trop lent
	// pour le délai : refus immédiat, sans attendre ni inventer d'octets.
	f.mu.Lock()
	f.packets[p].Speed = 1 << 10
	f.mu.Unlock()
	_ = s.refresh(context.Background(), true)
	start := time.Now()
	w = get(t, s, p, "bytes=-4096")
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "" || time.Since(start) > time.Second {
		t.Fatalf("fin non reçue : %d en %s", w.Code, time.Since(start))
	}
	// Plage juste après la partie reçue, bot assez rapide : attente puis
	// octets envoyés au fil de leur arrivée.
	f.mu.Lock()
	f.packets[p].Speed = 8 << 20
	f.mu.Unlock()
	_ = s.refresh(context.Background(), true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		f.receive(p, 1<<20)
	}()
	w = get(t, s, p, fmt.Sprintf("bytes=%d-%d", 2<<20, 2<<20+65535))
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), content[2<<20:2<<20+65536]) {
		t.Fatalf("plage attendue : %d, %d octets", w.Code, w.Body.Len())
	}
	// Une plage qui déborde de la partie reçue : la réponse annonce la plage
	// complète, envoie ce qui arrive, puis se coupe si le bot se tait (le
	// moteur reprend depuis l'octet manquant) ; jamais d'octets inventés.
	w = get(t, s, p, fmt.Sprintf("bytes=%d-", 3<<20-1024))
	got := w.Body.Bytes()
	if len(got) != 1024 || !bytes.Equal(got, content[3<<20-1024:3<<20]) {
		t.Fatalf("plage coupée : %d octets", len(got))
	}
	if w.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", 3<<20-1024, size-1, size) {
		t.Fatalf("Content-Range : %q", w.Header().Get("Content-Range"))
	}
	if w := get(t, s, p, fmt.Sprintf("bytes=%d-", size)); w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("hors fichier : %d", w.Code)
	}
}

func TestServeCompletedFile(t *testing.T) {
	const size = 2 << 20
	f, s, p, _ := streamFixture(t, ModeDownload, size)
	f.complete(p)
	if err := s.refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	w := get(t, s, p, "bytes=-100")
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), f.packets[p].content[size-100:]) {
		t.Fatalf("fichier complet : %d", w.Code)
	}
	w = get(t, s, p, "")
	if w.Code != http.StatusOK || w.Body.Len() != size {
		t.Fatalf("sans Range : %d %d", w.Code, w.Body.Len())
	}
}

func TestPrepareStreamsAfterBufferAndServesLocally(t *testing.T) {
	const size = 8 << 20
	f, s, p, name := streamFixture(t, ModeStream, size)
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(200 * time.Millisecond)
			f.receive(p, 512<<10)
		}
	}()
	out, handled, err := s.Prepare(playCtx(t), release(p, name, size))
	if !handled || err != nil {
		t.Fatalf("préparation : %v %v", handled, err)
	}
	if !strings.HasPrefix(out.StreamURL, "http://127.0.0.1:") || !strings.Contains(out.StreamURL, "/x/") || out.StreamOrigin == "" {
		t.Fatalf("lien local : %q", out.StreamURL)
	}
	defer s.Close()
	req, _ := http.NewRequest(http.MethodGet, out.StreamURL, nil)
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, f.packets[p].content[:1024]) {
		t.Fatalf("lecture locale : %d", resp.StatusCode)
	}
	// Un jeton inconnu n'ouvre rien.
	bad := out.StreamURL[:strings.LastIndex(out.StreamURL, "/")+1] + "inconnu"
	resp, err = http.Get(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("jeton inconnu : %d", resp.StatusCode)
	}
}

func TestPrepareDownloadModeWaitsForCompleteFile(t *testing.T) {
	const size = 4 << 20
	f, s, p, name := streamFixture(t, ModeDownload, size)
	f.receive(p, 3<<20)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second) // attente bornée à 2 s
	defer cancel()
	_, _, err := s.Prepare(ctx, release(p, name, size))
	if !errors.Is(err, core.ErrDownloadQueued) || !errors.Is(err, core.ErrPrepareSlow) {
		t.Fatalf("mode téléchargement : %v", err)
	}
	if msg := core.LocalDownloadMessage(err, "fr"); !strings.Contains(msg, "Téléchargement XDCC en cours") {
		t.Fatalf("message : %q", msg)
	}
	f.complete(p)
	out, _, err := s.Prepare(playCtx(t), release(p, name, size))
	if err != nil || out.StreamURL == "" {
		t.Fatalf("fichier complet : %v", err)
	}
	s.Close()
}

func TestNonStreamableFormatWaits(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB", true)
	name := "Inception.2010.FRENCH.720p.BluRay.x264-LAB.avi"
	data := append([]byte("RIFF\x00\x00\x00\x00AVI LIST"), make([]byte, 4<<20)...)
	p := f.addPacket(1, bot, name, data)
	s := f.service(ModeStream)
	s.init()
	if _, err := s.ensure(playCtx(t), p, true, startOptions{}); err != nil {
		t.Fatal(err)
	}
	f.receive(p, 2<<20)
	_, _, err := s.Prepare(playCtx(t), release(p, name, len(data)))
	if !strings.Contains(core.LocalDownloadMessage(err, "fr"), "ne se lit qu'une fois complet") {
		t.Fatalf("AVI en cours : %v", err)
	}
}

func TestFailureIsExplainedAndRemembered(t *testing.T) {
	f, s, p, name := streamFixture(t, ModeStream, 4<<20)
	f.fail(p, "the bot only offers passive DCC, set XG_PASSIVE_DCC_PORTS")
	_, _, err := s.Prepare(playCtx(t), release(p, name, 4<<20))
	if !errors.Is(err, core.ErrSourceUnavailable) || !strings.Contains(core.LocalDownloadMessage(err, "fr"), "DCC passif") {
		t.Fatalf("échec : %v", err)
	}
	// La recherche écarte ensuite le paquet et son bot.
	rels, _ := s.Search(context.Background(), movieReq(), nil)
	if len(rels) != 0 {
		t.Fatalf("paquet en échec encore proposé : %d", len(rels))
	}
	// Une lecture ne relance pas l'échec tout de suite.
	before := f.addurlCount()
	_, _, _ = s.Prepare(playCtx(t), release(p, name, 4<<20))
	if f.addurlCount() != before {
		t.Fatal("échec relancé aussitôt")
	}
}

func TestCleanKeepsKeptReadingAndLibraryFiles(t *testing.T) {
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB", true)
	s := f.service(ModeStream)
	s.Settings.MaxActive = 10
	s.init()
	ctx := playCtx(t)
	mk := func(n int, keep bool) (string, string) {
		p := f.addPacket(n, bot, fmt.Sprintf("Movie%d.2010.1080p.mkv", n), mkv(1<<20))
		if _, err := s.ensure(ctx, p, true, startOptions{keep: keep}); err != nil {
			t.Fatal(err)
		}
		return p, f.complete(p)
	}
	expired, expiredPath := mk(1, false)
	kept, keptPath := mk(2, true)
	reading, readingPath := mk(3, false)
	library, libraryPath := mk(4, false)
	if err := s.refresh(ctx, true); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	s.mu.Lock()
	for _, p := range []string{expired, kept, reading, library} {
		s.tasks[p].Done = old
	}
	s.readers[reading] = 1
	s.mu.Unlock()
	s.Protected = func(local string) bool { return local == libraryPath }
	s.clean(ctx, true)
	if localSize(expiredPath) >= 0 {
		t.Fatal("fichier expiré du cache gardé")
	}
	for _, p := range []string{keptPath, readingPath, libraryPath} {
		if localSize(p) < 0 {
			t.Fatalf("%s effacé", p)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[expired].State != StateRemoved || !s.tasks[library].Keep {
		t.Fatalf("états : %s, bibliothèque gardée %v", s.tasks[expired].State, s.tasks[library].Keep)
	}
}

func TestCancelStopsAndDeletesPartial(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 4<<20)
	f.receive(p, 1<<20)
	tmp := f.tmpPath(f.packets[p])
	if err := s.Cancel(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if localSize(tmp) >= 0 {
		t.Fatal("fichier partiel gardé")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[p].State != StateCancelled {
		t.Fatal(s.tasks[p].State)
	}
}

func TestTasksPersist(t *testing.T) {
	db := store.OpenTest(t)
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB", true)
	p := f.addPacket(1, bot, "Inception.2010.1080p.mkv", mkv(1<<20))
	s := f.service(ModeBoth)
	s.DB = db
	s.Start()
	if _, err := s.ensure(playCtx(t), p, true, startOptions{keep: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // enregistrement asynchrone
	s.Close()
	again := f.service(ModeBoth)
	again.DB = db
	again.Start()
	defer again.Close()
	again.mu.Lock()
	defer again.mu.Unlock()
	if tk := again.tasks[p]; tk == nil || !tk.Keep || tk.NZO == "" {
		t.Fatalf("tâche perdue au redémarrage : %+v", tk)
	}
}

func TestStatusAndTest(t *testing.T) {
	f, s, p, _ := streamFixture(t, ModeStream, 4<<20)
	f.receive(p, 2<<20)
	st := s.Status(context.Background())
	if !st.Reachable || !st.ReadyOK || !st.TempOK || len(st.Tasks) != 1 || st.Tasks[0].State != "playable" {
		t.Fatalf("état : %+v", st)
	}
	msg, err := Test(context.Background(), s.Settings, s.Client)
	if err != nil || !strings.Contains(msg, "lecture progressive possible") {
		t.Fatalf("test : %q %v", msg, err)
	}
	bad := s.Settings
	bad.PathMap = []Mapping{{Remote: "/autre", Local: "/ailleurs"}}
	if _, err := Test(context.Background(), bad, s.Client); err == nil {
		t.Fatal("dossier invisible non signalé")
	}
}

func TestApproximateAnnouncedSize(t *testing.T) {
	const exact = 5<<20 + 12345
	f := newFakeXG(t)
	bot := f.addBot(1, "LAB", true)
	name := "Inception.2010.MULTi.1080p.BluRay.x264-LAB.mkv"
	p := f.addPacket(1, bot, name, mkv(exact))
	f.announce(p, 5<<20) // « 5M » dans l'annonce du bot
	s := f.service(ModeStream)
	s.init()
	defer s.Close()
	rels, err := s.Search(context.Background(), movieReq(), nil)
	if err != nil || len(rels) != 1 || rels[0].Size != 5<<20 {
		t.Fatalf("recherche : %+v %v", rels, err)
	}
	if _, err := s.ensure(playCtx(t), p, true, startOptions{name: name, size: rels[0].Size}); err != nil {
		t.Fatal(err)
	}
	f.receive(p, 2<<20)
	_ = s.refresh(context.Background(), true)
	w := get(t, s, p, "bytes=0-0")
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-0/%d", exact) {
		t.Fatalf("taille exacte attendue : %d %q", w.Code, w.Header().Get("Content-Range"))
	}
	// Une seconde lecture (taille de la release toujours arrondie) rejoint
	// la même tâche au lieu de croire à un autre fichier.
	if _, _, err := s.Prepare(playCtx(t), release(p, name, 5<<20)); err != nil {
		t.Fatalf("seconde lecture : %v", err)
	}
	if f.addurlCount() != 1 {
		t.Fatal("tâche dupliquée")
	}
}

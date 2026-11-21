package music

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dz "github.com/Cycl0o0/OpenDeezer/v3/sdk/deezer"
	"golang.org/x/crypto/blowfish"
)

const odARL = "ARL-SECRET-0123456789abcdef"

// odEncrypt chiffre comme le CDN Deezer (BF_CBC_STRIPE) : un bloc de 2048
// octets sur trois est chiffré en CBC, IV fixe 0..7.
func odEncrypt(t *testing.T, id string, plain []byte) []byte {
	t.Helper()
	c, err := blowfish.NewCipher(dz.BlowfishKey(id))
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte(nil), plain...)
	for i := 0; (i+1)*2048 <= len(out); i++ {
		if i%3 != 0 {
			continue
		}
		prev := []byte{0, 1, 2, 3, 4, 5, 6, 7}
		chunk := out[i*2048 : (i+1)*2048]
		for off := 0; off < 2048; off += 8 {
			for k := 0; k < 8; k++ {
				chunk[off+k] ^= prev[k]
			}
			c.Encrypt(chunk[off:off+8], chunk[off:off+8])
			prev = chunk[off : off+8]
		}
	}
	return out
}

// mockDeezerServer simule gw-light, le service média, l'API publique et le CDN.
type mockDeezerServer struct {
	t        *testing.T
	srv      *httptest.Server
	plain    []byte
	cipher   []byte
	premium  bool // le compte annonce web_hq
	preview  bool // pas de flux complet : aperçu seulement
	cutOnce  atomic.Bool
	cutAt    int
	hang     bool
	cdnHits  atomic.Int32
	cdnDone  chan struct{}
	mu       sync.Mutex
	formats  []string
	listens  atomic.Int32
	rangeLog []string
}

func newMockDeezer(t *testing.T, id string, size int) *mockDeezerServer {
	f := &mockDeezerServer{t: t, plain: make([]byte, size), cdnDone: make(chan struct{}, 4)}
	_, _ = rand.Read(f.plain)
	f.cipher = odEncrypt(t, id, f.plain)
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	old := http.DefaultTransport
	addr := f.srv.Listener.Addr().String()
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr) // tout Deezer simulé ici
		},
	}
	t.Cleanup(func() { http.DefaultTransport = old })
	return f
}

func (f *mockDeezerServer) handle(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	switch {
	case strings.HasPrefix(host, "www.deezer.com"):
		method := r.URL.Query().Get("method")
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "deezer.getUserData":
			if !strings.Contains(r.Header.Get("Cookie"), "arl="+odARL) {
				fmt.Fprint(w, `{"error":[],"results":{"checkForm":"","USER":{"USER_ID":0}}}`)
				return
			}
			fmt.Fprintf(w, `{"error":[],"results":{"checkForm":"api","USER":{"USER_ID":42,"BLOG_NAME":"Test","OPTIONS":{"license_token":"lic","web_hq":%v}},"OFFERS":[]}}`, f.premium)
		case "song.getData":
			fmt.Fprint(w, `{"error":[],"results":{"TRACK_TOKEN":"tt","GAIN":"-6.5"}}`)
		case "log.listen":
			f.listens.Add(1)
			fmt.Fprint(w, `{"error":[],"results":true}`)
		default:
			fmt.Fprint(w, `{"error":[],"results":{}}`)
		}
	case strings.HasPrefix(host, "media.deezer.com"):
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Media []struct {
				Formats []struct{ Format string }
			}
		}
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		for _, m := range req.Media {
			for _, ft := range m.Formats {
				f.formats = append(f.formats, ft.Format)
			}
		}
		f.mu.Unlock()
		if f.preview {
			fmt.Fprint(w, `{"data":[{"errors":[{"code":2002,"message":"Track not entitled"}]}]}`)
			return
		}
		fmt.Fprintf(w, `{"data":[{"media":[{"format":"MP3_128","sources":[{"url":"https://cdn.deezer.test/full.mp3"}]}]}]}`)
	case strings.HasPrefix(host, "api.deezer.com"):
		fmt.Fprint(w, `{"preview":"https://cdn.deezer.test/preview.mp3"}`)
	case strings.HasPrefix(host, "cdn.deezer.test"):
		f.cdn(w, r)
	default:
		http.Error(w, "inconnu "+host, 502)
	}
}

func (f *mockDeezerServer) cdn(w http.ResponseWriter, r *http.Request) {
	f.cdnHits.Add(1)
	f.mu.Lock()
	f.rangeLog = append(f.rangeLog, r.Header.Get("Range"))
	f.mu.Unlock()
	if r.URL.Path == "/preview.mp3" {
		http.ServeContent(w, r, "p", time.Time{}, bytes.NewReader(f.cipher[:30000]))
		return
	}
	if f.hang {
		w.Header().Set("Content-Length", fmt.Sprint(len(f.cipher)))
		w.WriteHeader(200)
		_, _ = w.Write(f.cipher[:4096])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		f.cdnDone <- struct{}{}
		return
	}
	if f.cutAt > 0 && f.cutOnce.CompareAndSwap(false, true) {
		// Coupure en cours de transfert : annonce tout, envoie une partie, ferme.
		w.Header().Set("Content-Length", fmt.Sprint(len(f.cipher)))
		w.WriteHeader(200)
		_, _ = w.Write(f.cipher[:f.cutAt])
		w.(http.Flusher).Flush()
		hj, _, _ := w.(http.Hijacker).Hijack()
		hj.Close()
		return
	}
	http.ServeContent(w, r, "full", time.Time{}, bytes.NewReader(f.cipher))
}

func (f *mockDeezerServer) resolver() *OpenDeezer {
	return NewOpenDeezer(odARL, nil)
}

func TestOpenDeezerEncryptionHelperMatchesSDK(t *testing.T) {
	plain := make([]byte, 20000)
	_, _ = rand.Read(plain)
	got, err := dz.DecryptBytes("77", odEncrypt(t, "77", plain))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("le chiffrement de test ne correspond pas au SDK")
	}
}

// Lecture complète, déplacements et Range à travers le relais existant,
// avec compte gratuit : MP3 128 seulement, sans fichier ni téléchargement préalable.
func TestOpenDeezerRelayStreamsRangeAndSeek(t *testing.T) {
	f := newMockDeezer(t, "7", 200_000)
	var logs bytes.Buffer
	s := NewServer(Options{Logger: slog.New(slog.NewTextHandler(&logs, nil)), Source: failoverSource{rs: Resolvers{f.resolver()}}})

	get := func(rng string) (*httptest.ResponseRecorder, error) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/rest/stream", nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		return rec, s.relayAudio(rec, req, 7)
	}
	rec, err := get("")
	if err != nil || rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), f.plain) {
		t.Fatalf("lecture complète : %v %d", err, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" || rec.Header().Get("X-Accent-Audio-Source") != "OpenDeezer" {
		t.Fatalf("en-têtes %v", rec.Header())
	}
	// Plages : début, milieu non aligné (déchiffrement à froid), fin, suffixe.
	for _, c := range []struct {
		hdr      string
		from, to int
	}{
		{"bytes=0-99", 0, 99},
		{"bytes=100000-100999", 100000, 100999},
		{"bytes=2047-2049", 2047, 2049},
		{"bytes=6143-6145", 6143, 6145},
		{"bytes=12288-", 12288, 199999},
		{"bytes=-500", 199500, 199999},
	} {
		rec, err := get(c.hdr)
		if err != nil || rec.Code != http.StatusPartialContent {
			t.Fatalf("%s : %v %d", c.hdr, err, rec.Code)
		}
		if !bytes.Equal(rec.Body.Bytes(), f.plain[c.from:c.to+1]) {
			t.Fatalf("%s : contenu faux", c.hdr)
		}
	}
	// Compte gratuit : jamais de FLAC ni de MP3 320 demandés à Deezer.
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ft := range f.formats {
		if ft == "FLAC" || ft == "MP3_320" {
			t.Fatalf("format non autorisé demandé : %s", ft)
		}
	}
	if len(f.formats) == 0 {
		t.Fatal("aucune résolution")
	}
	// Une écoute est signalée à Deezer par lecture (comptabilisation conservée).
	time.Sleep(50 * time.Millisecond)
	if f.listens.Load() < 1 {
		t.Fatal("écoute non signalée")
	}
	if strings.Contains(logs.String(), odARL) {
		t.Fatal("ARL dans les journaux")
	}
}

func TestOpenDeezerPreviewIsUnavailableAndFallsBack(t *testing.T) {
	f := newMockDeezer(t, "7", 50_000)
	f.preview = true
	_, err := f.resolver().Resolve(context.Background(), Track{ID: 7})
	if !errors.Is(err, errODPreview) && (err == nil || strings.Contains(err.Error(), "30 s") == false && strings.Contains(err.Error(), "indisponible") == false) {
		t.Fatalf("aperçu accepté ou erreur inattendue : %v", err)
	}
	if f.cdnHits.Load() != 0 {
		t.Fatal("l'aperçu a été téléchargé")
	}
	// Repli : la source suivante sert le titre.
	next := openResolver{[]byte("fallback")}
	s := NewServer(Options{Source: failoverSource{rs: Resolvers{f.resolver(), next}}})
	rec := httptest.NewRecorder()
	if err := s.relayAudio(rec, httptest.NewRequest(http.MethodGet, "/rest/stream", nil), 7); err != nil || rec.Body.String() != "fallback" {
		t.Fatalf("repli : %v %q", err, rec.Body.String())
	}
}

// Un aperçu annoncé comme flux complet (fichier trop court pour la durée) est refusé.
func TestOpenDeezerShortFileForLongTrackIsRejected(t *testing.T) {
	f := newMockDeezer(t, "7", 30_000)
	_, err := f.resolver().Resolve(context.Background(), Track{ID: 7, Duration: 240})
	if !errors.Is(err, errODPreview) {
		t.Fatalf("aperçu présenté comme complet : %v", err)
	}
}

func TestOpenDeezerAuthErrorsNeverLeakARL(t *testing.T) {
	newMockDeezer(t, "7", 10_000)
	bad := NewOpenDeezer("mauvais-"+odARL[:8], nil)
	_, err := bad.Resolve(context.Background(), Track{ID: 7})
	if !errors.Is(err, errODSession) {
		t.Fatalf("ARL refusé mal signalé : %v", err)
	}
	if strings.Contains(err.Error(), "mauvais") {
		t.Fatal("ARL dans l'erreur")
	}
	none := NewOpenDeezer("", nil)
	if _, err := none.Resolve(context.Background(), Track{ID: 7}); !errors.Is(err, errODNoARL) {
		t.Fatalf("sans ARL : %v", err)
	}
	// scrub retire l'ARL d'une erreur du SDK.
	o := NewOpenDeezer(odARL, nil)
	if e := o.scrub(errors.New("échec cookie arl=" + odARL)); strings.Contains(e.Error(), odARL) {
		t.Fatal("scrub insuffisant")
	}
	// Statut du compte (bouton de test).
	good := NewOpenDeezer(odARL, nil)
	if offer, err := good.Status(context.Background()); err != nil || offer != "Deezer Free" {
		t.Fatalf("statut : %q %v", offer, err)
	}
}

// Annuler la requête interrompt la lecture et ferme la connexion au CDN.
func TestOpenDeezerCancellationStopsCDNRead(t *testing.T) {
	f := newMockDeezer(t, "7", 300_000)
	f.hang = true
	ctx, cancel := context.WithCancel(context.Background())
	st, err := f.resolver().Resolve(ctx, Track{ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	rc, _, _ := st.Open()
	buf := make([]byte, 4096)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := rc.Read(make([]byte, 1<<16)); errc <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("erreur après annulation : %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lecture non interrompue")
	}
	select {
	case <-f.cdnDone:
	case <-time.After(3 * time.Second):
		t.Fatal("connexion CDN restée ouverte")
	}
	rc.Close()
}

// Coupure du CDN en cours de lecture : reprise Range transparente.
func TestOpenDeezerResumesAfterConnectionDrop(t *testing.T) {
	f := newMockDeezer(t, "7", 120_000)
	f.cutAt = 50_000
	st, err := f.resolver().Resolve(context.Background(), Track{ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	rc, _, _ := st.Open()
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, f.plain) {
		t.Fatalf("reprise : %v, %d/%d octets", err, len(got), len(f.plain))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resumed := false
	for _, r := range f.rangeLog {
		if strings.HasPrefix(r, "bytes=") && r != "bytes=0-" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("aucune reprise Range : %v", f.rangeLog)
	}
}

func TestOpenDeezerDisabledChangesNothing(t *testing.T) {
	base := Settings{Audius: "last"}
	if _, ok := base.NewSource(nil).(*DeezerSource).Streams.(Resolvers); !ok {
		t.Fatal("chaîne de base modifiée")
	}
	// Activé sans ARL, ou désactivé avec ARL : aucune source ajoutée.
	for _, s := range []Settings{{Audius: "last", OpenDeezer: true}, {Audius: "last", OpenDeezerARL: "x"}} {
		rs := s.NewSource(nil).(*DeezerSource).Streams.(Resolvers)
		if len(rs) != 1 {
			t.Fatalf("source ajoutée à tort : %d", len(rs))
		}
	}
	if (Settings{OpenDeezer: true}).Enabled() || (Settings{OpenDeezerARL: "x"}).Enabled() {
		t.Fatal("API montée sans activation complète")
	}
	on := Settings{OpenDeezer: true, OpenDeezerARL: "x", Audius: "last", OpenDeezerOrder: "last"}
	rs := on.NewSource(nil).(*DeezerSource).Streams.(Resolvers)
	if len(rs) != 2 {
		t.Fatalf("secours absent : %d", len(rs))
	}
	if _, ok := rs[1].(*OpenDeezer); !ok {
		t.Fatal("OpenDeezer pas en secours")
	}
	on.OpenDeezerOrder = "first"
	if _, ok := on.NewSource(nil).(*DeezerSource).Streams.(Resolvers)[0].(*OpenDeezer); !ok {
		t.Fatal("OpenDeezer pas en premier")
	}
	if (Settings{OpenDeezer: true, OpenDeezerARL: "x"}).Mode() != "opendeezer" {
		t.Fatal("mode")
	}
}

// openResolver sert des octets en mémoire (source suivante des tests).
type openResolver struct{ b []byte }

func (o openResolver) Resolve(context.Context, Track) (Stream, error) {
	return Stream{Provider: "test", Mime: "audio/mpeg", URL: "test://next", Open: func() (io.ReadSeekCloser, time.Time, error) {
		return nopSeekCloser{bytes.NewReader(o.b)}, time.Time{}, nil
	}}, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

package torrentclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

// fakeClient est un client BitTorrent en mémoire.
type fakeClient struct {
	mu       sync.Mutex
	torrents map[string]*Torrent
	files    map[string][]File
	adds     []AddOptions
	stopped  []string
	notified []string
	selfStop bool // qBittorrent : limites appliquées par le client
	dir      string
	addErr   error
}

func (f *fakeClient) DefaultDir(context.Context) (string, error) { return f.dir, nil }

func newFakeClient() *fakeClient {
	return &fakeClient{torrents: map[string]*Torrent{}, files: map[string][]File{}}
}

func (f *fakeClient) Kind() string                            { return "fake" }
func (f *fakeClient) Version(context.Context) (string, error) { return "1.0", nil }
func (f *fakeClient) Add(_ context.Context, o AddOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	f.adds = append(f.adds, o)
	h := strings.TrimPrefix(o.Magnet, "magnet:?xt=urn:btih:")
	f.torrents[h] = &Torrent{Hash: h, Category: o.Category, Active: true}
	return nil
}
func (f *fakeClient) Get(_ context.Context, hash string) (*Torrent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.torrents[normHash(hash)]; ok {
		c := *t
		return &c, nil
	}
	return nil, nil
}
func (f *fakeClient) Lookup(ctx context.Context, hashes []string) (map[string]Torrent, error) {
	out := map[string]Torrent{}
	for _, h := range hashes {
		if t, _ := f.Get(ctx, h); t != nil {
			out[t.Hash] = *t
		}
	}
	return out, nil
}
func (f *fakeClient) Files(_ context.Context, t Torrent) ([]File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.files[t.Hash], nil
}
func (f *fakeClient) List(_ context.Context, category string) ([]Torrent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Torrent
	for _, t := range f.torrents {
		if t.Category == category {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (f *fakeClient) MarkNotified(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified = append(f.notified, hash)
	f.torrents[hash].Notified = true
	return nil
}
func (f *fakeClient) Stop(_ context.Context, hash string) error {
	if f.selfStop {
		return ErrUnsupported
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, hash)
	f.torrents[hash].Active = false
	return nil
}

func (f *fakeClient) finish(hash, root string, files ...File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.torrents[hash]
	t.Done, t.Root = true, root
	f.files[hash] = files
}

func one(c Client) []*Slot { return []*Slot{{Client: c, Conf: ClientConf{N: 1}}} }

func chooseFirstVideo(fs []debrid.File) (debrid.File, error) {
	for _, f := range fs {
		if strings.HasSuffix(f.Path, ".mkv") {
			return f, nil
		}
	}
	return debrid.File{}, errors.New("aucune vidéo")
}

func TestProviderAddsOnlyForPlaybackAndPlaysWhenDone(t *testing.T) {
	dir := t.TempDir()
	clientRoot := "/downloads/remix"
	if err := os.MkdirAll(filepath.Join(dir, "Film.2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Film.2024", "Film.2024.mkv"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newFakeClient()
	files := &FileServer{}
	defer files.Close()
	p := &Provider{Slots: []*Slot{{Client: c, Conf: ClientConf{N: 1, Ratio: 1, SeedTime: 72 * time.Hour, PathMap: []Mapping{{Client: clientRoot, Local: dir}}}}},
		Files: files, Settings: Settings{Download: true, Music: true, Category: "remix", MaxPerDay: 2}}
	ctx := t.Context()
	magnet := "magnet:?xt=urn:btih:" + hashA

	// Une vérification (cache uniquement) n'ajoute jamais rien.
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashA, Magnet: magnet, CachedOnly: true, Choose: chooseFirstVideo}); !errors.Is(err, core.ErrNotCached) || len(c.adds) != 0 {
		t.Fatalf("vérification : err=%v ajouts=%d", err, len(c.adds))
	}
	// Une lecture demandée l'envoie au client, avec la catégorie et les limites.
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashA, Magnet: magnet, Choose: chooseFirstVideo}); !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("ajout : %v", err)
	}
	if len(c.adds) != 1 || c.adds[0].Category != "remix" || c.adds[0].Ratio != 1 || c.adds[0].SeedTime != 72*time.Hour {
		t.Fatalf("ajouts %+v", c.adds)
	}
	// Pendant le téléchargement : rien de plus.
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashA, Magnet: magnet, TorrentID: hashA, Choose: chooseFirstVideo}); !errors.Is(err, core.ErrNotCached) || len(c.adds) != 1 {
		t.Fatalf("en cours : err=%v ajouts=%d", err, len(c.adds))
	}
	if cached, _ := p.CheckCached(ctx, []string{hashA}); cached[hashA] {
		t.Fatal("incomplet annoncé en cache")
	}

	c.finish(hashA, clientRoot,
		File{Path: clientRoot + "/Film.2024/Film.2024.mkv", Size: 10},
		File{Path: clientRoot + "/Film.2024/info.nfo", Size: 1})
	if cached, _ := p.CheckCached(ctx, []string{hashA}); !cached[hashA] {
		t.Fatal("complet non annoncé")
	}
	acq, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashA, CachedOnly: true, Choose: chooseFirstVideo})
	if err != nil {
		t.Fatal(err)
	}
	if acq.File.Path != "Film.2024/Film.2024.mkv" || acq.TorrentID != hashA || len(acq.Files) != 2 {
		t.Fatalf("fichier %+v", acq)
	}
	link, err := p.Link(ctx, debrid.Handle{TorrentID: acq.TorrentID, FileID: acq.File.ID, Path: acq.File.Path, Link: acq.File.Link, Size: acq.File.Size})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link.URL, "http://127.0.0.1:") || link.Size != 10 {
		t.Fatalf("lien %+v", link)
	}
	req, _ := http.NewRequest(http.MethodGet, link.URL, nil)
	req.Header.Set("Range", "bytes=2-5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("lecture partielle %d %q", resp.StatusCode, body)
	}
	if resp, err := http.Get(strings.TrimSuffix(link.URL, link.URL[len(link.URL)-4:]) + "zzzz"); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("jeton inconnu : %d", resp.StatusCode)
		}
	}
	// Remix ne retire jamais un torrent.
	if err := p.Remove(ctx, hashA); err != nil || len(c.torrents) != 1 {
		t.Fatalf("retrait : %v", err)
	}
}

func TestProviderDailyLimitAndDownloadsOff(t *testing.T) {
	c := newFakeClient()
	p := &Provider{Slots: one(c), Files: &FileServer{}, Settings: Settings{Download: true, Category: "remix", MaxPerDay: 1}}
	ctx := t.Context()
	hashB := strings.Repeat("b", 40)
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashA, Magnet: "magnet:?xt=urn:btih:" + hashA}); !errors.Is(err, core.ErrNotCached) {
		t.Fatal(err)
	}
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashB, Magnet: "magnet:?xt=urn:btih:" + hashB}); !errors.Is(err, core.ErrDownloadLimit) || len(c.adds) != 1 {
		t.Fatalf("plafond : err=%v ajouts=%d", err, len(c.adds))
	}
	p.Settings.Download = false
	if p.LocalDownloads() {
		t.Fatal("téléchargements annoncés")
	}
	hashC := strings.Repeat("c", 40)
	if _, err := p.Acquire(ctx, debrid.AcquireRequest{InfoHash: hashC, Magnet: "magnet:?xt=urn:btih:" + hashC}); !errors.Is(err, core.ErrNotCached) || len(c.adds) != 1 {
		t.Fatalf("désactivé : err=%v ajouts=%d", err, len(c.adds))
	}
}

func TestProviderMusicOneAlbumAtATime(t *testing.T) {
	c := newFakeClient()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	p := &Provider{Slots: one(c), Files: &FileServer{}, Now: func() time.Time { return now },
		Settings: Settings{Download: true, Music: true, Category: "remix", MaxPerDay: 10}}
	ctx := t.Context()
	for i, h := range []string{strings.Repeat("1", 40), strings.Repeat("2", 40)} {
		_, err := p.AcquireMusic(ctx, debrid.AcquireRequest{InfoHash: h, Magnet: "magnet:?xt=urn:btih:" + h}, true)
		if !errors.Is(err, core.ErrNotCached) {
			t.Fatalf("album %d : %v", i, err)
		}
	}
	if len(c.adds) != 1 {
		t.Fatalf("ajouts %d, attendu 1 (un album à la fois)", len(c.adds))
	}
	now = now.Add(3 * time.Minute)
	h := strings.Repeat("3", 40)
	_, _ = p.AcquireMusic(ctx, debrid.AcquireRequest{InfoHash: h, Magnet: "magnet:?xt=urn:btih:" + h}, true)
	if len(c.adds) != 2 {
		t.Fatalf("ajouts %d après le délai, attendu 2", len(c.adds))
	}
}

func TestProviderRefusesFilesOutsideMappedFolders(t *testing.T) {
	c := newFakeClient()
	c.torrents[hashA] = &Torrent{Hash: hashA, Done: true, Root: "/elsewhere"}
	c.files[hashA] = []File{{Path: "/elsewhere/Film.mkv", Size: 4}}
	p := &Provider{Slots: []*Slot{{Client: c, Conf: ClientConf{N: 1, PathMap: []Mapping{{Client: "/downloads", Local: t.TempDir()}}}}}, Files: &FileServer{}}
	if _, err := p.Link(t.Context(), debrid.Handle{TorrentID: hashA, Link: "/elsewhere/Film.mkv"}); err == nil || !strings.Contains(err.Error(), "hors des dossiers partagés") {
		t.Fatalf("err %v", err)
	}
}

func TestMonitorNotifiesCrossSeedOnceAndStopsRTorrentSeeding(t *testing.T) {
	var mu sync.Mutex
	var hooks []string
	xs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/webhook" || r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		hooks = append(hooks, r.PostForm.Get("infoHash"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer xs.Close()
	cs, err := NewCrossSeed(xs.URL, "k", xs.Client())
	if err != nil {
		t.Fatal(err)
	}
	c := newFakeClient()
	fin := time.Now().Add(-100 * time.Hour)
	c.torrents[hashA] = &Torrent{Hash: hashA, Category: "remix", Done: true, Active: true, Ratio: 0.2, Finished: fin, Seeding: 100 * time.Hour}
	hashB := strings.Repeat("b", 40)
	c.torrents[hashB] = &Torrent{Hash: hashB, Category: "remix", Active: true} // en cours
	hashC := strings.Repeat("c", 40)
	c.torrents[hashC] = &Torrent{Hash: hashC, Category: "autre", Done: true, Active: true, Ratio: 5}
	m := &Monitor{XSeed: cs, Provider: &Provider{Slots: []*Slot{{Client: c, Conf: ClientConf{N: 1, Ratio: 1, SeedTime: 72 * time.Hour}}},
		Settings: Settings{Category: "remix"}}}
	m.Round(t.Context())
	m.Round(t.Context())
	if len(hooks) != 1 || hooks[0] != hashA {
		t.Fatalf("cross-seed %v, attendu un seul envoi", hooks)
	}
	if len(c.stopped) != 1 || c.stopped[0] != hashA {
		t.Fatalf("arrêts %v", c.stopped)
	}
	if len(c.torrents) != 3 {
		t.Fatal("un torrent a disparu")
	}
}

func TestSettingsPathMapAndRatio(t *testing.T) {
	m, err := ParsePathMap(" /downloads=/data/torrents ; /downloads/films=/media/films ")
	if err != nil {
		t.Fatal(err)
	}
	s := ClientConf{PathMap: m}
	if got, ok := s.Local("/downloads/films/A/a.mkv"); !ok || got != "/media/films/A/a.mkv" {
		t.Fatalf("plus long préfixe : %q %v", got, ok)
	}
	if got, ok := s.Local("/downloads/series/b.mkv"); !ok || got != "/data/torrents/series/b.mkv" {
		t.Fatalf("préfixe : %q %v", got, ok)
	}
	if _, ok := s.Local("/downloadsX/c.mkv"); ok {
		t.Fatal("préfixe partiel accepté")
	}
	if _, ok := s.Local("/downloads/../etc/passwd"); ok {
		t.Fatal("remontée acceptée")
	}
	if _, err := ParsePathMap("downloads=/x"); err == nil {
		t.Fatal("chemin relatif accepté")
	}
	if r, err := ParseRatio("1,5"); err != nil || r != 1.5 {
		t.Fatalf("ratio %v %v", r, err)
	}
	if _, err := ParseRatio("abc"); err == nil {
		t.Fatal("ratio invalide accepté")
	}
}

package music

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// flakyAccount répond par une panne tant que down est vrai.
type flakyAccount struct {
	mu     sync.Mutex
	down   bool
	cached map[string][]DebridFile
	opened []string
}

func (a *flakyAccount) OpenCached(_ context.Context, h TorrentHit) ([]DebridFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.opened = append(a.opened, h.Magnet)
	if a.down {
		return nil, errors.New("debrid: rate limited")
	}
	if fs, ok := a.cached[h.Magnet]; ok {
		return fs, nil
	}
	return nil, ErrNotCached
}

func (a *flakyAccount) Unlock(_ context.Context, f DebridFile) (Stream, error) {
	return Stream{URL: "https://cdn.example/" + f.Link}, nil
}

func (a *flakyAccount) openedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.opened)
}

type errFinder struct {
	fakeFinder
	err atomic.Pointer[error]
}

func (f *errFinder) FindAlbum(ctx context.Context, q string) ([]TorrentHit, error) {
	if e := f.err.Load(); e != nil {
		f.queries.Add(1)
		return nil, *e
	}
	return f.fakeFinder.FindAlbum(ctx, q)
}

func TestDebridBackoffIsBounded(t *testing.T) {
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		if got := debridBackoff(i + 1); got != w {
			t.Fatalf("échec %d : attente %s, %s attendue", i+1, got, w)
		}
	}
	if debridBackoff(1000) != debridRetryMax {
		t.Fatal("attente non bornée")
	}
}

// Une panne du compte n'est pas une absence : courte attente, puis nouvel
// essai, au lieu de 30 minutes d'« absent ».
func TestDebridOutageIsNotAbsence(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	f := &fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree FLAC", Magnet: "m1"}}}
	acc := &flakyAccount{down: true, cached: map[string][]DebridFile{"m1": {{Path: "01 - Ta fête.flac", Link: "a1"}}}}
	r := &DebridResolver{Finder: f, Account: acc, Now: func() time.Time { return now }}
	_, err := r.Resolve(t.Context(), track(1, 1, "Ta fête"))
	if !errors.Is(err, ErrDebridUnavailable) || errors.Is(err, ErrNotCached) {
		t.Fatalf("panne : %v, ErrDebridUnavailable attendue", err)
	}
	// Pendant l'attente : pas de nouvelle requête au compte.
	if _, err := r.Resolve(t.Context(), track(1, 1, "Ta fête")); !errors.Is(err, ErrDebridUnavailable) {
		t.Fatalf("pendant l'attente : %v", err)
	}
	if n := acc.openedCount(); n != 1 {
		t.Fatalf("compte interrogé %d fois pendant l'attente", n)
	}
	// Seconde panne : l'attente double.
	now = now.Add(debridRetryMin + time.Second)
	if _, err := r.Resolve(t.Context(), track(1, 1, "Ta fête")); !errors.Is(err, ErrDebridUnavailable) {
		t.Fatalf("seconde panne : %v", err)
	}
	now = now.Add(debridRetryMin + time.Second)
	if _, err := r.Resolve(t.Context(), track(1, 1, "Ta fête")); err == nil || acc.openedCount() != 2 {
		t.Fatalf("l'attente doit doubler : %v, %d ouvertures", err, acc.openedCount())
	}
	// Le compte revient : lecture dès la fin de l'attente.
	acc.mu.Lock()
	acc.down = false
	acc.mu.Unlock()
	now = now.Add(2 * debridRetryMin)
	s, err := r.Resolve(t.Context(), track(1, 1, "Ta fête"))
	if err != nil || s.URL != "https://cdn.example/a1" {
		t.Fatalf("après la panne : %+v %v", s, err)
	}
}

func TestDebridIndexerOutageIsNotAbsence(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	f := &errFinder{fakeFinder: fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree FLAC", Magnet: "m1"}}}}
	e := errors.New("prowlarr injoignable")
	f.err.Store(&e)
	acc := &flakyAccount{cached: map[string][]DebridFile{"m1": {{Path: "01 - Ta fête.flac", Link: "a1"}}}}
	r := &DebridResolver{Finder: f, Account: acc, Now: func() time.Time { return now }}
	if _, err := r.Resolve(t.Context(), track(1, 1, "Ta fête")); !errors.Is(err, ErrDebridUnavailable) {
		t.Fatalf("indexeurs en panne : %v", err)
	}
	f.err.Store(nil)
	now = now.Add(debridRetryMin + time.Second)
	if _, err := r.Resolve(t.Context(), track(1, 1, "Ta fête")); err != nil {
		t.Fatalf("indexeurs revenus : %v", err)
	}
}

// Un titre absent du premier album en cache est cherché dans les torrents
// suivants (album partiel, disque séparé), sans rouvrir le premier.
func TestDebridMissingTrackTriesNextTorrent(t *testing.T) {
	f := &fakeFinder{hits: []TorrentHit{
		{Title: "Stromae - Racine Carree [FLAC] CD1", Magnet: "cd1", Seeders: 50},
		{Title: "Stromae - Racine Carree MP3", Magnet: "absent", Seeders: 30},
		{Title: "Stromae - Racine Carree [FLAC] CD2", Magnet: "cd2", Seeders: 20},
	}}
	acc := &flakyAccount{cached: map[string][]DebridFile{
		"cd1": {{Path: "CD1/01 - Ta fête.flac", Link: "d1t1"}, {Path: "CD1/02 - Papaoutai.flac", Link: "d1t2"}},
		"cd2": {{Path: "CD2/01 - Ave Cesaria (live).flac", Link: "d2t1"}},
	}}
	r := &DebridResolver{Finder: f, Account: acc}
	if s, err := r.Resolve(t.Context(), track(1, 2, "Papaoutai")); err != nil || s.URL != "https://cdn.example/d1t2" {
		t.Fatalf("disque 1 : %+v %v", s, err)
	}
	if n := acc.openedCount(); n != 1 {
		t.Fatalf("un seul torrent ouvert attendu, %d", n)
	}
	disc2 := Track{ID: 2, Number: 1, Volume: 2, Title: "Ave Cesaria (live)", Artist: Artist{Name: "Stromae"}, Album: Album{ID: 9, Title: "Racine carrée"}}
	if s, err := r.Resolve(t.Context(), disc2); err != nil || s.URL != "https://cdn.example/d2t1" {
		t.Fatalf("disque 2 dans un autre torrent : %+v %v", s, err)
	}
	// Le FLAC CD2 est classé avant le MP3 : cd1 puis cd2.
	if n := acc.openedCount(); n != 2 {
		t.Fatalf("ouvertures %d, 2 attendues (cd1, cd2)", n)
	}
	// Les deux torrents restent partagés : aucun nouvel accès.
	if _, err := r.Resolve(t.Context(), track(3, 1, "Ta fête")); err != nil || acc.openedCount() != 2 {
		t.Fatalf("titre déjà couvert : %v, %d ouvertures", err, acc.openedCount())
	}
	// Titre introuvable partout : pas de boucle, candidats épuisés.
	if _, err := r.Resolve(t.Context(), track(4, 9, "Inexistant")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("titre introuvable : %v", err)
	}
	if f.queries.Load() != 1 {
		t.Fatalf("une seule recherche d'album attendue : %d", f.queries.Load())
	}
}

// Le nombre de torrents vérifiés reste borné, même titre après titre.
func TestDebridTriesStayBounded(t *testing.T) {
	var hits []TorrentHit
	cached := map[string][]DebridFile{}
	for i := 0; i < 10; i++ {
		m := "m" + string(rune('a'+i))
		hits = append(hits, TorrentHit{Title: "Stromae Racine Carree", Magnet: m, Seeders: 50 - i})
		cached[m] = []DebridFile{{Path: "01 - Autre.flac", Link: m}}
	}
	acc := &flakyAccount{cached: cached}
	r := &DebridResolver{Finder: &fakeFinder{hits: hits}, Account: acc, Tries: 4}
	for i := 0; i < 3; i++ {
		_, _ = r.Resolve(t.Context(), track(int64(10+i), 5, "Introuvable"))
	}
	if n := acc.openedCount(); n != 4 {
		t.Fatalf("%d torrents ouverts, 4 au plus", n)
	}
}

// Un client qui abandonne cesse d'attendre l'album, la recherche partagée
// continue et sert les suivants ; aucune goroutine ne reste bloquée.
func TestDebridWaitIsCancellable(t *testing.T) {
	base := runtime.NumGoroutine()
	f := &slowFinder{fakeFinder: fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree [FLAC]", Magnet: "m2"}}}, release: make(chan struct{})}
	acc := &flakyAccount{cached: map[string][]DebridFile{"m2": {{Path: "a/01 - Ta fête.flac", Link: "a1"}, {Path: "a/02 - Papaoutai.flac", Link: "a2"}}}}
	r := &DebridResolver{Finder: f, Account: acc}

	first := make(chan error, 1)
	go func() {
		_, err := r.Resolve(context.Background(), track(1, 1, "Ta fête"))
		first <- err
	}()
	time.Sleep(20 * time.Millisecond) // la première requête tient l'album

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := r.Resolve(ctx, track(2, 2, "Papaoutai")); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("attente annulée : %v", err)
			}
			if time.Since(start) > time.Second {
				t.Errorf("l'abandon a attendu la recherche")
			}
		}()
	}
	wg.Wait()
	close(f.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if s, err := r.Resolve(context.Background(), track(2, 2, "Papaoutai")); err != nil || s.URL != "https://cdn.example/a2" {
		t.Fatalf("après la recherche : %+v %v", s, err)
	}
	if f.queries.Load() != 1 {
		t.Fatalf("une seule recherche attendue : %d", f.queries.Load())
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines restantes : %d > %d", n, base)
	}
}

// Beaucoup de titres du même album en même temps : une seule recherche.
func TestDebridConcurrentTracksShareSearch(t *testing.T) {
	f := &fakeFinder{hits: []TorrentHit{{Title: "Stromae - Racine Carree [FLAC]", Magnet: "m"}}}
	acc := &flakyAccount{cached: map[string][]DebridFile{"m": {{Path: "01 - Ta fête.flac", Link: "a1"}, {Path: "02 - Papaoutai.flac", Link: "a2"}}}}
	r := &DebridResolver{Finder: f, Account: acc}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Resolve(context.Background(), track(int64(i%2+1), i%2+1, []string{"Ta fête", "Papaoutai"}[i%2])); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.queries.Load() != 1 || acc.openedCount() != 1 {
		t.Fatalf("recherches %d, ouvertures %d", f.queries.Load(), acc.openedCount())
	}
}

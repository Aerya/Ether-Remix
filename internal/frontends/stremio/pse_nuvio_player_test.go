package stremio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type nuvioExperience struct {
	frontendtest.Experience
	mu       sync.Mutex
	progress []frontend.OfflineProgress
	played   []core.RatingKey
	unplayed []core.RatingKey
	watchlst map[core.RatingKey]bool
}

func (e *nuvioExperience) SetOfflineProgress(_ context.Context, _ *core.Viewer, _ core.RatingKey, p frontend.OfflineProgress) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress = append(e.progress, p)
	return nil
}

func newNuvioInstance(t *testing.T, denied bool) (http.Handler, *nuvioExperience) {
	t.Helper()
	pseNuvioRates.Lock()
	pseNuvioRates.last = map[pseNuvioRateKey]time.Time{}
	pseNuvioRates.Unlock()
	e := &nuvioExperience{watchlst: map[core.RatingKey]bool{}}
	e.ResolveExternalFunc = func(_ context.Context, v *core.Viewer, _ core.Device, r frontend.ExternalRequest) (core.RatingKey, error) {
		if denied || r.ID == "tt404" {
			return 0, core.ErrNotFound
		}
		if r.Season != nil {
			return core.EpisodeKey(2, *r.Season, *r.Episode), nil
		}
		if r.Type == core.TypeShow {
			return core.ShowKey(2), nil
		}
		return core.MovieKey(1), nil
	}
	e.SetPlayedFunc = func(_ context.Context, _ *core.Viewer, k core.RatingKey) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.played = append(e.played, k)
		return nil
	}
	e.SetUnplayedFunc = func(_ context.Context, _ *core.Viewer, k core.RatingKey) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.unplayed = append(e.unplayed, k)
		return nil
	}
	e.SetWatchlistFunc = func(_ context.Context, _ *core.Viewer, k core.RatingKey, on bool) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.watchlst[k] = on
		return nil
	}
	host := frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
			if token != "grant" {
				return nil, core.ErrNotFound
			}
			return &frontend.Viewer{ID: 7}, nil
		}},
		Experience: e, Sessions: frontendtest.Sessions{},
	}
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	return inst.Handler(), e
}

func nuvioCall(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("")))
	return rec
}

func TestNuvioExtraDecoding(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?foo=1", nil)
	r.SetPathValue("extra", "action=pause&currentTime=1234&duration=5000.json")
	got := pseNuvioExtra(r)
	if got.Get("action") != "pause" || got.Get("currentTime") != "1234" || got.Get("duration") != "5000" || got.Get("foo") != "1" {
		t.Fatalf("%v", got)
	}
	r = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"action":"watched","videoId":["tt1:1:1","tt1:1:2"],"currentTime":42}`))
	got = pseNuvioExtra(r)
	if got.Get("videoId") != "tt1:1:1,tt1:1:2" || got.Get("currentTime") != "42" {
		t.Fatalf("%v", got)
	}
	enc := url.Values{"action": {"stop"}}.Encode()
	r = httptest.NewRequest(http.MethodGet, "/x", nil)
	r.SetPathValue("extra", enc)
	if pseNuvioExtra(r).Get("action") != "stop" {
		t.Fatal("extra encodé")
	}
}

func TestNuvioManifestsDeclareResources(t *testing.T) {
	h, _ := newNuvioInstance(t, false)
	for _, p := range []string{"/stremio/grant/manifest.json", "/stremio/grant/fichiers/manifest.json"} {
		body := nuvioCall(h, http.MethodGet, p).Body.String()
		if !strings.Contains(body, `"name":"player"`) || !strings.Contains(body, `"name":"library"`) {
			t.Fatalf("%s : %s", p, body)
		}
	}
}

func TestNuvioPlayerStartPauseStop(t *testing.T) {
	h, e := newNuvioInstance(t, false)
	base := "/stremio/grant/player/series/tt0000002:1:3/"
	rec := nuvioCall(h, http.MethodGet, base+"action=start&currentTime=60000&duration=2700000.json")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	// un second « start » dans la foulée est limité
	nuvioCall(h, http.MethodGet, base+"action=start&currentTime=61000&duration=2700000.json")
	if len(e.progress) != 1 {
		t.Fatalf("limité : %d écritures", len(e.progress))
	}
	// pause : toujours écrite, au-delà du débit
	nuvioCall(h, http.MethodPost, base+"action=pause&currentTime=900000&duration=2700000.json")
	// arrêt à 95 % : vu
	nuvioCall(h, http.MethodGet, base+"action=stop&currentTime=2600000&duration=2700000.json")
	if len(e.progress) != 3 {
		t.Fatalf("écritures : %d", len(e.progress))
	}
	if p := e.progress[1]; p.Position != 900*time.Second || p.Duration != 2700*time.Second || p.Played {
		t.Fatalf("pause : %+v", p)
	}
	if p := e.progress[2]; !p.Played {
		t.Fatalf("stop à 95 %% : %+v", p)
	}
	// arrêt à 50 % : pas vu
	nuvioCall(h, http.MethodGet, "/stremio/grant/player/movie/tt0000001/action=stop&currentTime=100&duration=1000.json")
	if p := e.progress[3]; p.Played {
		t.Fatalf("%+v", p)
	}
	// l'extra peut aussi venir de la chaîne de requête, et la route sous /fichiers
	nuvioCall(h, http.MethodGet, "/stremio/grant/fichiers/player/movie/tt0000001.json?action=pause&currentTime=5&duration=10")
	if len(e.progress) != 5 {
		t.Fatalf("variante fichiers : %d", len(e.progress))
	}
}

func TestNuvioPlayerIgnoresUnknownAndUnauthorized(t *testing.T) {
	h, e := newNuvioInstance(t, false)
	for _, p := range []string{
		"/stremio/grant/player/movie/tt404/action=pause&currentTime=5&duration=10.json", // titre inconnu
		"/stremio/grant/player/movie/abc/action=pause&currentTime=5&duration=10.json",   // identifiant invalide
		"/stremio/grant/player/movie/tt1/action=bogus&currentTime=5.json",               // action inconnue
		"/stremio/grant/player/movie/tt1/action=pause&currentTime=-5.json",
	} {
		rec := nuvioCall(h, http.MethodGet, p)
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Fatalf("%s : %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if len(e.progress) != 0 {
		t.Fatal("écriture inattendue")
	}
	if nuvioCall(h, http.MethodGet, "/stremio/nope/player/movie/tt1.json?action=pause&currentTime=5").Code != 401 {
		t.Fatal("jeton refusé attendu")
	}
	// spectateur sans droit sur le titre : l'identifiant ne se résout pas
	h, e = newNuvioInstance(t, true)
	nuvioCall(h, http.MethodGet, "/stremio/grant/player/movie/tt1/action=pause&currentTime=5&duration=10.json")
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/movie/tt1/action=watched.json")
	if len(e.progress) != 0 || len(e.played) != 0 {
		t.Fatal("écriture sans droit")
	}
}

func TestNuvioLibraryActions(t *testing.T) {
	h, e := newNuvioInstance(t, false)
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/series/tt0000002/action=watched&videoId=tt0000002:1:1,tt0000002:1:2,tt404:1:1.json")
	if len(e.played) != 2 || e.played[0] != core.EpisodeKey(2, 1, 1) || e.played[1] != core.EpisodeKey(2, 1, 2) {
		t.Fatalf("vus : %v", e.played)
	}
	// série sans videoId : ignorée ; film sans videoId : marqué
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/series/tt0000002/action=watched.json")
	if len(e.played) != 2 {
		t.Fatalf("série entière marquée : %v", e.played)
	}
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/movie/tt0000001/action=unwatched.json")
	if len(e.unplayed) != 1 || e.unplayed[0] != core.MovieKey(1) {
		t.Fatalf("non vu : %v", e.unplayed)
	}
	nuvioCall(h, http.MethodPost, "/stremio/grant/library/movie/tt0000001/action=libraryAdd.json")
	if !e.watchlst[core.MovieKey(1)] {
		t.Fatal("liste de suivi")
	}
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/movie/tt0000001/action=libraryRemove.json")
	if e.watchlst[core.MovieKey(1)] {
		t.Fatal("retrait")
	}
	// variante avec options d'affichage dans le lien
	nuvioCall(h, http.MethodGet, "/stremio/grant/o/compact-2160.2-1080.3-720.1/library/movie/tt0000001/action=watched.json")
	if len(e.played) != 3 {
		t.Fatalf("variante o/ : %v", e.played)
	}
}

type nuvioBackendFake struct {
	mu       sync.Mutex
	progress []frontend.OfflineProgress
	played   []core.RatingKey
}

func (b *nuvioBackendFake) Resolve(_ context.Context, imdb, kind string, season, episode int) (core.RatingKey, bool) {
	if kind == "episode" {
		return core.EpisodeKey(2, season, episode), true
	}
	return core.MovieKey(1), true
}
func (b *nuvioBackendFake) Progress(_ context.Context, user int64, _ core.RatingKey, p frontend.OfflineProgress) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if user != 7 {
		return core.ErrNotFound
	}
	b.progress = append(b.progress, p)
	return nil
}
func (b *nuvioBackendFake) Played(_ context.Context, _ int64, k core.RatingKey, played bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if played {
		b.played = append(b.played, k)
	}
	return nil
}

// Titre interdit au contrôle parental : l'historique est écrit quand même,
// pour le spectateur du lien ; un jeton invalide reste refusé (401).
func TestNuvioWritesHistoryEvenWhenTitleIsHidden(t *testing.T) {
	h, e := newNuvioInstance(t, true) // l'expérience refuse tout titre
	b := &nuvioBackendFake{}
	frontend.SetPSENuvioBackend(b)
	t.Cleanup(func() { frontend.SetPSENuvioBackend(nil) })
	nuvioCall(h, http.MethodGet, "/stremio/grant/player/movie/tt0000001/action=pause&currentTime=5000&duration=10000.json")
	nuvioCall(h, http.MethodGet, "/stremio/grant/library/series/tt0000002/action=watched&videoId=tt0000002:1:4.json")
	if len(b.progress) != 1 || b.progress[0].Position != 5*time.Second || len(b.played) != 1 || b.played[0] != core.EpisodeKey(2, 1, 4) {
		t.Fatalf("historique : %+v %v", b.progress, b.played)
	}
	if len(e.progress) != 0 || len(e.played) != 0 {
		t.Fatal("le chemin avec test de visibilité ne doit pas servir")
	}
	if nuvioCall(h, http.MethodGet, "/stremio/nope/player/movie/tt0000001.json?action=pause&currentTime=5").Code != 401 {
		t.Fatal("jeton invalide accepté")
	}
}

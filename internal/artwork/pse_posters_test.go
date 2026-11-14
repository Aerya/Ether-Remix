package artwork

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func psePosterFixture(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPSEResizePosterCachedMatchesAndReuses(t *testing.T) {
	src := psePosterFixture(t, 400, 600)
	want, err := ResizePoster(src, 200)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ResizePosterCached(src, 200)
	if err != nil || !bytes.Equal(first, want) {
		t.Fatalf("résultat différent de ResizePoster : %v", err)
	}
	second, err := ResizePosterCached(src, 200)
	if err != nil || &second[0] != &first[0] {
		t.Fatal("le second appel a recalculé l'image")
	}
	other, err := ResizePosterCached(src, 150)
	if err != nil || bytes.Equal(other, first) {
		t.Fatal("deux largeurs partagent une entrée")
	}
	changed, err := ResizePosterCached(psePosterFixture(t, 420, 600), 200)
	if err != nil || &changed[0] == &first[0] {
		t.Fatal("une image modifiée a repris l'ancienne entrée")
	}
	if _, err := ResizePosterCached([]byte("pas une image"), 200); err == nil {
		t.Fatal("une image invalide doit échouer")
	}
}

func TestPSEPosterOriginMarksProviderNotFallback(t *testing.T) {
	poster := psePosterFixture(t, 400, 600)
	fail := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "panne", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(poster)
	}))
	defer upstream.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(poster) }))
	defer fallback.Close()
	p := config.Posters{Enabled: true, Source: "postersplus", BaseURL: upstream.URL}
	im := NewImages(ImagesOptions{Dir: t.TempDir(), Posters: func() config.Posters { return p }, ImageURL: func(string, string) string { return fallback.URL }})
	movie := &core.Title{Type: core.TypeMovie, IDs: core.IDs{TMDB: 603}}
	read := func(title *core.Title) *PosterOrigin {
		t.Helper()
		ctx, origin := WithPosterOrigin(context.Background())
		if _, _, err := im.GetPoster(ctx, title, "/repli.jpg", 200); err != nil {
			t.Fatal(err)
		}
		return origin
	}
	if !read(movie).FromProvider() {
		t.Fatal("l'affiche du fournisseur n'est pas marquée")
	}
	if !read(movie).FromProvider() {
		t.Fatal("l'affiche du fournisseur lue depuis le cache n'est pas marquée")
	}
	// Un épisode n'est jamais demandé au fournisseur : repli TMDB.
	if read(&core.Title{Type: core.TypeEpisode, IDs: core.IDs{TMDB: 603}}).FromProvider() {
		t.Fatal("un repli TMDB est marqué comme venant du fournisseur")
	}
	fail = true
	p.APIKey = "autre-cle" // autre adresse : rien en cache, le fournisseur est en panne
	if read(movie).FromProvider() {
		t.Fatal("un repli après panne est marqué comme venant du fournisseur")
	}
	// Sans témoin, GetPoster se comporte comme avant.
	if _, _, err := im.GetPoster(context.Background(), movie, "/repli.jpg", 200); err != nil {
		t.Fatal(err)
	}
	var none *PosterOrigin
	if none.FromProvider() {
		t.Fatal("un témoin nil doit valoir « repli »")
	}
}

func TestPSEPosterRefreshHours(t *testing.T) {
	for value, want := range map[string]int64{"": 24, "6": 6, "1": 1, "0": 24, "-3": 24, "abc": 24, "500": 168, " 48 ": 48} {
		restore := pseenv.Replace(map[string]string{"PSE_POSTER_REFRESH_HOURS": value})
		got := PosterRefreshHours()
		restore()
		if got != want {
			t.Errorf("PSE_POSTER_REFRESH_HOURS=%q : %d heures, attendu %d", value, got, want)
		}
	}
	if psePosterRefreshSeconds() <= 0 {
		t.Fatal("la durée de renouvellement doit rester positive")
	}
}

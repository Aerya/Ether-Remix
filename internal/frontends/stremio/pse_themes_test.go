package stremio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type themeExperience struct {
	frontendtest.Experience
	catalogs []frontend.PSEThemeCatalog
	titles   map[string][]frontend.Title
}

func (e themeExperience) PSEThemeCatalogs(context.Context, *frontend.Viewer) ([]frontend.PSEThemeCatalog, error) {
	return e.catalogs, nil
}

func (e themeExperience) PSEThemeCatalogTitles(_ context.Context, _ *frontend.Viewer, typ, row string) ([]frontend.Title, error) {
	if titles, ok := e.titles[typ+"/"+row]; ok {
		return titles, nil
	}
	return nil, frontend.ErrNotFound
}

func mountThemes(t *testing.T, e themeExperience) http.Handler {
	t.Helper()
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "valid" {
			return nil, errors.New("invalid")
		}
		return &frontend.Viewer{ID: 7}, nil
	}}
	inst, err := (Frontend{}).Mount(context.Background(), frontend.Host{Identity: identity, Experience: e, Sessions: frontendtest.Sessions{}})
	if err != nil {
		t.Fatal(err)
	}
	return inst.Handler()
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestManifestListsRemixRowCatalogs(t *testing.T) {
	h := mountThemes(t, themeExperience{catalogs: []frontend.PSEThemeCatalog{{ID: "row1", Name: "Films familiaux · Disney", Type: "movie"}}})
	rec := get(h, "/stremio/valid/manifest.json")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"id":"ether-pse-row1"`) || !strings.Contains(body, "Films familiaux · Disney") {
		t.Fatalf("manifest: %d %s", rec.Code, body)
	}
}

func TestRemixRowCatalogReturnsTitlesAndPaginates(t *testing.T) {
	var titles []frontend.Title
	for n := 1; n <= 150; n++ {
		title := frontend.Title{Title: "T" + strconv.Itoa(n), Type: "movie"}
		title.IDs.TMDB = int64(n)
		titles = append(titles, title)
	}
	h := mountThemes(t, themeExperience{titles: map[string][]frontend.Title{"movie/row1": titles}})
	first := get(h, "/stremio/valid/catalog/movie/ether-pse-row1.json")
	if first.Code != 200 || !strings.Contains(first.Body.String(), `"id":"tmdb:1"`) || strings.Contains(first.Body.String(), `"id":"tmdb:101"`) {
		t.Fatalf("first page: %d %s", first.Code, first.Body.String())
	}
	second := get(h, "/stremio/valid/catalog/movie/ether-pse-row1/skip=100.json")
	if second.Code != 200 || !strings.Contains(second.Body.String(), `"id":"tmdb:101"`) || strings.Contains(second.Body.String(), `"id":"tmdb:1"`) {
		t.Fatalf("second page: %d %s", second.Code, second.Body.String())
	}
	if rec := get(h, "/stremio/valid/catalog/movie/ether-pse-other.json"); rec.Code != http.StatusNotFound {
		t.Fatalf("row not granted: %d", rec.Code)
	}
	if rec := get(h, "/stremio/invalid/catalog/movie/ether-pse-row1.json"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token: %d", rec.Code)
	}
}

func TestFilesOnlyManifestHasNoCatalogNorMeta(t *testing.T) {
	h := mountThemes(t, themeExperience{catalogs: []frontend.PSEThemeCatalog{{ID: "row1", Name: "x", Type: "movie"}}})
	rec := get(h, "/stremio/valid/fichiers/manifest.json")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"catalogs":[]`) || !strings.Contains(body, `"resources":["stream",{`) || !strings.Contains(body, `"name":"player"`) || !strings.Contains(body, `"name":"library"`) ||
		!strings.Contains(body, `"id":"org.ether.viewer.files"`) || strings.Contains(body, "meta") {
		t.Fatalf("files manifest: %d %s", rec.Code, body)
	}
	if rec := get(h, "/stremio/invalid/fichiers/manifest.json"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token: %d", rec.Code)
	}
	if rec := get(h, "/stremio/valid/fichiers/catalog/movie/ether-movies.json"); rec.Code != http.StatusNotFound {
		t.Fatalf("catalog on files link: %d", rec.Code)
	}
}

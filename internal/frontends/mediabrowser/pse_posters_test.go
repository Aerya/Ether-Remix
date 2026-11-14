package mediabrowser

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

func pseImageRequest(t *testing.T, imageType, query string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/Items/abc/Images/"+imageType+"?"+query, nil)
	r.SetPathValue("itemId", "abc")
	r.SetPathValue("imageType", imageType)
	return r
}

func TestPSEImageSize(t *testing.T) {
	cases := []struct{ imageType, query, want string }{
		{"Primary", "tag=x&fillWidth=200&fillHeight=300&quality=96", "300"},
		{"Primary", "tag=x&maxWidth=600", "500"},
		{"Primary", "tag=x&MaxWidth=600", "500"},
		{"primary", "tag=x&width=342", "342"},
		{"Primary", "tag=x&fillHeight=450", "300"},
		{"Primary", "tag=x&maxWidth=2000", "2000"},
		{"Primary", "tag=x", ""},
		{"Primary", "tag=x&maxWidth=abc", ""},
		{"Primary", "tag=x&maxWidth=-4", ""},
		{"Primary", "tag=x&w=250&maxWidth=600", "250"},
		{"Backdrop", "tag=x&maxWidth=1920", "1920"},
		{"Backdrop", "tag=x&fillWidth=780", "780"},
		{"Backdrop", "tag=x&fillHeight=720", ""},
		{"Logo", "tag=x&maxWidth=400", "400"},
	}
	for _, c := range cases {
		r := pseImageRequest(t, c.imageType, c.query)
		out := pseImageSize(r)
		if got := out.URL.Query().Get("w"); got != c.want {
			t.Errorf("%s ?%s : w=%q, attendu %q", c.imageType, c.query, got, c.want)
		}
		if out.URL.Query().Get("tag") != "x" || out.PathValue("itemId") != "abc" || out.PathValue("imageType") != c.imageType {
			t.Errorf("%s ?%s : la demande a perdu son étiquette ou son chemin", c.imageType, c.query)
		}
		if out.URL.Path != r.URL.Path {
			t.Errorf("%s ?%s : chemin modifié", c.imageType, c.query)
		}
	}
	// La demande d'origine n'est pas modifiée.
	r := pseImageRequest(t, "Primary", "tag=x&maxWidth=600")
	_ = pseImageSize(r)
	if r.URL.Query().Get("w") != "" {
		t.Fatal("la demande d'origine a été modifiée")
	}
}

func TestPSEImageRoute(t *testing.T) {
	for path, want := range map[string]bool{
		"/jellyfin/Items/abc/Images/Primary": true, "/jellyfin/Items/abc/Images/Backdrop/0": true,
		"/jellyfin/items/abc/images/primary": true,
		"/jellyfin/Items":                    false, "/jellyfin/Users/u/Items/abc": false,
	} {
		if got := pseImageRoute(httptest.NewRequest(http.MethodGet, path, nil)); got != want {
			t.Errorf("pseImageRoute(%s) = %v, attendu %v", path, got, want)
		}
	}
}

func TestPSEImageRoutesSkipArtworkMetadataWait(t *testing.T) {
	router := &route.Router{}
	seen := map[string]bool{}
	record := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, waits := frontend.SynchronousArtworkDeadline(r.Context())
		seen[r.URL.Path] = waits
	})
	for _, pattern := range []string{"/Items/{itemId}/Images/{imageType}", "/Items/{itemId}/Images/{imageType}/{index}", "/Items"} {
		if err := router.Handle(http.MethodGet, pattern, record); err != nil {
			t.Fatal(err)
		}
	}
	h := (&instance{protocol: "jellyfin", router: router}).Handler()
	for _, path := range []string{"/jellyfin/Items/abc/Images/Primary", "/jellyfin/Items/abc/Images/Backdrop/0", "/jellyfin/items/abc/images/primary", "/jellyfin/Items"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	for path, wantWait := range map[string]bool{"/Items/abc/Images/Primary": false, "/Items/abc/Images/Backdrop/0": false, "/Items": true, "/items/abc/images/primary": false} {
		got, ok := seen[path]
		if !ok {
			t.Fatalf("%s n'a pas atteint le routeur (vu : %v)", path, seen)
		}
		if got != wantWait {
			t.Errorf("%s : attente d'empreinte = %v, attendu %v", path, got, wantWait)
		}
	}
}

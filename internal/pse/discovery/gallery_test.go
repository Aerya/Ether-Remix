package discovery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestGalleryFiltersAndEscapesPaths(t *testing.T) {
	old := galleryClient
	defer func() { galleryClient = old; galleryCache.images = nil; galleryCache.at = time.Time{} }()
	galleryCache.images = nil
	galleryCache.at = time.Time{}
	calls := 0
	galleryClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.github.com" {
			t.Fatal("unexpected host")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tree":[{"type":"blob","path":"posters/A24 choice.png"},{"type":"blob","path":"config/A24.yaml"}]}`))}, nil
	})}
	s := New(&fakeCollections{}, func() bool { return false }, func(bool) error { return nil })
	for range 2 {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/gallery?q=A24", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "A24%20choice.png") || strings.Contains(w.Body.String(), "yaml") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("gallery cache not reused", calls)
	}
}

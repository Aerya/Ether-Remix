package alko

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientTypedErrorsNeverLeakKeyOrURL(t *testing.T) {
	f := newFakeSFR(t)
	c := &Client{Settings: Settings{URL: f.srv.URL, Key: testKey}}
	for path, want := range map[string]error{"/api/alko/v1/etat": ErrKey} {
		_ = path
		bad := &Client{Settings: Settings{URL: f.srv.URL, Key: "mauvaise"}}
		if _, err := bad.State(context.Background()); !errors.Is(err, want) {
			t.Fatalf("401 : %v", err)
		}
	}
	cases := []struct {
		path string
		code int
		want error
		call func() error
	}{
		{"/api/alko/v1/etat", 404, ErrDisabled, func() error { _, err := c.State(context.Background()); return err }},
		{"/api/alko/v1/etat", 503, ErrViews, func() error { _, err := c.State(context.Background()); return err }},
		{"/api/alko/v1/catalogue", 422, ErrParams, func() error { _, err := c.Catalogue(context.Background(), "", 10); return err }},
		{"/api/alko/v1/modifications", 410, ErrFullReread, func() error { _, err := c.Modifications(context.Background(), 1, 10); return err }},
		{"/api/alko/v1/fichiers", 500, ErrProtocol, func() error { _, _, err := c.Files(context.Background(), []string{testID(1)}); return err }},
	}
	for _, tc := range cases {
		f.mu.Lock()
		f.status = map[string]int{tc.path: tc.code}
		f.mu.Unlock()
		err := tc.call()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s %d : %v", tc.path, tc.code, err)
		}
		if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), f.srv.URL) || strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatalf("erreur trop bavarde : %q", err)
		}
	}
}

func TestClientUnreachableIsGeneric(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := (&Client{Settings: Settings{URL: url, Key: testKey}}).State(context.Background())
	if !errors.Is(err, ErrUnreachable) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("erreur : %v", err)
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer srv.Close()
	if _, err := (&Client{Settings: Settings{URL: srv.URL, Key: testKey}}).State(context.Background()); !errors.Is(err, ErrProtocol) || hit {
		t.Fatalf("redirection suivie ou acceptée : %v %v", err, hit)
	}
}

func TestClientSendsKeyAndPaginates(t *testing.T) {
	f := newFakeSFR(t)
	for i := 1; i <= 5; i++ {
		f.put(movie(i, 10, "Film.2020.FRENCH.mkv"), "I")
	}
	c := &Client{Settings: Settings{URL: f.srv.URL, Key: testKey}}
	after, total, pages := "", 0, 0
	for {
		p, err := c.Catalogue(context.Background(), after, 2)
		if err != nil {
			t.Fatal(err)
		}
		total, pages = total+len(p.Elements), pages+1
		if p.Next == nil {
			break
		}
		after = *p.Next
	}
	if total != 5 || pages != 3 {
		t.Fatalf("éléments %d pages %d", total, pages)
	}
	if _, err := (&Client{Settings: Settings{URL: f.srv.URL, Key: "autre"}}).State(context.Background()); !errors.Is(err, ErrKey) {
		t.Fatalf("clé fausse acceptée : %v", err)
	}
}

func TestSettingsValidation(t *testing.T) {
	for _, bad := range []string{"", "ftp://x", "http://u:p@h", "http://h?x=1", "http://h#f", "not a url", "http://"} {
		if _, err := ValidateURL(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	if u, err := ValidateURL("http://stream-fusion:8080/"); err != nil || u != "http://stream-fusion:8080" {
		t.Errorf("adresse interne refusée : %q %v", u, err)
	}
	if (Settings{URL: DefaultURL}).Problem() == "" {
		t.Error("clé manquante non signalée")
	}
	if (Settings{URL: DefaultURL, Key: testKey}).Problem() != "" {
		t.Error("réglages complets refusés")
	}
}

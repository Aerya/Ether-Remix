package discovery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPackImportBounds(t *testing.T) {
	old := packClient
	defer func() { packClient = old }()
	calls := 0
	packClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("credentials forwarded")
		}
		body := `{"collections":[]}`
		if r.URL.Path == "/large" {
			body = strings.Repeat(" ", 4<<20) + "{}"
		}
		if r.URL.Path == "/html" {
			body = "<html>login</html>"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"url":"http://example.com/a"}`, 400}, {`{"url":"https://user:secret@example.com/a"}`, 400},
		{`{"url":"https://example.com/a#fragment"}`, 400}, {`{"url":"https://example.com/large"}`, 400},
		{`{"url":"https://example.com/html"}`, 400}, {`{"url":"https://example.com/ok"}`, 200},
	} {
		w := httptest.NewRecorder()
		(&Service{}).Handler().ServeHTTP(w, httptest.NewRequest("POST", "/pack", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 3 {
		t.Fatal("invalid URL was fetched", calls)
	}
}
func TestPackImportRejectsPrivateDestinations(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1/x", "https://169.254.169.254/latest", "https://192.168.0.2/x"} {
		w := httptest.NewRecorder()
		(&Service{}).Handler().ServeHTTP(w, httptest.NewRequest("POST", "/pack", strings.NewReader(`{"url":"`+address+`"}`)))
		if w.Code != 502 {
			t.Fatal(w.Code)
		}
	}
}

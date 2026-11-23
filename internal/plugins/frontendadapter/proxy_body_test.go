package frontendadapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// A non-JSON adapter body must reach the plugin as exact bytes in
// bodyBase64 (never as an invalid json.RawMessage body), and a JSON body
// carries both the parsed value and the exact bytes (#645).
func TestProxyDeliversNonJSONBodyAsBase64(t *testing.T) {
	viewer := viewerWithLibraries(7, "Alice", core.LibraryMovies)
	mgr := &fakeManager{handler: func(req plugins.Request) (plugins.Response, io.ReadCloser, error) {
		return plugins.Response{Status: 200}, io.NopCloser(strings.NewReader("ok")), nil
	}}
	f, err := New(testDecl(core.LibraryMovies), mgr)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := f.Mount(context.Background(), testHost(map[string]*core.Viewer{"tok1": viewer}))
	if err != nil {
		t.Fatal(err)
	}

	multipart := "--boundary\r\nContent-Disposition: form-data; name=\"f\"; filename=\"a.bin\"\r\n\r\n\x00\x01raw\r\n--boundary--\r\n"
	req := httptest.NewRequest(http.MethodPost, "/opds/upload", strings.NewReader(multipart))
	req.Header.Set("Authorization", "Bearer tok1")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := mgr.calls[len(mgr.calls)-1]
	if len(got.Body) != 0 {
		t.Fatalf("non-JSON body set Body = %q, want empty", got.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(got.BodyBase64)
	if err != nil || string(raw) != multipart {
		t.Fatalf("bodyBase64 decoded to %q err=%v, want exact bytes", raw, err)
	}
	if got.ContentType != "multipart/form-data; boundary=boundary" {
		t.Fatalf("contentType = %q", got.ContentType)
	}

	jsonBody := `{"a": 1}`
	req = httptest.NewRequest(http.MethodPost, "/opds/upload", strings.NewReader(jsonBody))
	req.Header.Set("Authorization", "Bearer tok1")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got = mgr.calls[len(mgr.calls)-1]
	if !json.Valid(got.Body) || string(got.Body) != jsonBody {
		t.Fatalf("JSON Body = %q, want %q", got.Body, jsonBody)
	}
	raw, err = base64.StdEncoding.DecodeString(got.BodyBase64)
	if err != nil || string(raw) != jsonBody {
		t.Fatalf("JSON bodyBase64 decoded to %q err=%v", raw, err)
	}
}

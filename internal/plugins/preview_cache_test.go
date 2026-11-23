package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func zipTestPackage(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	cp := make(map[string][]byte, len(files)+1)
	for k, v := range files {
		cp[k] = v
	}
	cp["digest"] = []byte(PkgDigest(cp))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range cp {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPreviewCacheSkipsDuplicateCheckAndDownloadOnInstall verifies that
// PreviewInstall immediately followed by Install on the same package reuses
// the verified download and CheckPackage verdict, while evicting the URL cache
// on Install so a later upgrade at the same URL fetches fresh bytes.
func TestPreviewCacheSkipsDuplicateCheckAndDownloadOnInstall(t *testing.T) {
	env := newTestEnv(t)
	var checks atomic.Int32
	env.m.deps.CheckPackage = func(context.Context, []byte, map[string][]byte) error {
		checks.Add(1)
		return nil
	}

	files1 := helloFiles()
	files1["manifest.json"] = helloManifest("1.0.0")
	pkg1 := zipTestPackage(t, files1)

	files2 := helloFiles()
	files2["manifest.json"] = helloManifest("1.1.0")
	pkg2 := zipTestPackage(t, files2)

	var version atomic.Int32
	version.Store(1)
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		if version.Load() == 1 {
			_, _ = w.Write(pkg1)
			return
		}
		_, _ = w.Write(pkg2)
	}))
	defer srv.Close()
	env.m.client = srv.Client()

	ctx := context.Background()
	src := Source{Kind: "url", URL: srv.URL + "/hello.etherpkg"}

	// First preview fetches and checks once.
	if _, err := env.m.PreviewInstall(ctx, src); err != nil {
		t.Fatalf("first PreviewInstall: %v", err)
	}
	// Second PreviewInstall (as POST /install does right after preview) reuses both caches.
	p, err := env.m.PreviewInstall(ctx, src)
	if err != nil {
		t.Fatalf("second PreviewInstall: %v", err)
	}
	if fetches.Load() != 1 || checks.Load() != 1 {
		t.Fatalf("expected 1 fetch and 1 check across preview+install, got fetches=%d checks=%d", fetches.Load(), checks.Load())
	}
	if _, err := env.m.Install(ctx, p, InstallOptions{}); err != nil {
		t.Fatalf("Install v1: %v", err)
	}

	// After Install, the URL source cache entry must be evicted so v2 at the same URL is fetched fresh.
	version.Store(2)
	p2, err := env.m.PreviewInstall(ctx, src)
	if err != nil {
		t.Fatalf("PreviewInstall v2: %v", err)
	}
	if p2.Manifest.Version != "1.1.0" {
		t.Fatalf("expected fresh v1.1.0 after install evicted URL cache, got %s", p2.Manifest.Version)
	}
	if fetches.Load() != 2 || checks.Load() != 2 {
		t.Fatalf("expected 2 fetches and 2 checks for v2, got fetches=%d checks=%d", fetches.Load(), checks.Load())
	}
}

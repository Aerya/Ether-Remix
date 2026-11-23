package rangefile

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeValidatorsAndIfRangeResume(t *testing.T) {
	const body = "0123456789abcdefghijklmnopqrstuvwxyz"
	mod := time.Date(2026, 11, 21, 14, 22, 10, 987654321, time.UTC)

	do := func(method string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/download", nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "video/mp4")
		Serve(rec, r, "episode.mp4", "dl-0123456789abcdef.mp4", int64(len(body)), mod, strings.NewReader(body))
		return rec
	}

	head := do(http.MethodHead, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD = %d len=%d", head.Code, head.Body.Len())
	}
	etag := head.Header().Get("ETag")
	lastMod := head.Header().Get("Last-Modified")
	if etag == "" || strings.HasPrefix(etag, "W/") || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("HEAD ETag = %q, want strong quoted ETag", etag)
	}
	if lastMod == "" || head.Header().Get("Accept-Ranges") != "bytes" || head.Header().Get("Content-Length") != "36" {
		t.Fatalf("HEAD headers = %v", head.Header())
	}

	full := do(http.MethodGet, nil)
	if full.Code != http.StatusOK || full.Body.String() != body || full.Header().Get("ETag") != etag || full.Header().Get("Last-Modified") != lastMod {
		t.Fatalf("GET = %d etag=%q lm=%q", full.Code, full.Header().Get("ETag"), full.Header().Get("Last-Modified"))
	}

	// Matching strong ETag in If-Range resumes at the requested byte offset.
	resumed := do(http.MethodGet, map[string]string{"Range": "bytes=10-", "If-Range": etag})
	if resumed.Code != http.StatusPartialContent || resumed.Body.String() != body[10:] || resumed.Header().Get("Content-Range") != "bytes 10-35/36" || resumed.Header().Get("ETag") != etag {
		t.Fatalf("If-Range ETag match = %d cr=%q body=%q", resumed.Code, resumed.Header().Get("Content-Range"), resumed.Body.String())
	}

	// Matching Last-Modified in If-Range also resumes.
	resumedLM := do(http.MethodGet, map[string]string{"Range": "bytes=10-19", "If-Range": lastMod})
	if resumedLM.Code != http.StatusPartialContent || resumedLM.Body.String() != body[10:20] {
		t.Fatalf("If-Range Last-Modified match = %d body=%q", resumedLM.Code, resumedLM.Body.String())
	}

	// Mismatched or weak ETag in If-Range must fall back to full 200.
	for _, ir := range []string{`"stale"`, "W/" + etag} {
		rec := do(http.MethodGet, map[string]string{"Range": "bytes=10-", "If-Range": ir})
		if rec.Code != http.StatusOK || rec.Body.String() != body {
			t.Fatalf("If-Range %q = %d len=%d, want 200 full body", ir, rec.Code, rec.Body.Len())
		}
	}

	// Sub-second timestamp differences must not change ETag or Last-Modified.
	if got := StrongETag("dl-0123456789abcdef.mp4", int64(len(body)), mod.Truncate(time.Second)); got != etag {
		t.Fatalf("sub-second truncation changed ETag: %q vs %q", got, etag)
	}
}

func TestServeFileFallsBackToFileModTime(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dl-abcd.mp4")
	if err := os.WriteFile(p, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/media", nil)
	if err := ServeFile(rec, req, "episode.mp4", filepath.Base(p), time.Time{}, f); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" || rec.Header().Get("Last-Modified") == "" || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("ServeFile headers = %v", rec.Header())
	}
}

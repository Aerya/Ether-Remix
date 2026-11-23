package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"testing"
)

type directoryCacheTestHost struct {
	Host
	cache           archiveDirectoryCache
	data            []byte
	directoryReads  int
	directoryOffset int64
}

func (h *directoryCacheTestHost) archiveDirectoryCache() *archiveDirectoryCache { return &h.cache }
func (h *directoryCacheTestHost) CacheOpen(context.Context, string) (io.ReadSeekCloser, error) {
	return &directoryCacheTestReader{Reader: bytes.NewReader(h.data), host: h}, nil
}

type directoryCacheTestReader struct {
	*bytes.Reader
	host *directoryCacheTestHost
}

func (r *directoryCacheTestReader) Close() error { return nil }
func (r *directoryCacheTestReader) Read(p []byte) (int, error) {
	off, _ := r.Seek(0, io.SeekCurrent)
	if off == r.host.directoryOffset {
		r.host.directoryReads++
	}
	return r.Reader.Read(p)
}

func (r *directoryCacheTestReader) ReadAt(p []byte, off int64) (int, error) {
	if off == r.host.directoryOffset {
		r.host.directoryReads++
	}
	return r.Reader.ReadAt(p, off)
}

// Reuse must preserve independent per-call reader lifetimes, while the second
// member read performs no central-directory I/O after the first call expires.
func TestArchiveDirectoryCache(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	bodies := []string{"first member", "second member"}
	for i, body := range bodies {
		w, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%d.txt", i), Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	h := &directoryCacheTestHost{data: b.Bytes()}
	h.directoryOffset = int64(bytes.Index(h.data, []byte{'P', 'K', 1, 2}))
	var reads int
	for i, want := range bodies {
		ctx, cancel := context.WithCancel(context.Background())
		raw, err := CallDataHost(ctx, h, "archive.open", json.RawMessage(fmt.Sprintf(`["blob:test",%d,{"offset":0,"length":100}]`, i)))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ BodyBase64 string }
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		got, err := base64.StdEncoding.DecodeString(out.BodyBase64)
		if err != nil || string(got) != want {
			t.Fatalf("member %d: %q, %v", i, got, err)
		}
		if i == 0 {
			reads = h.directoryReads
			if reads == 0 {
				t.Fatal("first call did not parse directory")
			}
		} else if h.directoryReads != reads {
			t.Fatal("second call parsed directory again")
		}
	}
	if h.cache.lru.Len() != 1 {
		t.Fatalf("cache entries: %d", h.cache.lru.Len())
	}
}

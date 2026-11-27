package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"net/http"
	"time"

	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"io"
	"strings"
	"testing"
)

type dataRoundTripper func(*http.Request) (*http.Response, error)

func (f dataRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type dataTestHost struct {
	Host
	data   []byte
	opens  int
	closes int
}
type dataTestFile struct {
	*bytes.Reader
	close func()
}

func (f dataTestFile) Close() error { f.close(); return nil }
func (h *dataTestHost) CacheOpen(context.Context, string) (io.ReadSeekCloser, error) {
	h.opens++
	return dataTestFile{bytes.NewReader(h.data), func() { h.closes++ }}, nil
}
func dataZIP(t *testing.T, name string, method uint16, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(body); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// These are host-service invariants: bounded envelopes, random access without
// whole-archive guest copies, pre-allocation directory validation and containment.
func TestDataHostInvariants(t *testing.T) {
	ctx := context.Background()

	t.Run("streaming acquisition and grants", func(t *testing.T) {
		env := newTestEnv(t)
		rec := installContent(t, env)
		rec.Manifest.Ether.Permissions.Network = []string{"data.example.test"}
		var marshalErr error
		rec.ManifestRaw, marshalErr = json.Marshal(rec.Manifest)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
		body := []byte("a streamed dataset")
		requests := 0
		writes := 0
		env.m.client = &http.Client{Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header), Request: r}, nil
		})}
		env.m.SetKindBackends(KindBackends{CachePut: func(_ context.Context, plugin, library, name string, r io.Reader) (string, error) {
			writes++
			data, err := io.ReadAll(r)
			if plugin != rec.ID || library != core.PluginLibraryKey(rec.ID, "recording") || name != "data.zip" || !bytes.Equal(data, body) {
				t.Fatal("cache received wrong bytes or namespace")
			}
			return "blob:test", err
		}})
		h := env.m.HostFor(rec.ID).(*host)
		req := FetchCacheRequest{FetchRequest: FetchRequest{URL: "https://data.example.test/data.zip"}, LibraryKey: core.PluginLibraryKey(rec.ID, "recording"), Name: "data.zip"}
		out, err := h.HTTPFetchToCache(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Size != int64(len(body)) || out.CRC32 != fmt.Sprintf("%08x", crc32.ChecksumIEEE(body)) || out.MD5 != fmt.Sprintf("%x", md5.Sum(body)) || out.SHA1 != fmt.Sprintf("%x", sha1.Sum(body)) {
			t.Fatalf("digests: %+v", out)
		}
		bad := req
		bad.LibraryKey = "another-plugin-library"
		if _, err = h.HTTPFetchToCache(ctx, bad); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("foreign cache namespace: %v", err)
		}
		if requests != 1 || writes != 1 {
			t.Fatal("foreign cache namespace caused I/O")
		}
		if err = env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"network"}}); err != nil {
			t.Fatal(err)
		}
		if _, err = h.HTTPFetchToCache(ctx, req); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("revoked network: %v", err)
		}
		if requests != 1 || writes != 1 {
			t.Fatal("revoked network caused I/O")
		}
	})
	t.Run("ranged blob and ZIP member", func(t *testing.T) {
		body := bytes.Repeat([]byte{0, 255, 1, 2, 3, 4, 5, 6}, (5<<20)/8)
		h := &dataTestHost{data: body}
		raw, err := CallDataHost(ctx, h, "cache.read", json.RawMessage(`["blob:test",{"offset":4194300,"length":100}]`))
		if err != nil {
			t.Fatal(err)
		}
		var answer struct {
			BodyBase64 string `json:"bodyBase64"`
		}
		if err = json.Unmarshal(raw, &answer); err != nil {
			t.Fatal(err)
		}
		got, err := base64.StdEncoding.DecodeString(answer.BodyBase64)
		if err != nil || !bytes.Equal(got, body[4194300:4194400]) {
			t.Fatalf("range mismatch: %v", err)
		}
		h.data = dataZIP(t, "data/member.bin", zip.Store, body)
		raw, err = CallDataHost(ctx, h, "archive.list", json.RawMessage(`["blob:test"]`))
		if err != nil {
			t.Fatal(err)
		}
		var members []ArchiveMember
		if err = json.Unmarshal(raw, &members); err != nil || len(members) != 1 || members[0].Size != uint64(len(body)) {
			t.Fatalf("list %s: %v", raw, err)
		}
		raw, err = CallDataHost(ctx, h, "archive.open", json.RawMessage(`["blob:test",0,{"offset":4194300,"length":1048576}]`))
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) >= 4<<20 {
			t.Fatalf("range envelope too large: %d", len(raw))
		}
		if err = json.Unmarshal(raw, &answer); err != nil {
			t.Fatal(err)
		}
		got, err = base64.StdEncoding.DecodeString(answer.BodyBase64)
		if err != nil || !bytes.Equal(got, body[4194300:4194300+(1<<20)]) {
			t.Fatalf("member mismatch: %v", err)
		}

		h.data = dataZIP(t, "data/member.bin", zip.Deflate, body)
		if _, err = CallDataHost(ctx, h, "archive.open", json.RawMessage(`["blob:test",0,{"offset":4194300,"length":100}]`)); err != nil {
			t.Fatalf("deflate range: %v", err)
		}
		if h.opens != h.closes {
			t.Fatalf("leaked readers: %d opens/%d closes", h.opens, h.closes)
		}
		before := h.opens
		for _, args := range []string{`["blob:test",{"offset":-1,"length":1}]`, `["blob:test",{"offset":0,"length":8388609}]`} {
			if _, err = CallDataHost(ctx, h, "cache.read", json.RawMessage(args)); err == nil {
				t.Fatal("invalid range accepted")
			}
		}
		if before != h.opens {
			t.Fatal("invalid range opened cache")
		}
	})
	t.Run("directory and expansion limits", func(t *testing.T) {
		bomb := dataZIP(t, "bomb", zip.Deflate, bytes.Repeat([]byte("a"), 2<<20))
		central := bytes.Index(bomb, []byte{0x50, 0x4b, 0x01, 0x02})
		binary.LittleEndian.PutUint32(bomb[central+24:], 64<<20)
		if _, err := openDataZIP(ctx, bytes.NewReader(bomb)); err == nil {
			t.Fatal("compression bomb accepted")
		}
		b := dataZIP(t, "ok", zip.Store, []byte("ok"))
		end := len(b) - 22
		binary.LittleEndian.PutUint16(b[end+8:], 0)
		binary.LittleEndian.PutUint16(b[end+10:], 0)
		if _, err := openDataZIP(ctx, bytes.NewReader(b)); err == nil {
			t.Fatal("directory count mismatch accepted")
		}
		binary.LittleEndian.PutUint16(b[end+8:], 1)
		binary.LittleEndian.PutUint16(b[end+10:], 1)
		binary.LittleEndian.PutUint32(b[end+12:], maxArchiveDirectory+1)
		if _, err := openDataZIP(ctx, bytes.NewReader(b)); err == nil {
			t.Fatal("oversized directory accepted")
		}
	})
	t.Run("overflow and cancellation propagate", func(t *testing.T) {
		r := &boundedDataReader{ctx: ctx, r: strings.NewReader("12345"), left: 4}
		if _, err := io.ReadAll(r); err == nil {
			t.Fatal("overflow became successful EOF")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		r = &boundedDataReader{ctx: canceled, r: strings.NewReader("a"), left: 4}
		if _, err := io.ReadAll(r); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	})
	t.Run("archive.extract streams into blobcache with digests and enforces grants", func(t *testing.T) {
		env := newTestEnv(t)
		rec := installContent(t, env)
		libKey := core.PluginLibraryKey(rec.ID, "recording")

		blobs, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = blobs.Close() })
		quotaBytes := int64(16 << 20)
		cachePut, cacheOpen, cacheDelete := blobs.HostBackends(func(_, _ string) blobcache.Quota {
			return blobcache.Quota{MaxBytes: quotaBytes}
		})
		env.m.SetKindBackends(KindBackends{CachePut: cachePut, CacheOpen: cacheOpen, CacheDelete: cacheDelete})

		body := bytes.Repeat([]byte("3DO-track-sector-payload-01234567"), (2<<20)/33+4096)
		var archiveBuf bytes.Buffer
		zw := zip.NewWriter(&archiveBuf)
		w0, err := zw.CreateHeader(&zip.FileHeader{Name: "disc/track01.bin", Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w0.Write(body); err != nil {
			t.Fatal(err)
		}
		w1, err := zw.CreateHeader(&zip.FileHeader{Name: "../escape.bin", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w1.Write([]byte("nope")); err != nil {
			t.Fatal(err)
		}
		if _, err = zw.CreateHeader(&zip.FileHeader{Name: "disc/", Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
		if err = zw.Close(); err != nil {
			t.Fatal(err)
		}

		h := env.m.HostFor(rec.ID).(*host)
		archiveRef, err := h.CachePut(ctx, libKey, "game.zip", bytes.NewReader(archiveBuf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}

		args, _ := json.Marshal([]any{archiveRef, 0})
		raw, err := CallDataHost(ctx, h, "archive.extract", args)
		if err != nil {
			t.Fatalf("archive.extract: %v", err)
		}
		var out ArchiveExtractResult
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		wantRef := fmt.Sprintf("blob:%x", sha256.Sum256(body))
		if out.Ref != wantRef ||
			out.Size != int64(len(body)) ||
			out.SHA1 != fmt.Sprintf("%x", sha1.Sum(body)) ||
			out.MD5 != fmt.Sprintf("%x", md5.Sum(body)) ||
			out.CRC32 != fmt.Sprintf("%08x", crc32.ChecksumIEEE(body)) {
			t.Fatalf("unexpected extract result: %+v", out)
		}
		rc, err := h.CacheOpen(ctx, out.Ref)
		if err != nil {
			t.Fatalf("open extracted ref: %v", err)
		}
		stored, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil || !bytes.Equal(stored, body) {
			t.Fatalf("stored blob mismatch (len=%d want=%d err=%v)", len(stored), len(body), err)
		}

		// Flagged member extracts by index and falls back to member-<index> as cache name.
		unsafeArgs, _ := json.Marshal([]any{archiveRef, 1})
		rawUnsafe, err := CallDataHost(ctx, h, "archive.extract", unsafeArgs)
		if err != nil {
			t.Fatalf("unsafe extract err = %v", err)
		}
		var unsafeOut ArchiveExtractResult
		if err = json.Unmarshal(rawUnsafe, &unsafeOut); err != nil {
			t.Fatal(err)
		}
		if unsafeOut.Size != 4 || unsafeOut.Ref != fmt.Sprintf("blob:%x", sha256.Sum256([]byte("nope"))) {
			t.Fatalf("unexpected unsafe extract result: %+v", unsafeOut)
		}
		rcUnsafe, err := h.CacheOpen(ctx, unsafeOut.Ref)
		if err != nil {
			t.Fatalf("open extracted unsafe ref: %v", err)
		}
		named, _ := rcUnsafe.(interface{ Name() string })
		unsafeStored, err := io.ReadAll(rcUnsafe)
		_ = rcUnsafe.Close()
		if err != nil || string(unsafeStored) != "nope" || (named != nil && named.Name() != "member-1") {
			t.Fatalf("unsafe extracted blob: data=%q name=%v err=%v", unsafeStored, named, err)
		}

		// Directory member refused.
		dirArgs, _ := json.Marshal([]any{archiveRef, 2})
		if _, err = CallDataHost(ctx, h, "archive.extract", dirArgs); err == nil {
			t.Fatal("directory extract succeeded")
		}

		// Foreign libraryKey refused with ErrPermissionDenied.
		foreignArgs, _ := json.Marshal([]any{archiveRef, 0, ArchiveExtractOptions{LibraryKey: "pk-other-plugin-recording"}})
		if _, err = CallDataHost(ctx, h, "archive.extract", foreignArgs); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("foreign libraryKey err = %v, want ErrPermissionDenied", err)
		}

		// Per-library cache quota enforced before committing.
		rec.Manifest.Ether.Contributes.MediaKinds[0].Cache.MaxBytes = 512 << 10
		rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
		if err = env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if _, err = CallDataHost(ctx, h, "archive.extract", args); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("cache quota err = %v, want ErrQuotaExceeded", err)
		}
		rec.Manifest.Ether.Contributes.MediaKinds[0].Cache.MaxBytes = 32 << 20
		rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
		if err = env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}

		// archiveMemberBytes grant enforced.
		rec.Manifest.Ether.Runtime.Limits = &RuntimeLimits{ArchiveMemberBytes: 512 << 10}
		rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
		if err = env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if _, err = CallDataHost(ctx, h, "archive.extract", args); err == nil {
			t.Fatal("archiveMemberBytes limit not enforced")
		}
		rec.Manifest.Ether.Runtime.Limits = nil
		rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
		if err = env.m.store.upsert(ctx, rec); err != nil {
			t.Fatal(err)
		}

		// Hosted QuickJS ABI + incremental per-MiB governance charging.
		eng := wasm.NewEngine(nil)
		t.Cleanup(func() { _ = eng.Close() })
		jsSrc := `globalThis.handle = (req) => ({ status: 200, body: ether.archive.extract(req.body.ref, 0) });`
		pOk, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{UnitBudget: 500})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pOk.Close() })
		reqBody, _ := json.Marshal(map[string]string{"ref": archiveRef})
		wasmResp, err := pOk.Call(ctx, wasm.Request{Method: "POST", Path: "/extract", Body: reqBody})
		if err != nil {
			t.Fatalf("hosted Call: %v", err)
		}
		var jsOut ArchiveExtractResult
		if err = json.Unmarshal(wasmResp.Body, &jsOut); err != nil || jsOut != out {
			t.Fatalf("hosted JS result = %+v (err=%v), want %+v", jsOut, err, out)
		}

		// Tight unit budget (65 < 50 base + 3 started MiB * 10) aborts mid-stream with governance fault.
		pTight, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{UnitBudget: 65})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pTight.Close() })
		if _, err = pTight.Call(ctx, wasm.Request{Method: "POST", Path: "/extract", Body: reqBody}); err == nil || !strings.Contains(err.Error(), "budget") {
			t.Fatalf("tight unit budget err = %v, want budget fault", err)
		}
	})
}

func TestCacheRasterizeHostService(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	rec := installContent(t, env)
	libKey := core.PluginLibraryKey(rec.ID, "recording")

	blobs, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	cachePut, cacheOpen, _ := blobs.HostBackends(func(_, _ string) blobcache.Quota {
		return blobcache.Quota{MaxBytes: 16 << 20}
	})
	env.m.SetKindBackends(KindBackends{CachePut: cachePut, CacheOpen: cacheOpen})
	h := env.m.HostFor(rec.ID).(*host)

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10"><rect width="20" height="10" fill="#336699"/></svg>`)
	sourceRef, err := h.CachePut(ctx, libKey, "logo.svg", bytes.NewReader(svg))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]any{libKey, "logo.png", sourceRef, RasterizeOptions{Width: 40, Format: "png"}})
	raw, err := CallDataHost(ctx, h, "cache.rasterize", args)
	if err != nil {
		t.Fatalf("cache.rasterize: %v", err)
	}
	var out RasterizeResult
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "image/png" || out.Width != 40 || out.Height != 20 || out.Size <= 0 || !strings.HasPrefix(out.Blob, "blob:") {
		t.Fatalf("unexpected rasterize result: %+v", out)
	}
	rc, err := h.CacheOpen(ctx, out.Blob)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || int64(len(stored)) != out.Size {
		t.Fatalf("stored bitmap size = %d, want %d (err=%v)", len(stored), out.Size, err)
	}
	// Repeat calls return the same content-addressed blob (cached result).
	again, err := h.CacheRasterize(ctx, libKey, "logo.png", sourceRef, RasterizeOptions{Width: 40, Format: "png"})
	if err != nil || again.Blob != out.Blob {
		t.Fatalf("repeat rasterize = %+v err=%v, want blob %q", again, err, out.Blob)
	}
	// Unsafe SVG, oversize dimensions and foreign libraries are refused.
	unsafeRef, err := h.CachePut(ctx, libKey, "bad.svg", bytes.NewReader([]byte(`<svg><image href="http://127.0.0.1/"/></svg>`)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.CacheRasterize(ctx, libKey, "bad.png", unsafeRef, RasterizeOptions{Width: 40}); !errors.Is(err, artwork.ErrSVG) {
		t.Fatalf("unsafe SVG err = %v, want ErrSVG", err)
	}
	if _, err = h.CacheRasterize(ctx, libKey, "logo.png", sourceRef, RasterizeOptions{Width: 2049}); !errors.Is(err, artwork.ErrSVG) {
		t.Fatalf("oversize err = %v, want ErrSVG", err)
	}
	if _, err = h.CacheRasterize(ctx, "pk-other-plugin-recording", "logo.png", sourceRef, RasterizeOptions{Width: 40}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("foreign library err = %v, want ErrPermissionDenied", err)
	}
}

type blockingBodyReader struct {
	ctx context.Context
}

func (b blockingBodyReader) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (blockingBodyReader) Close() error { return nil }

func TestFetchToCacheHonorsTimeoutMS(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = []string{"data.example.test"}
	var err error
	rec.ManifestRaw, err = json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}

	libraryKey := core.PluginLibraryKey(rec.ID, "recording")
	var gotDeadline time.Time
	var hasDeadline bool
	env.m.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			gotDeadline, hasDeadline = r.Context().Deadline()
			switch r.URL.Path {
			case "/hang-headers":
				<-r.Context().Done()
				return nil, r.Context().Err()
			case "/hang-body":
				return &http.Response{
					StatusCode: 200,
					Body:       blockingBodyReader{ctx: r.Context()},
					Header:     make(http.Header),
					Request:    r,
				}, nil
			default:
				return &http.Response{StatusCode: 404, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header), Request: r}, nil
			}
		}),
	}
	committed := false
	env.m.SetKindBackends(KindBackends{CachePut: func(_ context.Context, _, _, _ string, r io.Reader) (string, error) {
		if _, err := io.ReadAll(r); err != nil {
			return "", err
		}
		committed = true
		return "blob:unexpected", nil
	}})

	h := env.m.HostFor(rec.ID)

	// 1. Hosted JS call: timeoutMs tightens request deadline below the call
	// deadline and throws a catchable Error with code "timeout".
	eng := wasm.NewEngine(nil)
	t.Cleanup(func() { _ = eng.Close() })
	jsSrc := `
globalThis.handle = function(req) {
  try {
    ether.http.fetchToCache(req.body);
    return { status: 200, body: { timedOut: false } };
  } catch (e) {
    return { status: 200, body: { timedOut: true, code: e && e.code || "" } };
  }
};
`
	p, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{Deadline: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	callCtx, cancelCall := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCall()
	callDeadline, _ := callCtx.Deadline()
	reqJSON, _ := json.Marshal(map[string]any{
		"url":        "https://data.example.test/hang-headers",
		"libraryKey": libraryKey,
		"name":       "headers.bin",
		"timeoutMs":  25,
	})
	resp, err := p.Call(callCtx, wasm.Request{Method: "POST", Path: "/fetch", Body: reqJSON})
	if err != nil {
		t.Fatalf("hosted Call failed: %v", err)
	}
	var jsReply struct {
		TimedOut bool   `json:"timedOut"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(resp.Body, &jsReply); err != nil {
		t.Fatalf("unmarshal JS reply %s: %v", resp.Body, err)
	}
	if !jsReply.TimedOut || jsReply.Code != "timeout" {
		t.Fatalf("hosted fetchToCache timeout reply = %+v, want timedOut=true code=timeout", jsReply)
	}
	if !hasDeadline || !gotDeadline.Before(callDeadline) {
		t.Fatalf("header fetch deadline = %v (ok=%v), want before call deadline %v", gotDeadline, hasDeadline, callDeadline)
	}

	// 2. timeoutMs also bounds body streaming into cache and returns a timeout error.
	bodyArgs, _ := json.Marshal([]any{map[string]any{
		"url":        "https://data.example.test/hang-body",
		"libraryKey": libraryKey,
		"name":       "body.bin",
		"timeoutMs":  25,
	}})
	_, err = CallDataHost(callCtx, h, "http.fetchToCache", bodyArgs)
	if !errors.Is(err, ErrFetchFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body-copy timeout err = %v, want ErrFetchFailed wrapping DeadlineExceeded", err)
	}
	var fault *wasm.Fault
	if !errors.As(mapHostError(err), &fault) || fault.Code != "timeout" {
		t.Fatalf("mapHostError(body timeout) = %+v, want code=timeout", mapHostError(err))
	}
	if !hasDeadline || !gotDeadline.Before(callDeadline) {
		t.Fatalf("body fetch deadline = %v (ok=%v), want before call deadline %v", gotDeadline, hasDeadline, callDeadline)
	}

	// 3. A timeoutMs longer than the remaining call deadline is capped by the call deadline.
	tightCtx, cancelTight := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancelTight()
	tightDeadline, _ := tightCtx.Deadline()
	cappedArgs, _ := json.Marshal([]any{map[string]any{
		"url":        "https://data.example.test/hang-headers",
		"libraryKey": libraryKey,
		"name":       "capped.bin",
		"timeoutMs":  60000,
	}})
	_, err = CallDataHost(tightCtx, h, "http.fetchToCache", cappedArgs)
	if !errors.Is(err, ErrFetchFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capped timeout err = %v, want ErrFetchFailed wrapping DeadlineExceeded", err)
	}
	if !hasDeadline || gotDeadline.After(tightDeadline) {
		t.Fatalf("capped fetch deadline = %v (ok=%v), want at or before call deadline %v", gotDeadline, hasDeadline, tightDeadline)
	}
	if committed {
		t.Fatal("timed-out fetchToCache unexpectedly committed a cache blob")
	}
}

func TestFetchToCacheLongJobTimeout(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = []string{"data.example.test"}
	rec.Manifest.Ether.Schedule = []ScheduleEntry{{Every: "1h", Call: "/ether/refresh"}}
	var err error
	rec.ManifestRaw, err = json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	libraryKey := core.PluginLibraryKey(rec.ID, "recording")

	var gotDeadline time.Time
	var hasDeadline bool
	env.m.client = &http.Client{
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			gotDeadline, hasDeadline = r.Context().Deadline()
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}
	env.m.SetKindBackends(KindBackends{CachePut: func(_ context.Context, _, _, _ string, r io.Reader) (string, error) {
		_, _ = io.Copy(io.Discard, r)
		return "blob:" + strings.Repeat("a", 64), nil
	}})

	h := env.m.HostFor(rec.ID)
	run := func(callCtx context.Context, timeoutMS int64) time.Duration {
		t.Helper()
		gotDeadline = time.Time{}
		hasDeadline = false
		args, _ := json.Marshal([]any{map[string]any{
			"url":        "https://data.example.test/large.zip",
			"libraryKey": libraryKey,
			"name":       "large.zip",
			"timeoutMs":  timeoutMS,
		}})
		before := time.Now()
		if _, err := CallDataHost(callCtx, h, "http.fetchToCache", args); err != nil {
			t.Fatalf("CallDataHost(timeoutMs=%d): %v", timeoutMS, err)
		}
		if !hasDeadline {
			t.Fatal("outbound HTTP request had no context deadline")
		}
		return gotDeadline.Sub(before)
	}

	twoHourCtx, cancelTwo := context.WithTimeout(ctx, 2*time.Hour)
	defer cancelTwo()

	// 1. Job without timeoutMs stays at the 10-minute default.
	if rem := run(withCallPath(twoHourCtx, "/ether/job/import"), 0); rem < 9*time.Minute || rem > 11*time.Minute {
		t.Fatalf("job default timeout = %v, want ~10m", rem)
	}
	// 2. Job with 1 h timeoutMs extends past 10 m up to 1 h.
	if rem := run(withCallPath(twoHourCtx, "/ether/job/import"), int64(time.Hour/time.Millisecond)); rem < 55*time.Minute || rem > 65*time.Minute {
		t.Fatalf("job 1h timeout = %v, want ~1h", rem)
	}
	// 3. Job requesting 5 h is clamped to the calling job's remaining 2 h deadline.
	if rem := run(withCallPath(twoHourCtx, "/ether/job/import"), int64((5*time.Hour)/time.Millisecond)); rem < 115*time.Minute || rem > 125*time.Minute {
		t.Fatalf("job 5h timeout = %v, want clamped to ~2h", rem)
	}
	// 4. Declared schedule route and explicit background context also extend up to remaining deadline.
	if rem := run(withCallPath(twoHourCtx, "/ether/refresh"), int64(time.Hour/time.Millisecond)); rem < 55*time.Minute || rem > 65*time.Minute {
		t.Fatalf("schedule route 1h timeout = %v, want ~1h", rem)
	}
	if rem := run(WithBackgroundCall(twoHourCtx), int64(time.Hour/time.Millisecond)); rem < 55*time.Minute || rem > 65*time.Minute {
		t.Fatalf("background ctx 1h timeout = %v, want ~1h", rem)
	}
	// 5. Viewer and interactive request paths stay capped at 10 m even if ctx has 2 h left.
	for _, p := range []string{"/stream/movie/tt1.json", "/ether/v1/kinds/recording/resolve", "/ether/v1/frontend/opds/feed", "/ether/action/import", ""} {
		callCtx := twoHourCtx
		if p != "" {
			callCtx = withCallPath(twoHourCtx, p)
		}
		if rem := run(callCtx, int64(time.Hour/time.Millisecond)); rem > 11*time.Minute {
			t.Fatalf("request path %q timeout = %v, want <= 10m ceiling", p, rem)
		}
	}
	// 6. Even if WithBackgroundCall wraps a viewer path, viewer path ceiling still wins.
	if rem := run(withCallPath(WithBackgroundCall(twoHourCtx), "/ether/v1/kinds/recording/resolve"), int64(time.Hour/time.Millisecond)); rem > 11*time.Minute {
		t.Fatalf("viewer path with background flag timeout = %v, want <= 10m", rem)
	}
}

type failingPrefixReader struct {
	data []byte
	pos  int
	err  error
}

func (r *failingPrefixReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, r.err
	}
	return n, nil
}

func TestFetchToCacheResume(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = []string{"data.example.test"}
	var err error
	rec.ManifestRaw, err = json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	libraryKey := core.PluginLibraryKey(rec.ID, "recording")

	var quotaBytes int64 = 16 << 20
	blobs, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	cachePut, cacheOpen, cacheDelete := blobs.HostBackends(func(_, _ string) blobcache.Quota {
		return blobcache.Quota{MaxBytes: quotaBytes}
	})
	cachePartial := blobs.HostPartialBackend(func(_, _ string) blobcache.Quota {
		return blobcache.Quota{MaxBytes: quotaBytes}
	})
	env.m.SetKindBackends(KindBackends{
		CachePut:     cachePut,
		CacheOpen:    cacheOpen,
		CacheDelete:  cacheDelete,
		CachePartial: cachePartial,
	})
	h := env.m.HostFor(rec.ID)
	ns := blobs.Namespace(libraryKey, blobcache.Quota{MaxBytes: quotaBytes})

	callResume := func(rawURL, name string, extraHeaders map[string]string) (FetchCacheResult, error) {
		req := map[string]any{
			"url":        rawURL,
			"libraryKey": libraryKey,
			"name":       name,
			"resume":     true,
		}
		if len(extraHeaders) > 0 {
			req["headers"] = extraHeaders
		}
		args, _ := json.Marshal([]any{req})
		raw, err := CallDataHost(withCallPath(ctx, "/ether/job/import"), h, "http.fetchToCache", args)
		if err != nil {
			return FetchCacheResult{}, err
		}
		var out FetchCacheResult
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal FetchCacheResult: %v", err)
		}
		return out, nil
	}

	// 1. Multi-hop resume with Range: bytes=<cached>-, server-capped 206 chunks,
	//    and unreadable partial until complete.
	t.Run("resumes across failures and capped 206 chunks with exact digests", func(t *testing.T) {
		payload := bytes.Repeat([]byte("chunked-archive-content-0123456789abcdef"), 100) // 4000 bytes
		const etag = `"archive-v1"`
		const lm = "Sun, 22 Nov 2026 10:00:00 GMT"
		var seenRanges []string
		callNum := 0

		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				callNum++
				rng := r.Header.Get("Range")
				seenRanges = append(seenRanges, rng)
				hdr := make(http.Header)
				hdr.Set("ETag", etag)
				hdr.Set("Last-Modified", lm)
				switch callNum {
				case 1:
					// First attempt: delivers 1000 bytes then drops connection.
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(payload)),
						Header:        hdr,
						Body:          io.NopCloser(&failingPrefixReader{data: payload[:1000], err: io.ErrUnexpectedEOF}),
						Request:       r,
					}, nil
				case 2:
					// Second attempt: server returns 206 starting at 1000, caps chunk at 1500 bytes (1000..2499),
					// succeeds cleanly, leaving bytes 2500..3999 for the next chunk request inside the same call.
					if rng != "bytes=1000-" {
						t.Fatalf("attempt 2 Range = %q, want bytes=1000-", rng)
					}
					hdr.Set("Content-Range", fmt.Sprintf("bytes 1000-2499/%d", len(payload)))
					return &http.Response{
						StatusCode:    206,
						ContentLength: 1500,
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(payload[1000:2500])),
						Request:       r,
					}, nil
				case 3:
					// Third request (auto-continued in call 2): delivers 2500..3200 then times out!
					if rng != "bytes=2500-" {
						t.Fatalf("attempt 3 Range = %q, want bytes=2500-", rng)
					}
					hdr.Set("Content-Range", fmt.Sprintf("bytes 2500-%d/%d", len(payload)-1, len(payload)))
					return &http.Response{
						StatusCode:    206,
						ContentLength: int64(len(payload) - 2500),
						Header:        hdr,
						Body:          io.NopCloser(&failingPrefixReader{data: payload[2500:3200], err: context.DeadlineExceeded}),
						Request:       r,
					}, nil
				case 4:
					// Final call: resumes at 3200 and completes!
					if rng != "bytes=3200-" {
						t.Fatalf("attempt 4 Range = %q, want bytes=3200-", rng)
					}
					hdr.Set("Content-Range", fmt.Sprintf("bytes 3200-%d/%d", len(payload)-1, len(payload)))
					return &http.Response{
						StatusCode:    206,
						ContentLength: int64(len(payload) - 3200),
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(payload[3200:])),
						Request:       r,
					}, nil
				default:
					t.Fatalf("unexpected call %d", callNum)
					return nil, nil
				}
			}),
		}

		if _, err := callResume("https://data.example.test/game.zip", "game.zip", nil); err == nil {
			t.Fatal("attempt 1 unexpectedly succeeded")
		}
		if _, ok := ns.LookupKey("game.zip"); ok {
			t.Fatal("partial blob became readable after attempt 1 failure")
		}
		if pBytes, _ := ns.PartialStats(); pBytes != 1000 {
			t.Fatalf("staged partial bytes after attempt 1 = %d, want 1000", pBytes)
		}

		if _, err := callResume("https://data.example.test/game.zip", "game.zip", nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("attempt 2 err = %v, want DeadlineExceeded", err)
		}
		if _, ok := ns.LookupKey("game.zip"); ok {
			t.Fatal("partial blob became readable after attempt 2 failure")
		}
		if pBytes, _ := ns.PartialStats(); pBytes != 3200 {
			t.Fatalf("staged partial bytes after attempt 2 = %d, want 3200", pBytes)
		}

		res, err := callResume("https://data.example.test/game.zip", "game.zip", nil)
		if err != nil {
			t.Fatalf("final resume call failed: %v", err)
		}
		wantSHA256 := sha256.Sum256(payload)
		wantSHA1 := sha1.Sum(payload)
		wantMD5 := md5.Sum(payload)
		wantCRC := fmt.Sprintf("%08x", crc32.ChecksumIEEE(payload))
		if res.Blob != "blob:"+hex.EncodeToString(wantSHA256[:]) ||
			res.Size != int64(len(payload)) ||
			res.SHA1 != hex.EncodeToString(wantSHA1[:]) ||
			res.MD5 != hex.EncodeToString(wantMD5[:]) ||
			res.CRC32 != wantCRC ||
			res.Status != 206 {
			t.Fatalf("resumed FetchCacheResult = %+v, want blob=blob:%x size=%d status=206", res, wantSHA256, len(payload))
		}
		rc, err := h.CacheOpen(ctx, res.Blob)
		if err != nil {
			t.Fatalf("CacheOpen(%s): %v", res.Blob, err)
		}
		gotBytes, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(gotBytes, payload) {
			t.Fatalf("cached bytes mismatch: got %d bytes", len(gotBytes))
		}
		wantRanges := []string{"", "bytes=1000-", "bytes=2500-", "bytes=3200-"}
		if fmt.Sprint(seenRanges) != fmt.Sprint(wantRanges) {
			t.Fatalf("seen Range headers = %v, want %v", seenRanges, wantRanges)
		}
	})

	// 2. Validator mismatch on 206 (server ignores If-Range and returns new ETag) -> restarts cleanly from byte 0.
	t.Run("restarts cleanly when ETag or Last-Modified changes on 206", func(t *testing.T) {
		v1 := bytes.Repeat([]byte("version-one-data"), 100) // 1600 bytes
		v2 := bytes.Repeat([]byte("VERSION-TWO-NEW!"), 100) // 1600 bytes
		var requests []string
		step := 0

		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				step++
				requests = append(requests, r.Header.Get("Range"))
				switch step {
				case 1:
					h1 := make(http.Header)
					h1.Set("ETag", `"etag-v1"`)
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(v1)),
						Header:        h1,
						Body:          io.NopCloser(&failingPrefixReader{data: v1[:600], err: io.ErrUnexpectedEOF}),
						Request:       r,
					}, nil
				case 2:
					// Server ignores If-Range and returns 206 with mismatched ETag `"etag-v2"`.
					h2 := make(http.Header)
					h2.Set("ETag", `"etag-v2"`)
					h2.Set("Content-Range", fmt.Sprintf("bytes 600-%d/%d", len(v2)-1, len(v2)))
					return &http.Response{
						StatusCode:    206,
						ContentLength: int64(len(v2) - 600),
						Header:        h2,
						Body:          io.NopCloser(bytes.NewReader(v2[600:])),
						Request:       r,
					}, nil
				case 3:
					// Clean restart without Range header returns full v2.
					if r.Header.Get("Range") != "" {
						t.Fatalf("restart request unexpectedly sent Range=%q", r.Header.Get("Range"))
					}
					h3 := make(http.Header)
					h3.Set("ETag", `"etag-v2"`)
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(v2)),
						Header:        h3,
						Body:          io.NopCloser(bytes.NewReader(v2)),
						Request:       r,
					}, nil
				default:
					t.Fatalf("unexpected step %d", step)
					return nil, nil
				}
			}),
		}

		if _, err := callResume("https://data.example.test/changing.zip", "changing.zip", nil); err == nil {
			t.Fatal("step 1 unexpectedly succeeded")
		}
		res, err := callResume("https://data.example.test/changing.zip", "changing.zip", nil)
		if err != nil {
			t.Fatalf("resume after ETag change failed: %v", err)
		}
		wantV2Hash := sha256.Sum256(v2)
		if res.Blob != "blob:"+hex.EncodeToString(wantV2Hash[:]) || res.Status != 200 {
			t.Fatalf("restarted result = %+v, want v2 blob:%x status=200", res, wantV2Hash)
		}
		if want := []string{"", "bytes=600-", ""}; fmt.Sprint(requests) != fmt.Sprint(want) {
			t.Fatalf("requests = %v, want %v", requests, want)
		}
	})

	// 3. Server ignoring Range (returning 200 OK) resets partial cleanly without corrupting bytes.
	t.Run("resets cleanly when server ignores Range and returns 200", func(t *testing.T) {
		payload := bytes.Repeat([]byte("no-range-server-data"), 80) // 1600 bytes
		step := 0
		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				step++
				hdr := http.Header{"Last-Modified": {"Sun, 22 Nov 2026 11:00:00 GMT"}}
				if step == 1 {
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(payload)),
						Header:        hdr,
						Body:          io.NopCloser(&failingPrefixReader{data: payload[:500], err: io.ErrUnexpectedEOF}),
						Request:       r,
					}, nil
				}
				return &http.Response{
					StatusCode:    200,
					ContentLength: int64(len(payload)),
					Header:        hdr,
					Body:          io.NopCloser(bytes.NewReader(payload)),
					Request:       r,
				}, nil
			}),
		}

		if _, err := callResume("https://data.example.test/norange.zip", "norange.zip", nil); err == nil {
			t.Fatal("step 1 unexpectedly succeeded")
		}
		res, err := callResume("https://data.example.test/norange.zip", "norange.zip", nil)
		if err != nil {
			t.Fatalf("resume against 200-only server failed: %v", err)
		}
		wantHash := sha256.Sum256(payload)
		if res.Blob != "blob:"+hex.EncodeToString(wantHash[:]) || res.Size != int64(len(payload)) {
			t.Fatalf("200 fallback result = %+v, want blob:%x size=%d", res, wantHash, len(payload))
		}
	})

	// 4. Quota enforcement rejects an oversized resumed download and discards the partial.
	t.Run("enforces cache quota and cleans up oversized partial", func(t *testing.T) {
		quotaBytes = 1024
		ns.SetQuota(blobcache.Quota{MaxBytes: quotaBytes})
		t.Cleanup(func() {
			quotaBytes = 16 << 20
			ns.SetQuota(blobcache.Quota{MaxBytes: quotaBytes})
		})

		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				hdr := make(http.Header)
				hdr.Set("ETag", `"huge"`)
				return &http.Response{
					StatusCode:    200,
					ContentLength: -1, // unknown upfront so quota trips mid-stream
					Header:        hdr,
					Body:          io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 2048))),
					Request:       r,
				}, nil
			}),
		}
		if _, err := callResume("https://data.example.test/huge.zip", "huge.zip", nil); !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("oversized resume err = %v, want ErrQuotaExceeded", err)
		}
		if pBytes, pCount := ns.PartialStats(); pBytes != 0 || pCount != 0 {
			t.Fatalf("oversized partial left on disk: (%d, %d)", pBytes, pCount)
		}
	})

	// 5. Last-Modified mismatch on 206, 416 Range Not Satisfiable, and premature EOF before Content-Length.
	t.Run("restarts on Last-Modified mismatch or 416 and never commits premature EOF", func(t *testing.T) {
		payload := bytes.Repeat([]byte("lm-and-eof-payload"), 80) // 1440 bytes
		step := 0
		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				step++
				switch step {
				case 1:
					// Clean EOF after only 400 of 1440 bytes: must NOT commit, must keep 400-byte partial.
					hdr := make(http.Header)
					hdr.Set("Last-Modified", "Sun, 22 Nov 2026 09:00:00 GMT")
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(payload)),
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(payload[:400])),
						Request:       r,
					}, nil
				case 2:
					// Server returns 416 -> client discards partial and restarts from byte 0.
					if r.Header.Get("Range") != "bytes=400-" {
						t.Fatalf("step 2 Range = %q, want bytes=400-", r.Header.Get("Range"))
					}
					return &http.Response{
						StatusCode: http.StatusRequestedRangeNotSatisfiable,
						Header:     make(http.Header),
						Body:       io.NopCloser(bytes.NewReader(nil)),
						Request:    r,
					}, nil
				case 3:
					// Restarted GET after 416 completes cleanly.
					if r.Header.Get("Range") != "" {
						t.Fatalf("step 3 Range = %q, want empty", r.Header.Get("Range"))
					}
					hdr := make(http.Header)
					hdr.Set("Last-Modified", "Sun, 22 Nov 2026 09:30:00 GMT")
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(payload)),
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(payload)),
						Request:       r,
					}, nil
				default:
					t.Fatalf("unexpected step %d", step)
					return nil, nil
				}
			}),
		}

		if _, err := callResume("https://data.example.test/truncated.zip", "truncated.zip", nil); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("premature EOF err = %v, want io.ErrUnexpectedEOF", err)
		}
		if _, ok := ns.LookupKey("truncated.zip"); ok {
			t.Fatal("premature EOF committed an incomplete blob")
		}
		if pBytes, _ := ns.PartialStats(); pBytes != 400 {
			t.Fatalf("partial bytes after premature EOF = %d, want 400", pBytes)
		}

		res, err := callResume("https://data.example.test/truncated.zip", "truncated.zip", nil)
		if err != nil {
			t.Fatalf("resume after 416 restart failed: %v", err)
		}
		wantHash := sha256.Sum256(payload)
		if res.Blob != "blob:"+hex.EncodeToString(wantHash[:]) || res.Size != int64(len(payload)) {
			t.Fatalf("restarted result = %+v, want blob:%x size=%d", res, wantHash, len(payload))
		}
	})

	// 6. Last-Modified mismatch on 206 discards partial and restarts cleanly from byte 0.
	t.Run("restarts cleanly when Last-Modified changes on 206", func(t *testing.T) {
		v1 := bytes.Repeat([]byte("old-lm-version-01"), 60)
		v2 := bytes.Repeat([]byte("new-lm-version-02"), 60)
		step := 0
		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				step++
				switch step {
				case 1:
					hdr := make(http.Header)
					hdr.Set("Last-Modified", "Sat, 21 Nov 2026 08:00:00 GMT")
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(v1)),
						Header:        hdr,
						Body:          io.NopCloser(&failingPrefixReader{data: v1[:300], err: io.ErrUnexpectedEOF}),
						Request:       r,
					}, nil
				case 2:
					hdr := make(http.Header)
					hdr.Set("Last-Modified", "Sun, 22 Nov 2026 08:00:00 GMT")
					hdr.Set("Content-Range", fmt.Sprintf("bytes 300-%d/%d", len(v2)-1, len(v2)))
					return &http.Response{
						StatusCode:    206,
						ContentLength: int64(len(v2) - 300),
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(v2[300:])),
						Request:       r,
					}, nil
				case 3:
					if r.Header.Get("Range") != "" {
						t.Fatalf("restart after LM mismatch sent Range=%q", r.Header.Get("Range"))
					}
					hdr := make(http.Header)
					hdr.Set("Last-Modified", "Sun, 22 Nov 2026 08:00:00 GMT")
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(v2)),
						Header:        hdr,
						Body:          io.NopCloser(bytes.NewReader(v2)),
						Request:       r,
					}, nil
				default:
					t.Fatalf("unexpected step %d", step)
					return nil, nil
				}
			}),
		}
		if _, err := callResume("https://data.example.test/lm-change.zip", "lm-change.zip", nil); err == nil {
			t.Fatal("step 1 unexpectedly succeeded")
		}
		res, err := callResume("https://data.example.test/lm-change.zip", "lm-change.zip", nil)
		if err != nil {
			t.Fatalf("resume after Last-Modified mismatch failed: %v", err)
		}
		wantHash := sha256.Sum256(v2)
		if res.Blob != "blob:"+hex.EncodeToString(wantHash[:]) || res.Status != 200 {
			t.Fatalf("result = %+v, want v2 blob:%x status=200", res, wantHash)
		}
	})

	// 7. Hosted JS job end-to-end: long timeoutMs (> 10 m) and resume: true through wasm + host_data + blobcache.
	t.Run("hosted JS job end-to-end long timeout and resume", func(t *testing.T) {
		payload := bytes.Repeat([]byte("hosted-job-resume-payload!"), 80) // 2080 bytes
		const etag = `"hosted-v1"`
		var seenDeadline time.Time
		step := 0
		env.m.client = &http.Client{
			Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
				step++
				seenDeadline, _ = r.Context().Deadline()
				hdr := make(http.Header)
				hdr.Set("ETag", etag)
				if step == 1 {
					return &http.Response{
						StatusCode:    200,
						ContentLength: int64(len(payload)),
						Header:        hdr,
						Body:          io.NopCloser(&failingPrefixReader{data: payload[:800], err: io.ErrUnexpectedEOF}),
						Request:       r,
					}, nil
				}
				if r.Header.Get("Range") != "bytes=800-" {
					t.Fatalf("hosted resume Range = %q, want bytes=800-", r.Header.Get("Range"))
				}
				hdr.Set("Content-Range", fmt.Sprintf("bytes 800-%d/%d", len(payload)-1, len(payload)))
				return &http.Response{
					StatusCode:    206,
					ContentLength: int64(len(payload) - 800),
					Header:        hdr,
					Body:          io.NopCloser(bytes.NewReader(payload[800:])),
					Request:       r,
				}, nil
			}),
		}

		eng := wasm.NewEngine(nil)
		t.Cleanup(func() { _ = eng.Close() })
		jsSrc := `
globalThis.handle = function(req) {
  try {
    const res = ether.http.fetchToCache(req.body);
    return { status: 200, body: { ok: true, res: res } };
  } catch (e) {
    return { status: 200, body: { ok: false, err: String(e && e.message || e) } };
  }
};
`
		p, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{Deadline: 2 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close() })

		reqBody, _ := json.Marshal(map[string]any{
			"url":        "https://data.example.test/hosted.zip",
			"libraryKey": libraryKey,
			"name":       "hosted.zip",
			"timeoutMs":  int64(time.Hour / time.Millisecond),
			"resume":     true,
		})
		before := time.Now()
		resp1, err := p.Call(ctx, wasm.Request{Method: "POST", Path: "/ether/job/import", Body: reqBody})
		if err != nil {
			t.Fatalf("hosted job attempt 1 failed: %v", err)
		}
		var reply1 struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(resp1.Body, &reply1); err != nil || reply1.OK {
			t.Fatalf("hosted job attempt 1 reply = %s (err=%v), want ok=false", resp1.Body, err)
		}
		if rem := seenDeadline.Sub(before); rem < 55*time.Minute || rem > 65*time.Minute {
			t.Fatalf("hosted job fetch deadline = %v, want ~1h (past 10m ceiling)", rem)
		}

		resp2, err := p.Call(ctx, wasm.Request{Method: "POST", Path: "/ether/job/import", Body: reqBody})
		if err != nil {
			t.Fatalf("hosted job attempt 2 failed: %v", err)
		}
		var reply2 struct {
			OK  bool             `json:"ok"`
			Res FetchCacheResult `json:"res"`
		}
		if err := json.Unmarshal(resp2.Body, &reply2); err != nil || !reply2.OK {
			t.Fatalf("hosted job attempt 2 reply = %s (err=%v), want ok=true", resp2.Body, err)
		}
		wantHash := sha256.Sum256(payload)
		if reply2.Res.Blob != "blob:"+hex.EncodeToString(wantHash[:]) || reply2.Res.Size != int64(len(payload)) {
			t.Fatalf("hosted job resume result = %+v, want blob:%x size=%d", reply2.Res, wantHash, len(payload))
		}
	})
}

type steppedReader struct {
	data        []byte
	pos         int
	step        int
	onFirstRead func()
	readCalled  bool
}

func (r *steppedReader) Read(p []byte) (int, error) {
	if !r.readCalled {
		r.readCalled = true
		if r.onFirstRead != nil {
			r.onFirstRead()
		}
	}
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	limit := len(p)
	if r.step > 0 && r.step < limit {
		limit = r.step
	}
	n := copy(p[:limit], r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestFetchToCacheResumeEvictsNearFullAndResumesAfterQuotaRefusal(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = []string{"data.example.test"}
	var err error
	rec.ManifestRaw, err = json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	libraryKey := core.PluginLibraryKey(rec.ID, "recording")

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	advance := func(d time.Duration) { now = now.Add(d) }

	const nsQuota int64 = 8192
	const sharedCap int64 = 12288
	blobs, err := blobcache.Open(t.TempDir(), blobcache.Options{
		SweepPeriod: -1,
		SharedCap:   sharedCap,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()

	cachePut, cacheOpen, cacheDelete := blobs.HostBackends(func(_, _ string) blobcache.Quota {
		return blobcache.Quota{MaxBytes: nsQuota}
	})
	cachePartial := blobs.HostPartialBackend(func(_, _ string) blobcache.Quota {
		return blobcache.Quota{MaxBytes: nsQuota}
	})
	env.m.SetKindBackends(KindBackends{
		CachePut:     cachePut,
		CacheOpen:    cacheOpen,
		CacheDelete:  cacheDelete,
		CachePartial: cachePartial,
	})
	h := env.m.HostFor(rec.ID)
	ns := blobs.Namespace(libraryKey, blobcache.Quota{MaxBytes: nsQuota})

	callResume := func(rawURL, name string) (FetchCacheResult, error) {
		args, _ := json.Marshal([]any{map[string]any{
			"url":        rawURL,
			"libraryKey": libraryKey,
			"name":       name,
			"resume":     true,
		}})
		raw, err := CallDataHost(withCallPath(ctx, "/ether/job/import"), h, "http.fetchToCache", args)
		if err != nil {
			return FetchCacheResult{}, err
		}
		var out FetchCacheResult
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal FetchCacheResult: %v", err)
		}
		return out, nil
	}

	// 1. Near-full namespace (7800 / 8192 bytes) accepts a 2400-byte resumable
	//    fetch by reserving up front and evicting the oldest non-open blobs,
	//    never evicting an open blob even when it has the oldest LastOpen.
	openRef, err := ns.Put(ctx, "open-oldest.bin", bytes.NewReader(bytes.Repeat([]byte("O"), 1950)), 1950)
	if err != nil {
		t.Fatal(err)
	}
	openSrc, err := ns.Open(openRef)
	if err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	if _, err := ns.Put(ctx, "old-1.bin", bytes.NewReader(bytes.Repeat([]byte("1"), 1950)), 1950); err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	if _, err := ns.Put(ctx, "old-2.bin", bytes.NewReader(bytes.Repeat([]byte("2"), 1950)), 1950); err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	newerRef, err := ns.Put(ctx, "newer.bin", bytes.NewReader(bytes.Repeat([]byte("N"), 1950)), 1950)
	if err != nil {
		t.Fatal(err)
	}
	if used, count := ns.Stats(); used != 7800 || count != 4 {
		t.Fatalf("initial near-full Stats = (%d, %d), want (7800, 4)", used, count)
	}

	largePayload := bytes.Repeat([]byte("L"), 2400)
	var preReadUsed int64
	var preReadBlobs int
	env.m.client = &http.Client{
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			hdr := make(http.Header)
			hdr.Set("ETag", `"large-v1"`)
			body := &steppedReader{
				data: largePayload,
				step: 600,
				onFirstRead: func() {
					// Up-front reservation must already have evicted old-1 and old-2
					// before a single byte of the response body is read.
					preReadUsed, preReadBlobs = ns.Stats()
				},
			}
			return &http.Response{
				StatusCode:    200,
				ContentLength: int64(len(largePayload)),
				Header:        hdr,
				Body:          io.NopCloser(body),
				Request:       r,
			}, nil
		}),
	}
	res, err := callResume("https://data.example.test/large.bin", "large.bin")
	if err != nil {
		t.Fatalf("large resumable fetch in near-full namespace failed: %v", err)
	}
	if preReadUsed != 3900 || preReadBlobs != 2 {
		t.Fatalf("stats before first body read = (%d, %d), want (3900, 2) from upfront eviction", preReadUsed, preReadBlobs)
	}
	wantLargeHash := sha256.Sum256(largePayload)
	if res.Blob != "blob:"+hex.EncodeToString(wantLargeHash[:]) || res.Size != int64(len(largePayload)) {
		t.Fatalf("large fetch result = %+v, want blob:%x size=%d", res, wantLargeHash, len(largePayload))
	}
	if _, ok := ns.LookupKey("old-1.bin"); ok {
		t.Fatal("expected old-1.bin to be evicted")
	}
	if _, ok := ns.LookupKey("old-2.bin"); ok {
		t.Fatal("expected old-2.bin to be evicted")
	}
	if _, ok := ns.LookupKey("open-oldest.bin"); !ok {
		t.Fatal("open blob was evicted despite active Open handle")
	}
	if _, ok := ns.LookupKey("newer.bin"); !ok {
		t.Fatal("newer.bin was evicted unnecessarily")
	}
	if err := openSrc.Close(); err != nil {
		t.Fatal(err)
	}

	// 2. Shared global cap near-full across namespaces evicts the oldest closed
	//    blob from another namespace when this namespace itself has headroom.
	nsOther := blobs.Namespace("other-lib", blobcache.Quota{MaxBytes: nsQuota})
	advance(time.Minute)
	if _, err := nsOther.Put(ctx, "other-old.bin", bytes.NewReader(bytes.Repeat([]byte("X"), 5600)), 5600); err != nil {
		t.Fatal(err)
	}
	// Refresh ns blobs so other-old.bin is globally the least-recently-opened blob.
	advance(time.Minute)
	for _, ref := range []blobcache.Ref{openRef, newerRef, blobcache.Ref(res.Blob)} {
		src, oerr := ns.Open(ref)
		if oerr != nil {
			t.Fatalf("reopen %s: %v", ref, oerr)
		}
		_ = src.Close()
	}
	globalPayload := bytes.Repeat([]byte("G"), 1500)
	env.m.client = &http.Client{
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			hdr := make(http.Header)
			hdr.Set("ETag", `"global-v1"`)
			return &http.Response{
				StatusCode:    200,
				ContentLength: int64(len(globalPayload)),
				Header:        hdr,
				Body:          io.NopCloser(bytes.NewReader(globalPayload)),
				Request:       r,
			}, nil
		}),
	}
	if _, err := callResume("https://data.example.test/global.bin", "global.bin"); err != nil {
		t.Fatalf("fetch requiring global cap eviction failed: %v", err)
	}
	if _, ok := nsOther.LookupKey("other-old.bin"); ok {
		t.Fatal("expected other-old.bin to be evicted for shared cap headroom")
	}

	// 3. Quota-refused partial keeps its bytes (both on mid-stream write refusal
	//    and on up-front 206 reserve refusal), resumes from the saved offset once
	//    open blobs close, and idle quota-refused partials still expire on TTL.
	ns.Purge()
	advance(time.Minute)
	pinARef, err := ns.Put(ctx, "pin-a.bin", bytes.NewReader(bytes.Repeat([]byte("A"), 3000)), 3000)
	if err != nil {
		t.Fatal(err)
	}
	pinASrc, err := ns.Open(pinARef)
	if err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	pinBRef, err := ns.Put(ctx, "pin-b.bin", bytes.NewReader(bytes.Repeat([]byte("B"), 3000)), 3000)
	if err != nil {
		t.Fatal(err)
	}
	pinBSrc, err := ns.Open(pinBRef)
	if err != nil {
		t.Fatal(err)
	}

	resumePayload := bytes.Repeat([]byte("R0123456"), 400) // 3200 bytes
	const resumeETag = `"quota-resume-v1"`
	var seenRanges []string
	var body2 *steppedReader
	step := 0
	env.m.client = &http.Client{
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			step++
			seenRanges = append(seenRanges, r.Header.Get("Range"))
			hdr := make(http.Header)
			hdr.Set("ETag", resumeETag)
			switch step {
			case 1:
				// Unknown Content-Length streams 500-byte chunks: 2000 bytes fit
				// (6000 open + 2000 partial <= 8192), then chunk 5 hits quota.
				return &http.Response{
					StatusCode:    200,
					ContentLength: -1,
					Header:        hdr,
					Body:          io.NopCloser(&steppedReader{data: resumePayload, step: 500}),
					Request:       r,
				}, nil
			case 2:
				// Open blobs still block the remaining 1200 bytes; up-front Reserve
				// must reject before reading any body bytes and keep the 2000-byte partial.
				hdr.Set("Content-Range", fmt.Sprintf("bytes 2000-%d/%d", len(resumePayload)-1, len(resumePayload)))
				body2 = &steppedReader{data: resumePayload[2000:], step: 500}
				return &http.Response{
					StatusCode:    206,
					ContentLength: int64(len(resumePayload) - 2000),
					Header:        hdr,
					Body:          io.NopCloser(body2),
					Request:       r,
				}, nil
			case 3:
				// After open blobs close, resume from byte 2000 evicts pin-a and completes.
				hdr.Set("Content-Range", fmt.Sprintf("bytes 2000-%d/%d", len(resumePayload)-1, len(resumePayload)))
				return &http.Response{
					StatusCode:    206,
					ContentLength: int64(len(resumePayload) - 2000),
					Header:        hdr,
					Body:          io.NopCloser(bytes.NewReader(resumePayload[2000:])),
					Request:       r,
				}, nil
			default:
				t.Fatalf("unexpected step %d", step)
				return nil, nil
			}
		}),
	}

	if _, err := callResume("https://data.example.test/quota-resume.bin", "quota-resume.bin"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("attempt 1 err = %v, want ErrQuotaExceeded", err)
	}
	if pBytes, pCount := ns.PartialStats(); pBytes != 2000 || pCount != 1 {
		t.Fatalf("partial after mid-stream quota refusal = (%d, %d), want (2000, 1)", pBytes, pCount)
	}

	if _, err := callResume("https://data.example.test/quota-resume.bin", "quota-resume.bin"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("attempt 2 err = %v, want ErrQuotaExceeded", err)
	}
	if body2 == nil || body2.readCalled {
		t.Fatal("attempt 2 read response body despite up-front Reserve failure")
	}
	if pBytes, pCount := ns.PartialStats(); pBytes != 2000 || pCount != 1 {
		t.Fatalf("partial after 206 reserve quota refusal = (%d, %d), want (2000, 1)", pBytes, pCount)
	}

	// Release open handles so the oldest closed blob can now be evicted.
	_ = pinASrc.Close()
	_ = pinBSrc.Close()

	resumed, err := callResume("https://data.example.test/quota-resume.bin", "quota-resume.bin")
	if err != nil {
		t.Fatalf("attempt 3 resume after closing open blobs failed: %v", err)
	}
	wantResumeHash := sha256.Sum256(resumePayload)
	if resumed.Blob != "blob:"+hex.EncodeToString(wantResumeHash[:]) || resumed.Size != int64(len(resumePayload)) || resumed.Status != 206 {
		t.Fatalf("resumed result = %+v, want blob:%x size=%d status=206", resumed, wantResumeHash, len(resumePayload))
	}
	if wantRanges := []string{"", "bytes=2000-", "bytes=2000-"}; fmt.Sprint(seenRanges) != fmt.Sprint(wantRanges) {
		t.Fatalf("seen Range headers = %v, want %v", seenRanges, wantRanges)
	}
	if _, ok := ns.LookupKey("pin-a.bin"); ok {
		t.Fatal("expected oldest closed blob pin-a.bin to be evicted")
	}
	if _, ok := ns.LookupKey("pin-b.bin"); !ok {
		t.Fatal("expected newer blob pin-b.bin to remain")
	}
	if pBytes, pCount := ns.PartialStats(); pBytes != 0 || pCount != 0 {
		t.Fatalf("partial stats after commit = (%d, %d), want (0, 0)", pBytes, pCount)
	}

	// Verify an unresumed quota-refused partial still expires past DefaultPartialTTL.
	pinBSrc, err = ns.Open(pinBRef)
	if err != nil {
		t.Fatal(err)
	}
	resumedSrc, err := ns.Open(blobcache.Ref(resumed.Blob))
	if err != nil {
		t.Fatal(err)
	}
	env.m.client = &http.Client{
		Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			hdr := make(http.Header)
			hdr.Set("ETag", `"expire-me"`)
			return &http.Response{
				StatusCode:    200,
				ContentLength: -1,
				Header:        hdr,
				Body:          io.NopCloser(&steppedReader{data: bytes.Repeat([]byte("E"), 3000), step: 500}),
				Request:       r,
			}, nil
		}),
	}
	if _, err := callResume("https://data.example.test/expire.bin", "expire.bin"); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expire partial fetch err = %v, want ErrQuotaExceeded", err)
	}
	if pBytes, pCount := ns.PartialStats(); pBytes != 1500 || pCount != 1 {
		t.Fatalf("staged bytes before TTL expiry = (%d, %d), want (1500, 1)", pBytes, pCount)
	}
	_ = pinBSrc.Close()
	_ = resumedSrc.Close()
	advance(blobcache.DefaultPartialTTL + time.Minute)
	ns.Sweep(0)
	if pBytes, pCount := ns.PartialStats(); pBytes != 0 || pCount != 0 {
		t.Fatalf("quota-refused partial did not expire after DefaultPartialTTL: (%d, %d)", pBytes, pCount)
	}
}

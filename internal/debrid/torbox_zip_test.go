package debrid

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type testZipSpec struct {
	name      string
	data      []byte
	method    uint16
	encrypted bool
}

func buildTestTorBoxZIP(t *testing.T, specs []testZipSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, s := range specs {
		method := s.method
		if method == 0 && s.method != zip.Store {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   s.name,
			Method: method,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(s.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	// Mark any requested encrypted members by setting bit 0 in both their CD
	// entry and local file header general-purpose bit flag.
	for _, s := range specs {
		if !s.encrypted {
			continue
		}
		markZIPEntryEncrypted(t, raw, s.name)
	}
	return raw
}

func markZIPEntryEncrypted(t *testing.T, raw []byte, name string) {
	t.Helper()
	// Locate central directory headers matching name.
	nameBytes := []byte(name)
	found := false
	for i := 0; i+46+len(nameBytes) <= len(raw); i++ {
		if string(raw[i:i+4]) == "PK\x01\x02" {
			nLen := int(binary.LittleEndian.Uint16(raw[i+28 : i+30]))
			if nLen == len(nameBytes) && bytes.Equal(raw[i+46:i+46+nLen], nameBytes) {
				flags := binary.LittleEndian.Uint16(raw[i+8 : i+10])
				binary.LittleEndian.PutUint16(raw[i+8:i+10], flags|1)
				lfhOff := int(binary.LittleEndian.Uint32(raw[i+42 : i+46]))
				if lfhOff+30 <= len(raw) && string(raw[lfhOff:lfhOff+4]) == "PK\x03\x04" {
					lFlags := binary.LittleEndian.Uint16(raw[lfhOff+6 : lfhOff+8])
					binary.LittleEndian.PutUint16(raw[lfhOff+6:lfhOff+8], lFlags|1)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("member %q not found in built zip", name)
	}
}

func serveRangeZIP(t *testing.T, raw []byte, noRange bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		if noRange || r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
		rng := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		parts := strings.SplitN(rng, "-", 2)
		start, err1 := strconv.ParseInt(parts[0], 10, 64)
		end, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 != nil || err2 != nil || start < 0 || start >= int64(len(raw)) || end < start {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(raw)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= int64(len(raw)) {
			end = int64(len(raw)) - 1
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(raw)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(raw[start : end+1])
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// TorBox holds 100+-file torrents cached only as a single server-side zip:
// checkcached reports them uncached, allow_zip=false has no effect, and
// cached-only createtorrent returns the zip. Ether must bypass the uncertain
// checkcached miss for public likely-zip-only torrents (never private ones),
// read the named stored member over HTTP Range requests without scanning all N
// LFHs, disable DirectURL, and expose the zip's file list via ListReleaseFiles
// (#667, #709).
func TestTorBoxZipOnlyCheckBlindStreamsStoredMemberAndProtectsPrivate(t *testing.T) {
	const (
		smallHash   = "1111111111111111111111111111111111111111"
		largeHash   = "2222222222222222222222222222222222222222"
		privateHash = "3333333333333333333333333333333333333333"
	)

	targetPayload := bytes.Repeat([]byte("ROM-GAME-123-PAYLOAD-"), 4000)
	specs := make([]testZipSpec, 150)
	for i := range specs {
		data := []byte(fmt.Sprintf("placeholder-rom-%03d", i))
		if i == 123 {
			data = targetPayload
		}
		specs[i] = testZipSpec{
			name:   fmt.Sprintf("ROMs/Game %03d (USA).zip", i),
			data:   data,
			method: zip.Store,
		}
	}
	zipBytes := buildTestTorBoxZIP(t, specs)
	cdn, cdnReqs := serveRangeZIP(t, zipBytes, false)

	var createCalls atomic.Int32
	tbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/checkcached":
			// TorBox's checkcached misses zip-only torrents.
			fmt.Fprint(w, `{"success":true,"data":{}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/createtorrent":
			createCalls.Add(1)
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			magnet := strings.ToLower(r.FormValue("magnet"))
			if strings.Contains(magnet, privateHash) {
				t.Errorf("private torrent must NEVER be added to TorBox")
			}
			if !strings.Contains(magnet, largeHash) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"success":false,"error":"NO_CACHED_TORRENT","detail":"not cached"}`)
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":91}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/mylist":
			if r.URL.Query().Get("id") == "" {
				fmt.Fprint(w, `{"success":true,"data":[]}`)
				return
			}
			fmt.Fprintf(w, `{"success":true,"data":{"id":91,"hash":"%s","name":"ROMs","download_present":true,"download_finished":true,"files":[{"id":0,"name":"ROMs.zip","size":%d,"mimetype":"application/zip","zipped":true}]}}`, largeHash, len(zipBytes))
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/requestdl":
			fmt.Fprintf(w, `{"success":true,"data":%q}`, cdn.URL+"/ROMs.zip")
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/controltorrent":
			fmt.Fprint(w, `{"success":true,"data":null}`)
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer tbSrv.Close()

	tb := newTorBox(testKey, fastOpts(tbSrv.URL))
	cfg := config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers:      []config.DebridProvider{{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true}},
	}
	svc := NewService(cfg, Options{
		NewProvider: func(config.DebridProvider, ProviderOptions) (Provider, error) { return tb, nil },
	})
	eng := NewEngine(svc, EngineOptions{})
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	// Seed the uncertain checkcached misses for all three hashes.
	if cached, ok := svc.CheckCached(ctx, []string{smallHash, largeHash, privateHash}); !ok || cached[smallHash] || cached[largeHash] || cached[privateHash] {
		t.Fatalf("CheckCached = (%v, %v), want all false/ok", cached, ok)
	}

	// 1. Small torrent respects the checkcached miss (0 createtorrent calls).
	_, err := svc.Acquire(ctx, core.Release{
		ID:        "small",
		Name:      "Small.Release",
		Protocol:  core.ProtocolTorrent,
		InfoHash:  smallHash,
		FileCount: 5,
		Target:    &core.ReleaseTarget{ExactFile: "Game 001 (USA).zip", AllowNonVideo: true},
	})
	if !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("small Acquire err = %v, want ErrNotCached", err)
	}
	if createCalls.Load() != 0 {
		t.Fatalf("small torrent after checkcached miss made %d createtorrent calls, want 0", createCalls.Load())
	}

	// 2. Private 150-file torrent NEVER calls createtorrent.
	_, err = svc.Acquire(ctx, core.Release{
		ID:        "priv",
		Name:      "Private.Pack",
		Protocol:  core.ProtocolTorrent,
		InfoHash:  privateHash,
		FileCount: 150,
		Private:   true,
		Target:    &core.ReleaseTarget{ExactFile: "Game 123 (USA).zip", AllowNonVideo: true},
	})
	if !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("private Acquire err = %v, want ErrNotCached", err)
	}
	if createCalls.Load() != 0 {
		t.Fatalf("private torrent made %d createtorrent calls, want 0", createCalls.Load())
	}

	// 3. Public 150-file torrent bypasses the uncertain checkcached miss,
	//    inspects only CD + 1 LFH, disables DirectURL, and streams exact bytes.
	payload, err := svc.Acquire(ctx, core.Release{
		ID:        "large",
		Name:      "Large.ROM.Pack",
		Protocol:  core.ProtocolTorrent,
		InfoHash:  largeHash,
		FileCount: 150,
		Target:    &core.ReleaseTarget{ExactFile: "Game 123 (USA).zip", AllowNonVideo: true},
	})
	if err != nil {
		t.Fatalf("large Acquire: %v", err)
	}
	if reqs := cdnReqs.Load(); reqs >= 15 {
		t.Fatalf("acquire made %d CDN range requests for 150-file zip; must not scan all LFHs", reqs)
	}

	ms, err := eng.Open(ctx, "large", payload)
	if err != nil {
		t.Fatalf("Engine.Open: %v", err)
	}
	defer ms.Close()

	if ms.Size() != int64(len(targetPayload)) {
		t.Fatalf("Size = %d, want %d", ms.Size(), len(targetPayload))
	}
	if d, ok := ms.(core.DirectURLSource); ok {
		if u, ok := d.DirectURL(); ok {
			t.Fatalf("DirectURL returned %q for zip member, want false", u)
		}
	}

	// Read full payload and a mid-file seek slice.
	got := make([]byte, len(targetPayload))
	if n, err := ms.ReadAt(got, 0); (err != nil && !errors.Is(err, io.EOF)) || n != len(targetPayload) || !bytes.Equal(got, targetPayload) {
		t.Fatalf("ReadAt full: n=%d err=%v match=%v", n, err, bytes.Equal(got, targetPayload))
	}
	mid := make([]byte, 512)
	const off = 12345
	if n, err := ms.ReadAt(mid, off); err != nil || n != len(mid) || !bytes.Equal(mid, targetPayload[off:off+len(mid)]) {
		t.Fatalf("ReadAt mid: n=%d err=%v match=%v", n, err, bytes.Equal(mid, targetPayload[off:off+len(mid)]))
	}

	// 4. ListReleaseFiles after checkcached miss lists all 150 members inside the zip.
	files, err := svc.ListReleaseFiles(ctx, core.Release{
		ID:        "large-list",
		Name:      "Large.ROM.Pack",
		Protocol:  core.ProtocolTorrent,
		InfoHash:  largeHash,
		FileCount: 150,
	})
	if err != nil {
		t.Fatalf("ListReleaseFiles: %v", err)
	}
	if len(files) != 150 {
		t.Fatalf("ListReleaseFiles count = %d, want 150", len(files))
	}
}

// Unstreamable zip members (deflate for video playback, encrypted members, or a
// non-rangeable 200 OK CDN) must still return errProviderZipped (wrapping
// ErrNotCached, not ErrWrongContent), never trip the circuit breaker, never
// poison the hash for 30 minutes, and fail over cleanly to another provider
// (#667, #709).
func TestTorBoxZipUnstreamableMemberDoesNotPoisonAndFailsOver(t *testing.T) {
	movieData := bytes.Repeat([]byte("mkv-video-stream-bytes-"), 2000)
	bonusData := bytes.Repeat([]byte("mkv-bonus-stored-bytes-"), 2000)
	zipBytes := buildTestTorBoxZIP(t, []testZipSpec{
		{name: "Pack/Movie.mkv", data: movieData, method: zip.Deflate},
		{name: "Pack/Bonus.mkv", data: bonusData, method: zip.Store},
		{name: "Pack/Secret.mkv", data: []byte("encrypted-bytes"), method: zip.Store, encrypted: true},
	})
	cdnRange, _ := serveRangeZIP(t, zipBytes, false)
	cdnNoRange, _ := serveRangeZIP(t, zipBytes, true)

	var useNoRange atomic.Bool
	tbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/createtorrent":
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":99}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/mylist":
			if r.URL.Query().Get("id") == "" {
				fmt.Fprint(w, `{"success":true,"data":[]}`)
				return
			}
			fmt.Fprintf(w, `{"success":true,"data":{"id":99,"hash":"%s","name":"Pack","download_present":true,"download_finished":true,"files":[{"id":0,"name":"Pack.zip","size":%d,"mimetype":"application/zip","zipped":true}]}}`, testHash, len(zipBytes))
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/requestdl":
			u := cdnRange.URL + "/Pack.zip"
			if useNoRange.Load() {
				u = cdnNoRange.URL + "/Pack.zip"
			}
			fmt.Fprintf(w, `{"success":true,"data":%q}`, u)
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/controltorrent":
			fmt.Fprint(w, `{"success":true,"data":null}`)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer tbSrv.Close()

	tb := newTorBox(testKey, fastOpts(tbSrv.URL))
	svc := NewService(config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers:      []config.DebridProvider{{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true}},
	}, Options{
		NewProvider: func(config.DebridProvider, ProviderOptions) (Provider, error) { return tb, nil },
	})
	ctx := context.Background()

	// 1. Deflate member for video (!AllowNonVideo) returns errProviderZipped (ErrNotCached, not ErrWrongContent).
	_, err := svc.Acquire(ctx, core.Release{
		ID:       "vid-deflate",
		Name:     "Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Movie.mkv"},
	})
	if !errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("deflate video err = %v, want ErrNotCached (not ErrWrongContent)", err)
	}

	// 2. Encrypted member returns errProviderZipped without poisoning the hash.
	_, err = svc.Acquire(ctx, core.Release{
		ID:       "vid-enc",
		Name:     "Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Secret.mkv", AllowNonVideo: true},
	})
	if !errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("encrypted member err = %v, want ErrNotCached (not ErrWrongContent)", err)
	}

	// 3. Non-rangeable 200 OK CDN refuses with errProviderZipped without tripping breaker.
	useNoRange.Store(true)
	_, err = svc.Acquire(ctx, core.Release{
		ID:       "vid-norange",
		Name:     "Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Bonus.mkv"},
	})
	if !errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("non-rangeable CDN err = %v, want ErrNotCached", err)
	}
	useNoRange.Store(false)

	if st := svc.Status(); len(st.Providers) != 1 || st.Providers[0].State != "ok" || st.Providers[0].Failures != 0 {
		t.Fatalf("unstreamable zip errors tripped breaker: %+v", st.Providers)
	}

	// 4. The hash was NOT poisoned for 30 minutes: immediately requesting the
	//    streamable stored member Bonus.mkv on the SAME hash succeeds!
	payload, err := svc.Acquire(ctx, core.Release{
		ID:       "vid-stored",
		Name:     "Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Bonus.mkv"},
	})
	if err != nil || !IsPayload(payload) {
		t.Fatalf("stored member on same hash after zip-only refusal failed: %v", err)
	}

	// 5. With a second provider, deflate video fails over cleanly.
	media := newMediaServer(t, 4096)
	pm := newFakeProvider("premiumize", media, []File{{ID: "1", Path: "Pack/Movie.mkv", Size: 4096}})
	pm.cached[testHash] = true
	svcPair := NewService(config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers: []config.DebridProvider{
			{ID: "tb", Kind: "torbox", Name: "TorBox", APIKey: testKey, Enabled: true, Priority: 1},
			{ID: "pm", Kind: "premiumize", Name: "Premiumize", APIKey: testKey, Enabled: true, Priority: 2},
		},
	}, Options{
		NewProvider: func(c config.DebridProvider, _ ProviderOptions) (Provider, error) {
			if c.ID == "tb" {
				return tb, nil
			}
			return pm, nil
		},
	})
	failoverPayload, err := svcPair.Acquire(ctx, core.Release{
		ID:       "vid-failover",
		Name:     "Pack",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		Target:   &core.ReleaseTarget{ExactFile: "Movie.mkv"},
	})
	if err != nil || !IsPayload(failoverPayload) {
		t.Fatalf("failover after deflate video zip failed: %v", err)
	}
}

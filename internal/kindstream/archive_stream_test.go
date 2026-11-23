package kindstream

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/archive"
	at "github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
)

func buildMultiMemberZIP(t *testing.T, files map[string][]byte, order []string, encrypted bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestSpeed)
	})
	for _, name := range order {
		hdr := &zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("CreateHeader(%s): %v", name, err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip: %v", err)
	}
	raw := buf.Bytes()
	if encrypted {
		// Flip general-purpose bit 0 (traditional/AES encryption flag) in both local and central headers.
		for i := 0; i+30 <= len(raw); i++ {
			sig := binary.LittleEndian.Uint32(raw[i:])
			switch sig {
			case 0x04034b50: // local file header: flags at +6
				raw[i+6] |= 0x01
			case 0x02014b50: // central directory header: flags at +8
				raw[i+8] |= 0x01
			}
		}
	}
	return raw
}

func highEntropyBytes(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	out := make([]byte, n)
	_, _ = r.Read(out)
	return out
}

func TestCompressedArchivesStreamOverRangedHTTPWithoutWholeVolumeCache(t *testing.T) {
	ctx := context.Background()

	altDir := filepath.Join("..", "archive", "testdata", "altmount")
	rarComp, err := os.ReadFile(filepath.Join(altDir, "compressed.rar"))
	if err != nil {
		t.Fatalf("read compressed.rar: %v", err)
	}
	rarSolid, err := os.ReadFile(filepath.Join(altDir, "solid.rar"))
	if err != nil {
		t.Fatalf("read solid.rar: %v", err)
	}
	rarMulti1, err := os.ReadFile(filepath.Join(altDir, "multi.part1.rar"))
	if err != nil {
		t.Fatalf("read multi.part1.rar: %v", err)
	}
	rarMulti2, err := os.ReadFile(filepath.Join(altDir, "multi.part2.rar"))
	if err != nil {
		t.Fatalf("read multi.part2.rar: %v", err)
	}
	rarMulti3, err := os.ReadFile(filepath.Join(altDir, "multi.part3.rar"))
	if err != nil {
		t.Fatalf("read multi.part3.rar: %v", err)
	}

	// Build 1 MiB padded multi-member compressed ZIP and non-solid 7z so whole-volume caching
	// would transfer >1 MiB and require non-Range GETs, while ranged member streaming reads only
	// central directory/header + target member (<256 KiB).
	bigPrefix := highEntropyBytes(77, 1024*1024)
	targetPayload := bytes.Repeat([]byte("streamed-archive-member-content-"), 300) // 9,600 B

	paddedZIP := buildMultiMemberZIP(t, map[string][]byte{
		"00_prefix.bin":    bigPrefix,
		"books/chapter.md": targetPayload,
	}, []string{"00_prefix.bin", "books/chapter.md"}, false)

	nonSolid7z := at.Build7zLZMA2([]at.SevenZipFile{
		{Name: "00_prefix.bin", Data: bigPrefix},
		{Name: "books/chapter.md", Data: targetPayload},
	}, false)

	var nonRangeRequests atomic.Int64
	var zipBytesServed atomic.Int64
	var sz7BytesServed atomic.Int64

	serveRangedOnly := func(name string, data []byte, counter *atomic.Int64) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") == "" {
				nonRangeRequests.Add(1)
				http.Error(w, "whole-volume GET forbidden; expected Range", http.StatusBadRequest)
				return
			}
			rec := httptest.NewRecorder()
			http.ServeContent(rec, r, name, time.Time{}, bytes.NewReader(data))
			for k, vals := range rec.Header() {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.Code)
			n, _ := w.Write(rec.Body.Bytes())
			if counter != nil {
				counter.Add(int64(n))
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/vol/padded.zip", serveRangedOnly("padded.zip", paddedZIP, &zipBytesServed))
	mux.HandleFunc("/vol/nonsolid.7z", serveRangedOnly("nonsolid.7z", nonSolid7z, &sz7BytesServed))
	mux.HandleFunc("/vol/compressed.rar", serveRangedOnly("compressed.rar", rarComp, nil))
	mux.HandleFunc("/vol/solid.rar", serveRangedOnly("solid.rar", rarSolid, nil))
	mux.HandleFunc("/vol/multi.part1.rar", serveRangedOnly("multi.part1.rar", rarMulti1, nil))
	mux.HandleFunc("/vol/multi.part2.rar", serveRangedOnly("multi.part2.rar", rarMulti2, nil))
	mux.HandleFunc("/vol/multi.part3.rar", serveRangedOnly("multi.part3.rar", rarMulti3, nil))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatalf("blobcache.Open: %v", err)
	}
	defer cache.Close()
	ns := cache.Namespace("ranged-archives", blobcache.Quota{MaxBytes: 32 << 20})

	deps := Deps{
		HTTP:  srv.Client(),
		Cache: ns,
		OpenRelease: func(ctx context.Context, r core.ReleaseHint) (core.MediaSource, error) {
			switch r.InfoHash {
			case "rel-7z-nonsolid":
				return &fakeMediaSource{id: "r7z", name: "pack.7z", data: nonSolid7z}, nil
			case "rel-rar-comp":
				return &fakeMediaSource{id: "rrar", name: "pack.rar", data: rarComp}, nil
			default:
				return nil, core.ErrSourceUnavailable
			}
		},
	}

	// 1. Deflate ZIP streams over HTTP Range without downloading the 1 MiB prefix member.
	bsZIP, err := Open(ctx, core.PlayableSource{
		ID:      "zip-ranged",
		Pattern: PatternContainer,
		MIME:    "text/markdown",
		Container: &core.ContainerRef{
			Format:  "zip",
			Volumes: []core.VolumeRef{{URL: srv.URL + "/vol/padded.zip", Size: int64(len(paddedZIP))}},
			Member:  "books/chapter.md",
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open(padded.zip): %v", err)
	}
	gotZIP := make([]byte, bsZIP.Size())
	if _, err := bsZIP.ReadAt(gotZIP, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(padded.zip): %v", err)
	}
	_ = bsZIP.Close()
	if !bytes.Equal(gotZIP, targetPayload) {
		t.Fatalf("padded.zip payload mismatch")
	}
	if zipBytesServed.Load() >= 256*1024 {
		t.Fatalf("padded.zip transferred %d bytes over HTTP, want <256 KiB (archive is %d bytes)", zipBytesServed.Load(), len(paddedZIP))
	}

	// 2. Non-solid LZMA2 7z streams over HTTP Range without downloading the 1 MiB prefix folder.
	bs7z, err := Open(ctx, core.PlayableSource{
		ID:      "7z-nonsolid-ranged",
		Pattern: PatternContainer,
		MIME:    "text/markdown",
		Container: &core.ContainerRef{
			Format:  "7z",
			Volumes: []core.VolumeRef{{URL: srv.URL + "/vol/nonsolid.7z", Size: int64(len(nonSolid7z))}},
			Member:  "books/chapter.md",
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open(nonsolid.7z): %v", err)
	}
	got7z := make([]byte, bs7z.Size())
	if _, err := bs7z.ReadAt(got7z, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(nonsolid.7z): %v", err)
	}
	_ = bs7z.Close()
	if !bytes.Equal(got7z, targetPayload) {
		t.Fatalf("nonsolid.7z payload mismatch")
	}
	if sz7BytesServed.Load() >= 256*1024 {
		t.Fatalf("nonsolid.7z transferred %d bytes over HTTP, want <256 KiB (archive is %d bytes)", sz7BytesServed.Load(), len(nonSolid7z))
	}

	// 3. Compressed RAR, solid RAR (second member), and 3-part compressed RAR all stream via Range without whole-volume GET.
	for _, tc := range []struct {
		name     string
		vols     []core.VolumeRef
		member   string
		wantSize int64
	}{
		{
			name:     "compressed.rar",
			vols:     []core.VolumeRef{{URL: srv.URL + "/vol/compressed.rar", Size: int64(len(rarComp))}},
			member:   "release.nzb",
			wantSize: 269,
		},
		{
			name:     "solid.rar",
			vols:     []core.VolumeRef{{URL: srv.URL + "/vol/solid.rar", Size: int64(len(rarSolid))}},
			member:   "release.nzb",
			wantSize: 270,
		},
		{
			name: "multi.part1..3.rar",
			vols: []core.VolumeRef{
				{URL: srv.URL + "/vol/multi.part1.rar", Size: int64(len(rarMulti1))},
				{URL: srv.URL + "/vol/multi.part2.rar", Size: int64(len(rarMulti2))},
				{URL: srv.URL + "/vol/multi.part3.rar", Size: int64(len(rarMulti3))},
			},
			member:   "release.nzb",
			wantSize: 10945,
		},
	} {
		bs, err := Open(ctx, core.PlayableSource{
			ID:      tc.name,
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:  "rar",
				Volumes: tc.vols,
				Member:  tc.member,
			},
		}, deps)
		if err != nil {
			t.Fatalf("Open(%s): %v", tc.name, err)
		}
		if bs.Size() != tc.wantSize {
			t.Fatalf("%s size = %d, want %d", tc.name, bs.Size(), tc.wantSize)
		}
		buf := make([]byte, bs.Size())
		if _, err := bs.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt(%s): %v", tc.name, err)
		}
		_ = bs.Close()
		if !bytes.Contains(buf, []byte("<nzb")) {
			t.Fatalf("%s decoded content missing expected XML", tc.name)
		}
	}

	if nonRangeRequests.Load() != 0 {
		t.Fatalf("observed %d whole-volume non-Range GET requests, want 0", nonRangeRequests.Load())
	}

	// 4. Release + container composition extracts compressed 7z and compressed RAR over MediaSource.
	bsRel7z, err := Open(ctx, core.PlayableSource{
		ID:      "rel-comp-7z",
		Pattern: PatternRelease,
		MIME:    "text/markdown",
		Release: &core.ReleaseHint{InfoHash: "rel-7z-nonsolid"},
		Container: &core.ContainerRef{
			Format: "7z",
			Member: "books/chapter.md",
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open(release+7z): %v", err)
	}
	gotRel7z := make([]byte, bsRel7z.Size())
	_, _ = bsRel7z.ReadAt(gotRel7z, 0)
	_ = bsRel7z.Close()
	if !bytes.Equal(gotRel7z, targetPayload) {
		t.Fatalf("release+7z payload mismatch")
	}

	bsRelRAR, err := Open(ctx, core.PlayableSource{
		ID:      "rel-comp-rar",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		Release: &core.ReleaseHint{InfoHash: "rel-rar-comp", FileHint: "release.nzb"},
	}, deps)
	if err != nil {
		t.Fatalf("Open(release+rar): %v", err)
	}
	if bsRelRAR.Size() != 269 {
		t.Fatalf("release+rar size = %d, want 269", bsRelRAR.Size())
	}
	_ = bsRelRAR.Close()
}

func TestSolid7zCachesRemoteVolumeHonoursContextAndQuota(t *testing.T) {
	// Verify default streaming HTTP client does not set a fixed 30s body deadline.
	defClient := defaultStreamingHTTPClient()
	if defClient.Timeout != 0 {
		t.Fatalf("defaultStreamingHTTPClient().Timeout = %v, want 0 (honour ctx)", defClient.Timeout)
	}
	tr, ok := defClient.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("defaultStreamingHTTPClient() ResponseHeaderTimeout = %v, want 30s", tr)
	}

	sz7Dir := filepath.Join("..", "archive", "testdata", "7z")
	solid7z, err := os.ReadFile(filepath.Join(sz7Dir, "compressed.7z"))
	if err != nil {
		t.Fatalf("read compressed.7z: %v", err)
	}
	wantC, err := os.ReadFile(filepath.Join(sz7Dir, "rand_c.bin"))
	if err != nil {
		t.Fatalf("read rand_c.bin: %v", err)
	}

	var wholeVolumeGets atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/solid/compressed.7z", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			wholeVolumeGets.Add(1)
		}
		http.ServeContent(w, r, "compressed.7z", time.Time{}, bytes.NewReader(solid7z))
	})
	mux.HandleFunc("/solid/slow.7z", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			http.ServeContent(w, r, "slow.7z", time.Time{}, bytes.NewReader(solid7z))
			return
		}
		// Stall whole-volume body stream until request context cancels.
		w.Header().Set("Content-Length", "38529")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(solid7z[:1024])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatalf("blobcache.Open: %v", err)
	}
	defer cache.Close()
	ns := cache.Namespace("solid-7z", blobcache.Quota{MaxBytes: 32 << 20})

	// 1. Quota cap (MaxDownloadBytes) rejects solid-7z whole-volume download before caching.
	_, err = Open(context.Background(), core.PlayableSource{
		ID:      "solid-over-quota",
		Pattern: PatternContainer,
		MIME:    "application/octet-stream",
		Container: &core.ContainerRef{
			Format:  "7z",
			Volumes: []core.VolumeRef{{URL: srv.URL + "/solid/compressed.7z", Size: int64(len(solid7z))}},
			Member:  "sub/rand_c.bin",
		},
	}, Deps{
		HTTP:             srv.Client(),
		Cache:            ns,
		MaxDownloadBytes: 4096, // smaller than 38,529 B volume
	})
	if !errors.Is(err, ErrSizeLimitExceeded) {
		t.Fatalf("solid 7z over MaxDownloadBytes = %v, want ErrSizeLimitExceeded", err)
	}
	if wholeVolumeGets.Load() != 0 {
		t.Fatalf("solid 7z over MaxDownloadBytes issued %d whole-volume GETs, want 0", wholeVolumeGets.Load())
	}

	// 2. Within quota, solid 7z caches the remote volume once and extracts second substream accurately.
	bs, err := Open(context.Background(), core.PlayableSource{
		ID:      "solid-ok",
		Pattern: PatternContainer,
		MIME:    "application/octet-stream",
		Container: &core.ContainerRef{
			Format:  "7z",
			Volumes: []core.VolumeRef{{URL: srv.URL + "/solid/compressed.7z", Size: int64(len(solid7z))}},
			Member:  "sub/rand_c.bin",
		},
	}, Deps{
		HTTP:             srv.Client(),
		Cache:            ns,
		MaxDownloadBytes: 8 << 20,
	})
	if err != nil {
		t.Fatalf("Open(solid-ok): %v", err)
	}
	got := make([]byte, bs.Size())
	if _, err := bs.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(solid-ok): %v", err)
	}
	_ = bs.Close()
	if !bytes.Equal(got, wantC) {
		t.Fatalf("solid 7z extracted payload mismatch")
	}
	if wholeVolumeGets.Load() != 1 {
		t.Fatalf("wholeVolumeGets = %d, want 1", wholeVolumeGets.Load())
	}

	// 3. Context cancellation aborts an in-flight solid 7z volume download promptly.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err = Open(cancelCtx, core.PlayableSource{
		ID:      "solid-cancel",
		Pattern: PatternContainer,
		MIME:    "application/octet-stream",
		Container: &core.ContainerRef{
			Format:  "7z",
			Volumes: []core.VolumeRef{{URL: srv.URL + "/solid/slow.7z", Size: int64(len(solid7z))}},
			Member:  "sub/rand_c.bin",
		},
	}, Deps{
		HTTP:  srv.Client(),
		Cache: ns,
	})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled solid 7z Open = %v, want context deadline/canceled", err)
	}
}

func TestUnstreamableArchivesReturnTypedError(t *testing.T) {
	ctx := context.Background()

	encRAR, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "altmount", "encrypted.rar"))
	if err != nil {
		t.Fatalf("read encrypted.rar: %v", err)
	}
	enc7zContent, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "7z", "enc_content.7z"))
	if err != nil {
		t.Fatalf("read enc_content.7z: %v", err)
	}
	enc7zHeader, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "7z", "enc_header.7z"))
	if err != nil {
		t.Fatalf("read enc_header.7z: %v", err)
	}
	bcj7z, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "7z", "bcj_lzma.7z"))
	if err != nil {
		t.Fatalf("read bcj_lzma.7z: %v", err)
	}
	encZIP := buildMultiMemberZIP(t, map[string][]byte{"secret.txt": []byte("encrypted payload")}, []string{"secret.txt"}, true)

	var nonRangeGets atomic.Int64
	mux := http.NewServeMux()
	for path, data := range map[string][]byte{
		"/u/enc.rar":        encRAR,
		"/u/enc_content.7z": enc7zContent,
		"/u/enc_header.7z":  enc7zHeader,
		"/u/bcj.7z":         bcj7z,
		"/u/enc.zip":        encZIP,
	} {
		p, b := path, data
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") == "" {
				nonRangeGets.Add(1)
			}
			http.ServeContent(w, r, filepath.Base(p), time.Time{}, bytes.NewReader(b))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatalf("blobcache.Open: %v", err)
	}
	defer cache.Close()
	deps := Deps{
		HTTP:  srv.Client(),
		Cache: cache.Namespace("unstreamable", blobcache.Quota{MaxBytes: 16 << 20}),
	}

	candidates := []core.PlayableSource{
		{
			ID:      "enc-rar",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:      "rar",
				Volumes:     []core.VolumeRef{{URL: srv.URL + "/u/enc.rar", Size: int64(len(encRAR))}},
				MemberIndex: new(0),
			},
		},
		{
			ID:      "enc-7z-content",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:      "7z",
				Volumes:     []core.VolumeRef{{URL: srv.URL + "/u/enc_content.7z", Size: int64(len(enc7zContent))}},
				MemberIndex: new(0),
			},
		},
		{
			ID:      "enc-7z-header",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:      "7z",
				Volumes:     []core.VolumeRef{{URL: srv.URL + "/u/enc_header.7z", Size: int64(len(enc7zHeader))}},
				MemberIndex: new(0),
			},
		},
		{
			ID:      "bcj-7z",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:      "7z",
				Volumes:     []core.VolumeRef{{URL: srv.URL + "/u/bcj.7z", Size: int64(len(bcj7z))}},
				MemberIndex: new(0),
			},
		},
		{
			ID:      "enc-zip",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &core.ContainerRef{
				Format:      "zip",
				Volumes:     []core.VolumeRef{{URL: srv.URL + "/u/enc.zip", Size: int64(len(encZIP))}},
				MemberIndex: new(0),
			},
		},
	}

	for _, src := range candidates {
		_, err := Open(ctx, src, deps)
		if !errors.Is(err, ErrMemberUndecodable) || !errors.Is(err, core.ErrArchiveUnstreamable) {
			t.Fatalf("Open(%s) = %v, want ErrMemberUndecodable + core.ErrArchiveUnstreamable", src.ID, err)
		}
	}

	// Inspect7zMember / ranged checks must refuse encrypted & unsupported archives before any whole-volume download.
	if nonRangeGets.Load() != 0 {
		t.Fatalf("unstreamable archives triggered %d whole-volume downloads, want 0", nonRangeGets.Load())
	}

	// OpenSources across only unstreamable candidates preserves core.ErrArchiveUnstreamable (not ErrSourceUnavailable).
	_, _, err = OpenSources(ctx, candidates, deps)
	if !errors.Is(err, core.ErrArchiveUnstreamable) {
		t.Fatalf("OpenSources(all unstreamable) = %v, want core.ErrArchiveUnstreamable", err)
	}
	if errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("OpenSources(all unstreamable) masked error as ErrSourceUnavailable: %v", err)
	}
}

func TestOpenSourcesPreservesPermanentRejectionWithoutSourceUnavailable(t *testing.T) {
	ctx := context.Background()
	sources := []core.PlayableSource{
		{ID: "rel-1", Pattern: PatternRelease, MIME: "application/x-cbr", Release: &core.ReleaseHint{NZBURL: "https://idx.test/1.nzb"}},
		{ID: "rel-2", Pattern: PatternRelease, MIME: "application/x-cbr", Release: &core.ReleaseHint{NZBURL: "https://idx.test/2.nzb"}},
	}

	// All candidates failing with permanent rejection (e.g. missing archive volume)
	// must surface core.ErrReleaseRejected, NOT core.ErrSourceUnavailable.
	_, _, err := OpenSources(ctx, sources, Deps{
		OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
			if src.ID == "rel-1" {
				return nil, fmt.Errorf("rar open: %w", archive.ErrMissingVolume)
			}
			return nil, fmt.Errorf("%w: %w", core.ErrReleaseRejected, core.ErrArticleMissing)
		},
	})
	if !errors.Is(err, core.ErrReleaseRejected) || !errors.Is(err, archive.ErrMissingVolume) {
		t.Fatalf("OpenSources(all rejected) = %v, want core.ErrReleaseRejected + archive.ErrMissingVolume", err)
	}
	if errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("OpenSources(all rejected) masked permanent rejection as ErrSourceUnavailable: %v", err)
	}

	// Mixed permanent rejection + transient failure stays core.ErrSourceUnavailable.
	_, _, err = OpenSources(ctx, sources, Deps{
		OpenReleaseSource: func(_ context.Context, src core.PlayableSource) (core.MediaSource, error) {
			if src.ID == "rel-1" {
				return nil, fmt.Errorf("%w: %w", core.ErrReleaseRejected, archive.ErrMissingVolume)
			}
			return nil, errors.New("nntp connection reset")
		},
	})
	if !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("OpenSources(mixed permanent+transient) = %v, want core.ErrSourceUnavailable", err)
	}
}

func TestReleaseNonVideoMIMEResolutionAndNoAutoUnwrap(t *testing.T) {
	ctx := context.Background()

	// Build a valid EPUB (ZIP container) and a binary file containing "ftyp" at offset 4.
	epubBytes := buildTestZIP(t, "OEBPS/chapter1.xhtml", []byte("<html><body>Chapter 1</body></html>"), zip.Deflate)
	ftypLikeBin := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypisom-not-a-video-payload")...)

	deps := Deps{
		OpenRelease: func(ctx context.Context, r core.ReleaseHint) (core.MediaSource, error) {
			switch r.InfoHash {
			case "hash-epub":
				return &fakeMediaSource{id: "r-epub", name: "Novel.epub", data: epubBytes}, nil
			case "hash-cbz":
				return &fakeMediaSource{id: "r-cbz", name: "Issue01.cbz", data: epubBytes}, nil
			case "hash-bin":
				return &fakeMediaSource{id: "r-bin", name: "Data.bin", data: ftypLikeBin}, nil
			default:
				return nil, core.ErrSourceUnavailable
			}
		},
	}

	// 1. EPUB release with plugin MIME "application/epub+zip" is served whole (not auto-unwrapped as ZIP).
	bsEPUB, err := Open(ctx, core.PlayableSource{
		ID:      "rel-epub",
		Pattern: PatternRelease,
		MIME:    "application/epub+zip",
		Release: &core.ReleaseHint{InfoHash: "hash-epub", File: "Novel.epub"},
	}, deps)
	if err != nil {
		t.Fatalf("Open(rel-epub): %v", err)
	}
	defer bsEPUB.Close()
	if bsEPUB.MIME() != "application/epub+zip" || bsEPUB.Size() != int64(len(epubBytes)) {
		t.Fatalf("rel-epub: mime=%q size=%d, want application/epub+zip size=%d", bsEPUB.MIME(), bsEPUB.Size(), len(epubBytes))
	}

	// 2. Explicitly-selected .cbz release with omitted MIME stays whole and defaults to application/octet-stream.
	bsCBZ, err := Open(ctx, core.PlayableSource{
		ID:      "rel-cbz",
		Pattern: PatternRelease,
		Release: &core.ReleaseHint{InfoHash: "hash-cbz", FileIndex: new(0)},
	}, deps)
	if err != nil {
		t.Fatalf("Open(rel-cbz): %v", err)
	}
	defer bsCBZ.Close()
	if bsCBZ.MIME() != "application/octet-stream" || bsCBZ.Size() != int64(len(epubBytes)) {
		t.Fatalf("rel-cbz: mime=%q size=%d, want application/octet-stream size=%d", bsCBZ.MIME(), bsCBZ.Size(), len(epubBytes))
	}

	// 3. Explicitly-selected .bin release with omitted MIME and "ftyp" magic bytes still gets application/octet-stream, never video/*.
	bsBin, err := Open(ctx, core.PlayableSource{
		ID:      "rel-bin",
		Pattern: PatternRelease,
		Release: &core.ReleaseHint{InfoHash: "hash-bin", File: "Data.bin"},
	}, deps)
	if err != nil {
		t.Fatalf("Open(rel-bin): %v", err)
	}
	defer bsBin.Close()
	if bsBin.MIME() != "application/octet-stream" {
		t.Fatalf("rel-bin MIME = %q, want application/octet-stream (never video/*)", bsBin.MIME())
	}

	rec := httptest.NewRecorder()
	Handler(bsBin).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/item", nil))
	if gotCT := rec.Header().Get("Content-Type"); gotCT != "application/octet-stream" {
		t.Fatalf("Handler Content-Type = %q, want application/octet-stream", gotCT)
	}
}

package kindstream

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

func makeInnerZIP(t *testing.T, name string, data []byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// End-to-end test for #709 and #667: TorBox holds a 150-file ROM pack only as a
// single server-side zip, checkcached misses it, createtorrent returns the zip,
// and kindstream reads both stored and deflate named members (including
// member-inside-a-member archives) and verifies declared digests against the
// final served bytes.
func TestTorBoxZipStoredAndDeflateMemberInsideMemberEndToEnd(t *testing.T) {
	const packHash = "abcdef0123456789abcdef0123456789abcdef01"

	marioNES := append([]byte("NES\x1a"), bytes.Repeat([]byte("mario-prg-rom-data-"), 512)...)
	marioInnerZIP := makeInnerZIP(t, "Super Mario Bros. (World).nes", marioNES, zip.Deflate)

	zeldaNES := append([]byte("NES\x1a"), bytes.Repeat([]byte("zelda-prg-rom-data-"), 512)...)
	zeldaInnerZIP := makeInnerZIP(t, "Zelda (USA).nes", zeldaNES, zip.Deflate)

	manualPDF := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("instruction booklet page\n"), 512)...)

	// Build outer TorBox server-side zip containing both stored and deflate members
	// (including archives-inside-the-zip) plus filler files.
	var outerBuf bytes.Buffer
	zw := zip.NewWriter(&outerBuf)
	addOuter := func(name string, data []byte, method uint16) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 120 {
		addOuter(fmt.Sprintf("Pack/Filler %03d.zip", i), []byte("filler"), zip.Store)
	}
	addOuter("Pack/Super Mario Bros. (World).zip", marioInnerZIP, zip.Store)
	addOuter("Pack/Zelda (USA).zip", zeldaInnerZIP, zip.Deflate)
	addOuter("Pack/Manual.pdf", manualPDF, zip.Deflate)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	outerZIP := outerBuf.Bytes()

	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		parts := strings.SplitN(rng, "-", 2)
		start, err1 := strconv.ParseInt(parts[0], 10, 64)
		end, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 != nil || err2 != nil || start < 0 || start >= int64(len(outerZIP)) || end < start {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(outerZIP)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= int64(len(outerZIP)) {
			end = int64(len(outerZIP)) - 1
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(outerZIP)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(outerZIP[start : end+1])
	}))
	defer cdn.Close()

	tbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/checkcached":
			fmt.Fprint(w, `{"success":true,"data":{}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/createtorrent":
			fmt.Fprint(w, `{"success":true,"data":{"torrent_id":77}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/mylist":
			if r.URL.Query().Get("id") == "" {
				fmt.Fprint(w, `{"success":true,"data":[]}`)
				return
			}
			fmt.Fprintf(w, `{"success":true,"data":{"id":77,"hash":"%s","name":"Pack","download_present":true,"download_finished":true,"files":[{"id":0,"name":"Pack.zip","size":%d,"mimetype":"application/zip","zipped":true}]}}`, packHash, len(outerZIP))
		case r.Method == http.MethodGet && r.URL.Path == "/torrents/requestdl":
			fmt.Fprintf(w, `{"success":true,"data":%q}`, cdn.URL+"/Pack.zip")
		case r.Method == http.MethodPost && r.URL.Path == "/torrents/controltorrent":
			fmt.Fprint(w, `{"success":true,"data":null}`)
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer tbSrv.Close()

	svc := debrid.NewService(config.Debrid{
		CachedOnly:     true,
		RemoveUncached: true,
		Providers: []config.DebridProvider{{
			ID:      "tb",
			Kind:    "torbox",
			Name:    "TorBox",
			APIKey:  "test-key",
			Enabled: true,
		}},
	}, debrid.Options{
		NewProvider: func(c config.DebridProvider, o debrid.ProviderOptions) (debrid.Provider, error) {
			o.BaseURL = tbSrv.URL
			return debrid.NewProvider(c, o)
		},
	})
	eng := debrid.NewEngine(svc, debrid.EngineOptions{})
	t.Cleanup(func() { _ = eng.Close() })

	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatalf("blobcache.Open: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	ns := cache.Namespace("torbox-zip-test", blobcache.Quota{MaxBytes: 64 << 20})

	ctx := context.Background()
	// Prime TorBox's uncertain checkcached miss first.
	if cached, ok := svc.CheckCached(ctx, []string{packHash}); !ok || cached[packHash] {
		t.Fatalf("CheckCached = (%v, %v), want false/ok", cached, ok)
	}

	deps := Deps{
		Cache: ns,
		OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
			rel := core.Release{
				ID:        src.ID,
				Name:      "No-Intro NES",
				Protocol:  core.ProtocolTorrent,
				InfoHash:  src.Release.InfoHash,
				FileCount: src.Release.FileCount,
				Target: &core.ReleaseTarget{
					ExactFile:     src.Release.File,
					Member:        src.Release.FileHint,
					MIME:          src.MIME,
					AllowNonVideo: true,
				},
			}
			payload, err := svc.Acquire(ctx, rel)
			if err != nil {
				return nil, err
			}
			return eng.Open(ctx, src.ID, payload)
		},
	}

	marioSum := sha256.Sum256(marioNES)
	zeldaSum := sha256.Sum256(zeldaNES)

	// 1. Stored outer member (zip) + inner container member (.nes) + matching SHA256 & CRC32.
	bsMario, err := Open(ctx, core.PlayableSource{
		ID:      "mario-stored-nested",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		SHA256:  hex.EncodeToString(marioSum[:]),
		CRC32:   fmt.Sprintf("%08x", crc32.ChecksumIEEE(marioNES)),
		Release: &core.ReleaseHint{
			InfoHash:  packHash,
			FileHint:  "Super Mario Bros. (World).zip",
			FileCount: 123,
		},
		Container: &core.ContainerRef{
			Member: "Super Mario Bros. (World).nes",
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open stored nested ROM: %v", err)
	}
	if bsMario.Name() != "Super Mario Bros. (World).nes" {
		_ = bsMario.Close()
		t.Fatalf("mario Name = %q", bsMario.Name())
	}
	if got := readAllByteSource(t, bsMario); !bytes.Equal(got, marioNES) {
		_ = bsMario.Close()
		t.Fatal("mario inner ROM bytes mismatch")
	}
	_ = bsMario.Close()

	// 2. Deflate outer member (zip) + inner container member (.nes) + matching SHA256 & CRC32.
	bsZelda, err := Open(ctx, core.PlayableSource{
		ID:      "zelda-deflate-nested",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		SHA256:  hex.EncodeToString(zeldaSum[:]),
		CRC32:   fmt.Sprintf("%08x", crc32.ChecksumIEEE(zeldaNES)),
		Release: &core.ReleaseHint{
			InfoHash:  packHash,
			FileHint:  "Zelda (USA).zip",
			FileCount: 123,
		},
		Container: &core.ContainerRef{
			Member: "Zelda (USA).nes",
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open deflate nested ROM: %v", err)
	}
	if bsZelda.Name() != "Zelda (USA).nes" {
		_ = bsZelda.Close()
		t.Fatalf("zelda Name = %q", bsZelda.Name())
	}
	if got := readAllByteSource(t, bsZelda); !bytes.Equal(got, zeldaNES) {
		_ = bsZelda.Close()
		t.Fatal("zelda inner ROM bytes mismatch")
	}
	_ = bsZelda.Close()

	// 3. Wrong declared SHA256 against the final unwrapped ROM fails as ErrDigestMismatch.
	_, err = Open(ctx, core.PlayableSource{
		ID:      "zelda-bad-digest",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		SHA256:  strings.Repeat("0", 64),
		Release: &core.ReleaseHint{
			InfoHash:  packHash,
			FileHint:  "Zelda (USA).zip",
			FileCount: 123,
		},
		Container: &core.ContainerRef{
			Member: "Zelda (USA).nes",
		},
	}, deps)
	if !errors.Is(err, ErrDigestMismatch) || !errors.Is(err, ErrPluginAnswer) {
		t.Fatalf("wrong digest err = %v, want ErrDigestMismatch wrapping ErrPluginAnswer", err)
	}

	// 4. Named outer archive member WITHOUT Container serves the outer .zip itself
	//    instead of falsely auto-composing into itself with fileHint.
	bsRawZIP, err := Open(ctx, core.PlayableSource{
		ID:      "mario-raw-zip",
		Pattern: PatternRelease,
		MIME:    "application/octet-stream",
		Release: &core.ReleaseHint{
			InfoHash:  packHash,
			FileHint:  "Super Mario Bros. (World).zip",
			FileCount: 123,
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open raw outer zip member: %v", err)
	}
	if got := readAllByteSource(t, bsRawZIP); !bytes.Equal(got, marioInnerZIP) {
		_ = bsRawZIP.Close()
		t.Fatal("raw zip member bytes mismatch")
	}
	_ = bsRawZIP.Close()

	// 5. Deflate loose member (Manual.pdf) inflates cleanly and sniffs MIME.
	bsPDF, err := Open(ctx, core.PlayableSource{
		ID:      "manual-deflate-pdf",
		Pattern: PatternRelease,
		MIME:    "application/epub+zip",
		Release: &core.ReleaseHint{
			InfoHash:  packHash,
			FileHint:  "Manual.pdf",
			FileCount: 123,
		},
	}, deps)
	if err != nil {
		t.Fatalf("Open deflate Manual.pdf: %v", err)
	}
	if bsPDF.MIME() != "application/pdf" || bsPDF.Name() != "Manual.pdf" {
		_ = bsPDF.Close()
		t.Fatalf("Manual.pdf = (name=%q, mime=%q), want (Manual.pdf, application/pdf)", bsPDF.Name(), bsPDF.MIME())
	}
	if got := readAllByteSource(t, bsPDF); !bytes.Equal(got, manualPDF) {
		_ = bsPDF.Close()
		t.Fatal("Manual.pdf bytes mismatch")
	}
	_ = bsPDF.Close()
}

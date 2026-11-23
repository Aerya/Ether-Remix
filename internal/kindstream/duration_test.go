package kindstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// buildSyntheticMKV builds a minimal, spec-valid Matroska header with an AV1
// video track so transcode.ProbeHeader reads exact duration and dimensions
// without external test fixtures.
func buildSyntheticMKV(durationSec float64, width, height uint64) []byte {
	var encID func(uint32) []byte
	encID = func(id uint32) []byte {
		var out []byte
		switch {
		case id <= 0xff:
			out = []byte{byte(id)}
		case id <= 0xffff:
			out = []byte{byte(id >> 8), byte(id)}
		case id <= 0xffffff:
			out = []byte{byte(id >> 16), byte(id >> 8), byte(id)}
		default:
			out = []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
		}
		return out
	}
	encSize := func(n int) []byte {
		switch {
		case n < 0x7f:
			return []byte{byte(0x80 | n)}
		case n < 0x3fff:
			return []byte{byte(0x40 | (n >> 8)), byte(n)}
		default:
			return []byte{byte(0x20 | (n >> 16)), byte(n >> 8), byte(n)}
		}
	}
	el := func(id uint32, body ...[]byte) []byte {
		joined := bytes.Join(body, nil)
		out := append(encID(id), encSize(len(joined))...)
		return append(out, joined...)
	}
	elU := func(id uint32, v uint64) []byte {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], v)
		i := 0
		for i < 7 && buf[i] == 0 {
			i++
		}
		return el(id, buf[i:])
	}
	elS := func(id uint32, s string) []byte { return el(id, []byte(s)) }
	elF := func(id uint32, v float64) []byte {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(v))
		return el(id, buf[:])
	}

	// AV1CodecConfigurationRecord: marker=1/version=1 (0x81), Main profile
	// level 4 (0x04), 8-bit 4:2:0 (0x0c), initial_presentation_delay=0.
	av1Config := []byte{0x81, 0x04, 0x0c, 0x00}
	colour := el(0x55b0, elU(0x55ba, 1)) // mkvColour { mkvTransfer: BT.709 }
	video := el(0xe0, elU(0xb0, width), elU(0xba, height), colour)
	track := el(0xae,
		elU(0xd7, 1),              // mkvTrackNumber
		elU(0x83, 1),              // mkvTrackType = video
		elS(0x86, "V_AV1"),        // mkvCodecID
		el(0x63a2, av1Config),     // mkvCodecPrivate
		elU(0x23e383, 41_708_333), // mkvDefaultDuration (~23.976 fps)
		video,
	)
	info := el(0x1549a966,
		elU(0x2ad7b1, 1_000_000),      // mkvTimecodeScale = 1 ms
		elF(0x4489, durationSec*1000), // mkvDuration in timecode ticks
	)
	ebmlHdr := el(0x1a45dfa3, elS(0x4282, "matroska"))
	seg := el(0x18538067, info, el(0x1654ae6b, track))
	return append(ebmlHdr, seg...)
}

func TestDurationVerificationOptInAndFailover(t *testing.T) {
	ctx := context.Background()
	trailerMKV := buildSyntheticMKV(90, 1280, 720)
	episodeMKV := buildSyntheticMKV(2400, 1920, 1080)

	// Sanity check synthetic fixtures probe cleanly natively.
	if info, err := transcode.ProbeHeader(ctx, bytes.NewReader(episodeMKV), int64(len(episodeMKV)), 0); err != nil {
		t.Fatalf("synthetic episode fixture failed ProbeHeader: %v", err)
	} else if math.Abs(info.Duration.Seconds()-2400) > 1 {
		t.Fatalf("synthetic duration = %v, want 2400s", info.Duration)
	}

	trailerSum := sha256.Sum256(trailerMKV)
	episodeSum := sha256.Sum256(episodeMKV)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trailer.mkv":
			http.ServeContent(w, r, "trailer.mkv", time.Time{}, bytes.NewReader(trailerMKV))
		case "/episode.mkv":
			http.ServeContent(w, r, "episode.mkv", time.Time{}, bytes.NewReader(episodeMKV))
		case "/opaque.bin":
			raw := []byte("not-a-supported-container-header")
			http.ServeContent(w, r, "opaque.bin", time.Time{}, bytes.NewReader(raw))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	deps := Deps{HTTP: srv.Client()}

	// 1. Without durationToleranceSec, durationSec stays estimated metadata only.
	bs, err := Open(ctx, core.PlayableSource{
		ID:          "unopted",
		Pattern:     "range",
		MIME:        "video/x-matroska",
		URL:         srv.URL + "/trailer.mkv",
		DurationSec: 2400,
	}, deps)
	if err != nil {
		t.Fatalf("unopted source with mismatched durationSec should open, got %v", err)
	}
	_ = bs.Close()

	// 2. Opted in with mismatched duration fails with ErrDurationMismatch +
	// core.ErrWrongContent + ErrPluginAnswer.
	_, err = Open(ctx, core.PlayableSource{
		ID:                   "trailer",
		Pattern:              "range",
		MIME:                 "video/x-matroska",
		URL:                  srv.URL + "/trailer.mkv",
		DurationSec:          2400,
		DurationToleranceSec: 60,
		SHA256:               hex.EncodeToString(trailerSum[:]),
	}, deps)
	if !errors.Is(err, ErrDurationMismatch) || !errors.Is(err, core.ErrWrongContent) || !errors.Is(err, ErrPluginAnswer) {
		t.Fatalf("mismatched duration err = %v, want ErrDurationMismatch + ErrWrongContent + ErrPluginAnswer", err)
	}

	// 3. Opted in within tolerance opens AND preserves sequential SHA256
	// verification even though ProbeByteSource read the header on open.
	okBS, err := Open(ctx, core.PlayableSource{
		ID:                   "episode",
		Pattern:              "range",
		MIME:                 "video/x-matroska",
		URL:                  srv.URL + "/episode.mkv",
		DurationSec:          2410,
		DurationToleranceSec: 30,
		SHA256:               hex.EncodeToString(episodeSum[:]),
	}, deps)
	if err != nil {
		t.Fatalf("matching episode with sha256 failed open: %v", err)
	}
	gotAll, err := io.ReadAll(io.NewSectionReader(okBS, 0, okBS.Size()))
	if err != nil {
		t.Fatalf("full read after probe + sha256 verification failed: %v", err)
	}
	if !bytes.Equal(gotAll, episodeMKV) {
		t.Fatalf("bytes mismatch after open")
	}
	_ = okBS.Close()

	// 4. OpenSources fails over from mismatched trailer to matching episode and
	// marks the failed trailer as PluginAnswer (#641 path).
	var failures []SourceFailure
	depsFailover := deps
	depsFailover.OnSourceSkipped = func(skipped []SourceFailure, _ core.PlayableSource) {
		failures = append(failures, skipped...)
	}
	bs, chosen, err := OpenSources(ctx, []core.PlayableSource{
		{
			ID:                   "trailer",
			Pattern:              "range",
			MIME:                 "video/x-matroska",
			URL:                  srv.URL + "/trailer.mkv",
			DurationSec:          2400,
			DurationToleranceSec: 60,
		},
		{
			ID:                   "episode",
			Pattern:              "range",
			MIME:                 "video/x-matroska",
			URL:                  srv.URL + "/episode.mkv",
			DurationSec:          2400,
			DurationToleranceSec: 60,
		},
	}, depsFailover)
	if err != nil {
		t.Fatalf("OpenSources failover: %v", err)
	}
	_ = bs.Close()
	if chosen.ID != "episode" {
		t.Fatalf("chosen = %q, want episode", chosen.ID)
	}
	if len(failures) != 1 || failures[0].Source.ID != "trailer" || !failures[0].PluginAnswer() {
		t.Fatalf("failures = %+v, want 1 PluginAnswer failure on trailer", failures)
	}

	// 5. When ALL candidates fail duration check, OpenSources preserves
	// core.ErrWrongContent and ErrDurationMismatch.
	_, _, err = OpenSources(ctx, []core.PlayableSource{
		{
			ID:                   "trailer-a",
			Pattern:              "range",
			MIME:                 "video/x-matroska",
			URL:                  srv.URL + "/trailer.mkv",
			DurationSec:          2400,
			DurationToleranceSec: 60,
		},
		{
			ID:                   "trailer-b",
			Pattern:              "range",
			MIME:                 "video/x-matroska",
			URL:                  srv.URL + "/trailer.mkv",
			DurationSec:          2400,
			DurationToleranceSec: 60,
		},
	}, deps)
	if !errors.Is(err, core.ErrWrongContent) || !errors.Is(err, ErrDurationMismatch) {
		t.Fatalf("all-mismatched OpenSources err = %v, want ErrWrongContent + ErrDurationMismatch", err)
	}

	// 6. Unrecognized container fails open when no fallback prober can read it,
	// but uses probeStream fallback when provided.
	opaqueBS, err := Open(ctx, core.PlayableSource{
		ID:                   "opaque",
		Pattern:              "range",
		MIME:                 "application/octet-stream",
		URL:                  srv.URL + "/opaque.bin",
		DurationSec:          2400,
		DurationToleranceSec: 60,
	}, deps)
	if err != nil {
		t.Fatalf("opaque container without fallback prober should fail open, got %v", err)
	}
	_ = opaqueBS.Close()

	var fallbackCalls atomic.Int32
	depsFallback := deps
	depsFallback.ProbeMedia = func(ctx context.Context, _ core.PlayableSource, bs ByteSource) (*core.MediaInfo, error) {
		return ProbeByteSource(ctx, bs, func(_ context.Context, rawURL string) (*core.MediaInfo, error) {
			fallbackCalls.Add(1)
			resp, err := http.Get(rawURL)
			if err != nil {
				return nil, err
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, errors.New("unexpected loopback probe status")
			}
			return &core.MediaInfo{Duration: 90 * time.Second, Container: "avi"}, nil
		})
	}
	_, err = Open(ctx, core.PlayableSource{
		ID:                   "opaque-with-fallback",
		Pattern:              "range",
		MIME:                 "video/x-msvideo",
		URL:                  srv.URL + "/opaque.bin",
		DurationSec:          2400,
		DurationToleranceSec: 60,
	}, depsFallback)
	if fallbackCalls.Load() != 1 || !errors.Is(err, ErrDurationMismatch) {
		t.Fatalf("fallback probe calls=%d err=%v, want 1 call + ErrDurationMismatch", fallbackCalls.Load(), err)
	}
}

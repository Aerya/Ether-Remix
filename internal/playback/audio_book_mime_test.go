package playback

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type memAudioBookSource struct {
	id   string
	name string
	mime string
	data []byte
}

func (m *memAudioBookSource) ReleaseID() string { return m.id }
func (m *memAudioBookSource) Name() string      { return m.name }
func (m *memAudioBookSource) Size() int64       { return int64(len(m.data)) }
func (m *memAudioBookSource) MIME() string      { return m.mime }
func (m *memAudioBookSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if off+int64(n) >= int64(len(m.data)) {
		return n, io.EOF
	}
	return n, nil
}
func (m *memAudioBookSource) Close() error { return nil }

func TestServeRangeAudioContentTypeNeverOctetStream(t *testing.T) {
	cases := []struct {
		name      string
		fileName  string
		srcMIME   string
		container string
		policy    *core.RangeResponse
		wantCT    string
	}{
		{
			name:      "flac file with extension",
			fileName:  "05-metallica-you_must_burn.flac",
			container: "flac",
			wantCT:    "audio/flac",
		},
		{
			name:     "extensionless flac via MIMESource",
			fileName: "stream",
			srcMIME:  "audio/flac",
			wantCT:   "audio/flac",
		},
		{
			name:      "extensionless audio-only mp4 via probed info",
			fileName:  "track",
			container: "mp4",
			wantCT:    "audio/mp4",
		},
		{
			name:      "mp3 does not get clobbered by octet-stream policy MIME",
			fileName:  "01-song.mp3",
			container: "mp3",
			policy:    &core.RangeResponse{MIME: func(string) string { return "application/octet-stream" }},
			wantCT:    "audio/mpeg",
		},
		{
			name:      "opus/ogg",
			fileName:  "track.opus",
			container: "ogg",
			wantCT:    "audio/ogg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte("fLaC0123456789abcdef")
			src := &memAudioBookSource{id: "rel-1", name: tc.fileName, mime: tc.srcMIME, data: payload}
			info := core.MediaInfo{
				Container: tc.container,
				Size:      int64(len(payload)),
				Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2}},
			}
			gen := &generation{
				src:  src,
				res:  &core.Resolution{Release: core.Release{ID: "rel-1"}},
				info: info,
			}
			sess := &session{
				id:          "sess-audio",
				name:        tc.fileName,
				contentType: "application/octet-stream", // deliberately stale initial value
				etag:        `"etag1"`,
				modTime:     time.Unix(1700000000, 0),
				virtual:     TimeMap{Size: int64(len(payload))},
				virtualGen:  gen,
				cur:         gen,
			}
			gen.sess = sess
			h := newHarness(t, nil, nil)
			h.m.mu.Lock()
			h.m.sessions[sess.id] = sess
			h.m.mu.Unlock()
			req := httptest.NewRequest(http.MethodGet, "/sessions/media/sess-audio/g/file", nil)
			if tc.policy != nil {
				req = req.WithContext(core.WithRangeResponse(req.Context(), *tc.policy))
			}
			rec := httptest.NewRecorder()
			h.m.ServeRange(rec, req, sess.id)
			h.m.mu.Lock()
			delete(h.m.sessions, sess.id)
			h.m.mu.Unlock()
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantCT {
				t.Fatalf("Content-Type = %q, want %q (must never be application/octet-stream)", got, tc.wantCT)
			}
		})
	}
}

func makeEPUBBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("application/epub+zip"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestProbeGenerationRefusesBooksWithoutRunningFFprobe(t *testing.T) {
	epubData := makeEPUBBytes(t)
	pdfData := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")

	cases := []struct {
		label string
		src   core.MediaSource
	}{
		{"epub by extension", &memAudioBookSource{id: "b1", name: "dune.epub", data: []byte("not-even-zip")}},
		{"pdf by extension", &memAudioBookSource{id: "b2", name: "book.pdf", data: []byte("raw")}},
		{"cbz by extension", &memAudioBookSource{id: "b3", name: "issue1.cbz", data: []byte("raw")}},
		{"epub by MIME", &memAudioBookSource{id: "b4", name: "blob", mime: "application/epub+zip", data: []byte("raw")}},
		{"pdf by MIME", &memAudioBookSource{id: "b5", name: "blob", mime: "application/pdf", data: []byte("raw")}},
		{"epub by ZIP magic bytes", &memAudioBookSource{id: "b6", name: "opaque-blob", data: epubData}},
		{"pdf by %PDF- magic bytes", &memAudioBookSource{id: "b7", name: "opaque-blob", data: pdfData}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			var probeCalls atomic.Int32
			mgr := &Manager{
				log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				deps: Deps{
					NativeProbe: func(context.Context, core.MediaSource) (*core.MediaInfo, error) {
						probeCalls.Add(1)
						return &core.MediaInfo{}, nil
					},
				},
			}
			sess := &session{id: "sess-book"}
			_, err := mgr.probeGeneration(context.Background(), sess, &core.Resolution{Release: core.Release{ID: tc.src.ReleaseID()}}, tc.src, nil, nil)
			if !errors.Is(err, core.ErrNoUsableFile) {
				t.Fatalf("probeGeneration(%s) err = %v, want core.ErrNoUsableFile", tc.label, err)
			}
			if got := probeCalls.Load(); got != 0 {
				t.Fatalf("ffprobe/NativeProbe was invoked %d times on a book (%s), want 0", got, tc.label)
			}
		})
	}
}

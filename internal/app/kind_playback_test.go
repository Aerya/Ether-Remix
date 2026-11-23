package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/playback"
)

type fakeDebridKindSource struct {
	data           []byte
	releaseID      string
	name           string
	mime           string
	origin         string
	concurrentHits atomic.Int32
	contextHits    atomic.Int32
	metadataHits   atomic.Int32
	prefetchHits   atomic.Int32
	analysisHits   atomic.Int32
	duration       time.Duration
	onConcurrent   func()
}

func (s *fakeDebridKindSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) ReadAtContext(_ context.Context, p []byte, off int64) (int, error) {
	s.contextHits.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) ReadAtConcurrentContext(_ context.Context, p []byte, off int64) (int, error) {
	s.concurrentHits.Add(1)
	if s.onConcurrent != nil {
		s.onConcurrent()
	}
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) ReadMetadataAtContext(_ context.Context, p []byte, off int64) (int, error) {
	s.metadataHits.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) ReadAtPrefetchContext(_ context.Context, p []byte, off int64) (int, error) {
	s.prefetchHits.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) ReadAtAnalysisContext(_ context.Context, p []byte, off int64) (int, error) {
	s.analysisHits.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeDebridKindSource) RetryDirectPlay() bool { return true }
func (s *fakeDebridKindSource) DirectURL() (core.DirectSourceLink, bool) {
	return core.DirectSourceLink{URL: "https://cdn.example/file.mp4"}, true
}
func (s *fakeDebridKindSource) MediaCapWaitKnown() bool             { return true }
func (s *fakeDebridKindSource) BufferedAhead() int64                { return 4 << 20 }
func (s *fakeDebridKindSource) SetPlaybackDuration(d time.Duration) { s.duration = d }
func (s *fakeDebridKindSource) Origin() string                      { return s.origin }
func (s *fakeDebridKindSource) MIME() string                        { return s.mime }
func (s *fakeDebridKindSource) Size() int64                         { return int64(len(s.data)) }
func (s *fakeDebridKindSource) Name() string                        { return s.name }
func (s *fakeDebridKindSource) ReleaseID() string                   { return s.releaseID }
func (s *fakeDebridKindSource) Close() error                        { return nil }

type fakeUsenetKindSource struct {
	data           []byte
	releaseID      string
	damageAttached bool
	repairAttached bool
	delivery       core.SourceDelivery
}

func (s *fakeUsenetKindSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *fakeUsenetKindSource) Size() int64                              { return int64(len(s.data)) }
func (s *fakeUsenetKindSource) Name() string                             { return "concert.mkv" }
func (s *fakeUsenetKindSource) ReleaseID() string                        { return s.releaseID }
func (s *fakeUsenetKindSource) Close() error                             { return nil }
func (s *fakeUsenetKindSource) SourceDelivery() core.SourceDelivery      { return s.delivery }
func (s *fakeUsenetKindSource) SetDamageHandler(func(int64, int64) bool) { s.damageAttached = true }
func (s *fakeUsenetKindSource) SetRepairHandler(func(core.RepairEvent) core.RepairWindow) {
	s.repairAttached = true
}

type plainKindReaderAt struct{ *bytes.Reader }

func (plainKindReaderAt) Close() error { return nil }

func TestWrapKindMediaSourcePreservesCapabilitiesWithoutFalseClaims(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 1024)

	t.Run("debrid source with different releaseID keeps concurrent, retrier, direct URL, metadata, prefetch, analysis", func(t *testing.T) {
		inner := &fakeDebridKindSource{
			data:      payload,
			releaseID: "", // mismatches rel.ID so wrapKindMediaSource is used
			name:      "pulse.mp4",
			mime:      "video/mp4",
			origin:    "torbox",
		}
		wrapped := wrapKindMediaSource(&frontend.KindMedia{
			ReaderAt:    inner,
			Closer:      inner,
			Size:        int64(len(payload)),
			MIME:        "video/mp4",
			Name:        "pulse.mp4",
			MediaSource: inner,
		}, "kind:pulse")

		if wrapped.ReleaseID() != "kind:pulse" {
			t.Fatalf("ReleaseID = %q, want kind:pulse", wrapped.ReleaseID())
		}
		if _, ok := wrapped.(core.ConcurrentRangeReaderAt); !ok {
			t.Fatalf("wrapped debrid source lost core.ConcurrentRangeReaderAt")
		}
		if r, ok := wrapped.(core.DirectPlayRetrier); !ok || !r.RetryDirectPlay() {
			t.Fatalf("wrapped debrid source lost core.DirectPlayRetrier")
		}
		if d, ok := wrapped.(core.DirectURLSource); !ok {
			t.Fatalf("wrapped debrid source lost core.DirectURLSource")
		} else if link, lok := d.DirectURL(); !lok || link.URL == "" {
			t.Fatalf("DirectURL = (%+v, %v)", link, lok)
		}
		if _, ok := wrapped.(core.SourceDeliveryReporter); ok {
			t.Fatalf("wrapped debrid source must not falsely implement core.SourceDeliveryReporter")
		}

		ctx := context.Background()
		buf := make([]byte, 32)
		if _, err := wrapped.(core.ConcurrentRangeReaderAt).ReadAtConcurrentContext(ctx, buf, 0); err != nil {
			t.Fatalf("ReadAtConcurrentContext: %v", err)
		}
		if _, err := wrapped.(core.MetadataReaderAt).ReadMetadataAtContext(ctx, buf, 0); err != nil {
			t.Fatalf("ReadMetadataAtContext: %v", err)
		}
		if _, err := wrapped.(core.PrefetchReaderAt).ReadAtPrefetchContext(ctx, buf, 0); err != nil {
			t.Fatalf("ReadAtPrefetchContext: %v", err)
		}
		if _, err := wrapped.(core.AnalysisReaderAt).ReadAtAnalysisContext(ctx, buf, 0); err != nil {
			t.Fatalf("ReadAtAnalysisContext: %v", err)
		}
		wrapped.(core.PlaybackDurationSetter).SetPlaybackDuration(2 * time.Hour)
		if inner.concurrentHits.Load() != 1 || inner.metadataHits.Load() != 1 || inner.prefetchHits.Load() != 1 || inner.analysisHits.Load() != 1 || inner.duration != 2*time.Hour {
			t.Fatalf("delegation counts mismatch: conc=%d meta=%d pre=%d ana=%d dur=%v",
				inner.concurrentHits.Load(), inner.metadataHits.Load(), inner.prefetchHits.Load(), inner.analysisHits.Load(), inner.duration)
		}
	})

	t.Run("usenet source keeps delivery, damage, and repair hooks", func(t *testing.T) {
		inner := &fakeUsenetKindSource{
			data:      payload,
			releaseID: "inner-nzb",
			delivery:  core.SourceDelivery{Bytes: 2 << 20, Parallel: 4},
		}
		wrapped := wrapKindMediaSource(&frontend.KindMedia{
			ReaderAt:    inner,
			Closer:      inner,
			Size:        int64(len(payload)),
			MIME:        "video/x-matroska",
			Name:        "zimmer.mkv",
			MediaSource: inner,
		}, "kind:zimmer")

		rep, ok := wrapped.(core.SourceDeliveryReporter)
		if !ok || rep.SourceDelivery().Bytes != 2<<20 || rep.SourceDelivery().Parallel != 4 {
			t.Fatalf("SourceDeliveryReporter = (%v, %+v)", ok, rep)
		}
		wrapped.(core.DamageReporter).SetDamageHandler(func(int64, int64) bool { return true })
		wrapped.(core.RepairReporter).SetRepairHandler(func(core.RepairEvent) core.RepairWindow { return core.RepairWindow{} })
		if !inner.damageAttached || !inner.repairAttached {
			t.Fatalf("damage=%v repair=%v, want both attached", inner.damageAttached, inner.repairAttached)
		}
		if _, ok := wrapped.(core.ConcurrentRangeReaderAt); ok {
			t.Fatalf("usenet source must not falsely implement core.ConcurrentRangeReaderAt")
		}
		if _, ok := wrapped.(core.DirectPlayRetrier); ok {
			t.Fatalf("usenet source must not falsely implement core.DirectPlayRetrier")
		}
	})

	t.Run("plain ReaderAt does not falsely advertise optional delivery interfaces", func(t *testing.T) {
		plain := plainKindReaderAt{bytes.NewReader(payload)}
		wrapped := wrapKindMediaSource(&frontend.KindMedia{
			ReaderAt: plain,
			Closer:   plain,
			Size:     int64(len(payload)),
			MIME:     "video/mp4",
			Name:     "plain.mp4",
		}, "kind:plain")
		if _, ok := wrapped.(core.ConcurrentRangeReaderAt); ok {
			t.Fatal("plain ReaderAt must not implement ConcurrentRangeReaderAt")
		}
		if _, ok := wrapped.(core.DirectPlayRetrier); ok {
			t.Fatal("plain ReaderAt must not implement DirectPlayRetrier")
		}
		if _, ok := wrapped.(core.SourceDeliveryReporter); ok {
			t.Fatal("plain ReaderAt must not implement SourceDeliveryReporter")
		}
		if _, ok := wrapped.(core.DirectURLSource); ok {
			t.Fatal("plain ReaderAt must not implement DirectURLSource")
		}
	})
}

type stubKindResolver struct{ core.Resolver }
type stubKindEngine struct{ core.Engine }
type stubKindTranscoder struct{ core.Transcoder }
type stubKindCatalog struct{ core.Catalog }

func (stubKindTranscoder) Session(string) (core.HLSSession, bool) { return nil, false }

func TestPluginByteSourceDirectPlayRangesSuffixAndParallel(t *testing.T) {
	payload := make([]byte, 128<<10)
	for i := range payload {
		payload[i] = byte((i*17 + 3) & 0xff)
	}
	// Mark MP4 ftyp header for the MP4 case and EBML header for the MKV case.
	mp4Data := append([]byte(nil), payload...)
	copy(mp4Data[:12], []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	mkvData := append([]byte(nil), payload...)
	copy(mkvData[:4], []byte{0x1a, 0x45, 0xdf, 0xa3})

	cases := []struct {
		name      string
		filename  string
		container string
		wantMIME  string
		data      []byte
		useHTTP   bool
	}{
		{"mp4-debrid-wrapped", "pulse.mp4", "mp4", "video/mp4", mp4Data, false},
		{"mkv-http-range", "zimmer.mkv", "mkv", "video/x-matroska", mkvData, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var (
				src          core.MediaSource
				debridSource *fakeDebridKindSource
				firstEntered = make(chan struct{})
				releaseBoth  = make(chan struct{})
				entered      atomic.Int32
			)

			if tc.useHTTP {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", tc.wantMIME)
					http.ServeContent(w, r, tc.filename, time.Unix(1_700_000_000, 0), bytes.NewReader(tc.data))
				}))
				defer upstream.Close()
				bs, err := kindstream.Open(ctx, core.PlayableSource{
					ID:      tc.name,
					Pattern: kindstream.PatternRange,
					MIME:    tc.wantMIME,
					URL:     upstream.URL + "/" + tc.filename,
					Size:    int64(len(tc.data)),
				}, kindstream.Deps{HTTP: upstream.Client()})
				if err != nil {
					t.Fatalf("kindstream.Open: %v", err)
				}
				src = wrapKindMediaSource(&frontend.KindMedia{
					ReaderAt: bs,
					Closer:   bs,
					Size:     bs.Size(),
					MIME:     bs.MIME(),
					Name:     bs.Name(),
				}, "kind:"+tc.name)
			} else {
				debridSource = &fakeDebridKindSource{
					data:      tc.data,
					releaseID: "", // forces wrapKindMediaSource path
					name:      tc.filename,
					mime:      tc.wantMIME,
				}
				src = wrapKindMediaSource(&frontend.KindMedia{
					ReaderAt:    debridSource,
					Closer:      debridSource,
					Size:        int64(len(tc.data)),
					MIME:        tc.wantMIME,
					Name:        tc.filename,
					MediaSource: debridSource,
				}, "kind:"+tc.name)
			}

			mgr, err := playback.New(playback.Deps{
				Resolver:   stubKindResolver{},
				Engine:     stubKindEngine{},
				Transcoder: stubKindTranscoder{},
				Catalog:    stubKindCatalog{},
				Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
				SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
					return core.QualityProfile{}
				},
				NativeProbe: func(context.Context, core.MediaSource) (*core.MediaInfo, error) {
					return &core.MediaInfo{
						Container: tc.container,
						Size:      int64(len(tc.data)),
						Duration:  time.Hour,
						Streams: []core.StreamInfo{
							{Type: "video", Index: 0, Codec: "h264", Width: 1280, Height: 720},
							{Type: "audio", Index: 1, Codec: "aac", Channels: 2},
						},
					}, nil
				},
			}, config.Config{}, playback.Options{
				DataDir: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("playback.New: %v", err)
			}
			defer mgr.Close()

			ps, err := mgr.PrepareKind(ctx, &core.User{ID: 1, Username: "viewer"}, core.KindInput{
				Title: &core.Title{
					Key:   core.RatingKey(9005000000000001),
					Title: tc.name,
					Type:  core.TypeMovie,
				},
				OpenCandidate: func(_ context.Context, _ []string) (core.MediaSource, core.Release, error) {
					return src, core.Release{
						ID:   "kind:" + tc.name,
						Name: tc.filename,
						Size: int64(len(tc.data)),
					}, nil
				},
			}, core.ClientCaps{Protocol: "vela", ClientID: "android-tablet"})
			if err != nil {
				t.Fatalf("PrepareKind: %v", err)
			}

			// 1. HEAD: Accept-Ranges, Content-Length, Content-Type.
			headRec := httptest.NewRecorder()
			mgr.ServeRange(headRec, httptest.NewRequest(http.MethodHead, "/file", nil), ps.ID)
			if headRec.Code != http.StatusOK {
				t.Fatalf("HEAD status = %d, want 200", headRec.Code)
			}
			if got := headRec.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Fatalf("HEAD Accept-Ranges = %q, want bytes", got)
			}
			if got := headRec.Header().Get("Content-Length"); got != fmt.Sprintf("%d", len(tc.data)) {
				t.Fatalf("HEAD Content-Length = %q, want %d", got, len(tc.data))
			}
			if got := headRec.Header().Get("Content-Type"); got != tc.wantMIME {
				t.Fatalf("HEAD Content-Type = %q, want %q", got, tc.wantMIME)
			}

			// 2. Start range (bytes=0-4095).
			reqStart := httptest.NewRequest(http.MethodGet, "/file", nil)
			reqStart.Header.Set("Range", "bytes=0-4095")
			recStart := httptest.NewRecorder()
			mgr.ServeRange(recStart, reqStart, ps.ID)
			if recStart.Code != http.StatusPartialContent {
				t.Fatalf("start range status = %d, want 206", recStart.Code)
			}
			if got := recStart.Header().Get("Content-Range"); got != fmt.Sprintf("bytes 0-4095/%d", len(tc.data)) {
				t.Fatalf("start Content-Range = %q", got)
			}
			if !bytes.Equal(recStart.Body.Bytes(), tc.data[:4096]) {
				t.Fatal("start range body mismatch")
			}

			// 3. Suffix range (bytes=-8192) — how MP4 players locate a tail moov atom.
			reqSuffix := httptest.NewRequest(http.MethodGet, "/file", nil)
			reqSuffix.Header.Set("Range", "bytes=-8192")
			recSuffix := httptest.NewRecorder()
			mgr.ServeRange(recSuffix, reqSuffix, ps.ID)
			if recSuffix.Code != http.StatusPartialContent {
				t.Fatalf("suffix range status = %d, want 206", recSuffix.Code)
			}
			wantStart := len(tc.data) - 8192
			if got := recSuffix.Header().Get("Content-Range"); got != fmt.Sprintf("bytes %d-%d/%d", wantStart, len(tc.data)-1, len(tc.data)) {
				t.Fatalf("suffix Content-Range = %q", got)
			}
			if !bytes.Equal(recSuffix.Body.Bytes(), tc.data[wantStart:]) {
				t.Fatal("suffix range body mismatch")
			}

			// 4. Two concurrent overlapping Range connections must both succeed
			// without cancelling one another or tearing down the session.
			if debridSource != nil {
				debridSource.onConcurrent = func() {
					n := entered.Add(1)
					if n == 1 {
						close(firstEntered)
						<-releaseBoth
					} else if n == 2 {
						close(releaseBoth)
					}
				}
			}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			runRange := func(lo, hi int) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "/file", nil)
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", lo, hi))
				rec := httptest.NewRecorder()
				mgr.ServeRange(rec, req, ps.ID)
				if rec.Code != http.StatusPartialContent {
					errs <- fmt.Errorf("range %d-%d status=%d", lo, hi, rec.Code)
					return
				}
				if !bytes.Equal(rec.Body.Bytes(), tc.data[lo:hi+1]) {
					errs <- fmt.Errorf("range %d-%d body mismatch", lo, hi)
				}
			}
			wg.Add(2)
			go runRange(16<<10, (32<<10)-1)
			if debridSource != nil {
				<-firstEntered
			}
			go runRange(64<<10, (80<<10)-1)
			wg.Wait()
			close(errs)
			for e := range errs {
				t.Fatal(e)
			}
			if debridSource != nil {
				if debridSource.concurrentHits.Load() < 2 || debridSource.contextHits.Load() != 0 {
					t.Fatalf("expected direct-play ranges to use ReadAtConcurrentContext: concurrent=%d context=%d",
						debridSource.concurrentHits.Load(), debridSource.contextHits.Load())
				}
			}

			// 5. Session remains alive after parallel range requests complete.
			afterReq := httptest.NewRequest(http.MethodGet, "/file", nil)
			afterReq.Header.Set("Range", "bytes=32768-36863")
			afterRec := httptest.NewRecorder()
			mgr.ServeRange(afterRec, afterReq, ps.ID)
			if afterRec.Code != http.StatusPartialContent || !bytes.Equal(afterRec.Body.Bytes(), tc.data[32768:36864]) {
				t.Fatalf("post-parallel range failed: code=%d len=%d", afterRec.Code, afterRec.Body.Len())
			}
		})
	}
}

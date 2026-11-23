package livetv

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type spyLiveTranscoder struct {
	*transcode.Manager
	mu           sync.Mutex
	hlsReqs      []core.HLSRequest
	encoderCalls int
}

func (s *spyLiveTranscoder) StartHLS(ctx context.Context, req core.HLSRequest) (core.HLSSession, error) {
	s.mu.Lock()
	s.hlsReqs = append(s.hlsReqs, req)
	s.mu.Unlock()
	return s.Manager.StartHLS(ctx, req)
}

func (s *spyLiveTranscoder) StartEncoder(ctx context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	s.mu.Lock()
	s.encoderCalls++
	s.mu.Unlock()
	return s.Manager.StartEncoder(ctx, req)
}

func (s *spyLiveTranscoder) snapshot() (hls []core.HLSRequest, encoders int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.HLSRequest, len(s.hlsReqs))
	copy(out, s.hlsReqs)
	return out, s.encoderCalls
}

// startFakeIPTVServer streams a real continuous H.264/AAC MPEG-TS feed over
// HTTP generated on demand by ffmpeg, matching a typical IPTV live endpoint.
func startFakeIPTVServer(t *testing.T) *httptest.Server {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, ffmpeg,
			"-hide_banner", "-loglevel", "error", "-re",
			"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
			"-pix_fmt", "yuv420p", "-g", "25", "-keyint_min", "25", "-sc_threshold", "0",
			"-c:a", "aac", "-b:a", "96k", "-ac", "2", "-ar", "48000",
			"-f", "mpegts", "pipe:1",
		)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := cmd.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() {
			cancel()
			_ = cmd.Wait()
		}()
		w.Header().Set("Content-Type", "video/mp2t")
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 188*32)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					return
				}
				return
			}
		}
	}))
}

type testMPD struct {
	Type                       string `xml:"type,attr"`
	AvailabilityStartTime      string `xml:"availabilityStartTime,attr"`
	MinimumUpdatePeriod        string `xml:"minimumUpdatePeriod,attr"`
	MinBufferTime              string `xml:"minBufferTime,attr"`
	TimeShiftBufferDepth       string `xml:"timeShiftBufferDepth,attr"`
	SuggestedPresentationDelay string `xml:"suggestedPresentationDelay,attr"`
	Periods                    []struct {
		ID   string `xml:"id,attr"`
		Sets []struct {
			ContentType string `xml:"contentType,attr"`
			Rep         struct {
				ID     string `xml:"id,attr"`
				Codecs string `xml:"codecs,attr"`
				Tpl    struct {
					Timescale              uint32 `xml:"timescale,attr"`
					PresentationTimeOffset int64  `xml:"presentationTimeOffset,attr"`
					StartNumber            int64  `xml:"startNumber,attr"`
					Init                   string `xml:"initialization,attr"`
					Media                  string `xml:"media,attr"`
					Timeline               struct {
						S []struct {
							T int64 `xml:"t,attr"`
							D int64 `xml:"d,attr"`
						} `xml:"S"`
					} `xml:"SegmentTimeline"`
				} `xml:"SegmentTemplate"`
			} `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

func TestIPTVDefaultCopyAndDirectDASHFromOwnedTimeline(t *testing.T) {
	up := startFakeIPTVServer(t)
	t.Cleanup(func() {
		up.CloseClientConnections()
		up.Close()
	})

	mgr, err := transcode.New(config.Transcode{TempDir: t.TempDir(), HWAccel: "none"}, slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithoutHWDetection())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Close() })

	spy := &spyLiveTranscoder{Manager: mgr}
	s := testService(t, Options{
		Transcoder: spy,
		HTTPClient: up.Client(),
		Stability:  func() string { return stabilityBalanced },
	})
	ch, err := s.SaveChannel(context.Background(), Channel{
		Name:      "Live Test",
		Number:    "101",
		Kind:      "iptv",
		Enabled:   true,
		StreamURL: up.URL + "/stream.ts",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := &core.User{ID: 1, IsOwner: true}

	t.Run("native client copies MPEG-TS without Steady encode", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := s.Tune(ctx, user, ch.ID, core.ClientCaps{
			ClientID:      "ios-client",
			Platform:      "iOS",
			VideoCodecs:   []string{"h264", "hevc"},
			AudioCodecs:   []string{"aac", "ac3", "eac3"},
			HLSContainers: []string{"mpegts"},
		})
		if err != nil {
			t.Fatalf("Tune failed: %v", err)
		}
		defer s.Stop(context.Background(), info.ID, user)

		hlsReqs, encCalls := spy.snapshot()
		if encCalls != 0 {
			t.Fatalf("Steady encoder started %d times for copy-capable native client, want 0", encCalls)
		}
		if len(hlsReqs) != 1 {
			t.Fatalf("StartHLS calls = %d, want 1", len(hlsReqs))
		}
		if !hlsReqs[0].Decision.VideoCopy || !hlsReqs[0].Decision.AudioCopy || hlsReqs[0].Decision.Container != transcode.ContainerMPEGTS {
			t.Fatalf("expected MPEG-TS full copy, got %+v", hlsReqs[0].Decision)
		}

		pl := httptest.NewRecorder()
		s.ServePlaylist(pl, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil).WithContext(ctx), info.ID, user)
		if pl.Code != http.StatusOK || !strings.Contains(pl.Body.String(), "/relay/") {
			t.Fatalf("playlist = %d:\n%s", pl.Code, pl.Body.String())
		}
		seg := httptest.NewRecorder()
		s.ServeRelaySegment(seg, httptest.NewRequest(http.MethodGet, "/relay/0.ts", nil).WithContext(ctx), info.ID, 0, user)
		if seg.Code != http.StatusOK || seg.Body.Len() < 188 || seg.Body.Bytes()[0] != 0x47 {
			t.Fatalf("relay segment 0 = %d (%d bytes)", seg.Code, seg.Body.Len())
		}
		if err := s.Stop(context.Background(), info.ID, user); err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
		if len(s.ViewingSessions()) != 0 {
			t.Fatalf("sessions remaining after stop: %d", len(s.ViewingSessions()))
		}
	})

	t.Run("browser DASH serves directly from fMP4 copy timeline without second ffmpeg", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		beforeReqs, _ := spy.snapshot()
		info, err := s.Tune(ctx, user, ch.ID, core.ClientCaps{
			ClientID:      "web-client",
			Product:       "Plex Web",
			Platform:      "Firefox",
			VideoCodecs:   []string{"h264"},
			AudioCodecs:   []string{"aac"},
			HLSContainers: []string{"fmp4"},
		})
		if err != nil {
			t.Fatalf("Tune failed: %v", err)
		}
		defer s.Stop(context.Background(), info.ID, user)

		mpd1 := httptest.NewRecorder()
		if err := s.ServeDASH(mpd1, httptest.NewRequest(http.MethodGet, "/start.mpd", nil).WithContext(ctx), info.ID, user); err != nil {
			t.Fatalf("ServeDASH #1: %v", err)
		}
		if mpd1.Code != http.StatusOK {
			t.Fatalf("ServeDASH #1 status=%d body=%s", mpd1.Code, mpd1.Body.String())
		}
		mpd2 := httptest.NewRecorder()
		if err := s.ServeDASH(mpd2, httptest.NewRequest(http.MethodGet, "/start.mpd", nil).WithContext(ctx), info.ID, user); err != nil {
			t.Fatalf("ServeDASH #2: %v", err)
		}

		afterReqs, encCalls := spy.snapshot()
		if encCalls != 0 {
			t.Fatalf("Steady encoder started %d times for copy-capable browser, want 0", encCalls)
		}
		added := afterReqs[len(beforeReqs):]
		if len(added) != 1 {
			t.Fatalf("expected exactly 1 fMP4 copy relay and no second DASH ffmpeg, got %d StartHLS calls", len(added))
		}
		if !added[0].Decision.VideoCopy || !added[0].Decision.AudioCopy || added[0].Decision.Container != transcode.ContainerFMP4 {
			t.Fatalf("expected fMP4 full copy, got %+v", added[0].Decision)
		}

		var doc1, doc2 testMPD
		if err := xml.Unmarshal(mpd1.Body.Bytes(), &doc1); err != nil {
			t.Fatalf("parse MPD #1: %v\n%s", err, mpd1.Body.String())
		}
		if err := xml.Unmarshal(mpd2.Body.Bytes(), &doc2); err != nil {
			t.Fatalf("parse MPD #2: %v\n%s", err, mpd2.Body.String())
		}
		if doc1.Type != "dynamic" || doc1.AvailabilityStartTime == "" || doc1.AvailabilityStartTime != doc2.AvailabilityStartTime {
			t.Fatalf("availabilityStartTime unstable: %q vs %q", doc1.AvailabilityStartTime, doc2.AvailabilityStartTime)
		}
		if doc1.SuggestedPresentationDelay == "" || doc1.TimeShiftBufferDepth == "PT1800S" {
			t.Fatalf("bad live MPD window attrs: delay=%q depth=%q", doc1.SuggestedPresentationDelay, doc1.TimeShiftBufferDepth)
		}
		if len(doc1.Periods) == 0 || len(doc1.Periods[0].Sets) != 2 {
			t.Fatalf("expected 2 AdaptationSets (video+audio), got %+v", doc1.Periods)
		}
		for repIdx, set := range doc1.Periods[0].Sets {
			repID, _ := strconv.Atoi(set.Rep.ID)
			if len(set.Rep.Tpl.Timeline.S) == 0 {
				t.Fatalf("AdaptationSet %d (%s) has empty SegmentTimeline", repIdx, set.ContentType)
			}
			initRec := httptest.NewRecorder()
			s.ServeDASHPart(initRec, httptest.NewRequest(http.MethodGet, set.Rep.Tpl.Init, nil).WithContext(ctx), info.ID, user, repID, 0, true)
			if initRec.Code != http.StatusOK || initRec.Body.Len() == 0 {
				t.Fatalf("init rep %d: %d (%d bytes)", repID, initRec.Code, initRec.Body.Len())
			}
			tracks, err := transcode.ParseDASHInit(initRec.Body.Bytes())
			if err != nil || len(tracks) != 1 || tracks[0].Kind != set.ContentType {
				t.Fatalf("isolated init rep %d tracks=%+v err=%v", repID, tracks, err)
			}

			seq := int(set.Rep.Tpl.StartNumber)
			segRec := httptest.NewRecorder()
			s.ServeDASHPart(segRec, httptest.NewRequest(http.MethodGet, "/seg.m4s", nil).WithContext(ctx), info.ID, user, repID, seq, false)
			if segRec.Code != http.StatusOK || segRec.Body.Len() == 0 {
				t.Fatalf("media rep %d seq %d: %d (%d bytes)", repID, seq, segRec.Code, segRec.Body.Len())
			}
			gotStart, gotDur, ok := transcode.FMP4TrackSpan(segRec.Body.Bytes(), tracks[0].ID, tracks[0].DefaultDuration, tracks[0].EditShift)
			if !ok {
				t.Fatalf("isolated media rep %d seq %d had no track span", repID, seq)
			}
			wantS := set.Rep.Tpl.Timeline.S[0]
			if gotStart != wantS.T || gotDur != wantS.D {
				t.Fatalf("rep %d (%s) seq %d fMP4 span (%d, %d) != MPD SegmentTimeline (%d, %d)",
					repID, set.ContentType, seq, gotStart, gotDur, wantS.T, wantS.D)
			}
		}
	})
}

func TestRelayPlanKeepsVideoCopyOnAudioOnlyTranscode(t *testing.T) {
	s := testService(t, Options{Transcoder: &compatibilityTranscoder{}})
	ss := &session{
		info:          SessionInfo{Channel: Channel{ID: "ch1", Kind: "iptv"}},
		caps:          core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}},
		steadyRefused: steadyRefusedCopy,
	}
	ac3Info := core.MediaInfo{
		Container: "mpegts",
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 30},
			{Index: 1, Type: "audio", Codec: "ac3", Channels: 6, SampleRate: 48000},
		},
	}
	dec, _, steady := s.relayPlan(ss, ac3Info)
	if !dec.VideoCopy || dec.AudioCopy || dec.Container != transcode.ContainerFMP4 || steady {
		t.Fatalf("H.264 + AC3 on browser should keep video copy and encode audio only: dec=%+v steady=%v", dec, steady)
	}
}

func TestBroadcastDASHWindowAttributes(t *testing.T) {
	s := testService(t, Options{LatencyProfile: func() string { return "stable" }})
	lease := &broadcastLease{broadcast: &broadcast{segment: 4 * time.Second}}
	minBuf, win, delay := s.broadcastDASHWindow(&session{info: SessionInfo{Channel: Channel{Kind: "iptv"}}}, lease)
	if minBuf != 4 || win != 48 || delay != 12 {
		t.Fatalf("iptv stable DASH window = (%d, %d, %d), want (4, 48, 12)", minBuf, win, delay)
	}
	_, virtWin, _ := s.broadcastDASHWindow(&session{info: SessionInfo{Channel: Channel{Kind: "virtual"}}}, lease)
	if virtWin != 1800 {
		t.Fatalf("virtual DASH window = %d, want 1800", virtWin)
	}
}

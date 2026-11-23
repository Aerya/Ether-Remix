package transcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestDecideAudioOnlyHTTPAndHLS(t *testing.T) {
	flacInfo := core.MediaInfo{
		Container:   "flac",
		Duration:    4 * time.Minute,
		Size:        30 << 20,
		BitrateKbps: 1000,
		Streams: []core.StreamInfo{{
			Index:       0,
			Type:        "audio",
			Codec:       "flac",
			Channels:    2,
			SampleRate:  44100,
			BitrateKbps: 1000,
			Default:     true,
		}},
	}

	// 1. Vela Web declaring aac+opus (no flac) over progressive HTTP -> AAC in mp4, zero video fields.
	webCaps := core.ClientCaps{
		Product:              "Vela Web",
		Containers:           []string{"mp4", "mp3", "ogg"},
		AudioCodecs:          []string{"aac", "opus", "mp3"},
		TranscodeAudioCodecs: []string{"aac", "opus"},
		HLSContainers:        []string{"mp4"},
	}
	dHTTP := Decide(flacInfo, webCaps, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: true, DirectStream: true}, Policy{})
	if dHTTP.Kind != core.DecisionTranscode || dHTTP.AudioCopy || dHTTP.AudioCodec != "aac" || dHTTP.Container != "mp4" {
		t.Fatalf("audio HTTP decision = %+v", dHTTP)
	}
	if dHTTP.VideoCodec != "" || dHTTP.MaxHeight != 0 || dHTTP.H264Level != 0 {
		t.Fatalf("audio-only decision invented video fields: %+v", dHTTP)
	}
	if dHTTP.BitrateKbps <= 0 {
		t.Fatalf("audio-only decision missing BitrateKbps: %+v", dHTTP)
	}

	// 2. Same client over HLS -> AAC fMP4/TS segments, zero video fields.
	dHLS := Decide(flacInfo, webCaps, core.DecideOptions{Protocol: core.ProtocolHLS, DirectPlay: false, DirectStream: true}, Policy{})
	if dHLS.Kind != core.DecisionTranscode || dHLS.AudioCopy || dHLS.AudioCodec != "aac" {
		t.Fatalf("audio HLS decision = %+v", dHLS)
	}
	if dHLS.VideoCodec != "" || dHLS.MaxHeight != 0 {
		t.Fatalf("audio HLS decision invented video fields: %+v", dHLS)
	}

	// 3. Explicit MP3 target with bitrate cap clamps AudioBitrateKbps.
	mp3Caps := core.ClientCaps{
		Product:              "Plex Music",
		AudioCodecs:          []string{"mp3"},
		TranscodeAudioCodecs: []string{"mp3"},
		HLSContainers:        []string{"mp3"},
		MaxBitrateKbps:       128,
	}
	dMP3 := Decide(flacInfo, mp3Caps, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: false, DirectStream: true}, Policy{})
	if dMP3.Kind != core.DecisionTranscode || dMP3.AudioCodec != "mp3" || dMP3.Container != "mp3" || dMP3.AudioBitrateKbps != 128 {
		t.Fatalf("audio MP3 capped decision = %+v", dMP3)
	}

	// 4. Direct play when client supports flac container + codec within bitrate.
	directCaps := core.ClientCaps{
		Containers:  []string{"flac"},
		AudioCodecs: []string{"flac"},
	}
	dDirect := Decide(flacInfo, directCaps, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: true, DirectStream: true}, Policy{})
	if dDirect.Kind != core.DecisionDirectPlay {
		t.Fatalf("expected direct play for flac, got %+v", dDirect)
	}
}

func TestAudioOnlyHTTPMuxArgsAndLosslessFLAC(t *testing.T) {
	info := core.MediaInfo{
		Container: "flac",
		Duration:  2 * time.Minute,
		Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2}},
	}
	cases := []struct {
		container string
		codec     string
		wantFmt   string
		wantNoBA  bool
	}{
		{"mp3", "mp3", "mp3", false},
		{"aac", "aac", "adts", false},
		{"mp4", "aac", "mp4", false},
		{"opus", "opus", "opus", false},
		{"ogg", "opus", "ogg", false},
		{"flac", "flac", "flac", true},
		{"wav", "pcm_s16le", "wav", true},
	}
	for _, tc := range cases {
		spec := runSpec{
			Input:    "http://127.0.0.1/media",
			Info:     info,
			Protocol: core.ProtocolHTTP,
			Decision: core.Decision{
				Kind:             core.DecisionTranscode,
				VideoCopy:        true,
				AudioCopy:        false,
				AudioStream:      0,
				AudioCodec:       tc.codec,
				AudioChannels:    2,
				AudioBitrateKbps: 192,
				Container:        tc.container,
			},
		}
		args := buildArgs(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-f "+tc.wantFmt) {
			t.Fatalf("container=%s args missing -f %s: %s", tc.container, tc.wantFmt, joined)
		}
		hasBA := strings.Contains(joined, "-b:a")
		if tc.wantNoBA && hasBA {
			t.Fatalf("lossless %s should omit -b:a: %s", tc.codec, joined)
		}
		if !tc.wantNoBA && !hasBA {
			t.Fatalf("lossy %s should include -b:a: %s", tc.codec, joined)
		}
	}
}

func TestAudioSpoolRangeProbeAndSeek(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.mp4")
	payload := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sp := &audioSpool{path: path, written: int64(len(payload)), done: true}
	sp.cond = sync.NewCond(&sp.mu)

	s := &httpSession{
		dir:  dir,
		life: &encoderLifetime{},
		req: core.HLSRequest{
			Info: core.MediaInfo{
				Duration: 3 * time.Minute,
				Streams:  []core.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2}},
			},
			Decision: core.Decision{
				Kind:       core.DecisionTranscode,
				AudioCodec: "aac",
				Container:  "mp4",
			},
		},
		spool: sp,
	}

	// Safari/AVPlayer initial Range: bytes=0-1 probe gets exact 206 + Content-Type: audio/mp4.
	req := httptest.NewRequest(http.MethodGet, "/stream.mp4", nil)
	req.Header.Set("Range", "bytes=0-1")
	rec := httptest.NewRecorder()
	if !s.serveAudioSpool(rec, req) {
		t.Fatal("serveAudioSpool returned false")
	}
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 0-1 status = %d, want 206", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "audio/mp4" {
		t.Fatalf("Content-Type = %q, want audio/mp4", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", rec.Header().Get("Accept-Ranges"))
	}
	if rec.Body.String() != "01" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "01")
	}

	// Seek to middle range gets exact slice.
	req2 := httptest.NewRequest(http.MethodGet, "/stream.mp4", nil)
	req2.Header.Set("Range", "bytes=10-15")
	rec2 := httptest.NewRecorder()
	if !s.serveAudioSpool(rec2, req2) {
		t.Fatal("serveAudioSpool(2) returned false")
	}
	if rec2.Code != http.StatusPartialContent || rec2.Body.String() != "ABCDEF" {
		t.Fatalf("Range 10-15 = (%d, %q), want (206, ABCDEF)", rec2.Code, rec2.Body.String())
	}
}

func TestParseProbeM4BChaptersAndAttachedPicSkipped(t *testing.T) {
	raw := []byte(`{
		"format": {"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": "1200.0", "size": "19200000", "bit_rate": "128000"},
		"streams": [
			{"index": 0, "codec_type": "audio", "codec_name": "aac", "channels": 2, "sample_rate": "44100", "bit_rate": "128000", "disposition": {"default": 1}},
			{"index": 1, "codec_type": "video", "codec_name": "mjpeg", "width": 600, "height": 600, "disposition": {"attached_pic": 1}}
		],
		"chapters": [
			{"id": 0, "start_time": "0.0", "end_time": "300.0", "tags": {"title": "Chapter 1: The Beginning"}},
			{"id": 1, "start_time": "300.0", "end_time": "1200.0", "tags": {"title": "Chapter 2: The Journey"}}
		]
	}`)
	m, err := ParseProbe(raw)
	if err != nil {
		t.Fatalf("ParseProbe M4B: %v", err)
	}
	if m.Video() != nil {
		t.Fatalf("attached_pic cover art should be skipped, got video=%+v", m.Video())
	}
	if len(m.Streams) != 1 || m.Streams[0].Type != "audio" || m.Streams[0].Codec != "aac" {
		t.Fatalf("audio streams = %+v", m.Streams)
	}
	if len(m.Chapters) != 2 || m.Chapters[0].Title != "Chapter 1: The Beginning" || m.Chapters[1].Start != 300*time.Second {
		t.Fatalf("M4B chapters = %+v", m.Chapters)
	}
	_ = context.Background()
}

func TestAudioOnlyVideoCopyFalseHTTPPolicyNoCopytsAndNoTrickplay(t *testing.T) {
	flacInfo := core.MediaInfo{
		Container:   "flac",
		Duration:    4 * time.Minute,
		Size:        30 << 20,
		BitrateKbps: 1000,
		Streams: []core.StreamInfo{{
			Index:       0,
			Type:        "audio",
			Codec:       "flac",
			Channels:    2,
			SampleRate:  44100,
			BitrateKbps: 1000,
			Default:     true,
		}},
	}
	direct := Decide(flacInfo, core.ClientCaps{Containers: []string{"flac"}, AudioCodecs: []string{"flac"}}, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: true, DirectStream: true}, Policy{})
	if direct.Kind != core.DecisionDirectPlay || direct.VideoCopy {
		t.Fatalf("audio-only direct play should set VideoCopy=false, got %+v", direct)
	}
	remux := Decide(flacInfo, core.ClientCaps{Containers: []string{"ogg"}, AudioCodecs: []string{"flac"}, HLSContainers: []string{"ogg"}}, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: false, DirectStream: true}, Policy{})
	if remux.Kind != core.DecisionRemux || remux.VideoCopy {
		t.Fatalf("audio-only remux should set VideoCopy=false, got %+v", remux)
	}
	xc := Decide(flacInfo, core.ClientCaps{Containers: []string{"mp4"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"mp4"}}, core.DecideOptions{Protocol: core.ProtocolHTTP, DirectPlay: true, DirectStream: true}, Policy{})
	if xc.Kind != core.DecisionTranscode || xc.VideoCopy || xc.EncodesVideo(flacInfo) {
		t.Fatalf("audio-only transcode should set VideoCopy=false and EncodesVideo=false, got %+v", xc)
	}

	// Audio-only HTTP seek must not pass -copyts into fMP4 audio muxing.
	args := buildArgs(runSpec{
		Input:    "http://127.0.0.1/media",
		Info:     flacInfo,
		Protocol: core.ProtocolHTTP,
		Start:    53 * time.Second,
		Decision: xc,
	})
	for _, a := range args {
		if a == "-copyts" {
			t.Fatalf("audio-only HTTP seek must omit -copyts, got %v", args)
		}
	}

	// Audio-only transcode must not be blocked by VideoTranscodingOff or count against video transcodes.
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{VideoTranscoding: "off", MaxTranscodes: 1})
	hs, err := m.StartHLS(context.Background(), core.HLSRequest{
		SessionID: "audio-off-http",
		InputURL:  "http://127.0.0.1/audio",
		Info:      flacInfo,
		Decision:  xc,
		Protocol:  core.ProtocolHTTP,
	})
	if err != nil || hs == nil {
		t.Fatalf("StartHLS(audio-only HTTP with VideoTranscoding=off) = (%v, %v)", hs, err)
	}
	m.Stop("audio-off-http")

	// EnsureTrickplay must skip audio-only media without queueing thumbnail extraction.
	thumbs := &Thumbnails{ffmpeg: "ffmpeg", entries: map[string]*thumbEntry{}}
	thumbs.EnsureTrickplay(context.Background(), core.TrickplaySource{
		ReleaseID: "rel-audio",
		InputURL:  "http://127.0.0.1/audio",
		Info:      flacInfo,
	})
	if len(thumbs.entries) != 0 {
		t.Fatalf("EnsureTrickplay queued %d entries for audio-only media", len(thumbs.entries))
	}
}

func TestOwnedHTTPAudioRetireThenStopDrainsInputLease(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{})
	input := "http://127.0.0.1/src"
	lease := newTestEncoderInput(input, false)
	audioInfo := core.MediaInfo{
		Container: "mp3",
		Duration:  10 * time.Hour,
		Streams:   []core.StreamInfo{{Index: 0, Type: "audio", Codec: "mp3", Channels: 1}},
	}
	hs, proof, err := m.StartHLSOwned(context.Background(), core.HLSRequest{
		SessionID:  "owned-http-audio",
		InputURL:   input,
		InputLease: lease,
		Info:       audioInfo,
		Decision:   core.Decision{Kind: core.DecisionTranscode, AudioCodec: "aac", Container: "mp4", SubtitleStream: -1},
		Protocol:   core.ProtocolHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	owned := hs.(core.OwnedEncoderSession)
	if err := owned.RetireEncoder(); err != nil {
		t.Fatalf("RetireEncoder: %v", err)
	}
	// Mirrors sessions.Broker.retireBackends: RetireEncoder seals and detaches
	// m.httpSessions[id], then Manager.Stop must still wait for encoder cleanup
	// to seal and drain the input lease before RetireSource runs.
	m.Stop("owned-http-audio")
	select {
	case <-proof.EncoderDone():
	default:
		t.Fatal("Manager.Stop returned before owned HTTP encoder finished retirement")
	}
	select {
	case <-lease.InputDone():
	default:
		t.Fatal("Manager.Stop returned while encoder input lease was still held")
	}
}

func TestAudioSpoolOpenEndedRangeStreamsProgressively(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio.stream")
	if err := os.WriteFile(path, []byte("head-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := &audioSpool{path: path, written: int64(len("head-bytes")), done: false}
	sp.cond = sync.NewCond(&sp.mu)
	s := &httpSession{
		m:    &Manager{},
		dir:  dir,
		life: &encoderLifetime{},
		req: core.HLSRequest{
			Info: core.MediaInfo{
				Duration: 4 * time.Minute,
				Streams:  []core.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2}},
			},
			Decision: core.Decision{Kind: core.DecisionTranscode, AudioCodec: "aac", Container: "mp4"},
		},
		spool: sp,
	}

	if rangeNeedsFullSpool("bytes=0-") {
		t.Fatal("open-ended bytes=0- must stream progressively, not wait for full spool")
	}
	if !rangeNeedsFullSpool("bytes=0-1") || !rangeNeedsFullSpool("bytes=10-") || !rangeNeedsFullSpool("bytes=-10") {
		t.Fatal("bounded probe / nonzero seek / suffix ranges must wait for full spool")
	}

	req := httptest.NewRequest(http.MethodGet, "/stream.m4a", nil)
	req.Header.Set("Range", "bytes=0-")
	rec := httptest.NewRecorder()
	firstWrite := make(chan struct{})
	var once sync.Once
	w := &spoolSignalWriter{ResponseWriter: rec, onWrite: func() { once.Do(func() { close(firstWrite) }) }}
	done := make(chan bool, 1)
	go func() {
		done <- s.serveAudioSpool(w, req)
	}()
	// Wait until the open-ended reader has already started streaming while sp.done is still false.
	select {
	case <-firstWrite:
	case <-time.After(2 * time.Second):
		t.Fatal("open-ended Range bytes=0- blocked waiting for sp.done")
	}
	// Append tail and finish while the open-ended reader is already streaming.
	sp.mu.Lock()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		sp.mu.Unlock()
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("-tail"))
	_ = f.Close()
	sp.written = int64(len("head-bytes-tail"))
	sp.done = true
	sp.cond.Broadcast()
	sp.mu.Unlock()

	if !<-done {
		t.Fatal("serveAudioSpool returned false")
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "head-bytes-tail" {
		t.Fatalf("open-ended Range bytes=0- = (%d, %q), want (200, head-bytes-tail)", rec.Code, rec.Body.String())
	}
}

type spoolSignalWriter struct {
	http.ResponseWriter
	onWrite func()
}

func (w *spoolSignalWriter) Write(p []byte) (int, error) {
	w.onWrite()
	return w.ResponseWriter.Write(p)
}

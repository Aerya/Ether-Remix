package transcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// #650: Roku retries a stalled progressive stream with a nonzero Range.
// The session must refuse it without restarting (or killing) the encode:
// a 200 restart writes a fresh Matroska header where the player expects
// continuation bytes, and superseding ends the playing connection with a
// clean EOF that reads as the end of the title.
func TestHTTPSessionRangeRetryRefused(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{})
	hs, err := m.StartHLS(context.Background(), core.HLSRequest{
		SessionID:   "http-range",
		InputURL:    "http://127.0.0.1/src",
		Info:        httpInfo(),
		Decision:    core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: false, AudioCodec: "aac", Container: "mkv"},
		Protocol:    core.ProtocolHTTP,
		StartOffset: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream := hs.(core.HTTPSession)

	rec := httptest.NewRecorder()
	stream.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("initial serve: %d", rec.Code)
	}
	starts := fr.startCount()

	for _, rng := range []string{"bytes=74260014-", "bytes=1024-2048", "bytes=-4096"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Range", rng)
		stream.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("Range %q: status %d, want 416", rng, rec.Code)
		}
		if fr.startCount() != starts {
			t.Fatalf("Range %q restarted the encoder", rng)
		}
	}

	// A range from byte 0 is a fresh GET.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=0-")
	stream.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Range bytes=0-: status %d, want 200", rec.Code)
	}
}

func TestRangeStartsAfterZero(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"bytes=0-", false},
		{"bytes=0-65535", false},
		{"bytes=1-", true},
		{"bytes=74260014-", true},
		{"bytes=-4096", true},
		{"bytes=0-9,100-200", false},
		{"items=5-10", false},
		{"bytes=abc-", false},
		{"garbage", false},
	} {
		if got := rangeStartsAfterZero(tc.in); got != tc.want {
			t.Errorf("rangeStartsAfterZero(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// #650 (resume audio): a progressive video-copy start must not accurate-
// seek its audio to Start while the video opens at the keyframe before
// it; the player would show that GOP silent. HLS keeps its own cuts.
func TestHTTPProgressiveCopySeekArgs(t *testing.T) {
	info := core.MediaInfo{Container: "mkv", Duration: time.Hour, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080, FrameRate: 24000.0 / 1001.0},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, SampleRate: 48000},
	}}
	d := core.Decision{
		Kind:            core.DecisionRemux,
		Container:       "mkv",
		VideoCopy:       true,
		AudioCodec:      "aac",
		AudioStream:     1,
		AudioChannels:   2,
		AudioSampleRate: 48000,
		SubtitleStream:  -1,
	}

	spec := baseSpec(info, d)
	spec.Protocol = core.ProtocolHTTP
	args := buildArgs(spec)
	inIdx := inputIndex(args)
	if inIdx < 0 {
		t.Fatalf("no -i in args: %v", args)
	}
	mustContain(t, args[:inIdx+1], "-noaccurate_seek -ss 200.000 -copyts -i")

	// The same copy over HLS keeps the accurate audio cut.
	spec = baseSpec(info, d)
	spec.Protocol = core.ProtocolHLS
	args = buildArgs(spec)
	inIdx = inputIndex(args)
	mustNotContain(t, args[:inIdx+1], "-noaccurate_seek")
}

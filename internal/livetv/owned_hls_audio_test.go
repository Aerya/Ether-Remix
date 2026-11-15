package livetv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestHLSAudioVariantSelection(t *testing.T) {
	master := []byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="sound",NAME="English",DEFAULT=YES,AUTOSELECT=YES,URI="audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=415296,RESOLUTION=426x240,CODECS="avc1.42c020"
silent.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=4000000,RESOLUTION=1920x1080,CODECS="avc1.640028",AUDIO="sound"
video.m3u8
`)
	base, _ := url.Parse("https://provider.invalid/master.m3u8")
	m := parseMasterPlaylist(master, base)
	caps := core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 1080, MaxBitrateKbps: 6000}
	v, ok := pickVariant(m, caps)
	if !ok || v.height != 1080 || !variantHasAudio(v) || !m.demuxedAudio[v.audio] {
		t.Fatalf("lost best audio-bearing variant: %+v, ok=%v", v, ok)
	}
	filtered := parseMasterPlaylist(selectedHLSMaster(master, base, caps), base)
	if len(filtered.variants) != 1 || filtered.variants[0].height != 1080 || !filtered.demuxedAudio["sound"] {
		t.Fatalf("probe/decoder master lost audio or selected wrong program: %+v", filtered)
	}
	// The owned timeline must request remuxing, never ingest video alone.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(master) }))
	defer origin.Close()
	s := &Service{o: Options{HTTPClient: origin.Client()}}
	p := &passthroughProducer{s: s, caps: caps, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := p.open(context.Background(), StreamSource{ID: "fake"}, origin.URL, nil, false); !errors.Is(err, errNeedsRelay) {
		t.Fatalf("separate audio did not require remux: %v", err)
	}
	// With both variants muxed, choose the highest within each ceiling.
	m.variants[0].codecs += ",mp4a.40.2"
	m.variants[1].audio = ""
	m.variants[1].codecs += ",mp4a.40.2"
	for _, tc := range []struct{ height, bitrate, want int }{{1080, 6000, 1080}, {720, 6000, 240}, {1080, 1000, 240}, {0, 0, 1080}} {
		got, ok := pickVariant(m, core.ClientCaps{MaxHeight: tc.height, MaxBitrateKbps: tc.bitrate})
		if !ok || got.height != tc.want {
			t.Fatalf("caps %+v: selected %+v", tc, got)
		}
	}
}

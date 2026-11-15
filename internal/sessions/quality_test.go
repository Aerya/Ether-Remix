package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type qualityTranscoder struct{ fakeMediaTranscoder }

func (*qualityTranscoder) ProactiveFallbackAvailable() bool { return true }

func TestQualityPolicyInvariants(t *testing.T) {
	info := core.MediaInfo{Container: "mp4", Duration: time.Hour, BitrateKbps: 15000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}, {Index: 1, Type: "audio", Codec: "aac", BitrateKbps: 192}}}
	playback := &fakePlayback{sessions: map[string]*core.PlaySession{"source": {ID: "source", Info: info}}}
	b := &Broker{deps: BrokerDeps{Playback: playback, Transcoder: &qualityTranscoder{}, Now: time.Now, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}}}
	s := &brokerSession{public: frontend.Session{Protocol: "vela", ViewerID: 1, Item: core.MovieKey(1)}, playID: "source", caps: core.ClientCaps{VideoCodecs: []string{"h264"}, MaxHeight: 720, MaxBitrateKbps: 2000}, opts: core.DecideOptions{DirectPlay: true, DirectStream: true}, decision: core.Decision{Kind: core.DecisionRemux, Container: "fmp4", VideoCopy: true, AudioCopy: true, AudioStream: 1}}
	viewer := &core.Viewer{ID: 1}
	rungs, err := b.qualityRungs(context.Background(), s, viewer)
	if err != nil || len(rungs) == 0 {
		t.Fatal(rungs, err)
	}
	for _, r := range rungs {
		if r.Height > 720 || r.Bandwidth > 2000000 {
			t.Fatal("session caps escaped", r)
		}
	}
	denied := false
	s.allowTranscoding = &denied
	if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
		t.Fatal("allowTranscode=false", r, err)
	}
	s.allowTranscoding = nil
	for _, protocol := range []frontend.Protocol{"plex", "jellyfin"} {
		s.public.Protocol = protocol
		if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
			t.Fatal("changed another surface", r, err)
		}
	}
	s.public.Protocol = "vela"
	s.live = true
	if r, err := b.qualityRungs(context.Background(), s, viewer); err != nil || len(r) != 0 {
		t.Fatal("file ladder on live", r, err)
	}
	if playback.prepares != 0 {
		t.Fatal("quality planning resolved a source")
	}
}

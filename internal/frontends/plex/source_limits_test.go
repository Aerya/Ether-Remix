package plex

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// The quality slider changes output, while source eligibility remains identical
// to the device's original request. Direct-play checks must still see the cap.
func TestTranscodeOutputSourceIndependence(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	pr := &Request{User: &ownerUser, ClientID: "lg", Product: "PlxNative", Platform: "webOS"}
	parse := func(query string) playRequest {
		r := httptest.NewRequest("GET", "/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+query, nil)
		r = r.WithContext(withRequest(context.Background(), pr))
		p, err := f.srv.parsePlayRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	baseline := parse("")
	original := baseline.sourceSelectionCaps()
	for _, query := range []string{"&videoResolution=426x240&maxVideoBitrate=320", "&videoResolution=854x480&videoBitrate=720"} {
		p := parse(query)
		source := p.sourceSelectionCaps()
		if source.MaxHeight != original.MaxHeight || source.ExplicitMaxHeight != original.ExplicitMaxHeight || source.MaxBitrateKbps != 0 {
			t.Fatalf("quality changed source caps: %+v (original %+v)", source, original)
		}
		info := core.MediaInfo{Container: "mp4", BitrateKbps: 8000, Streams: []core.StreamInfo{
			{Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Type: "audio", Codec: "aac", Channels: 2},
		}}
		decision := transcode.Decide(info, p.caps, p.opts, transcode.Policy{})
		if decision.Kind != core.DecisionTranscode || decision.MaxHeight > p.caps.MaxHeight || p.opts.MaxBitrateKbps != p.caps.MaxBitrateKbps {
			t.Fatalf("output cap lost: %+v, caps %+v", decision, p.caps)
		}
	}
}

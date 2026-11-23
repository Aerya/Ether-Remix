package plex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// coldPlayback618 estimates without subtitle tracks until Prepare probes.
type coldPlayback618 struct {
	*fakePlayback
	probed core.MediaInfo
}

func (c *coldPlayback618) Estimate(_ context.Context, _ *core.Title, _ core.ClientCaps) core.MediaInfo {
	return estimateInfo
}

func (c *coldPlayback618) Prepare(ctx context.Context, u *core.User, k core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	c.fakePlayback.info = c.probed
	return c.fakePlayback.Prepare(ctx, u, k, caps)
}

// TestFirstPlayFireTVEmbeddedSubtitleReachesHLS is #618 on Plex for Amazon
// FireTV 2026.22.0 (AFTKRT): a cold title whose embedded English SRT is
// picked by the viewer's en/Always rule. The pre-play metadata has no
// embedded track, so the /decision MediaContainer and the next
// /library/metadata read must carry it selected, and the HLS start (a
// conversion, not direct play) must carry it as a WebVTT rendition:
// segmented TS/fMP4 cannot mux the track and this client never fetches
// the decision's sidecar key.
func TestFirstPlayFireTVEmbeddedSubtitleReachesHLS(t *testing.T) {
	info := core.MediaInfo{
		Container: "mkv", Duration: 25 * time.Minute, BitrateKbps: 20000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, Default: true, DolbyVision: true, DolbyVisionProfile: 5},
			{Index: 1, Type: "audio", Codec: "eac3", Language: "en", Channels: 6, Default: true},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
		},
	}
	f, _, _ := newIssue459Fixture(t, info, "en")
	f.srv.setPlayback(&coldPlayback618{fakePlayback: f.playback, probed: info})
	f.playback.info = estimateInfo
	key := fightClub.Key.String()
	hdr := []string{
		"X-Plex-Product", "Plex for Amazon FireTV",
		"X-Plex-Version", "2026.22.0",
		"X-Plex-Platform", "Android",
		"X-Plex-Device", "AFTKRT",
		"X-Plex-Model", "karat",
		"X-Plex-Client-Identifier", "firetv-618",
		"X-Plex-Session-Identifier", "play-618",
	}

	before := f.get(t, "/library/metadata/"+key, hdr...)
	for _, tag := range subtitleTags(before.Body.String()) {
		if strings.Contains(tag, `index="2"`) {
			t.Fatalf("pre-play metadata already lists the embedded track: %s", tag)
		}
	}

	dec := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+key+"&directPlay=1&directStream=1&hasMDE=1&mediaIndex=0&partIndex=0&protocol=hls&session=fire618", hdr...)
	if dec.Code != 200 {
		t.Fatalf("decision %d: %s", dec.Code, dec.Body.String())
	}
	selected := selectedTag(subtitleTags(dec.Body.String()))
	decID := streamAttr(selected, "id")
	if decID == "" || !strings.Contains(selected, `languageCode="eng"`) {
		t.Fatalf("decision did not list the embedded SRT selected: %q", selected)
	}

	meta := f.get(t, "/library/metadata/"+key, hdr...)
	selected = selectedTag(subtitleTags(meta.Body.String()))
	if streamAttr(selected, "id") != decID || !strings.Contains(selected, `index="2"`) || !strings.Contains(selected, `languageCode="eng"`) {
		t.Fatalf("metadata after decision did not list the embedded SRT selected: %q", selected)
	}

	start := f.get(t, "/video/:/transcode/universal/start.m3u8?path=/library/metadata/"+key+"&directPlay=0&directStream=1&hasMDE=1&protocol=hls&session=fire618", hdr...)
	if start.Code != 200 {
		t.Fatalf("start %d: %s", start.Code, start.Body.String())
	}
	hs := f.trans.sessions["fire618"]
	if hs == nil {
		t.Fatal("missing HLS session fire618")
	}
	if hs.req.Decision.SubtitleStream != 2 || len(hs.req.TextSubtitleStreams) != 1 || hs.req.TextSubtitleStreams[0] != 2 {
		t.Fatalf("HLS request subtitle=%d text=%v, want embedded track 2 as a text rendition", hs.req.Decision.SubtitleStream, hs.req.TextSubtitleStreams)
	}
	body := start.Body.String()
	if !strings.Contains(body, `TYPE=SUBTITLES`) || !strings.Contains(body, `LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES`) || !strings.Contains(body, `text-2/index.m3u8`) || !strings.Contains(body, `SUBTITLES="text"`) {
		t.Fatalf("master playlist has no selected English text rendition:\n%s", body)
	}
}

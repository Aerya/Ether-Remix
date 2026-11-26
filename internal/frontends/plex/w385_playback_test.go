package plex

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const w385Universal = "/video/:/transcode/universal/"

func TestW385CachedHLSSegmentURLsSurvivePlaybackUpdates(t *testing.T) {
	var elapsed atomic.Int64
	f, manager := issue94Fixture(t, false, "open", func(d *testServerDeps) {
		setDepsNow(d, func() time.Time { return fixedNow.Add(time.Duration(elapsed.Load())) })
	})
	// The offline resolver fixture has one release. Give the second title
	// the same source name so that both keys can resolve independently.
	otherTitle := fightClub
	otherTitle.Key, otherTitle.IDs = alien.Key, alien.IDs
	f.catalog.titles[otherTitle.Key] = otherTitle
	web := map[string]string{"X-Plex-Product": "Vela"}
	query := func(key core.RatingKey, session string) url.Values {
		return url.Values{"path": {"/library/metadata/" + key.String()}, "session": {session}, "directPlay": {"0"}, "directStream": {"1"}}
	}
	get := func(target string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		r := f.request(t, http.MethodGet, target, headers)
		if r.Code != http.StatusOK {
			t.Fatalf("request failed with HTTP %d", r.Code)
		}
		return r
	}
	q := query(fightClub.Key, "cached-hls")
	decision := w385Universal + "decision?" + q.Encode()
	start := w385Universal + "start.m3u8?" + q.Encode()
	get(decision, web)
	master := get(start, web)
	resolve := func(parent, body string) *url.URL {
		t.Helper()
		base, err := url.Parse(parent)
		if err != nil {
			t.Fatal("invalid playlist request URL")
		}
		for _, line := range strings.Split(body, "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				u, err := base.Parse(line)
				if err != nil {
					t.Fatal("invalid media URL in playlist")
				}
				return u
			}
		}
		t.Fatal("missing media URL in playlist")
		return nil
	}
	native := map[string]string{"X-Plex-Token": "", "X-Plex-Client-Identifier": ""}
	variant := resolve(start, master.Body.String())
	segment := resolve(variant.String(), get(variant.String(), native).Body.String())
	if !segment.Query().Has(streamGrantParam) || !segment.Query().Has(readCapParam) {
		t.Fatal("first playlist did not issue both capabilities")
	}
	streamOnly, readOnly := *segment, *segment
	grants := streamOnly.Query()
	grants.Del(readCapParam)
	streamOnly.RawQuery = grants.Encode()
	grants = readOnly.Query()
	grants.Del(streamGrantParam)
	readOnly.RawQuery = grants.Encode()
	checkCachedSegment := func() {
		t.Helper()
		get(streamOnly.String(), native)
		get(readOnly.String(), map[string]string{"X-Plex-Token": ""})
	}
	checkCachedSegment()
	timeline := func(key core.RatingKey, playback, session, state string) {
		t.Helper()
		get(fmt.Sprintf("/:/timeline?ratingKey=%d&state=%s&time=%d&X-Plex-Session-Identifier=%s&session=%s",
			key, state, time.Duration(elapsed.Load()).Milliseconds(), playback, session), web)
	}
	for _, secondMark := range []int{5, 15, 25, 33, 40} {
		elapsed.Store(int64(time.Duration(secondMark) * time.Second))
		timeline(fightClub.Key, "browser-one", "cached-hls", "playing")
		switch secondMark {
		case 15:
			get(decision, web)
			checkCachedSegment()
		case 25:
			part := fmt.Sprintf("/library/parts/%d/%d/file.mkv", otherTitle.Key, otherTitle.UpdatedAt.Unix())
			rangeHeaders := map[string]string{"X-Plex-Product": "Vela", "Range": "bytes=0-9"}
			if r := f.request(t, http.MethodGet, part, rangeHeaders); r.Code != http.StatusPartialContent {
				t.Fatalf("second title range returned HTTP %d", r.Code)
			}
			if _, ok := manager.SessionFor(ownerUser.ID, "client-1", fightClub.Key); !ok {
				t.Fatal("other player ended the first source")
			}
		case 33:
			// A timeline between encoder runs must leave the grant usable
			// when the player restarts under the same transcode ID.
			f.srv.transcoder().Stop("cached-hls")
			timeline(fightClub.Key, "browser-one", "cached-hls", "buffering")
			q.Set("offset", "33")
			get(w385Universal+"start.m3u8?"+q.Encode(), web)
		case 40:
			timeline(otherTitle.Key, "browser-two", "client-1-"+otherTitle.Key.String(), "stopped")
			if _, ok := manager.SessionFor(ownerUser.ID, "client-1", otherTitle.Key); ok {
				t.Fatal("explicit timeline stop left the second source open")
			}
		}
		checkCachedSegment()
	}
	timeline(fightClub.Key, "browser-one", "cached-hls", "stopped")
	if r := f.request(t, http.MethodGet, segment.String(), native); r.Code != http.StatusUnauthorized {
		t.Fatalf("cached media after playback ended returned HTTP %d", r.Code)
	}
}

func TestW385VelaHLSBurnsSelectedTextWithDirectStream(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprintf("sidecar=%t", external), func(t *testing.T) {
			f := newFixture()
			t.Cleanup(func() { f.srv.Close() })
			tr := &issue94Transcoder{newFakeTranscoder(core.Decision{})}
			f.srv.setTranscoder(tr)
			track := core.StreamInfo{Index: 4, Type: "subtitle", Codec: "srt", Language: "eng", External: external}
			if external {
				track.ExternalURL = "ether-subtitle:test-sidecar"
			}
			f.playback.info = core.MediaInfo{Container: "mkv", Duration: time.Hour, Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
				track,
			}}
			headers := map[string]string{"X-Plex-Product": "Vela", "X-Plex-Session-Identifier": "burn-playback"}
			key := fightClub.Key.String()
			rec := f.request(t, http.MethodGet, "/library/metadata/"+key, headers)
			streams := streamTagsOfType(rec.Body.String(), "3")
			if rec.Code != 200 || len(streams) != 1 {
				t.Fatalf("metadata HTTP %d, subtitle count %d", rec.Code, len(streams))
			}
			id := streamAttr(streams[0], "id")
			if r := f.request(t, http.MethodPut, "/library/parts/"+key+"?subtitleStreamID="+id, headers); r.Code != 200 {
				t.Fatalf("track selection returned HTTP %d", r.Code)
			}
			query := "?path=/library/metadata/" + key + "&session=burn-text&directPlay=0&directStream=1&subtitles=burn"
			for _, endpoint := range []string{"decision", "start.m3u8"} {
				if r := f.request(t, http.MethodGet, w385Universal+endpoint+query, headers); r.Code != 200 {
					t.Fatalf("%s returned HTTP %d", endpoint, r.Code)
				}
			}
			h, ok := tr.Session("burn-text")
			if !ok {
				t.Fatal("missing HLS encoder")
			}
			dec := h.(*fakeHLS).req.Decision
			if dec.VideoCopy || !dec.BurnSubtitles || dec.SubtitleStream != track.Index {
				t.Fatalf("HLS output videoCopy=%t burn=%t track=%d", dec.VideoCopy, dec.BurnSubtitles, dec.SubtitleStream)
			}
		})
	}
}

func TestW385VelaFirstMetadataIncludesAudioAndSubtitleTracks(t *testing.T) {
	f := newFixture()
	t.Cleanup(func() { f.srv.Close() })
	info := sampleInfo
	info.Streams = append([]core.StreamInfo(nil), sampleInfo.Streams...)
	info.Streams = append(info.Streams, core.StreamInfo{Index: 20, Type: "subtitle", Codec: "srt", Language: "spa", External: true, ExternalURL: "ether-subtitle:test-sidecar"})
	f.playback.info.Streams = f.playback.info.Streams[:2]
	warm := &warmWaiterPlayback{Playback: f.playback, info: info}
	f.srv.setPlayback(warm)
	rec := f.get(t, "/library/metadata/"+fightClub.Key.String(), "X-Plex-Product", "Vela")
	if a, s := len(audioTags(rec.Body.String())), len(streamTagsOfType(rec.Body.String(), "3")); rec.Code != 200 || a != 2 || s != 3 {
		t.Fatalf("first metadata HTTP %d, audio=%d subtitles=%d", rec.Code, a, s)
	}
}

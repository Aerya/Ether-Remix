package plex

import (
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type hevcDecideTranscoder struct {
	*fakeTranscoder
	policy transcode.Policy
}

func (r hevcDecideTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	r.fakeTranscoder.mu.Lock()
	r.fakeTranscoder.lastOpts, r.fakeTranscoder.lastCaps = opts, caps
	r.fakeTranscoder.mu.Unlock()
	return transcode.Decide(info, caps, opts, r.policy)
}

// Plex for iOS 2026.20 (Bedrock) sends X-Plex-Client-Profile-Name: Generic,
// a wildcard 8-bit videoCodec upperBound overridden by a codec-specific HEVC
// 10-bit replace=true upperBound, an H.264 HLS transcode target extended via
// append-transcode-target-codec(videoCodec=hevc), and a videoTranscodeTarget
// colorTrc allow-list containing smpte2084 and arib-std-b67.
const plexIOS202620HDRProfileExtra = "add-direct-play-profile(type=videoProfile&container=mp4,mov,mpegts,mkv&videoCodec=h264,hevc&audioCodec=aac,ac3,eac3)" +
	"+add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mp4&videoCodec=h264&audioCodec=aac,ac3,eac3)" +
	"+append-transcode-target-codec(type=videoProfile&context=streaming&protocol=hls&videoCodec=hevc)" +
	"+add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.bitDepth&value=8)" +
	"+add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.bitDepth&value=10&replace=true)" +
	"+add-limitation(scope=videoTranscodeTarget&scopeName=hevc&scopeType=videoCodec&context=streaming&protocol=hls&type=match&name=video.colorTrc&list=bt709|bt470m|smpte170m|smpte240m|bt2020-10|smpte2084|arib-std-b67&isRequired=false)"

func TestPlexIOS202620PreservesHDROn720pDownscale(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	f.srv.setTranscoder(hevcDecideTranscoder{
		fakeTranscoder: f.trans,
		policy:         transcode.Policy{HEVCEncoder: true, HEVCHardware: true},
	})

	hdrPlus := true
	f.playback.info = core.MediaInfo{
		Container:   "mkv",
		BitrateKbps: 24500,
		Duration:    sampleInfo.Duration,
		Streams: []core.StreamInfo{
			{
				Index:                      0,
				Type:                       "video",
				Codec:                      "hevc",
				Profile:                    "Main 10",
				Width:                      3840,
				Height:                     2160,
				BitDepth:                   10,
				PixelFormat:                "yuv420p10le",
				ColorPrimaries:             "bt2020",
				ColorTransfer:              "smpte2084",
				ColorSpace:                 "bt2020nc",
				DolbyVision:                true,
				DolbyVisionProfile:         8,
				DolbyVisionCompatibilityID: 1,
				HDR10Plus:                  &hdrPlus,
			},
			{
				Index:    1,
				Type:     "audio",
				Codec:    "eac3",
				Channels: 6,
				Default:  true,
			},
		},
	}

	headers := []string{
		"X-Plex-Product", "Plex for iOS",
		"X-Plex-Version", "2026.20",
		"X-Plex-Platform", "iOS",
		"X-Plex-Platform-Version", "18.4",
		"X-Plex-Device", "iPhone",
		"X-Plex-Client-Identifier", "ios-2026-20-hdr",
		"X-Plex-Client-Profile-Name", "Generic",
		"X-Plex-Client-Profile-Extra", plexIOS202620HDRProfileExtra,
	}

	q := url.Values{
		"path":            {"/library/metadata/1000000000000550"},
		"mediaIndex":      {"0"},
		"partIndex":       {"0"},
		"protocol":        {"hls"},
		"hasMDE":          {"1"},
		"directPlay":      {"0"},
		"directStream":    {"1"},
		"videoResolution": {"1280x720"},
		"maxVideoBitrate": {"4000"},
		"session":         {"ios-hdr-720p"},
	}

	res := f.get(t, "/video/:/transcode/universal/decision?"+q.Encode(), headers...)
	if res.Code != 200 {
		t.Fatalf("decision status = %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{
		`generalDecisionCode="1001"`,
		`videoCodec="hevc"`,
		`videoResolution="720p"`,
		`container="mp4"`,
		`codec="hevc"`,
		`decision="transcode"`,
		`bitDepth="10"`,
		`colorTrc="smpte2084"`,
		`height="720"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %s in decision XML:\n%s", want, body)
		}
	}
	if strings.Contains(body, `colorTrc="bt709"`) || strings.Contains(body, `videoCodec="h264"`) {
		t.Fatalf("720p downscale unexpectedly tone-mapped to H.264 SDR:\n%s", body)
	}

	// Direct play at original quality with Generic + wildcard 8-bit + HEVC 10-bit replace=true
	// must also be admitted without tripping the 8-bit wildcard limitation.
	dpQuery := url.Values{
		"path":         {"/library/metadata/1000000000000550"},
		"mediaIndex":   {"0"},
		"partIndex":    {"0"},
		"protocol":     {"hls"},
		"hasMDE":       {"1"},
		"directPlay":   {"1"},
		"directStream": {"1"},
		"session":      {"ios-hdr-dp"},
	}
	dpRes := f.get(t, "/video/:/transcode/universal/decision?"+dpQuery.Encode(), headers...)
	if dpRes.Code != 200 || !strings.Contains(dpRes.Body.String(), `mdeDecisionCode="1000"`) || !strings.Contains(dpRes.Body.String(), `decision="directplay"`) {
		t.Fatalf("expected direct play (1000) when uncapped, got %d:\n%s", dpRes.Code, dpRes.Body.String())
	}
}

func TestWildcardBitDepth8WithoutHEVC10OverrideStillDeniesHDR(t *testing.T) {
	extra := "add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mp4&videoCodec=h264,hevc&audioCodec=aac)" +
		"+add-limitation(scope=videoCodec&scopeName=*&type=upperBound&name=video.bitDepth&value=8)"
	caps := (&Request{
		Product:      "Plex for iOS",
		Version:      "2026.20",
		Platform:     "iOS",
		Device:       "iPhone",
		ProfileExtra: extra,
	}).ClientCaps(0)
	if caps.HDR10 || caps.DolbyVision || !caps.ExplicitNoHDR {
		t.Fatalf("wildcard 8-bit cap without HEVC 10-bit override must clear HDR: %+v", caps)
	}
}

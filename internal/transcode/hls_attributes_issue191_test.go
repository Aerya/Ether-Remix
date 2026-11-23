package transcode

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Contract under test: the HLS variant for Plex for Android's Matroska
// transport carries neither CODECS nor SUPPLEMENTAL-CODECS, and nothing else
// about it changes (VIDEO-RANGE stays). Every other client keeps both
// (Apple's HLS authoring spec requires CODECS). With CODECS declared, the
// app's media3 player builds its tracks from the playlist alone and drops the
// subrip stream muxed into the Matroska segments, so embedded subtitles are
// listed but never drawn. See the comment in HLSVariantAttributes.
//
// The app finds CODECS with an unanchored pattern, CODECS="...", so
// SUPPLEMENTAL-CODECS="..." is read as CODECS too. clientCodecs below is that
// same pattern: these tests fail on what the client would parse, not only on
// what Ether intends to send.
var clientCodecs = regexp.MustCompile(`CODECS="[^"]*"`)

// Used only to build the expected variant from the control.
var (
	plainCodecs        = regexp.MustCompile(`,CODECS="[^"]*"`)
	supplementalCodecs = regexp.MustCompile(`,SUPPLEMENTAL-CODECS="[^"]*"`)
)

// hasCodecs reports whether the app would find a CODECS value in attrs.
func hasCodecs(attrs string) bool { return clientCodecs.MatchString(attrs) }

func android191Info(t *testing.T, mutate func(v *core.StreamInfo)) core.MediaInfo {
	t.Helper()
	v := core.StreamInfo{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, BitDepth: 10, FrameRate: 23.976}
	if mutate != nil {
		mutate(&v)
	}
	return core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{
		v,
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Default: true},
		{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
	}}
}

// The Android Matroska variant is the ordinary variant minus CODECS and
// SUPPLEMENTAL-CODECS, for every kind of picture. The comparison against the
// control is exact on purpose: a change that drops or rewrites VIDEO-RANGE
// fails it.
func TestAndroidMatroskaHLSVariantOmitsOnlyCodecs(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  func(v *core.StreamInfo)
		// wantRange and wantSupplemental are asserted literally on the control
		// so the test still fails if both variants lose an attribute together.
		wantRange, wantSupplemental string
	}{
		{"SDR", func(v *core.StreamInfo) { v.BitDepth = 8; v.Codec = "h264"; v.Profile = "High" }, "SDR", ""},
		{"HDR10", func(v *core.StreamInfo) { v.ColorTransfer = "smpte2084" }, "PQ", ""},
		{"HLG", func(v *core.StreamInfo) { v.ColorTransfer = "arib-std-b67" }, "HLG", ""},
		{"Dolby Vision P8.1", func(v *core.StreamInfo) {
			v.ColorTransfer = "smpte2084"
			v.DolbyVision, v.DolbyVisionProfile, v.DolbyVisionLevel, v.DolbyVisionCompatibilityID = true, 8, 6, 1
		}, "PQ", "dvh1.08.06/db1p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := android191Info(t, tc.src)
			dec := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: 2, Container: ContainerMatroska}

			control := HLSVariantAttributes(info, dec) // not the Android quirk
			if !hasCodecs(control) {
				t.Fatalf("control variant lost CODECS, the test would prove nothing: %s", control)
			}
			if tc.wantSupplemental != "" && !strings.Contains(control, `,SUPPLEMENTAL-CODECS="`+tc.wantSupplemental+`"`) {
				t.Fatalf("control variant lost SUPPLEMENTAL-CODECS %q, the test would prove nothing: %s", tc.wantSupplemental, control)
			}

			dec.AndroidMatroskaHLS = true
			got := HLSVariantAttributes(info, dec)
			if hasCodecs(got) {
				t.Fatalf("Android Matroska HLS must not carry anything the app reads as CODECS (media3 would drop the muxed subtitle track): %s", got)
			}
			want := supplementalCodecs.ReplaceAllString(plainCodecs.ReplaceAllString(control, ""), "")
			if got != want {
				t.Fatalf("only CODECS and SUPPLEMENTAL-CODECS may differ from the regular variant\n got: %s\nwant: %s", got, want)
			}
			if !strings.Contains(got, ",VIDEO-RANGE="+tc.wantRange) {
				t.Fatalf("VIDEO-RANGE=%s missing: %s", tc.wantRange, got)
			}
		})
	}
}

// The omission is scoped to one transport: Android's
// Matroska HLS. The same player on MPEG-TS or fMP4 (or any other client on
// Matroska) keeps the CODECS the HLS spec asks for, and the Dolby Vision
// SUPPLEMENTAL-CODECS that goes with it.
func TestCodecsKeptOutsideAndroidMatroskaHLS(t *testing.T) {
	info := android191Info(t, func(v *core.StreamInfo) {
		v.ColorTransfer = "smpte2084"
		v.DolbyVision, v.DolbyVisionProfile, v.DolbyVisionLevel, v.DolbyVisionCompatibilityID = true, 8, 6, 1
	})
	for _, tc := range []struct {
		name      string
		container string
		android   bool
	}{
		{"Android quirk on MPEG-TS", ContainerMPEGTS, true},
		{"Android quirk on fMP4", ContainerFMP4, true},
		{"other client on Matroska", ContainerMatroska, false},
		{"other client on MPEG-TS", ContainerMPEGTS, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dec := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: -1, Container: tc.container, AndroidMatroskaHLS: tc.android}
			attrs := HLSVariantAttributes(info, dec)
			if !strings.Contains(attrs, `,CODECS="hvc1.`) || !strings.Contains(attrs, ",VIDEO-RANGE=PQ") ||
				!strings.Contains(attrs, `,SUPPLEMENTAL-CODECS="dvh1.08.06/db1p"`) {
				t.Fatalf("CODECS, VIDEO-RANGE and SUPPLEMENTAL-CODECS must stay: %s", attrs)
			}
		})
	}
}

// Wire the real pieces together: the registry quirk, Decide and the variant.
// Plex for Android (Mobile) asking for Matroska HLS gets no CODECS; Apple TV
// asking for the same container does.
//
// Plex for Android (TV) (Shield, Google TV, Fire TV) matches the same quirk
// and runs the same media3 code, so it gets the same variant. If TV ever needs
// CODECS, narrow the "plex-android-matroska-hls" registry match and change
// that case here.
func TestDecideDrivesCodecsForAndroidMatroskaHLS(t *testing.T) {
	info := android191Info(t, nil)
	info.Video().ColorTransfer = "smpte2084"
	mobile := core.ClientCaps{
		Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 9 Pro", MaxHeight: 2160, HDR10: true,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"},
		Containers: []string{"mp4", "mkv", "mpegts"},
	}
	tv := mobile
	tv.Product, tv.Device = "Plex for Android (TV)", "SHIELD Android TV"
	opts := allow()
	opts.DirectPlay = false // the player asked for HLS, as it does for a stream

	for _, tc := range []struct {
		name       string
		caps       core.ClientCaps
		container  string
		wantQuirk  bool
		wantCodecs bool
	}{
		{"Plex for Android (Mobile), Matroska HLS", mobile, ContainerMatroska, true, false},
		{"Plex for Android (TV), Matroska HLS (same quirk)", tv, ContainerMatroska, true, false},
		{"Plex for Android (Mobile), fMP4 HLS", mobile, ContainerFMP4, true, true},
		{"Plex for Apple TV, Matroska HLS", appleTV, ContainerMatroska, false, true},
		{"Plex Web, MPEG-TS HLS", plexWeb, ContainerMPEGTS, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := tc.caps
			caps.HLSContainers = []string{tc.container}
			d := Decide(info, caps, opts, Policy{})
			if d.Kind == core.DecisionDirectPlay || d.AndroidMatroskaHLS != tc.wantQuirk {
				t.Fatalf("decision: %+v", d)
			}
			if tc.wantQuirk && tc.container == ContainerMatroska && d.Container != ContainerMatroska {
				t.Fatalf("the Android Matroska case must be a Matroska decision, got %q: %+v", d.Container, d)
			}
			if got := hasCodecs(HLSVariantAttributes(info, d)); got != tc.wantCodecs {
				t.Fatalf("CODECS present = %v, want %v: %s", got, tc.wantCodecs, HLSVariantAttributes(info, d))
			}
		})
	}
}

// The case that made SUPPLEMENTAL-CODECS part of the omission: a Dolby Vision
// profile 8.1 title copied (not stripped) to a Plex for Android client that has
// Dolby Vision on (an owner override; it is off by default). Decide really
// copies the stream with its DV signalling, and the variant must still hand the
// app nothing it can read as CODECS.
func TestDecideDolbyVisionCopyOnAndroidMatroskaHLSHasNoCodecs(t *testing.T) {
	info := android191Info(t, func(v *core.StreamInfo) {
		v.ColorTransfer = "smpte2084"
		v.DolbyVision, v.DolbyVisionProfile, v.DolbyVisionLevel, v.DolbyVisionCompatibilityID = true, 8, 6, 1
	})
	caps := core.ClientCaps{
		Product: "Plex for Android (TV)", Platform: "Android", Device: "SHIELD Android TV", MaxHeight: 2160,
		HDR10: true, DolbyVision: true, DVProfiles: []int{8},
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"},
		Containers: []string{"mp4", "mkv", "mpegts"}, HLSContainers: []string{ContainerMatroska},
	}
	opts := allow()
	opts.DirectPlay = false
	d := Decide(info, caps, opts, Policy{})
	if d.Kind == core.DecisionDirectPlay || !d.AndroidMatroskaHLS || d.Container != ContainerMatroska || !d.VideoCopy || d.StripDolbyVision {
		t.Fatalf("fixture must be a Dolby Vision copy on the Android Matroska transport: %+v", d)
	}
	attrs := HLSVariantAttributes(info, d)
	if hasCodecs(attrs) {
		t.Fatalf("the app would read a CODECS value from this master: %s", attrs)
	}
	if !strings.Contains(attrs, ",VIDEO-RANGE=PQ") {
		t.Fatalf("VIDEO-RANGE lost: %s", attrs)
	}
}

// Question raised in review: the Plex ladder branch of writeMasterPlaylist
// prints each rung's own Codecs, outside HLSVariantAttributes. It must never
// run for Android Matroska, or the fix would be bypassed. It cannot: Matroska
// HLS sessions advertise exactly one rendition (Plex's native player probes
// every rendition at once and the rungs share one encoder), and the plex
// handler only takes the ladder branch for more than one rung. Remote workers
// run this same Manager, so the distributed path inherits the cap. This test
// is the tripwire for that argument: it uses the Android decision, a ladder
// that would otherwise produce several rungs, and fails if more than "base"
// is advertised.
func TestAndroidMatroskaHLSNeverAdvertisesLadder(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{ABRLadder: true})
	req := remuxRequest(t, "android-191-single-rendition", time.Hour)
	// An encoded primary is the case that normally gets a ladder.
	req.Decision = core.Decision{Kind: core.DecisionTranscode, Container: ContainerMatroska, AndroidMatroskaHLS: true,
		VideoCodec: "h264", BitrateKbps: 20000, AudioStream: 1, AudioCodec: "aac", SubtitleStream: -1}
	if len(buildRungs(req.Info, req.Decision, adaptiveSettings{Ladder: true})) < 2 {
		t.Fatal("fixture must otherwise produce multiple qualities")
	}
	s := startSession(t, m, req)
	if rungs := s.Rungs(); len(rungs) != 1 || rungs[0].ID != "base" {
		t.Fatalf("Android Matroska HLS must advertise only base so the master never uses per-rung CODECS: %+v", rungs)
	}
}

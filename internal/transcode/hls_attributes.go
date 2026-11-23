package transcode

import (
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
)

// HLSVariantAttributes returns the CODECS, VIDEO-RANGE and SUPPLEMENTAL-CODECS
// attributes (each with its leading comma) for the master playlist's
// #EXT-X-STREAM-INF line, describing the output the media decision selected.
// Apple HLS requires CODECS; HDR and Dolby Vision also need explicit signaling.
// https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html
//
// One transport is deliberately different: for the Plex for Android Matroska
// HLS session (d.AndroidMatroskaHLS with a Matroska container) CODECS and
// SUPPLEMENTAL-CODECS are left out on purpose. See the comment at the omission before changing that.
// HLSVariantGeometry returns the RESOLUTION and FRAME-RATE attributes (each
// with its leading comma) for the output video the decision selects, or
// nothing when the size is unknown. Apple's authoring spec requires both on
// every video variant (9.2, 9.15), and tvOS AVPlayer enforces it for HDR: a
// PQ or Dolby Vision variant without them is rejected at the master playlist,
// while SDR is tolerated.
func HLSVariantGeometry(info core.MediaInfo, d core.Decision) string {
	v := info.Video()
	if v == nil || v.Width <= 0 || v.Height <= 0 {
		return ""
	}
	w, h := v.Width, v.Height
	if !d.VideoCopy {
		w, h = decisionOutputSize(v, d)
	}
	attrs := fmt.Sprintf(",RESOLUTION=%dx%d", w, h)
	if v.FrameRate > 0 {
		attrs += fmt.Sprintf(",FRAME-RATE=%.3f", v.FrameRate)
	}
	return attrs
}

func HLSVariantAttributes(info core.MediaInfo, d core.Decision) string {
	var codecs []string
	videoRange, supplemental := "", ""
	if v := info.Video(); v != nil {
		codec, profile, depth, height := v.Codec, v.Profile, v.BitDepth, v.Height
		videoRange = "SDR"
		if !d.VideoCopy {
			codec, profile, depth = d.VideoCodec, "", 8
			if codec == "" {
				codec = "h264"
			}
			// The level follows the encoded size, not the source's.
			_, height = decisionOutputSize(v, d)
			// What planVideo encodes: HDR kept only in HEVC, from the
			// decoded picture (a Dolby Vision stream's base layer).
			if codec == "hevc" && keepsHDR(v) && !d.ToneMap {
				depth = 10
				videoRange = "PQ"
				if encodedRange(v) == rangeHLG {
					videoRange = "HLG"
				}
			}
		} else if !d.ToneMap {
			switch encodedRange(v) {
			case rangeHDR10:
				videoRange = "PQ"
			case rangeHLG:
				videoRange = "HLG"
			}
		}
		c := estimateCodecs(codec, profile, height, depth)
		if !d.VideoCopy && codec == "h264" && d.H264Level > 0 {
			c = fmt.Sprintf("avc1.6400%02x", d.H264Level)
		}
		if d.VideoCopy && !d.StripDolbyVision && v.CodecString != "" {
			c = v.CodecString
		}
		if d.VideoCopy && v.DolbyVision && !d.StripDolbyVision {
			if v.DolbyVisionProfile == 5 && v.DolbyVisionLevel > 0 {
				c = fmt.Sprintf("dvh1.05.%02d", v.DolbyVisionLevel)
				videoRange = "PQ"
			}
			if v.DolbyVisionProfile == 8 && v.DolbyVisionLevel > 0 {
				brand := ""
				switch v.DolbyVisionCompatibilityID {
				case 1:
					brand = "db1p"
					videoRange = "PQ"
				case 4:
					brand = "db4h"
					videoRange = "HLG"
				}
				if brand != "" {
					supplemental = fmt.Sprintf("dvh1.08.%02d/%s", v.DolbyVisionLevel, brand)
				}
			}
		}
		codecs = append(codecs, c)
	}
	if a := findStream(info, d.AudioStream, "audio"); a != nil {
		codec, profile := a.Codec, a.Profile
		if !d.AudioCopy {
			codec, profile = d.AudioCodec, ""
			if codec == "" {
				codec = "aac"
			}
		}
		codecs = append(codecs, estimateCodecs(codec, profile, 0, 0))
	}
	// Contract: the master for the Plex for Android Matroska transport carries
	// neither CODECS nor SUPPLEMENTAL-CODECS. VIDEO-RANGE stays. Every other
	// client keeps both CODECS (Apple's authoring spec asks for it) and
	// SUPPLEMENTAL-CODECS. Do not "fix" this to match the other masters and
	// do not move it into the generic path: nothing fails loudly when it breaks,
	// embedded subtitles just stop appearing.
	//
	// Why. Plex for Android plays HLS through media3 with chunkless preparation
	// on. When the master declares video and audio CODECS, media3 trusts the
	// playlist and builds the player's tracks from it alone (main, main:audio,
	// main:cc, main:id3). Embedded subtitles travel as a subrip stream muxed
	// into the Matroska segments, which the playlist does not describe, so
	// media3 would discard that stream. Without CODECS the client reads the
	// first segment to learn what it holds and finds the subrip track. This is
	// also what a real Plex Media Server sends this client: BANDWIDTH,
	// RESOLUTION and FRAME-RATE only.
	//
	// SUPPLEMENTAL-CODECS has to go as well because the app finds CODECS with an
	// unanchored pattern (CODECS="..."), so the value of SUPPLEMENTAL-CODECS is
	// read as a CODECS declaration and re-enables the chunkless path.
	//
	// Symptom if this regresses: the app's subtitle panel still lists and
	// selects the subtitle (it is built from the play-queue item, not from the
	// player) but no cue is drawn; the player log reports "Text: 0" in
	// "Initialising track selector" even though the extractor logs
	// "Track found: SubtitleStream[..-subrip]". Expected: "Text: 1".
	//
	// Subtitles stay muxed in the segments. Do not add a sidecar rendition
	// group for this transport.
	attrs := ""
	if !(d.AndroidMatroskaHLS && d.Container == ContainerMatroska) {
		attrs = `,CODECS="` + strings.Join(codecs, ",") + `"`
	} else {
		supplemental = ""
	}
	if videoRange != "" {
		attrs += `,VIDEO-RANGE=` + videoRange
	}
	if supplemental != "" {
		attrs += `,SUPPLEMENTAL-CODECS="` + supplemental + `"`
	}
	return attrs
}

package vela

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// pageLoudness looks up measured loudness for the page's audio titles in one
// batch; other titles never ask.
func (i *instance) pageLoudness(r *http.Request, ts []frontend.Title) map[frontend.RatingKey]frontend.Loudness {
	if i.host.Loudness == nil {
		return nil
	}
	var refs []frontend.LoudnessRef
	for _, t := range ts {
		switch t.MediaClass {
		case frontend.MediaClassMusic, frontend.MediaClassAudiobook, frontend.MediaClassPodcast:
			refs = append(refs, frontend.LoudnessRef{Key: t.Key, Parent: t.ParentKey})
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return i.host.Loudness.PageLoudness(r.Context(), refs)
}

// loudnessWire is media.loudness / PlaybackPlan.loudness.
func loudnessWire(l frontend.Loudness) object {
	out := object{
		"trackGainDb": l.TrackGainDB,
		"reference":   -18,
		"source":      l.Source,
	}
	if l.Source == frontend.LoudnessMeasured {
		out["trackPeak"] = l.TrackPeak
		out["lufs"] = l.LUFS
	}
	if l.AlbumGainDB != nil {
		out["albumGainDb"] = *l.AlbumGainDB
	}
	if l.AlbumPeak != nil {
		out["albumPeak"] = *l.AlbumPeak
	}
	if l.Trim != nil {
		out["trim"] = object{
			"startMs":        l.Trim.StartMs,
			"endMs":          l.Trim.EndMs,
			"fadeInEndMs":    l.Trim.FadeInEndMs,
			"fadeOutStartMs": l.Trim.FadeOutStartMs,
		}
	}
	return out
}

// losslessAudio names codecs that keep every sample.
var losslessAudio = map[string]bool{"flac": true, "alac": true, "wavpack": true, "ape": true, "tta": true, "truehd": true, "mlp": true}

// audioQualityWire describes the audio stream a plan plays (media.quality,
// PlaybackPlan.audioQuality): {codec, bitDepth?, sampleRateHz?, bitrateKbps?, lossless?}.
func audioQualityWire(m frontend.MediaInfo) object {
	for _, st := range m.Streams {
		if st.Type != "audio" || st.Codec == "" {
			continue
		}
		codec := st.Codec
		out := object{"codec": codec}
		lossless := losslessAudio[codec] || len(codec) > 4 && codec[:4] == "pcm_"
		if lossless {
			out["lossless"] = true
		}
		if st.BitsPerRawSample != nil && *st.BitsPerRawSample > 0 {
			out["bitDepth"] = *st.BitsPerRawSample
		} else if bits := sampleFormatBits(st.SampleFormat); bits > 0 && lossless {
			out["bitDepth"] = bits
		}
		if st.SampleRate > 0 {
			out["sampleRateHz"] = st.SampleRate
		}
		if st.BitrateKbps > 0 {
			out["bitrateKbps"] = st.BitrateKbps
		} else if m.BitrateKbps > 0 {
			out["bitrateKbps"] = m.BitrateKbps
		}
		return out
	}
	return nil
}

func sampleFormatBits(f string) int {
	switch f {
	case "s16", "s16p":
		return 16
	case "s32", "s32p":
		return 24 // ffmpeg decodes 24-bit lossless into 32-bit samples
	}
	return 0
}

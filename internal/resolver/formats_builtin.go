package resolver

import (
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Ether's built-in custom formats (core.BuiltinFormats) are matched here,
// from the parsed release and the session. They score on the profile like
// any other format (QualityProfile.CustomFormatScores).

// builtinFormatNames are the formats' names in traces and the explain
// output; the console shows its own translated names by id.
var builtinFormatNames = map[string]string{
	core.FormatDolbyVision:             "Dolby Vision",
	core.FormatHDR:                     "HDR",
	core.FormatHDR10:                   "HDR10",
	core.FormatAtmos:                   "Atmos",
	core.FormatRepackProper:            "Repack/Proper",
	core.FormatLowBitrate:              "Low bitrate",
	core.FormatPreferredLanguage:       "Preferred language",
	core.FormatPreferredLanguageRegion: "Preferred language (other region)",
	core.FormatOtherLanguage:           "Not a preferred language",
	core.FormatPreferredSubtitles:      "Preferred subtitles",
	core.FormatViewerLanguage:          "Viewer's language",
	core.FormatViewerSubtitles:         "Viewer's subtitles",
	core.FormatSeriesYearDiffers:       "Series year differs",
	core.FormatVideoTranscode:          "Video transcode",
	core.FormatAudioDirect:             "Audio plays directly",
	core.FormatAudioTranscode:          "Audio transcode",
	core.FormatAtmosPassthrough:        "Atmos passthrough",
	core.FormatToneMapping:             "Tone mapping",
	core.FormatPastRetention:           "Past retention",
	FormatSeaDexBest:                   "SeaDex Best",
	FormatSeaDexAlt:                    "SeaDex Alt",
}

func init() {
	for _, s := range core.FormatSources {
		builtinFormatNames[core.SourceFormat(s)] = core.SourceLabel(s)
	}
	codecs := map[string]string{"hevc": "HEVC", "av1": "AV1", "h264": "H.264", "vp9": "VP9", "vc1": "VC-1", "mpeg2": "MPEG-2", "xvid": "XviD"}
	for _, c := range core.FormatCodecs {
		builtinFormatNames[core.CodecFormat(c)] = codecs[c]
	}
	audio := map[string]string{"truehd": "TrueHD", "dtshd": "DTS-HD", "eac3": "E-AC-3", "dts": "DTS", "ac3": "AC-3", "aac": "AAC", "flac": "FLAC", "opus": "Opus", "mp3": "MP3"}
	for _, a := range core.FormatAudioCodecs {
		builtinFormatNames[core.AudioFormat(a)] = audio[a]
	}
}

// BuiltinFormatName is a built-in format's display name ("" if unknown).
func BuiltinFormatName(id string) string { return builtinFormatNames[id] }

// formatFacts is what the built-in formats read besides the release, the
// profile and the client.
type formatFacts struct {
	starved          bool                // below its bitrate floor or a presumed upscale: no HDR credit
	lowBitrate       bool                // below its bitrate floor
	viewerLanguage   viewerLanguageMatch // matches the viewer's ordered audio languages
	originalLanguage string              // title's original language, when known
	viewerSubs       bool                // the indexer lists subtitles in the viewer's language
	yearDiffers      bool                // accepted although its series year differs
	pastRetention    bool                // a Usenet post older than provider retention
}

// builtinFormatMatches lists the built-in formats a release matches for a
// profile (already clamped to the client) and client.
func builtinFormatMatches(rel core.Release, profile core.QualityProfile, caps core.ClientCaps, f formatFacts) []string {
	info := rel.Info
	var out []string
	add := func(id string) { out = append(out, id) }
	if slices.Contains(core.FormatSources, info.Source) {
		add(core.SourceFormat(info.Source))
	}
	if !f.starved {
		if info.DolbyVision && profile.AllowDolbyVision {
			add(core.FormatDolbyVision)
		}
		// A 2160p remux is a UHD disc, which carries HDR10 even when the
		// name does not say so.
		if profile.AllowHDR && (info.HDR10 || info.DolbyVision || (info.Height >= 2160 && info.Source == core.SourceRemux)) {
			add(core.FormatHDR)
		}
	}
	if info.HDR10 {
		if profile.AllowHDR {
			add(core.FormatHDR10)
		} else {
			add(core.FormatToneMapping)
		}
	}
	if slices.Contains(core.FormatCodecs, info.VideoCodec) {
		add(core.CodecFormat(info.VideoCodec))
	}
	if slices.Contains(core.FormatAudioCodecs, info.AudioCodec) {
		add(core.AudioFormat(info.AudioCodec))
	}
	if info.Atmos {
		add(core.FormatAtmos)
	}
	if info.Proper || info.Repack {
		add(core.FormatRepackProper)
	}
	if f.lowBitrate {
		add(core.FormatLowBitrate)
	}
	out = append(out, languageFormats(info, rel.SourceLanguage, profile.PreferredLanguages, f.originalLanguage)...)
	if f.viewerLanguage.Matched {
		add(core.FormatViewerLanguage)
	}
	if f.viewerSubs {
		add(core.FormatViewerSubtitles)
	}
	if f.yearDiffers {
		add(core.FormatSeriesYearDiffers)
	}
	if info.VideoCodec != "" && len(caps.VideoCodecs) > 0 && !caps.SupportsVideo(info.VideoCodec) {
		add(core.FormatVideoTranscode)
	}
	if info.AudioCodec != "" && len(caps.AudioCodecs) > 0 {
		if caps.SupportsAudio(info.AudioCodec) {
			add(core.FormatAudioDirect)
		} else {
			add(core.FormatAudioTranscode)
		}
	}
	if info.Atmos && caps.SupportsAudio(info.AudioCodec) {
		add(core.FormatAtmosPassthrough)
	}
	if f.pastRetention {
		add(core.FormatPastRetention)
	}
	return out
}

// languageFormats applies a profile's Languages, which are opt-in (an
// unset profile is language-neutral) and may be ISO codes ("fr", "es-419")
// or release language names. Audio decides: a release declaring only
// other audio languages is "Not a preferred language"; the preferred
// language or MULTI is not, and another regional variant of a preferred
// language (Castilian for a Latino preference) is the "other region" format.
// Bare DUAL ("original + one unknown") escapes "Not a preferred language"
// only when the title's original language is unknown or among prefs, or when
// the indexer declares a non-original preferred source language (sourceLang).
// Subtitle-only declarations (VOSTFR) never stand in for audio; they have a
// format of their own.
func languageFormats(info core.ReleaseInfo, sourceLang string, prefs []string, orig string) []string {
	if len(prefs) == 0 {
		return nil
	}
	sourceDub := dualSourceDubForOrig(sourceLang, orig)
	origOK := orig == ""
	if !origOK {
		for _, pref := range prefs {
			if releaseparse.LanguageMatch(orig, pref) != releaseparse.LanguageNoMatch {
				origOK = true
				break
			}
		}
	}
	var out []string
	if len(info.Languages) > 0 {
		ok, exact, base := false, false, false
		hasMulti, hasDual, hasConcreteNonOrig := false, false, false
		for _, l := range info.Languages {
			switch l {
			case releaseparse.LanguageMulti:
				hasMulti = true
			case releaseparse.LanguageDual:
				hasDual = true
			default:
				if orig == "" || releaseparse.LanguageMatch(l, orig) == releaseparse.LanguageNoMatch {
					hasConcreteNonOrig = true
				}
				best := releaseparse.LanguageNoMatch
				for _, pref := range prefs {
					best = max(best, releaseparse.LanguageMatch(l, pref))
				}
				switch best {
				case releaseparse.LanguageExactMatch:
					ok, exact = true, true
				case releaseparse.LanguageBaseMatch:
					ok, base = true, true
				}
			}
		}
		if hasMulti || hasDual {
			// A bare DUAL/MULTI carries the source language when the
			// indexer declares a non-original preferred language and the
			// release does not already name a non-original dub.
			best := releaseparse.LanguageNoMatch
			if !hasConcreteNonOrig && sourceDub != "" {
				for _, pref := range prefs {
					best = max(best, releaseparse.LanguageMatch(sourceDub, pref))
				}
			}
			switch best {
			case releaseparse.LanguageExactMatch:
				ok, exact = true, true
			case releaseparse.LanguageBaseMatch:
				ok, base = true, true
			default:
				if hasMulti || origOK {
					ok = true
				}
			}
		}
		switch {
		case exact:
			out = append(out, core.FormatPreferredLanguage)
		case base:
			out = append(out, core.FormatPreferredLanguageRegion)
		case !ok:
			out = append(out, core.FormatOtherLanguage)
		}
	}
	for _, l := range info.SubtitleLanguages {
		matched := false
		for _, pref := range prefs {
			matched = matched || releaseparse.LanguageMatch(l, pref) == releaseparse.LanguageExactMatch
		}
		if matched {
			out = append(out, core.FormatPreferredSubtitles)
			break
		}
	}
	return out
}

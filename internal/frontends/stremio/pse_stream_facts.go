package stremio

import (
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Ce qu'une release dit d'elle-même, pour le nom et la description d'un flux
// Stremio. Voir docs/pse/FORK.fr.md.

type pseFacts struct {
	res, source, hdr, flags, indexer, video, audio, group, file string
	size                                                        int64
	mbps                                                        float64
	service                                                     string
	cached                                                      bool
}

var pseDebridAbbrev = map[string]string{"alldebrid": "AD", "realdebrid": "RD", "torbox": "TB", "premiumize": "PM", "debridlink": "DL", "offcloud": "OC", "easydebrid": "ED"}

var pseSourceNames = map[string]string{"remux": "REMUX", "bluray": "BluRay", "webdl": "WEB-DL", "webrip": "WEBRip", "hdtv": "HDTV", "dvd": "DVD"}

var pseVideoNames = map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "mpeg2": "MPEG-2", "vc1": "VC-1"}

var pseAudioNames = map[string]string{"eac3": "EAC3", "ac3": "AC3", "truehd": "TrueHD", "dts": "DTS", "dtshd": "DTS-HD", "aac": "AAC", "flac": "FLAC", "opus": "Opus", "mp3": "MP3"}

var pseFlags = map[string]string{"french": "🇫🇷", "english": "🇬🇧", "spanish": "🇪🇸", "german": "🇩🇪", "italian": "🇮🇹", "japanese": "🇯🇵", "korean": "🇰🇷", "portuguese": "🇵🇹", "russian": "🇷🇺", "chinese": "🇨🇳", "dutch": "🇳🇱", "polish": "🇵🇱", "turkish": "🇹🇷", "arabic": "🇸🇦", "hindi": "🇮🇳"}

func pseFactsOf(v frontend.ReleaseVariant, runtime time.Duration) pseFacts {
	rel, info := v.Release, v.Release.Info
	f := pseFacts{size: rel.Size, indexer: rel.Indexer, group: strings.TrimSpace(info.Group), file: rel.FileName}
	if f.file == "" {
		f.file = rel.Name
	}
	if b := pseHeightBucket(info.Height); b > 0 {
		f.res = fmt.Sprintf("%dp", b)
	} else if info.Height > 0 {
		f.res = fmt.Sprintf("%dp", info.Height)
	}
	f.source = pseSourceNames[string(info.Source)]
	switch {
	case info.DolbyVision && info.HDR10:
		f.hdr = "DV HDR10"
	case info.DolbyVision:
		f.hdr = "DV"
	case info.HDR10:
		f.hdr = "HDR10"
	}
	if runtime > 0 && rel.Size > 0 {
		f.mbps = float64(rel.Size) * 8 / 1e6 / runtime.Seconds()
	}
	// Langues : drapeaux, 🌍 pour un multi.
	seen := map[string]bool{}
	multi := strings.Contains(strings.ToUpper(rel.Name), "MULTI")
	for _, l := range info.Languages {
		l = strings.ToLower(l)
		if l == "multi" {
			multi = true
		} else if flag := pseFlags[l]; flag != "" && !seen[flag] {
			seen[flag] = true
			f.flags += flag
		}
	}
	if multi && len(seen) < 2 {
		f.flags = "🌍"
	}
	f.video = pseVideoNames[strings.ToLower(info.VideoCodec)]
	if f.video == "" {
		f.video = strings.ToUpper(info.VideoCodec)
	}
	audio := pseAudioNames[strings.ToLower(info.AudioCodec)]
	if audio == "" {
		audio = strings.ToUpper(info.AudioCodec)
	}
	if info.Atmos {
		audio += " Atmos"
	}
	f.audio = strings.TrimSpace(audio + " " + info.Channels)
	// Débrideur : abréviation du service, ⚡ quand la source est en cache.
	switch {
	case len(rel.DebridCached) > 0:
		f.service, f.cached = rel.DebridCached[0], true
	case rel.StreamDebrid != "":
		f.service, f.cached = rel.StreamDebrid, true
	}
	if f.service != "" {
		key := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(f.service))
		if ab := pseDebridAbbrev[key]; ab != "" {
			f.service = ab
		} else {
			f.service = strings.ToUpper(f.service)
			if len(f.service) > 3 {
				f.service = f.service[:2]
			}
		}
	}
	return f
}

// serviceName : « [AD⚡] Ether », « [AD] Ether » ou « Ether ».
func (f pseFacts) serviceName() string {
	if f.service == "" {
		return "Ether"
	}
	bolt := ""
	if f.cached {
		bolt = "⚡"
	}
	return "[" + f.service + bolt + "] Ether"
}

func pseJoin(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func pseMbps(v float64) string {
	if v <= 0 {
		return ""
	}
	return "📶 " + strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1) + " Mb/s"
}

func pseWith(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

// compact tient sur une ligne.
func (f pseFacts) compact(auto bool) string {
	head := ""
	if auto {
		head = "Auto"
	}
	return pseJoin(" · ", head, f.res, f.source, f.hdr, f.audio, pseSizeFR(f.size), f.group)
}

// detailed : quatre lignes (cinq pour Auto, préfixé d'une ligne « Auto »).
func (f pseFacts) detailed(auto bool) string {
	const sep = " • "
	var lines []string
	if auto {
		lines = append(lines, "Auto")
	}
	add := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}
	add(pseJoin(sep, f.res, f.source, f.hdr, pseMbps(f.mbps)))
	add(pseJoin(sep, f.flags, pseWith("💾 ", pseSizeFR(f.size)), pseWith("🔎 ", f.indexer)))
	add(pseJoin(sep, pseWith("🎧 ", f.video), f.audio, f.group))
	add(pseWith("📁 ", f.file))
	return strings.Join(lines, "\n")
}

package releasedb

import (
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"strconv"
	"strings"
)

// Filter restreint les releases par langue audio, résolution et codec.
// Un champ vide n'impose rien.
type Filter struct {
	// Languages : au moins une langue audio parmi celles-ci. Valeurs :
	// "french" (ou "fr", "vf"), "vfq" (ou "qc"), "vff", "vfi", "multi",
	// "english" (ou "en")… Deux valeurs spéciales : "vostfr" (ou
	// "subfrench" : sous-titres français annoncés) et "vo" (aucun doublage annoncé, ou anglais).
	// "sub:<code>" accepts announced subtitles as an alternative to audio.
	Languages []string
	// MinHeight / MaxHeight : bornes de résolution (720, 1080, 2160…).
	MinHeight, MaxHeight int
	// VideoCodecs : codecs vidéo acceptés ("h264", "hevc", "av1").
	VideoCodecs []string
	// CachedOnly : uniquement les releases que la source signale en cache
	// chez un service debrid (marqueur ⚡ / + des addons Stremio).
	CachedOnly bool
}

// Match indique si r respecte le filtre.
func (f Filter) Match(r Release) bool {
	if len(f.Languages) > 0 && !wantedLanguage(r, f.Languages) {
		return false
	}
	if f.MinHeight > 0 && r.Height < f.MinHeight {
		return false
	}
	if f.MaxHeight > 0 && (r.Height == 0 || r.Height > f.MaxHeight) {
		return false
	}
	if len(f.VideoCodecs) > 0 && !contains(f.VideoCodecs, strings.ToLower(r.VideoCodec)) {
		return false
	}
	if f.CachedOnly && len(r.DebridCached) == 0 {
		return false
	}
	return true
}

// wantedLanguage gère les valeurs spéciales "vostfr" et "vo", puis les
// langues audio.
func wantedLanguage(r Release, want []string) bool {
	var audio []string
	for _, w := range want {
		if code, ok := strings.CutPrefix(strings.ToLower(w), "sub:"); ok {
			if anyLanguage(r.SubtitleLanguages, []string{code}) {
				return true
			}
			continue
		}
		switch canonicalLanguage(w) {
		case "vostfr":
			if contains(r.SubtitleLanguages, "french") {
				return true
			}
		case "vo":
			if isOriginal(r.Languages) {
				return true
			}
		default:
			audio = append(audio, w)
		}
	}
	return anyLanguage(r.Languages, audio)
}

// isOriginal : aucune langue audio annoncée, ou anglais seul. Un doublage
// (allemand, italien…) ou une MULTi ne comptent pas comme VO.
func isOriginal(langs []string) bool {
	for _, l := range langs {
		if l != "english" {
			return false
		}
	}
	return true
}

func anyLanguage(have, want []string) bool {
	for _, w := range want {
		w = canonicalLanguage(w)
		for _, h := range have {
			if h == w || releaseparse.LanguageMatch(h, w) == releaseparse.LanguageExactMatch {
				return true
			}
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if strings.ToLower(s) == v {
			return true
		}
	}
	return false
}

// ParseFilter lit un filtre au format « clé=valeur;… », par exemple
// "lang=fr;min=1080;codec=hevc,h264;cached=1". Les clés inconnues sont
// ignorées.
func ParseFilter(s string) Filter {
	var f Filter
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		list := func() []string {
			var out []string
			for _, x := range strings.Split(v, ",") {
				if x = strings.ToLower(strings.TrimSpace(x)); x != "" {
					out = append(out, x)
				}
			}
			return out
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "lang", "langs", "languages":
			f.Languages = list()
		case "min", "minheight":
			f.MinHeight, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "p"))
		case "max", "maxheight":
			f.MaxHeight, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "p"))
		case "codec", "codecs":
			f.VideoCodecs = list()
		case "cached":
			f.CachedOnly = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "oui")
		}
	}
	return f
}

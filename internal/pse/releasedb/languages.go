package releasedb

import (
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"regexp"
	"strings"
)

// Les langues sont stockées avec les noms du parseur d'Ether ("french",
// "english", "multi"…). PSE ajoute deux marqueurs pour distinguer les
// doublages français : "vfq" (version québécoise : VFQ, QC, QUEBEC) et
// "vff" (version française de France : VFF, TRUEFRENCH). VF2 donne les deux.
// VF, VFI (version française internationale) et VOF (film tourné en
// français), que le parseur d'Ether ignore, comptent comme français ; VFI
// ajoute aussi le marqueur "vfi".

var (
	vfqTags = regexp.MustCompile(`(?i)(^|[^a-z0-9])(vfq|vf2|qc|quebec|québec|qu[eé]b[eé]cois)([^a-z0-9]|$)`)
	vffTags = regexp.MustCompile(`(?i)(^|[^a-z0-9])(vff|vf2|truefrench)([^a-z0-9]|$)`)
	vfTags  = regexp.MustCompile(`(?i)(^|[^a-z0-9])(vf|vof)([^a-z0-9]|$)`)
	vfiTags = regexp.MustCompile(`(?i)(^|[^a-z0-9])vfi([^a-z0-9]|$)`)
	multi   = regexp.MustCompile(`(?i)(^|[^a-z0-9])multi([^a-z0-9]|$)`)
)

// releaseLanguages complète les langues d'Ether avec les marqueurs PSE.
func releaseLanguages(name string, parsed []string) []string {
	out := lower(parsed)
	add := func(tag string) {
		for _, l := range out {
			if l == tag {
				return
			}
		}
		out = append(out, tag)
	}
	if vfqTags.MatchString(name) {
		add("french")
		add("vfq")
	}
	if vffTags.MatchString(name) {
		add("french")
		add("vff")
	}
	if vfTags.MatchString(name) {
		add("french")
	}
	if vfiTags.MatchString(name) {
		add("french")
		add("vfi")
	}
	if multi.MatchString(name) {
		add("multi")
	}
	return out
}

// languageAliases traduit les valeurs de filtre courantes vers les noms
// stockés.
var languageAliases = map[string]string{
	"fr": "french", "fra": "french", "fre": "french", "francais": "french", "français": "french", "vf": "french",
	"qc": "vfq", "fr-ca": "vfq", "quebec": "vfq", "québec": "vfq",
	"fr-fr": "vff", "truefrench": "vff",
	"stfr": "vostfr", "subfrench": "vostfr", "vost": "vostfr", "version originale": "vo", "original": "vo",
	"en": "english", "eng": "english", "anglais": "english",
	"de": "german", "es": "spanish", "it": "italian", "pt": "portuguese", "ja": "japanese", "ko": "korean",
}

func canonicalLanguage(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if v, ok := languageAliases[s]; ok {
		return v
	}
	return releaseparse.LanguageName(s)
}

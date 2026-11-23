package experience

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

type continueTitleSet struct {
	watching  string
	reading   string
	listening string
}

// continueTitles covers all 20 Ether UI locales.
var continueTitles = map[string]continueTitleSet{
	"en":    {watching: "Continue Watching", reading: "Continue Reading", listening: "Continue Listening"},
	"fr":    {watching: "Continuer à regarder", reading: "Continuer à lire", listening: "Continuer à écouter"},
	"es":    {watching: "Seguir viendo", reading: "Seguir leyendo", listening: "Seguir escuchando"},
	"de":    {watching: "Weiterschauen", reading: "Weiterlesen", listening: "Weiterhören"},
	"cs":    {watching: "Pokračovat ve sledování", reading: "Pokračovat ve čtení", listening: "Pokračovat v poslechu"},
	"da":    {watching: "Fortsæt med at se", reading: "Fortsæt med at læse", listening: "Fortsæt med at lytte"},
	"fi":    {watching: "Jatka katsomista", reading: "Jatka lukemista", listening: "Jatka kuuntelua"},
	"it":    {watching: "Continua a guardare", reading: "Continua a leggere", listening: "Continua ad ascoltare"},
	"ja":    {watching: "視聴を再開", reading: "読書を再開", listening: "再生を再開"},
	"ko":    {watching: "계속 시청", reading: "계속 읽기", listening: "계속 듣기"},
	"nb":    {watching: "Fortsett å se", reading: "Fortsett å lese", listening: "Fortsett å lytte"},
	"nl":    {watching: "Verder kijken", reading: "Verder lezen", listening: "Verder luisteren"},
	"pl":    {watching: "Oglądaj dalej", reading: "Czytaj dalej", listening: "Słuchaj dalej"},
	"pt-BR": {watching: "Continuar assistindo", reading: "Continuar lendo", listening: "Continuar ouvindo"},
	"ru":    {watching: "Продолжить просмотр", reading: "Продолжить чтение", listening: "Продолжить прослушивание"},
	"sv":    {watching: "Fortsätt titta", reading: "Fortsätt läsa", listening: "Fortsätt lyssna"},
	"tr":    {watching: "İzlemeye devam et", reading: "Okumaya devam et", listening: "Dinlemeye devam et"},
	"uk":    {watching: "Продовжити перегляд", reading: "Продовжити читання", listening: "Продовжити прослуховування"},
	"zh-CN": {watching: "继续观看", reading: "继续阅读", listening: "继续收听"},
	"zh-TW": {watching: "繼續觀看", reading: "繼續閱讀", listening: "繼續收聽"},
}

// pluginContinueTitle returns the localized Continue shelf title for a plugin
// kind's media class ("Continue Watching" for video/photo/default, "Continue
// Reading" for book, "Continue Listening" for audiobook/podcast).
func pluginContinueTitle(media core.MediaClass, lang string) string {
	set := continueTitles[matchContinueLocale(lang)]
	switch media {
	case core.MediaClassBook:
		return set.reading
	case core.MediaClassAudiobook, core.MediaClassPodcast:
		return set.listening
	default:
		return set.watching
	}
}

func matchContinueLocale(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return "en"
	}
	bestLocale := ""
	bestQ := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if semi := strings.IndexByte(part, ';'); semi >= 0 {
			tag = strings.TrimSpace(part[:semi])
			for _, param := range strings.Split(part[semi+1:], ";") {
				param = strings.TrimSpace(param)
				if strings.HasPrefix(strings.ToLower(param), "q=") {
					if parsed, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64); err == nil {
						q = parsed
					}
				}
			}
		}
		if q <= 0 || q <= bestQ {
			continue
		}
		if loc := canonicalContinueLocale(tag); loc != "" {
			bestLocale = loc
			bestQ = q
		}
	}
	if bestLocale == "" {
		return "en"
	}
	return bestLocale
}

func canonicalContinueLocale(tag string) string {
	tag = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	if tag == "" || tag == "*" {
		return ""
	}
	parts := strings.Split(tag, "-")
	base := parts[0]
	switch base {
	case "zh":
		for _, sub := range parts[1:] {
			if sub == "tw" || sub == "hk" || sub == "mo" || sub == "hant" {
				return "zh-TW"
			}
		}
		return "zh-CN"
	case "pt":
		return "pt-BR"
	case "no", "nb", "nn":
		return "nb"
	}
	if _, ok := continueTitles[base]; ok {
		return base
	}
	return ""
}

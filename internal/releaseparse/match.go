package releaseparse

import (
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

// diacritics maps common accented latin runes to their base letters.
var diacritics = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'æ': 'a', 'ā': 'a',
	'ç': 'c', 'ć': 'c', 'č': 'c',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ē': 'e', 'ę': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ī': 'i', 'ı': 'i',
	'ñ': 'n', 'ń': 'n',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o', 'ō': 'o', 'œ': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ū': 'u',
	'ý': 'y', 'ÿ': 'y',
	'ß': 's', 'ś': 's', 'š': 's', 'ş': 's',
	'ž': 'z', 'ź': 'z', 'ż': 'z',
	'ł': 'l', 'ð': 'd', 'þ': 't', 'ğ': 'g', 'ř': 'r', 'ť': 't', 'ď': 'd',
}

// normalizeTitleFields lowercases, collapses dotted initialisms, strips
// diacritics and punctuation, expands "&" to "and", and splits into tokens
// while preserving any leading article.
func normalizeTitleFields(s string) []string {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, ".*") {
		s = collapseDottedInitialisms(s)
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, "’", "")
	var b strings.Builder
	for _, r := range s {
		if base, ok := diacritics[r]; ok {
			b.WriteRune(base)
			continue
		}
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Fields(b.String())
}

func stripLeadingArticle(fields []string) []string {
	if len(fields) > 1 {
		switch fields[0] {
		case "the", "a", "an", "le", "la", "les", "der", "die", "das", "el", "il":
			return fields[1:]
		}
	}
	return fields
}

// NormalizeTitle lowercases, strips diacritics and punctuation, expands
// "&" to "and", and drops a leading article, for title comparisons.
func NormalizeTitle(s string) string {
	return strings.Join(stripLeadingArticle(normalizeTitleFields(s)), " ")
}

// collapseDottedInitialisms joins single-letter sequences separated by dots
// or asterisks ("L.A." -> "LA", "M*A*S*H" -> "MASH", "A.I." -> "AI") so
// scene release names ("LA.Confidential", "MASH") match punctuated titles.
func collapseDottedInitialisms(s string) string {
	runes := []rune(s)
	var out []rune
	for i := 0; i < len(runes); {
		prevBoundary := i == 0 || !unicode.IsLetter(runes[i-1]) && !unicode.IsDigit(runes[i-1])
		if prevBoundary && unicode.IsLetter(runes[i]) && i+2 < len(runes) && (runes[i+1] == '.' || runes[i+1] == '*') {
			sep := runes[i+1]
			j := i
			var letters []rune
			for j < len(runes) && unicode.IsLetter(runes[j]) && (j+1 == len(runes) || !unicode.IsLetter(runes[j+1]) && !unicode.IsDigit(runes[j+1])) {
				letters = append(letters, runes[j])
				if j+1 < len(runes) && runes[j+1] == sep {
					j += 2
					continue
				}
				j++
				break
			}
			if len(letters) >= 2 {
				out = append(out, letters...)
				if j < len(runes) && unicode.IsLetter(runes[j]) {
					out = append(out, ' ')
				}
				i = j
				continue
			}
		}
		out = append(out, runes[i])
		i++
	}
	return string(out)
}

var tokenCanon = map[string]string{
	"ii": "2", "iii": "3", "iv": "4", "vi": "6", "vii": "7", "viii": "8", "ix": "9",
	"one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
	"six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10",
	"eleven": "11", "twelve": "12", "thirteen": "13", "fourteen": "14", "fifteen": "15",
	"sixteen": "16", "seventeen": "17", "eighteen": "18", "nineteen": "19", "twenty": "20",
	"thirty": "30", "forty": "40", "fifty": "50", "sixty": "60", "seventy": "70", "eighty": "80", "ninety": "90",
	"versus": "vs", "part": "pt", "partie": "pt", "volume": "vol",
	"mister": "mr", "doctor": "dr", "saint": "st", "brothers": "bros",
	"international": "intl",
}

var tensTokens = map[string]int{
	"20": 20, "30": 30, "40": 40, "50": 50, "60": 60, "70": 70, "80": 80, "90": 90,
}

var unitsTokens = map[string]int{
	"1": 1, "2": 2, "3": 3, "4": 4, "5": 5, "6": 6, "7": 7, "8": 8, "9": 9,
}

var regionalSuffixes = map[string]string{
	"us": "us", "usa": "us",
	"uk": "uk", "gb": "uk",
	"au": "au", "aus": "au",
	"ca": "ca", "can": "ca",
	"nz": "nz",
}

// canonicalTokens normalizes initialisms, numerals and common abbreviations
// across an already-normalized title string.
func canonicalTokens(norm string) []string {
	raw := strings.Fields(norm)
	// French elisions can be written with a scene separator instead of an
	// apostrophe: "l age" and "lage", "d amour" and "damour".
	for i := 0; i+1 < len(raw); i++ {
		if (raw[i] == "l" || raw[i] == "d") && len(raw[i+1]) > 1 && strings.ContainsRune("aeiouyh", rune(raw[i+1][0])) {
			raw[i] += raw[i+1]
			raw = append(raw[:i+1], raw[i+2:]...)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	// Collapse runs of 2+ single-letter ASCII tokens ("l a" -> "la").
	var collapsed []string
	for i := 0; i < len(raw); {
		if len(raw[i]) == 1 && raw[i][0] >= 'a' && raw[i][0] <= 'z' {
			j := i + 1
			for j < len(raw) && len(raw[j]) == 1 && raw[j][0] >= 'a' && raw[j][0] <= 'z' {
				j++
			}
			if j-i >= 2 {
				joined := strings.Join(raw[i:j], "")
				// Strip leading "la" if it was the first token of a 2+ word title
				// so "L.A. Confidential" and "LA Confidential" agree.
				collapsed = append(collapsed, joined)
				i = j
				continue
			}
		}
		collapsed = append(collapsed, raw[i])
		i++
	}
	if len(collapsed) > 1 {
		switch collapsed[0] {
		case "the", "a", "an", "le", "la", "les", "der", "die", "das", "el", "il":
			collapsed = collapsed[1:]
		}
	}
	out := make([]string, 0, len(collapsed))
	for i, tok := range collapsed {
		if i > 0 && (tok == "v" || tok == "x") {
			if tok == "v" {
				tok = "5"
			} else {
				tok = "10"
			}
		} else if c, ok := tokenCanon[tok]; ok && (i > 0 || len(collapsed) == 1 || tensTokens[c] > 0) {
			tok = c
		}
		if n := len(out); n > 0 {
			if tens, ok := tensTokens[out[n-1]]; ok {
				if unit, uok := unitsTokens[tok]; uok {
					out[n-1] = string(rune('0'+tens/10)) + string(rune('0'+unit))
					continue
				}
			}
		}
		if tok == "i" && len(out) > 0 && out[len(out)-1] == "pt" {
			tok = "1"
		}
		out = append(out, tok)
	}
	return out
}

// TrailingRegionTag returns the canonical region code ("us", "uk", "au",
// "ca", "nz") when a release or show title ends with an explicit country
// disambiguator ("The.Office.US", "Ghosts (UK)"), or "".
func TrailingRegionTag(title string) string {
	fields := strings.Fields(NormalizeTitle(title))
	if len(fields) < 2 {
		return ""
	}
	return regionalSuffixes[fields[len(fields)-1]]
}

// subtitlePrefix extracts the main title before an explicit alternate-title
// clause ("Dr. Strangelove or: How I Learned...", "Birdman or (The Unexpected...)").
// Ordinary colons/dashes ("The Signal: Reloaded") name sequels and must not be stripped.
func subtitlePrefix(raw string) string {
	s := strings.TrimSpace(raw)
	best := -1
	lower := strings.ToLower(s)
	for _, sep := range []string{" or (", " or:"} {
		if idx := strings.Index(lower, sep); idx > 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	if best <= 0 {
		return ""
	}
	prefix := strings.TrimSpace(s[:best])
	norm := NormalizeTitle(prefix)
	words := strings.Fields(norm)
	if len(words) == 0 {
		return ""
	}
	if len(words) == 1 && len(words[0]) < 5 {
		return ""
	}
	return prefix
}

// possessiveCreditStem returns the rest of a title that opens with a named
// person's possessive credit, which release names usually leave out:
// "Seol Min-seok's Naked World History" -> "Naked World History",
// "Tom Clancy's Jack Ryan" -> "Jack Ryan", "설민석의 벌거벗은 세계사" ->
// "벌거벗은 세계사". It returns "" for titles whose possessive is part of the
// name itself ("Grey's Anatomy", "Hell's Kitchen", "The Handmaid's Tale",
// "사랑의 불시착"): the possessor must be a two- or three-word capitalised
// name (or a Hangul name ending in 의 that is not a pronoun) and the rest must
// still be distinctive on its own.
func possessiveCreditStem(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, sep := range []string{"'s ", "’s ", "'S ", "’S "} {
		idx := strings.Index(s, sep)
		if idx <= 0 {
			continue
		}
		owner := strings.Fields(s[:idx])
		rest := strings.TrimSpace(s[idx+len(sep):])
		if len(owner) < 2 || len(owner) > 3 {
			return ""
		}
		switch strings.ToLower(owner[0]) {
		case "the", "a", "an":
			return ""
		}
		for _, w := range owner {
			r := []rune(w)
			if !unicode.IsUpper(r[0]) {
				return ""
			}
		}
		words := strings.Fields(NormalizeTitle(rest))
		letters := 0
		for _, w := range words {
			letters += len([]rune(w))
		}
		if len(words) < 2 || letters < 8 {
			return ""
		}
		return rest
	}
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return ""
	}
	owner := []rune(fields[0])
	if len(owner) < 3 || len(owner) > 5 || owner[len(owner)-1] != '의' {
		return ""
	}
	for _, r := range owner {
		if !unicode.In(r, unicode.Hangul) {
			return ""
		}
	}
	switch fields[0] {
	case "당신의", "우리의", "우리들의", "그녀의", "그들의", "너희의", "자네의":
		return ""
	}
	rest := strings.Join(fields[1:], " ")
	syllables := 0
	for _, r := range rest {
		if unicode.In(r, unicode.Hangul) {
			syllables++
		}
	}
	if syllables < 4 {
		return ""
	}
	return rest
}

// splitAKA splits a title on whole-word "AKA" or "A.K.A." into its
// alternative names, or returns nil when no AKA separator is present.
func splitAKA(s string) []string {
	norm := NormalizeTitle(s)
	fields := strings.Fields(norm)
	var parts []string
	start := 0
	for i := 0; i < len(fields); i++ {
		if fields[i] == "aka" {
			if i > start {
				parts = append(parts, strings.Join(fields[start:i], " "))
			}
			start = i + 1
		} else if i+2 < len(fields) && fields[i] == "a" && fields[i+1] == "k" && fields[i+2] == "a" {
			if i > start {
				parts = append(parts, strings.Join(fields[start:i], " "))
			}
			start = i + 3
			i += 2
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if start < len(fields) {
		parts = append(parts, strings.Join(fields[start:], " "))
	}
	return parts
}

// TitlesMatch reports whether two raw titles are the same after normalisation.
func TitlesMatch(a, b string) bool {
	na, nb := NormalizeTitle(a), NormalizeTitle(b)
	if na != "" && na == nb {
		return true
	}
	if na == "" || nb == "" {
		return false
	}
	ca := strings.Join(canonicalTokens(na), " ")
	cb := strings.Join(canonicalTokens(nb), " ")
	if ca != "" && ca == cb {
		return true
	}
	// Titles that differ only in how words are joined are the same title:
	// K-Pop/KPop, Spider-Man/Spiderman, X-Men/XMen, WALL-E/WALLE, and an
	// apostrophe written as a dot (JoJo.s/JoJo's). Different words, extra
	// words and sequel numbers still differ once spaces are removed.
	if ja, jb := strings.ReplaceAll(na, " ", ""), strings.ReplaceAll(nb, " ", ""); len(ja) >= 3 && ja == jb {
		return true
	}
	// Compare exact romanisations without relaxing sequel or word identity.
	if strings.ContainsAny(a+b, "АБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯабвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
		for _, foldYo := range []bool{false, true} {
			ra, rb := transliterateRussian(a, foldYo), transliterateRussian(b, foldYo)
			if TitlesMatch(ra, rb) {
				return true
			}
		}
	}
	if containsHangul(a) || containsHangul(b) {
		ra, rb := transliterateHangul(a), transliterateHangul(b)
		if !containsHangul(ra) && !containsHangul(rb) && TitlesMatch(ra, rb) {
			return true
		}
	}
	// Tracker dual titles pair a non-Latin original (Cyrillic/Hangul/CJK) with
	// a Latin title. Keep slashes in ordinary titles ("Face / Off") meaningful.
	isNonLatinDual := func(r rune) bool {
		return unicode.In(r, unicode.Cyrillic, unicode.Hangul, unicode.Han, unicode.Hiragana, unicode.Katakana)
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if !strings.ContainsFunc(pair[0], isNonLatinDual) ||
			!strings.ContainsFunc(pair[0], func(r rune) bool { return unicode.In(r, unicode.Latin) }) {
			continue
		}
		if parts := strings.Split(pair[0], " / "); len(parts) > 1 {
			for _, part := range parts {
				if TitlesMatch(part, pair[1]) {
					return true
				}
			}
		}
	}
	// AKA dual-title releases ("Le.Samourai.AKA.The.Godson.1967").
	if parts := splitAKA(a); len(parts) > 0 {
		for _, p := range parts {
			if TitlesMatch(p, b) {
				return true
			}
		}
	}
	if parts := splitAKA(b); len(parts) > 0 {
		for _, p := range parts {
			if TitlesMatch(a, p) {
				return true
			}
		}
	}
	// Regional suffix ("The Office US" vs "The Office", "Ghosts (UK)" vs "Ghosts").
	ta, tb := canonicalTokens(na), canonicalTokens(nb)
	if len(ta) == len(tb)+1 && regionalSuffixes[ta[len(ta)-1]] != "" && strings.Join(ta[:len(ta)-1], " ") == cb {
		return true
	}
	if len(tb) == len(ta)+1 && regionalSuffixes[tb[len(tb)-1]] != "" && strings.Join(tb[:len(tb)-1], " ") == ca {
		return true
	}
	// Explicit subtitle prefix ("Master and Commander: The Far Side of the World"
	// vs scene release "Master.and.Commander.2003").
	if pa := subtitlePrefix(a); pa != "" && subtitlePrefix(b) == "" {
		if TitlesMatch(pa, b) {
			return true
		}
	}
	if pb := subtitlePrefix(b); pb != "" && subtitlePrefix(a) == "" {
		if TitlesMatch(a, pb) {
			return true
		}
	}
	// A presenter's or creator's possessive credit that the release name
	// leaves out ("Seol Min-seok's Naked World History" vs
	// "Naked.World.History.E150", "설민석의 벌거벗은 세계사" vs "벌거벗은 세계사").
	if sa := possessiveCreditStem(a); sa != "" && TitlesMatch(sa, b) {
		return true
	}
	if sb := possessiveCreditStem(b); sb != "" && TitlesMatch(a, sb) {
		return true
	}
	return false
}

// TitleAliases returns alternative search query titles for titles with
// initialisms, explicit subtitles, parenthetical disambiguators or Roman
// numerals. Ordinary titles return nil so standard searches spend no extra
// budget hits.
func TitleAliases(title string) []string {
	raw := strings.TrimSpace(title)
	if raw == "" {
		return nil
	}
	baseNorm := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	seen := map[string]bool{baseNorm: true}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
		if s == "" {
			return
		}
		k := strings.ToLower(s)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, s)
	}
	if strings.ContainsAny(raw, ".*") {
		collapsed := collapseDottedInitialisms(raw)
		if collapsed != raw {
			add(collapsed)
		}
	}
	if strings.Contains(raw, "/") {
		add(strings.ReplaceAll(raw, "/", " "))
	}
	// Strip trailing parenthetical disambiguator, e.g. "The Office (US)" -> "The Office".
	if i := strings.LastIndex(raw, "("); i > 0 && strings.HasSuffix(raw, ")") {
		inner := strings.TrimSpace(raw[i+1 : len(raw)-1])
		stem := strings.TrimSpace(raw[:i])
		if stem != "" {
			add(stem)
			if regionalSuffixes[strings.ToLower(inner)] != "" {
				add(stem + " " + inner)
			}
		}
	}
	if p := subtitlePrefix(raw); p != "" {
		add(p)
	}
	if p := possessiveCreditStem(raw); p != "" {
		add(p)
	}
	if parts := splitAKA(raw); len(parts) > 0 {
		for _, p := range parts {
			add(p)
		}
	}
	return out
}

// SubtitleAliases returns standalone halves of a colon-delimited title
// (e.g. "Demon Slayer: Kimetsu no Yaiba" -> "Demon Slayer", "Kimetsu no Yaiba",
// or "The Lord of the Rings: The Rings of Power" -> "The Rings of Power")
// only when alternatives independently confirms that half as a known alias
// for the same title. Unconfirmed prefixes such as sequel names
// ("The Signal: Reloaded") are never returned.
func SubtitleAliases(title string, alternatives []string) []string {
	if len(alternatives) == 0 {
		return nil
	}
	left, right, ok := strings.Cut(strings.TrimSpace(title), ":")
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, half := range []string{strings.TrimSpace(left), strings.TrimSpace(right)} {
		norm := NormalizeTitle(half)
		if len([]rune(norm)) < 4 || seen[norm] || TitlesMatch(half, title) {
			continue
		}
		for _, alt := range alternatives {
			if TitlesMatch(half, alt) {
				seen[norm] = true
				out = append(out, half)
				break
			}
		}
	}
	return out
}

// TitleMatches reports whether a parsed release title matches the wanted title.
func TitleMatches(parsed core.ReleaseInfo, want string) bool {
	return TitlesMatch(parsed.Title, want)
}

// TitleMatchesAny reports whether parsed matches want or any of the supplied
// alternative titles.
func TitleMatchesAny(parsed core.ReleaseInfo, want string, alts []string) bool {
	if TitlesMatch(parsed.Title, want) {
		return true
	}
	for _, alt := range alts {
		if TitlesMatch(parsed.Title, alt) {
			return true
		}
	}
	return false
}

// YearMatches reports whether two years agree within ±1 (zero = unknown).
func YearMatches(got, want int) bool {
	if got == 0 || want == 0 {
		return true
	}
	d := got - want
	return d >= -1 && d <= 1
}

// EpisodeMatches reports whether parsed info is exactly the wanted episode
// (not a season pack, season matches, episode is listed).
func EpisodeMatches(parsed core.ReleaseInfo, season, episode int) bool {
	if parsed.FullSeason || len(parsed.Episodes) == 0 {
		return false
	}
	seasonOK := len(parsed.Seasons) == 0 && season != 0
	for _, s := range parsed.Seasons {
		if s == season {
			seasonOK = true
		}
	}
	if !seasonOK {
		return false
	}
	for _, e := range parsed.Episodes {
		if e == episode {
			return true
		}
	}
	return false
}

// IsObfuscated reports whether a name/title looks like a hash or random
// string with no readable words.
func IsObfuscated(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return true
	}
	fields := strings.Fields(t)
	if len(fields) != 1 {
		return false
	}
	w := fields[0]
	// Obfuscated Usenet/scene hashes are pure ASCII; non-Latin single-word
	// titles (Korean, Japanese, Chinese, Cyrillic, etc.) have no ASCII vowels
	// and occupy 3+ UTF-8 bytes per character.
	hasDigit, hasLetter := false, false
	runeCount := 0
	for _, r := range w {
		if r >= 128 {
			return false
		}
		runeCount++
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	if runeCount >= 12 && hasDigit && hasLetter {
		return true
	}
	// No vowel in a longish single ASCII token is a strong obfuscation signal.
	if runeCount >= 10 {
		lower := strings.ToLower(w)
		if !strings.ContainsAny(lower, "aeiou") {
			return true
		}
	}
	return false
}

// HasUsableInfo reports whether parsed info carries enough technical
// detail to consider a release whose title cannot be verified.
func HasUsableInfo(info core.ReleaseInfo) bool {
	return info.Height > 0 || info.Source != core.SourceUnknown || info.VideoCodec != ""
}

// genericTitleStopwords holds articles, contractions, and core function words
// across the supported release languages. A release whose parsed title consists
// solely of these tokens (or too few non-stopword characters) cannot identify
// a show or movie on its own during title-mismatch recovery.
var genericTitleStopwords = map[string]bool{
	// English
	"a": true, "an": true, "the": true,
	"and": true, "or": true, "nor": true, "but": true,
	"of": true, "in": true, "on": true, "at": true, "to": true, "for": true, "by": true, "as": true,
	"it": true, "is": true, "be": true,
	// French
	"le": true, "la": true, "les": true, "l": true,
	"un": true, "une": true, "des": true, "du": true, "de": true, "d": true,
	"au": true, "aux": true, "et": true, "ou": true, "en": true,
	// Spanish
	"el": true, "los": true, "las": true,
	"uno": true, "una": true, "unos": true, "unas": true,
	"al": true, "del": true, "y": true, "e": true, "o": true, "u": true, "por": true,
	// German
	"der": true, "die": true, "das": true, "den": true, "dem": true,
	"ein": true, "eine": true, "einen": true, "einem": true, "einer": true, "eines": true,
	"und": true, "oder": true, "von": true, "vom": true, "zum": true, "zur": true,
	"im": true, "am": true, "zu": true, "mit": true, "auf": true, "aus": true,
	// Italian
	"il": true, "lo": true, "i": true, "gli": true,
	"di": true, "da": true, "su": true,
	"dello": true, "della": true, "dei": true, "degli": true, "delle": true,
	"allo": true, "alla": true, "ai": true, "agli": true, "alle": true,
	"nel": true, "nello": true, "nella": true, "nei": true, "negli": true, "nelle": true,
	// Portuguese
	"os": true, "um": true, "uma": true, "uns": true, "umas": true,
	"do": true, "dos": true, "no": true, "na": true, "nos": true, "nas": true,
	// Dutch and Nordic
	"het": true, "een": true, "det": true, "ett": true, "och": true, "og": true,
	// Russian
	"и": true, "в": true, "во": true, "на": true, "по": true, "из": true, "от": true,
	"до": true, "за": true, "для": true, "без": true, "или": true, "но": true,
	"к": true, "с": true, "у": true, "о": true, "об": true, "а": true,
}

// TitleTooGeneric reports whether title is too generic to identify a show or
// movie during title-mismatch recovery: empty after normalization, made only
// of articles/stopwords, or too short once stopwords are excluded. Plain
// exact title matching is unaffected; this only guards recovery paths.
func TitleTooGeneric(title string) bool {
	fields := normalizeTitleFields(title)
	if len(fields) == 0 {
		return true
	}
	nonStopRunes := 0
	hasWideScript := false
	for _, tok := range fields {
		if genericTitleStopwords[tok] {
			continue
		}
		for _, r := range tok {
			nonStopRunes++
			if !hasWideScript && unicode.In(r, unicode.Han, unicode.Hangul, unicode.Hiragana, unicode.Katakana) {
				hasWideScript = true
			}
		}
	}
	if nonStopRunes == 0 {
		return true
	}
	if hasWideScript {
		return nonStopRunes < 2
	}
	return nonStopRunes < 3
}

// TitleLeadingFragment reports whether parsed is a strict leading token prefix
// of want (for example "The" against "The Mentalist", "Star" against "Star
// Trek", or "Walking" against "The Walking Dead"). Equal titles return false.
func TitleLeadingFragment(parsed, want string) bool {
	// A disambiguating qualifier ("Doctor Who (2005)", "Shameless (US)") is
	// not more of the name: a release named without it is the same show.
	pa, wa := normalizeTitleFields(parsed), trimTitleQualifiers(normalizeTitleFields(want))
	if isStrictTokenPrefix(pa, wa) {
		return true
	}
	return isStrictTokenPrefix(stripLeadingArticle(pa), stripLeadingArticle(wa))
}

// titleQualifierCountries are the country codes catalogues append to tell
// same-named shows apart.
var titleQualifierCountries = map[string]bool{"us": true, "uk": true, "au": true, "ca": true, "nz": true, "ie": true, "za": true, "in": true, "kr": true, "jp": true, "de": true, "fr": true, "es": true, "it": true, "nl": true, "se": true, "no": true, "dk": true, "br": true, "mx": true}

// trimTitleQualifiers drops trailing year and country-code tokens.
func trimTitleQualifiers(tokens []string) []string {
	for len(tokens) > 1 {
		last := tokens[len(tokens)-1]
		isYear := len(last) == 4 && (strings.HasPrefix(last, "19") || strings.HasPrefix(last, "20")) && strings.Trim(last, "0123456789") == ""
		if !isYear && !titleQualifierCountries[last] {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

func isStrictTokenPrefix(prefix, full []string) bool {
	if len(prefix) == 0 || len(prefix) >= len(full) {
		return false
	}
	for i := range prefix {
		if prefix[i] != full[i] {
			return false
		}
	}
	return true
}

// EpisodeMatchesAbsolute reports whether parsed info is the episode whose
// number counted from the series start is absolute: an anime release
// numbered that way ("Show - 1071" or "Show S10E01 (0337)").
func EpisodeMatchesAbsolute(parsed core.ReleaseInfo, absolute int) bool {
	if absolute <= 0 || parsed.FullSeason {
		return false
	}
	for _, e := range parsed.AbsoluteEpisodes {
		if e == absolute {
			return true
		}
	}
	return false
}

// AbsoluteConflict reports whether parsed info names absolute episode
// numbers and none of them is the wanted absolute count: the release
// names a different episode, whatever season numbering it also carries.
// A cour-numbered release's number is cour-local, not series-absolute,
// so cour releases never conflict on it.
func AbsoluteConflict(parsed core.ReleaseInfo, absolute int) bool {
	return parsed.Cour == 0 && absolute > 0 && len(parsed.AbsoluteEpisodes) > 0 && !EpisodeMatchesAbsolute(parsed, absolute)
}

// EpisodeMatchesNumbering accepts canonical season numbering or verified
// absolute numbering. Some anime releases put the absolute number in Eyyyy;
// allow that only in season 1 or at 100+, never for specials or cour resets.
func EpisodeMatchesNumbering(parsed core.ReleaseInfo, season, episode, absolute int) bool {
	if AbsoluteConflict(parsed, absolute) {
		return false
	}
	if EpisodeMatches(parsed, season, episode) {
		return true
	}
	if season <= 0 || absolute <= 0 || parsed.FullSeason || parsed.Cour > 0 ||
		(len(parsed.Seasons) == 1 && parsed.Seasons[0] == 0) {
		return false
	}
	if len(parsed.AbsoluteEpisodes) > 0 {
		return EpisodeMatchesAbsolute(parsed, absolute)
	}
	if len(parsed.Seasons) == 0 {
		for _, n := range parsed.Episodes {
			if n == absolute {
				return true
			}
		}
		return false
	}
	if len(parsed.Seasons) != 1 || parsed.Seasons[0] <= 0 {
		return false
	}
	for _, n := range parsed.Episodes {
		if n == absolute && (parsed.Seasons[0] == 1 || n >= 100) {
			return true
		}
	}
	return false
}

// SeasonNumberingUnderCour reports whether a release or file labelled with
// cour (part) cour > 1 may still be matched by its season-counted numbering.
// "Show.S04.Part.2.S04E09" counts E09 within season 4, so a request without
// a cour mapping matches it like any S04E09; "[Group] Show S01 Part 2 - 01"
// (courRelative) counts within the cour and only a cour mapping can place
// it. Anime without a mapping keeps the cour reading: its "Part 2" releases
// usually restart numbering. A mapped request reads season numbering only
// inside its own cour.
func SeasonNumberingUnderCour(cour int, courRelative bool, reqCour int, anime bool) bool {
	switch {
	case cour <= 1:
		return true
	case courRelative:
		return false
	case reqCour > 0:
		return reqCour == cour
	default:
		return !anime
	}
}

// EpisodeMatchesRequest reports whether parsed info matches req's canonical
// episode, verified absolute count, anime cour mapping, configured release
// numbering aliases, or exact broadcast air date. Shared by the resolver and
// Stremio stream name selection.
func EpisodeMatchesRequest(info core.ReleaseInfo, req core.SearchRequest) bool {
	abs := req.AbsoluteEpisode
	if abs <= 0 && req.Season == 1 {
		abs = req.Episode
	}
	// A release naming a different absolute episode is a different
	// episode, even when its season numbering matches a configured
	// alias (e.g. S17E005 = absolute 633 must not satisfy absolute 583).
	if AbsoluteConflict(info, abs) {
		return false
	}
	if info.Cour == 1 && req.Cour > 1 {
		return false
	}
	if info.Cour > 1 {
		if req.Cour == info.Cour && req.CourEpisode > 0 &&
			(EpisodeMatches(info, req.Season, req.CourEpisode) ||
				(req.CourTVDBSeason > 0 && EpisodeMatches(info, req.CourTVDBSeason, req.CourEpisode))) {
			return true
		}
		if !SeasonNumberingUnderCour(info.Cour, info.CourRelative, req.Cour, req.Anime) {
			return false
		}
	}
	if EpisodeMatchesNumbering(info, req.Season, req.Episode, abs) {
		return true
	}
	if req.CourTVDBSeason > 0 && req.CourTVDBEpisode > 0 && info.Cour == 0 && len(info.Seasons) > 0 &&
		(len(info.AbsoluteEpisodes) == 0 || abs <= 0 || EpisodeMatchesAbsolute(info, abs)) &&
		EpisodeMatches(info, req.CourTVDBSeason, req.CourTVDBEpisode) {
		return true
	}
	for _, n := range core.EpisodeAliases(req) {
		if EpisodeMatches(info, n.Season, n.Episode) {
			return true
		}
	}
	if (req.Season > 0 || req.EpisodeSource == core.MetadataTVDB) && !info.FullSeason && EpisodeMatchesAirDate(info, req.Released) {
		if len(info.Episodes) == 0 && len(info.AbsoluteEpisodes) == 0 {
			return true
		}
		// Cumulative bare episode numbering (e.g. Korean variety E600) on a
		// multi-season show whose absolute count is unknown still identifies
		// the episode when its air date matches exactly.
		if req.Season > 1 && abs <= 0 && len(info.Seasons) == 0 && len(info.AbsoluteEpisodes) == 0 && len(info.Episodes) == 1 && info.Episodes[0] > req.Episode {
			return true
		}
	}
	return false
}

// AnimeSearchEpisode is the absolute episode number an anime episode
// request is also searched by ("Show 1071"), or 0: the catalogue's count,
// or the episode itself in the first season.
func AnimeSearchEpisode(req core.SearchRequest) int {
	if !req.Anime || req.Type != core.TypeEpisode || req.Season <= 0 || req.Episode <= 0 {
		return 0
	}
	if req.AbsoluteEpisode > 0 {
		return req.AbsoluteEpisode
	}
	if req.Season == 1 {
		return req.Episode
	}
	return 0
}

// MovieTitleMatches allows an inserted part marker only when another trusted
// catalogue name confirms that part number. The remaining title must match
// completely; year and reported-ID checks remain the resolver's responsibility.
func MovieTitleMatches(parsed core.ReleaseInfo, names []string) bool {
	for _, name := range names {
		if TitleMatches(parsed, name) {
			return true
		}
	}
	strip := func(title string) (string, string) {
		tokens := canonicalTokens(NormalizeTitle(title))
		for i := 0; i+1 < len(tokens); i++ {
			if tokens[i] != "pt" {
				continue
			}
			n := tokens[i+1]
			if n == "" || strings.Trim(n, "0123456789") != "" {
				continue
			}
			rest := append(append([]string{}, tokens[:i]...), tokens[i+2:]...)
			return strings.Join(rest, " "), n
		}
		return "", ""
	}
	base, part := strip(parsed.Title)
	if part == "" {
		return false
	}
	confirmed := false
	for _, name := range names {
		_, n := strip(name)
		if n == part {
			confirmed = true
		}
	}
	if !confirmed {
		return false
	}
	for _, name := range names {
		stripped, n := strip(name)
		if n != "" && n != part {
			continue
		}
		if n == "" {
			stripped = strings.Join(canonicalTokens(NormalizeTitle(name)), " ")
		}
		if stripped != "" && stripped == base {
			return true
		}
	}
	return false
}

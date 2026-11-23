package releaseparse

import (
	"strings"
	"unicode"
)

// Revised Romanization of Korean tables for Hangul syllable decomposition
// (U+AC00..U+D7A3).
var (
	hangulInitials = [19]string{
		"g", "kk", "n", "d", "tt", "r", "m", "b", "pp", "s",
		"ss", "", "j", "jj", "ch", "k", "t", "p", "h",
	}
	hangulMedials = [21]string{
		"a", "ae", "ya", "yae", "eo", "e", "yeo", "ye", "o", "wa",
		"wae", "oe", "yo", "u", "wo", "we", "wi", "yu", "eu", "ui", "i",
	}
	hangulFinals = [28]string{
		"", "k", "k", "k", "n", "n", "n", "t", "l", "k",
		"m", "p", "l", "l", "p", "l", "m", "p", "p", "t",
		"t", "ng", "t", "t", "k", "t", "p", "t",
	}
	// When a syllable final is immediately followed by a vowel-initial
	// syllable (ㅇ) within the same word, Revised Romanization carries the
	// consonant over cleanly (e.g. 한국어 -> hangugeo, 일요일 -> iryoil).
	hangulFinalsBeforeVowel = [28]string{
		"", "g", "kk", "ks", "n", "nj", "nh", "d", "r", "lg",
		"lm", "lb", "ls", "lt", "lp", "lh", "m", "b", "bs", "s",
		"ss", "ng", "j", "ch", "k", "t", "p", "h",
	}
)

func isHangulSyllable(r rune) bool {
	return r >= 0xAC00 && r <= 0xD7A3
}

func containsHangul(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hangul) {
			return true
		}
	}
	return false
}

// transliterateHangul converts Korean Hangul syllables to South Korean Revised
// Romanization (e.g. "나 혼자 산다" -> "na honja sanda"), leaving non-Hangul
// runes intact.
func transliterateHangul(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range runes {
		if !isHangulSyllable(r) {
			b.WriteRune(r)
			continue
		}
		idx := int(r - 0xAC00)
		lead := idx / 588
		vowel := (idx % 588) / 28
		tail := idx % 28
		b.WriteString(hangulInitials[lead])
		b.WriteString(hangulMedials[vowel])
		if tail > 0 {
			nextIsVowelStart := i+1 < len(runes) && isHangulSyllable(runes[i+1]) && int(runes[i+1]-0xAC00)/588 == 11
			if nextIsVowelStart {
				b.WriteString(hangulFinalsBeforeVowel[tail])
			} else {
				b.WriteString(hangulFinals[tail])
			}
		}
	}
	return b.String()
}

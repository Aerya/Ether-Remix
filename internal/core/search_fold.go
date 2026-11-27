package core

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// FoldSearchText is s as search compares it: lower case, accents and other
// combining marks removed ("Amélie" is "amelie"), runs of spaces collapsed.
// Every layer that scores a title against a query folds both sides with
// it, so "james bond", "James Bond" and "JAMES BOND" rank alike.
func FoldSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range norm.NFD.String(strings.TrimSpace(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// FoldSearchWords is s's words, folded (FoldSearchText): runs of letters
// and digits, everything else a break.
func FoldSearchWords(s string) []string {
	return strings.FieldsFunc(FoldSearchText(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

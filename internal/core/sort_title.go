package core

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// sortNumberWidth is the fixed digit width used to encode numeric runs in
// stored sort keys so SQL byte-wise collation (SQLite binary and PostgreSQL
// COLLATE "C") orders numbers naturally (1 < 2 < 10 < 12 < 39 < 500 < 1917).
const sortNumberWidth = 10

// TitleSortKey derives the normalized sort key for a display title: strips
// invisible format characters, ignores leading non-alphanumerics, drops a
// leading English article ("the ", "a ", "an ", case-insensitively), folds
// Latin accents/diacritics and ligatures to base ASCII letters, and encodes
// digit runs for numeric collation.
func TitleSortKey(s string) string {
	l := strings.ToLower(strings.TrimSpace(stripFormatRunes(s)))
	if l == "" {
		return ""
	}
	if stripped := stripLeadingNonAlnum(l); stripped != "" {
		l = stripped
	}
	l = stripLeadingArticle(l)
	return FoldSortTitle(l)
}

// FoldSortTitle normalizes an explicit sort title (such as Plex titleSort or
// Jellyfin SortName) to lowercase with invisible format characters stripped,
// Latin diacritics and ligatures folded to base ASCII letters, and digit runs
// encoded for numeric collation. Explicit sort titles keep their leading
// articles and punctuation intact.
func FoldSortTitle(s string) string {
	return padSortNumbers(foldSortTitleBase(s))
}

// EffectiveSortTitle returns the collation key for a title: the folded explicit
// sortTitle when one is present, otherwise TitleSortKey(title).
func EffectiveSortTitle(title, explicitSortTitle string) string {
	if folded := FoldSortTitle(explicitSortTitle); folded != "" {
		return folded
	}
	return TitleSortKey(title)
}

// FoldedSortTitleForRow computes the current sort key for an existing stored
// row during backfill migrations. Stored values matching previous auto-derived
// rules (pre-fold v0 or pre-punctuation/numeric v1) are recomputed from title;
// custom source sort titles are preserved and normalized with FoldSortTitle.
func FoldedSortTitleForRow(title, stored string) string {
	want := TitleSortKey(title)
	if stored != "" && stored != want && stored != legacyV0SortTitle(title) && stored != legacyV1SortTitle(title) {
		if folded := FoldSortTitle(stored); folded != "" {
			return folded
		}
	}
	return want
}

func legacyV0SortTitle(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, p) {
			if rest := strings.TrimSpace(l[len(p):]); rest != "" {
				return rest
			}
		}
	}
	return l
}

func legacyV1SortTitle(s string) string {
	l := strings.ToLower(strings.TrimSpace(stripFormatRunes(s)))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, p) {
			if rest := strings.TrimSpace(l[len(p):]); rest != "" {
				l = rest
			}
			break
		}
	}
	return foldSortTitleBase(l)
}

func isSortAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func stripLeadingNonAlnum(s string) string {
	for i, r := range s {
		if isSortAlnum(r) {
			return s[i:]
		}
	}
	return ""
}

func stripLeadingArticle(l string) string {
	for _, article := range []string{"the", "a", "an"} {
		if !strings.HasPrefix(l, article) || len(l) <= len(article) {
			continue
		}
		r, size := utf8.DecodeRuneInString(l[len(article):])
		if !unicode.IsSpace(r) {
			continue
		}
		rest := strings.TrimSpace(l[len(article)+size:])
		if rest == "" {
			return l
		}
		if stripped := stripLeadingNonAlnum(rest); stripped != "" {
			return stripped
		}
		return rest
	}
	return l
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func stripFormatRunes(s string) string {
	if isASCII(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func foldSortTitleBase(s string) string {
	s = strings.ToLower(strings.TrimSpace(stripFormatRunes(s)))
	if s == "" || isASCII(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	lastLatin := false
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if lastLatin && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)) {
			continue
		}
		switch r {
		case 'æ':
			b.WriteString("ae")
			lastLatin = true
			continue
		case 'œ':
			b.WriteString("oe")
			lastLatin = true
			continue
		case 'ß':
			b.WriteString("ss")
			lastLatin = true
			continue
		case 'ø':
			b.WriteByte('o')
			lastLatin = true
			continue
		case 'ł':
			b.WriteByte('l')
			lastLatin = true
			continue
		case 'đ', 'ð':
			b.WriteByte('d')
			lastLatin = true
			continue
		case 'þ':
			b.WriteString("th")
			lastLatin = true
			continue
		case 'ı':
			b.WriteByte('i')
			lastLatin = true
			continue
		}
		if unicode.Is(unicode.Latin, r) {
			lastLatin = true
			if r < 0x80 {
				b.WriteByte(byte(r))
				continue
			}
			for _, dr := range norm.NFD.String(string(r)) {
				if unicode.Is(unicode.Mn, dr) || unicode.Is(unicode.Mc, dr) || unicode.Is(unicode.Me, dr) {
					continue
				}
				b.WriteRune(dr)
			}
			continue
		}
		lastLatin = false
		b.WriteRune(r)
	}
	return b.String()
}

func padSortNumbers(s string) string {
	hasDigit := false
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + sortNumberWidth)
	for i := 0; i < len(s); {
		if s[i] < '0' || s[i] > '9' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		num := strings.TrimLeft(s[i:j], "0")
		if num == "" {
			num = "0"
		}
		for pad := sortNumberWidth - len(num); pad > 0; pad-- {
			b.WriteByte('0')
		}
		b.WriteString(num)
		i = j
	}
	return b.String()
}

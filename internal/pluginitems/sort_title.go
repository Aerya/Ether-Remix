package pluginitems

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/braedonsaunders/ether/internal/core"
)

// SortTitle derives the normalized lowercase sort key for a display title,
// matching catalog.SortTitle rules (spec §3.2): strips invisible format
// characters, ignores leading non-alphanumerics, removes a leading English
// article ("the ", "a ", "an "), folds Latin accents/diacritics and ligatures
// to base ASCII letters, and applies numeric collation.
func SortTitle(s string) string {
	return core.TitleSortKey(s)
}

// FoldSortTitle normalizes an explicit sort title to lowercase with Latin
// diacritics and ligatures folded to base ASCII letters and numeric collation.
func FoldSortTitle(s string) string {
	return core.FoldSortTitle(s)
}

// foldFieldWords applies the same case, diacritic, ligature, and word-boundary
// folding used by plugin people search (experience.peopleFoldedWords).
func foldFieldWords(value string) []string {
	if value == "" {
		return nil
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range norm.NFD.String(strings.ToLower(value)) {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		switch r {
		case 'ł':
			b.WriteByte('l')
		case 'ø':
			b.WriteByte('o')
		case 'đ', 'ð':
			b.WriteByte('d')
		case 'ß':
			b.WriteString("ss")
		case 'æ':
			b.WriteString("ae")
		case 'œ':
			b.WriteString("oe")
		case 'þ':
			b.WriteString("th")
		case 'ı':
			b.WriteByte('i')
		default:
			b.WriteRune(r)
		}
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// FoldFieldText returns space-separated folded words for v_fold indexing on
// plugin_item_fields.
func FoldFieldText(value string) string {
	return strings.Join(foldFieldWords(value), " ")
}

func foldQueryTerms(value string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, word := range foldFieldWords(value) {
		if !seen[word] {
			seen[word] = true
			terms = append(terms, word)
			if len(terms) == 8 {
				break
			}
		}
	}
	return terms
}

func foldedWordsMatch(label string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	words := foldFieldWords(label)
	if len(words) == 0 {
		return false
	}
	for _, term := range terms {
		found := false
		for _, w := range words {
			if strings.HasPrefix(w, term) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func appendFoldedWordPrefix(where *strings.Builder, args *[]any, terms []string) {
	appendFoldedWordPrefixCol(where, args, "f.v_fold", terms)
}

func appendFoldedWordPrefixCol(where *strings.Builder, args *[]any, col string, terms []string) {
	if len(terms) == 0 {
		return
	}
	where.WriteString(` AND ` + col + ` > ''`)
	for _, term := range terms {
		where.WriteString(` AND (` + col + ` LIKE ? OR ` + col + ` LIKE ?)`)
		*args = append(*args, term+"%", "% "+term+"%")
	}
}

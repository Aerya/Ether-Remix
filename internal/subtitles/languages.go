package subtitles

import (
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/langtag"
	"golang.org/x/text/language"
)

// searchLanguage uses the shared aliases and the complete ISO registry in x/text.
func searchLanguage(raw string) string {
	raw = langtag.CanonicalBCP47(raw)
	if raw == "" {
		return ""
	}
	tag, err := language.Parse(raw)
	if err != nil {
		return ""
	}
	return langtag.Normalize(tag.String())
}

func searchLanguages(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		l := searchLanguage(part)
		if l != "" && !seen[l] {
			out = append(out, l)
			seen[l] = true
		}
	}
	return out
}

func matchesSearchLanguages(wanted, actual string) bool {
	actual = searchLanguage(actual)
	for _, want := range searchLanguages(wanted) {
		// Providers catalogue Bokmål under Norwegian, without a separate NB code.
		w, a := want, actual
		if w == "nb" {
			w = "no"
		}
		if a == "nb" {
			a = "no"
		}
		if langtag.MatchTier(w, a) > 0 {
			return true
		}
	}
	return false
}

// providerLanguages canonicalizes the set so cache keys do not depend on order
// or duplicate aliases. Preferences keep their order separately for ranking.
func providerLanguages(raw string, code func(string) string) string {
	seen := map[string]bool{}
	var codes []string
	for _, l := range searchLanguages(raw) {
		c := code(l)
		if c != "" && !seen[c] {
			codes = append(codes, c)
			seen[c] = true
		}
	}
	sort.Strings(codes)
	return strings.Join(codes, ",")
}

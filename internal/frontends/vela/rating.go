package vela

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/contentrating"
)

type langPref struct {
	region string
	q      float64
}

// acceptLanguageRegions parses an HTTP Accept-Language header into an ordered,
// deduplicated list of uppercase ISO 3166-1 alpha-2 region codes. Entries with
// q=0 or no alpha-2 region subtag (e.g. bare "en" or numeric "es-419") are
// ignored.
func acceptLanguageRegions(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	var prefs []langPref
	for _, raw := range strings.Split(header, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, ";")
		tag := strings.TrimSpace(parts[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		validQ := true
		for _, param := range parts[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				validQ = false
				break
			}
			q = parsed
		}
		if !validQ || q <= 0 {
			continue
		}
		if region := bcp47Region(tag); region != "" {
			prefs = append(prefs, langPref{region: region, q: q})
		}
	}
	if len(prefs) == 0 {
		return nil
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	out := make([]string, 0, len(prefs))
	seen := make(map[string]bool, len(prefs))
	for _, p := range prefs {
		if seen[p.region] {
			continue
		}
		seen[p.region] = true
		out = append(out, p.region)
	}
	return out
}

// bcp47Region extracts an ISO 3166-1 alpha-2 region subtag from a BCP 47
// language tag (accepting '-' or '_' separators), skipping script and extlang
// subtags and stopping at singleton extensions.
func bcp47Region(tag string) string {
	subtags := strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' })
	if len(subtags) < 2 {
		return ""
	}
	for _, sub := range subtags[1:] {
		if len(sub) == 1 {
			// Singleton extension (e.g. -u-, -x-, -t-) ends the language/region prefix.
			return ""
		}
		if len(sub) == 4 && isASCIILetters(sub) {
			// Script subtag (e.g. Hant, Latn).
			continue
		}
		if len(sub) == 2 && isASCIILetters(sub) {
			return contentrating.Alpha2(sub)
		}
		if len(sub) == 3 && isASCIIDigits(sub) {
			// UN M.49 numeric region (e.g. 419): no alpha-2 code.
			return ""
		}
	}
	return ""
}

func isASCIILetters(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return len(s) > 0
}

func isASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// preferredRatingRegions returns the ordered certificate country preferences
// for a request: Accept-Language regions, then the server's configured region,
// then US.
func (i *instance) preferredRatingRegions(r *http.Request) []string {
	var out []string
	seen := make(map[string]bool, 4)
	add := func(cc string) {
		cc = contentrating.Alpha2(cc)
		if cc == "" || seen[cc] {
			return
		}
		seen[cc] = true
		out = append(out, cc)
	}
	if r != nil {
		for _, cc := range acceptLanguageRegions(r.Header.Get("Accept-Language")) {
			add(cc)
		}
	}
	if i != nil && i.host.CatalogRegion != nil {
		add(i.host.CatalogRegion())
	}
	add("US")
	return out
}

func varyLanguage(w http.ResponseWriter) {
	w.Header().Set("Vary", "Authorization, X-Vela-Device-Id, Accept-Language")
}

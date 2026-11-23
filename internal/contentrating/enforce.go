package contentrating

import (
	"sort"
	"strings"
)

// UncertifiedAdult marks a title that has no usable certification ("" or "NR")
// on TMDB or any configured provider, yet carries TMDB adult=true or a
// strongly adult-leaning stored keyword set. Unlike ordinary unrated titles,
// UncertifiedAdult is hidden from every viewer with any content-rating limit,
// even when the owner allows unrated titles.
const UncertifiedAdult = "XXX"

type overrideSpec struct {
	country string
	entries []entry
}

var overrideSpecs = []overrideSpec{
	{"AU", e(Adult, "RC")},
	{"FI", e(Adult, "KK")},
	{"HK", e(Adult, "Category III", "Cat III", "Cat. III")},
	{"JP", e(Adult, "R18", "R-18", "18+")},
	{"KR", e(Adult, "Restricted Screening", "제한상영가")},
	{"PH", e(Adult, "X")},
	{"SG", e(Adult, "R(A)", "RA")},
	{"TH", e(Adult, "Banned")},
	{"TW", system(
		e(AllAges, "普遍級", "普"),
		e(Parental, "保護級", "護"),
		e(Teen, "輔12", "輔導十二歲級"),
		e(Mature, "輔15", "輔導十五歲級", "輔導級"),
		e(Adult, "限制級", "限"),
	)},
	{"ZA", e(Adult, "XX", "X18")},
}

// enforceOverrides maps refused, banned, or alternate adult/regional labels
// that Select leaves Unrated (so display stays faithful to Plex/Jellyfin
// conventions) onto the US enforcement scale.
var enforceOverrides = func() map[string]map[string]Level {
	out := make(map[string]map[string]Level, len(overrideSpecs))
	for _, spec := range overrideSpecs {
		idx := make(map[string]Level, len(spec.entries))
		for _, en := range spec.entries {
			idx[norm(en.label)] = en.level
		}
		out[spec.country] = idx
	}
	return out
}()

var overrideQualifiedByLevel = func() map[Level][]string {
	out := map[Level][]string{}
	for _, spec := range overrideSpecs {
		cc := strings.ToLower(spec.country)
		for _, en := range spec.entries {
			out[en.level] = append(out[en.level], cc+"/"+strings.ToLower(strings.TrimSpace(en.label)))
		}
	}
	for lvl := range out {
		sort.Strings(out[lvl])
	}
	return out
}()

// OverrideLabelsForLevel returns the sorted lowercase "cc/label" forms in
// enforceOverrides for lvl, so SQL rating filters match pre-refresh foreign
// labels (e.g. "ph/x", "au/rc") before lazy backfill updates their rank.
func OverrideLabelsForLevel(lvl Level) []string {
	return overrideQualifiedByLevel[lvl]
}

func normalizeCountryCode(country string) string {
	c := strings.ToUpper(strings.TrimSpace(country))
	if c == "UK" {
		return "GB"
	}
	return c
}

// EnforceLookup returns the enforcement Level of a country's certification,
// mapping 18+/R18/X/banned classifications to Adult (NC-17), 15–17/16/MA15+ to
// Mature (R), 12–14/13 to Teen (PG-13), 6–11 to Parental (PG), and 0–5/U/G to
// AllAges (G).
func EnforceLookup(country, label string) Level {
	c := normalizeCountryCode(country)
	if idx, ok := enforceOverrides[c]; ok {
		if l, ok := idx[norm(label)]; ok {
			return l
		}
	}
	return Lookup(c, label)
}

func splitDelimitedCountry(s string) (country, label string, ok bool) {
	for _, sep := range []byte{':', ' ', '\t'} {
		i := strings.IndexByte(s, sep)
		if i != 2 {
			continue
		}
		cc := normalizeCountryCode(s[:2])
		if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
			continue
		}
		if _, hasIdx := index[cc]; !hasIdx {
			if _, hasOv := enforceOverrides[cc]; !hasOv {
				continue
			}
		}
		rest := strings.TrimSpace(s[3:])
		if rest != "" {
			return cc, rest, true
		}
	}
	return "", "", false
}

// EnforceLevelOf returns the enforcement Level for a stored or raw rating
// label (e.g. "ph/R-18", "ph/X", "PH X", "de/18", "NC-17", "MA15+").
// UncertifiedAdult ("XXX") is not a certified band (LevelOf returns Unrated);
// use EnforceExceeds for viewer-limit checks.
func EnforceLevelOf(s string) Level {
	s = strings.TrimSpace(s)
	if s == "" || s == UncertifiedAdult {
		return Unrated
	}
	if country, label, ok := splitQualified(s); ok {
		return EnforceLookup(country, label)
	}
	if l, ok := lookup("US", s); ok {
		return l
	}
	// Bare "X" is Adult across US/ES/HU/BG/PH enforcement.
	if norm(s) == "X" {
		return Adult
	}
	if l := anyCountry(s); l != Unrated {
		return l
	}
	if country, label, ok := splitDelimitedCountry(s); ok {
		if l := EnforceLookup(country, label); l != Unrated {
			return l
		}
	}
	for _, idx := range enforceOverrides {
		if l, ok := idx[norm(s)]; ok && l != Unrated {
			return l
		}
	}
	return Unrated
}

// EnforceRating maps a stored or raw certification string to its canonical US
// enforcement rating ("G", "PG", "PG-13", "R", "NC-17"), preserving
// UncertifiedAdult ("XXX") and returning "" when unrated.
func EnforceRating(s string) string {
	s = strings.TrimSpace(s)
	if s == UncertifiedAdult {
		return UncertifiedAdult
	}
	if l := EnforceLevelOf(s); l != Unrated {
		return l.Key()
	}
	return ""
}

// EnforceCertifications chooses the canonical US enforcement rating ("G",
// "PG", "PG-13", "R", "NC-17") from TMDB release_dates / content_ratings
// certifications without modifying the displayed rating returned by Select.
// Within the winning country and release-type tier, stricter certifications
// break ties so a dual-cut listing does not under-enforce.
func EnforceCertifications(certs []Certification, region string) string {
	region = strings.ToUpper(strings.TrimSpace(region))
	byCountry := map[string][]Certification{}
	for _, c := range certs {
		country := strings.ToUpper(strings.TrimSpace(c.Country))
		if country == "" || strings.TrimSpace(c.Label) == "" {
			continue
		}
		byCountry[country] = append(byCountry[country], c)
	}
	order := make([]string, 0, len(byCountry))
	for country := range byCountry {
		if country != region && country != "US" {
			order = append(order, country)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		pi, pj := foreignPriority(order[i]), foreignPriority(order[j])
		if pi != pj {
			return pi < pj
		}
		return order[i] < order[j]
	})
	head := []string{"US"}
	if region != "" && region != "US" {
		head = []string{region, "US"}
	}
	order = append(head, order...)
	for _, country := range order {
		list := byCountry[country]
		sort.SliceStable(list, func(i, j int) bool { return rank(list[i].ReleaseType) < rank(list[j].ReleaseType) })
		bestLevel := Unrated
		bestRank := 0
		for _, c := range list {
			r := rank(c.ReleaseType)
			if bestLevel != Unrated && r > bestRank {
				break
			}
			if l := EnforceLookup(country, strings.TrimSpace(c.Label)); l > bestLevel {
				bestLevel = l
				bestRank = r
			}
		}
		if bestLevel != Unrated {
			return bestLevel.Key()
		}
	}
	return ""
}

// Strong single-keyword indicators for uncertified adult/soft-core titles on TMDB.
var strongAdultKeywords = map[string]struct{}{
	"adult animation":      {},
	"adult film":           {},
	"av idol":              {},
	"bestiality":           {},
	"erotic drama":         {},
	"erotic film":          {},
	"erotic movie":         {},
	"erotic thriller":      {},
	"erotica":              {},
	"eroticism":            {},
	"explicit sex":         {},
	"full frontal nudity":  {},
	"gangbang":             {},
	"hardcore":             {},
	"hardcore pornography": {},
	"hentai":               {},
	"incest":               {},
	"necrophilia":          {},
	"orgy":                 {},
	"pink film":            {},
	"porno":                {},
	"pornographic":         {},
	"pornography":          {},
	"roman porno":          {},
	"sex film":             {},
	"sexploitation":        {},
	"softcore":             {},
	"softcore erotica":     {},
	"snuff":                {},
	"unsimulated sex":      {},
	"zoophilia":            {},
}

// Secondary adult-leaning TMDB keywords: require at least two distinct hits
// so mainstream dramas with a single "nudity" or "adultery" tag are not
// misclassified when uncertified.
var leaningAdultKeywords = map[string]struct{}{
	"adultery":         {},
	"bdsm":             {},
	"brothel":          {},
	"camgirl":          {},
	"escort":           {},
	"exhibitionism":    {},
	"female nudity":    {},
	"fetish":           {},
	"lust":             {},
	"male nudity":      {},
	"masturbation":     {},
	"mistress":         {},
	"nudity":           {},
	"prostitution":     {},
	"sadomasochism":    {},
	"seduction":        {},
	"sensuality":       {},
	"sex":              {},
	"sex offender":     {},
	"sex scene":        {},
	"sex scandal":      {},
	"sex therapist":    {},
	"sex toy":          {},
	"sex worker":       {},
	"sexual awakening": {},
	"sexual desire":    {},
	"sexual fantasy":   {},
	"sexual obsession": {},
	"strip club":       {},
	"stripper":         {},
	"swinger":          {},
	"swingers":         {},
	"threesome":        {},
	"voyeur":           {},
	"voyeurism":        {},
}

var strongAdultKeywordSlice = sortedKeys(strongAdultKeywords)
var leaningAdultKeywordSlice = sortedKeys(leaningAdultKeywords)

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// StrongAdultKeywords returns the sorted single-hit adult keywords.
func StrongAdultKeywords() []string { return strongAdultKeywordSlice }

// LeaningAdultKeywords returns the sorted multi-hit (>=2) adult-leaning keywords.
func LeaningAdultKeywords() []string { return leaningAdultKeywordSlice }

// HasAdultKeywords reports whether a title's stored keyword list strongly
// indicates adult or soft-core sexual content.
func HasAdultKeywords(keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	var seen map[string]struct{}
	hits := 0
	for _, raw := range keywords {
		k := strings.ToLower(strings.TrimSpace(raw))
		if k == "" {
			continue
		}
		if _, ok := strongAdultKeywords[k]; ok {
			return true
		}
		if _, ok := leaningAdultKeywords[k]; ok {
			if seen == nil {
				seen = make(map[string]struct{}, 4)
			}
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				hits++
				if hits >= 2 {
					return true
				}
			}
		}
	}
	return false
}

// EffectiveEnforcement resolves the US enforcement rating for a title, using
// its persisted enforcementRating when present and falling back to its
// displayed contentRating, adult flag, and stored keywords before lazy
// backfill has refreshed the row.
func EffectiveEnforcement(enforcementRating, contentRating string, adult bool, keywords []string) string {
	if r := EnforceRating(enforcementRating); r != "" {
		return r
	}
	if r := EnforceRating(contentRating); r != "" {
		return r
	}
	if adult || HasAdultKeywords(keywords) {
		return UncertifiedAdult
	}
	return ""
}

// EnforceExceeds reports whether an enforcement or content rating exceeds max.
// An empty max is unrestricted. Unrated ("") and UncertifiedAdult ("XXX")
// always exceed a non-empty max.
func EnforceExceeds(rating, max string) bool {
	if strings.TrimSpace(max) == "" {
		return false
	}
	m := EnforceLevelOf(max)
	if m == Unrated {
		return true
	}
	if strings.TrimSpace(rating) == UncertifiedAdult {
		return true
	}
	r := EnforceLevelOf(rating)
	return r == Unrated || r > m
}

package contentrating

import (
	"sort"
	"strconv"
	"strings"
)

// Certification is one rating a provider lists for a title.
type Certification struct {
	Country string // ISO 3166-1, any case
	Label   string
	// ReleaseType is TMDB's movie release type (1 premiere, 2 limited
	// theatrical, 3 theatrical, 4 digital, 5 physical, 6 TV); 0 for TV
	// show content ratings.
	ReleaseType int
}

// Selection is the chosen rating in stored form, and where it came from.
type Selection struct {
	Value  string // stored form: "PG", "de/6"; "" when nothing usable
	Source string // "tmdb:US:6", "tmdb:CA"; "" when Value is ""
}

// releasePreference orders TMDB release types: theatrical, limited, digital,
// physical, TV, premiere. A TV movie has only a type 6 release, so its US
// rating is found there; a festival premiere's rating is the last resort.
var releasePreference = map[int]int{3: 0, 2: 1, 4: 2, 5: 3, 6: 4, 1: 5, 0: 6}

// Select chooses a title's rating from a provider's certifications: the
// owner's region first, then the US, then other countries (foreignPriority
// order). Within a country it takes the first label that has an age level,
// by release type. A foreign rating is kept region-qualified ("de/6"),
// never passed off as US. When no country has a rated label, an explicit
// "not rated" from the region or the US is kept ("NR"), as Plex shows it.
func Select(certs []Certification, region string) Selection {
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
	var notRated Selection
	for _, country := range order {
		list := byCountry[country]
		sort.SliceStable(list, func(i, j int) bool { return rank(list[i].ReleaseType) < rank(list[j].ReleaseType) })
		for _, c := range list {
			label := strings.TrimSpace(c.Label)
			if Lookup(country, label) != Unrated {
				return Selection{Value: Qualify(country, label), Source: source(country, c.ReleaseType)}
			}
			if notRated.Value == "" && (country == region || country == "US") && IsNotRated(label) {
				notRated = Selection{Value: Qualify(country, label), Source: source(country, c.ReleaseType)}
			}
		}
	}
	return notRated
}

func rank(releaseType int) int {
	if r, ok := releasePreference[releaseType]; ok {
		return r
	}
	return len(releasePreference)
}

func source(country string, releaseType int) string {
	s := "tmdb:" + country
	if releaseType > 0 {
		s += ":" + strconv.Itoa(releaseType)
	}
	return s
}

// Alpha2 normalizes a country or sub-region code (such as "us", "GB", or
// "CA-QC") to its uppercase ISO 3166-1 alpha-2 code, or "" when invalid.
func Alpha2(country string) string {
	c := strings.ToUpper(strings.TrimSpace(country))
	if len(c) == 5 && c[2] == '-' {
		c = c[:2]
	}
	if len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' {
		return c
	}
	return ""
}

// ByCountry reduces provider certifications to the best bare label per country
// (keyed by uppercase country code), following Select's release-type ordering
// and preferring a rated label over an explicit "not rated" mark.
func ByCountry(certs []Certification) map[string]string {
	byCountry := map[string][]Certification{}
	for _, c := range certs {
		country := strings.ToUpper(strings.TrimSpace(c.Country))
		label := strings.TrimSpace(c.Label)
		if Alpha2(country) == "" || label == "" {
			continue
		}
		byCountry[country] = append(byCountry[country], c)
	}
	if len(byCountry) == 0 {
		return nil
	}
	out := make(map[string]string, len(byCountry))
	for country, list := range byCountry {
		sort.SliceStable(list, func(i, j int) bool { return rank(list[i].ReleaseType) < rank(list[j].ReleaseType) })
		var nr string
		picked := ""
		for _, c := range list {
			label := strings.TrimSpace(c.Label)
			if Lookup(country, label) != Unrated {
				picked = label
				break
			}
			if nr == "" && IsNotRated(label) {
				nr = label
			}
		}
		if picked == "" {
			picked = nr
		}
		if picked != "" {
			out[country] = picked
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Display selects a bare certificate and optional ISO 3166-1 alpha-2 country
// code for a title, preferring the regions in preferredRegions in order (for
// example the viewer's Accept-Language region, the server's configured region,
// then "US"), before falling back to other countries in foreignPriority order
// or the parsed stored rating. Unknown bare ratings return label with "".
func Display(stored string, certs map[string]string, preferredRegions ...string) (label, country string) {
	pool := make(map[string]string, len(certs)+1)
	for rawCountry, rawLabel := range certs {
		cc := strings.ToUpper(strings.TrimSpace(rawCountry))
		lbl := strings.TrimSpace(rawLabel)
		if Alpha2(cc) == "" || lbl == "" {
			continue
		}
		pool[cc] = lbl
	}
	stored = strings.TrimSpace(stored)
	var parsed Rating
	if stored != "" {
		parsed = Parse(FromPrefixed(stored))
		if parsed.Country != "" && parsed.Label != "" {
			existing, exists := pool[parsed.Country]
			if !exists || (Lookup(parsed.Country, existing) == Unrated && parsed.Level != Unrated) {
				pool[parsed.Country] = parsed.Label
			}
		}
	}

	prefs := make([]string, 0, len(preferredRegions))
	seenPref := make(map[string]bool, len(preferredRegions))
	for _, r := range preferredRegions {
		cc := Alpha2(r)
		if cc == "" || seenPref[cc] {
			continue
		}
		seenPref[cc] = true
		prefs = append(prefs, cc)
	}

	keys := make([]string, 0, len(pool))
	for k := range pool {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, pj := foreignPriority(keys[i]), foreignPriority(keys[j])
		if pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})

	findRegion := func(cc string, ratedOnly bool) (string, string, bool) {
		if lbl := pool[cc]; lbl != "" && (!ratedOnly || Lookup(cc, lbl) != Unrated) {
			return lbl, cc, true
		}
		for _, k := range keys {
			if Alpha2(k) != cc {
				continue
			}
			if lbl := pool[k]; lbl != "" && (!ratedOnly || Lookup(k, lbl) != Unrated) {
				return lbl, cc, true
			}
		}
		return "", "", false
	}

	for _, cc := range prefs {
		if lbl, alpha, ok := findRegion(cc, true); ok {
			return lbl, alpha
		}
	}
	for _, k := range keys {
		if lbl := pool[k]; lbl != "" && Lookup(k, lbl) != Unrated {
			return lbl, Alpha2(k)
		}
	}
	for _, cc := range prefs {
		if lbl, alpha, ok := findRegion(cc, false); ok && IsNotRated(lbl) {
			return lbl, alpha
		}
	}
	if parsed.Label != "" {
		return parsed.Label, Alpha2(parsed.Country)
	}
	for _, k := range keys {
		if lbl := pool[k]; lbl != "" {
			return lbl, Alpha2(k)
		}
	}
	return "", ""
}

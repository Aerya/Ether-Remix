package catalog

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// applyTranslatedNames runs only on metadata already fetched for this title.
// TMDB keeps primary translations separately from alternative_titles, and may
// use a macrolanguage code where the owner's locale uses its dominant variant.
func (c *Catalog) applyTranslatedNames(t *core.Title, translations *tmdb.Translations, alts *tmdb.AltTitles) {
	cfg := c.config()
	requestedLanguage, wantCountry, _ := strings.Cut(strings.TrimSpace(cfg.Language), "-")
	wantLanguage := tmdb.MetadataLanguage(requestedLanguage)
	var preferred []string
	best, bestScore := "", -1
	if translations != nil {
		// Non-nil, even with no matching translation: replace stale aliases.
		preferred = []string{}
		for _, tr := range translations.Translations {
			if !strings.EqualFold(tmdb.MetadataLanguage(tr.Language), wantLanguage) {
				continue
			}
			name := strings.TrimSpace(tr.Data.Title)
			if t.Type == core.TypeShow {
				name = strings.TrimSpace(tr.Data.Name)
			}
			if name == "" || len(name) > maxAltTitleLen {
				continue
			}
			preferred = append(preferred, name)
			score := 0
			if strings.EqualFold(tr.Country, wantCountry) {
				score += 2
			}
			if strings.EqualFold(tr.Language, requestedLanguage) {
				score++
			}
			if (wantCountry == "" || strings.EqualFold(tr.Country, wantCountry)) && score > bestScore {
				best, bestScore = name, score
			}
		}
	}
	if best != "" {
		t.Title, t.SortTitle = best, sortTitle(best)
	}
	if requestedLanguage != "" && !strings.EqualFold(wantLanguage, "en") {
		t.EnglishTitle = englishTitle(t, translations, alts)
	} else {
		t.EnglishTitle = ""
	}
	if alts != nil && cfg.Region != "" {
		for _, list := range [][]tmdb.AltTitle{alts.Titles, alts.Results} {
			for _, alt := range list {
				if strings.EqualFold(alt.Country, cfg.Region) {
					preferred = append(preferred, alt.Title)
				}
			}
		}
	}
	t.AlternativeTitles = altTitles(alts, t.Title, t.OriginalTitle, preferred...)
}

// englishTitle extracts TMDB's en-US title from already-fetched metadata:
// preferring translations[] en-US data.title/name, then an English original,
// then the first Latin-script alternative_titles US entry, then any other
// English translation.
func englishTitle(t *core.Title, translations *tmdb.Translations, alts *tmdb.AltTitles) string {
	enOther := ""
	if translations != nil {
		for _, tr := range translations.Translations {
			if !strings.EqualFold(tmdb.MetadataLanguage(tr.Language), "en") {
				continue
			}
			name := strings.TrimSpace(tr.Data.Title)
			if t.Type == core.TypeShow {
				name = strings.TrimSpace(tr.Data.Name)
			}
			if name == "" || len(name) > maxAltTitleLen {
				continue
			}
			if strings.EqualFold(tr.Country, "US") {
				return name
			}
			if enOther == "" {
				enOther = name
			}
		}
	}
	if strings.EqualFold(t.Language, "en") {
		if orig := strings.TrimSpace(t.OriginalTitle); orig != "" && len(orig) <= maxAltTitleLen {
			return orig
		}
	}
	if alts != nil {
		for _, list := range [][]tmdb.AltTitle{alts.Titles, alts.Results} {
			for _, alt := range list {
				if !strings.EqualFold(alt.Country, "US") {
					continue
				}
				name := strings.TrimSpace(alt.Title)
				if name != "" && len(name) <= maxAltTitleLen && latinScript(name) {
					return name
				}
			}
		}
	}
	return enOther
}

package tmdb

import "golang.org/x/text/language"

// MetadataLanguage uses the ISO macrolanguage spelling used by TMDB metadata
// (for example nb-NO -> no-NO). Config keeps the owner's original locale.
// Use the standard language registry rather than a per-language alias table.
func MetadataLanguage(locale string) string {
	tag, err := language.All.Parse(locale)
	if err != nil {
		return locale
	}
	return tag.String()
}

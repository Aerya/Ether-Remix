package core

import "strings"

// IsAnime partage la classification des recherches de lecture et d’indexation.
func (t *Title) IsAnime() bool {
	if t == nil {
		return false
	}
	for _, k := range t.Keywords {
		if strings.EqualFold(k, "anime") {
			return true
		}
	}
	if !strings.EqualFold(t.Language, "ja") {
		return false
	}
	for _, g := range t.Genres {
		if strings.EqualFold(g, "Animation") {
			return true
		}
	}
	return false
}

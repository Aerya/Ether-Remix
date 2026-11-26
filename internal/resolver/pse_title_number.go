package resolver

import (
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// pseTitleWithNumber corrige une release dont le titre finit par un nombre à
// quatre chiffres lu comme une année (« Blade.Runner.2099.S01E01 » : titre
// « Blade Runner », année 2099) : quand titre lu + ce nombre redonne le titre
// cherché (ou l'un de ses titres alternatifs), le nombre est rendu au titre et
// l'année est considérée inconnue.
func pseTitleWithNumber(rel core.Release, sreq core.SearchRequest) core.Release {
	info := rel.Info
	if info.Year <= 0 || info.Title == "" || releaseparse.TitleMatches(info, sreq.Title) {
		return rel
	}
	full := info.Title + " " + strconv.Itoa(info.Year)
	wants := append([]string{sreq.Title, sreq.OriginalTitle, sreq.EnglishTitle}, sreq.AlternativeTitles...)
	for _, w := range wants {
		if w != "" && releaseparse.TitlesMatch(full, w) {
			rel.Info.Title = full
			rel.Info.Year = 0
			return rel
		}
	}
	return rel
}

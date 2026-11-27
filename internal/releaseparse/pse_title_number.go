package releaseparse

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// PSETitleWithNumber corrige un titre qui finit par un nombre à quatre
// chiffres lu comme une année (« Blade.Runner.2099.S01E01 » : titre
// « Blade Runner », année 2099) : quand titre lu + ce nombre redonne l'un des
// titres cherchés, le nombre est rendu au titre et l'année devient inconnue.
func PSETitleWithNumber(info core.ReleaseInfo, wants ...string) core.ReleaseInfo {
	if info.Year <= 0 || info.Title == "" {
		return info
	}
	for _, w := range wants {
		if w != "" && TitlesMatch(info.Title, w) {
			return info
		}
	}
	full := info.Title + " " + strconv.Itoa(info.Year)
	for _, w := range wants {
		if w != "" && TitlesMatch(full, w) {
			info.Title, info.Year = full, 0
			return info
		}
	}
	return info
}

// PSEEpisodeSeriesYear est EpisodeSeriesYear sans le nombre que
// PSETitleWithNumber a rendu au titre.
func PSEEpisodeSeriesYear(name string, info core.ReleaseInfo) int {
	y := EpisodeSeriesYear(name, info.Year)
	if y > 0 && strings.HasSuffix(info.Title, " "+strconv.Itoa(y)) {
		return 0
	}
	return y
}

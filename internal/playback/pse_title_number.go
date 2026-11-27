package playback

import (
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// pseSeriesYear est l'année de série lue dans le nom d'une release, sauf
// quand ce nombre termine le titre de la série (« Blade.Runner.2099 » pour
// « Blade Runner 2099 ») : le nom confirme alors la série elle-même, et
// l'année attendue est rendue.
func pseSeriesYear(rel core.Release, title *core.Title) int {
	year := releaseparse.EpisodeSeriesYear(rel.Name, rel.Info.Year)
	if year <= 0 || title.ShowYear <= 0 {
		return year
	}
	info := releaseparse.PSETitleWithNumber(rel.Info, title.ShowTitle, title.ShowOriginalTitle, title.ShowEnglishTitle)
	if strings.HasSuffix(info.Title, " "+strconv.Itoa(year)) && releaseparse.TitleMatchesAny(info, title.ShowTitle, append([]string{title.ShowOriginalTitle, title.ShowEnglishTitle}, title.ShowAlternativeTitles...)) {
		return title.ShowYear
	}
	return year
}
